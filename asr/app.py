from __future__ import annotations

import logging
import os
import tempfile
import threading
from contextlib import asynccontextmanager, contextmanager
from pathlib import Path

import onnxruntime as ort
from fastapi import FastAPI, HTTPException, Request
from tone import DecoderType, StreamingCTCPipeline
from tone.decoder import GreedyCTCDecoder
from tone.logprob_splitter import StreamingLogprobSplitter
from tone.onnx_wrapper import StreamingCTCModel
from tone.pipeline import TextPhrase

from audio import agc_filter, prepare_audio
from diarize import DiarConfig, SpeakerDiarizer
from vad import SAMPLE_RATE, SileroVAD, VADConfig, pad_for_tone, speech_segments

logging.basicConfig(
    level=os.getenv("LOG_LEVEL", "info").upper(),
    format="%(asctime)s %(levelname)s %(name)s: %(message)s",
)
logger = logging.getLogger("aura-asr")

pipeline: StreamingCTCPipeline | None = None
vad_model: SileroVAD | None = None
diarizer: SpeakerDiarizer | None = None
vad_cfg = VADConfig.from_env()
diar_cfg = DiarConfig.from_env()
load_error: str | None = None
process_lock = threading.Lock()


def _thread_count() -> int:
    raw = os.getenv("ORT_INTRA_OP_NUM_THREADS")
    if raw:
        return max(1, int(raw))
    return max(1, os.cpu_count() or 4)


def load_pipeline() -> StreamingCTCPipeline:
    decoder_name = os.getenv("ASR_DECODER", "greedy").strip().lower()
    threads = _thread_count()
    os.environ.setdefault("OMP_NUM_THREADS", str(threads))
    os.environ.setdefault("MKL_NUM_THREADS", str(threads))

    folder = Path(os.getenv("LOAD_FROM_FOLDER", "/models"))
    model_path = folder / "model.onnx"
    logger.info("loading T-one decoder=%s ort_threads=%s", decoder_name, threads)

    if model_path.is_file() and decoder_name == "greedy":
        opts = ort.SessionOptions()
        opts.intra_op_num_threads = threads
        opts.inter_op_num_threads = 1
        opts.graph_optimization_level = ort.GraphOptimizationLevel.ORT_ENABLE_ALL
        sess = ort.InferenceSession(
            str(model_path),
            sess_options=opts,
            providers=["CPUExecutionProvider"],
        )
        return StreamingCTCPipeline(
            StreamingCTCModel(sess),
            StreamingLogprobSplitter(),
            GreedyCTCDecoder(),
        )

    decoder_type = DecoderType.GREEDY if decoder_name == "greedy" else DecoderType.BEAM_SEARCH
    if model_path.is_file():
        return StreamingCTCPipeline.from_local(folder, decoder_type=decoder_type)
    return StreamingCTCPipeline.from_hugging_face(decoder_type=decoder_type)


def _load() -> None:
    global pipeline, vad_model, diarizer, load_error
    if vad_cfg.enabled:
        try:
            vad_model = SileroVAD(vad_cfg.model_path)
            logger.info(
                "Silero VAD ready threshold=%.2f min_speech=%dms min_silence=%dms pad=%dms",
                vad_cfg.threshold,
                vad_cfg.min_speech_ms,
                vad_cfg.min_silence_ms,
                vad_cfg.pad_ms,
            )
        except Exception:
            vad_model = None
            logger.exception("failed to load Silero VAD, transcribing full audio")
    else:
        logger.info("Silero VAD disabled")

    filt = agc_filter()
    if filt:
        logger.info("AGC enabled filter=%s", filt)
    else:
        logger.info("AGC disabled")

    if diar_cfg.enabled:
        try:
            diarizer = SpeakerDiarizer(diar_cfg)
            logger.info("diarization ready speakers=%d emb=%s", diar_cfg.num_speakers, diar_cfg.emb_model)
        except Exception:
            diarizer = None
            logger.exception("failed to load diarization, phrases will have no speaker")
    else:
        logger.info("diarization disabled")

    try:
        pipeline = load_pipeline()
        load_error = None
        logger.info("T-one ready")
    except Exception as exc:
        load_error = str(exc)
        logger.exception("failed to load T-one")


def phrases_to_result(phrases: list[TextPhrase], offset: float = 0.0) -> list[dict]:
    items = []
    for phrase in phrases:
        text = (phrase.text or "").strip()
        if not text:
            continue
        items.append(
            {
                "text": text,
                "start": round(float(phrase.start_time) + offset, 3),
                "end": round(float(phrase.end_time) + offset, 3),
            }
        )
    return items


def result_from_items(items: list[dict]) -> dict:
    return {"text": " ".join(item["text"] for item in items), "phrases": items}


def transcribe_pcm(audio, diar_wav=None, raw_wav=None) -> dict:
    total = len(audio)
    segments: list[tuple[int, int]]
    if vad_cfg.enabled and vad_model is not None and total > 0:
        segments = speech_segments(audio, vad_model, vad_cfg)
        if not segments:
            logger.info("VAD found no speech in %.1fs, using full audio", total / SAMPLE_RATE)
            segments = [(0, total)]
    else:
        segments = [(0, total)]

    kept = sum(end - start for start, end in segments)
    logger.info(
        "VAD kept %.1fs / %.1fs in %d segment(s)",
        kept / SAMPLE_RATE,
        total / SAMPLE_RATE,
        len(segments),
    )

    items: list[dict] = []
    for start, end in segments:
        chunk = pad_for_tone(audio[start:end])
        if pipeline is None:
            raise RuntimeError("model not loaded")
        phrases = pipeline.forward_offline(chunk)
        items.extend(phrases_to_result(phrases, start / SAMPLE_RATE))

    if diarizer is not None and diar_wav is not None and items:
        try:
            diarizer.label_phrases(items, diar_wav, raw_wav if raw_wav is not None else diar_wav)
            speakers = {item.get("speaker") for item in items}
            logger.info(
                "diarization labeled %d phrase(s) speakers=%s",
                len(items),
                ",".join(sorted(s for s in speakers if s)),
            )
        except Exception:
            logger.exception("diarization failed")

    return result_from_items(items)


@contextmanager
def temp_upload(suffix: str):
    with tempfile.NamedTemporaryFile(suffix=suffix, delete=False) as tmp:
        path = Path(tmp.name)
    try:
        yield path
    finally:
        path.unlink(missing_ok=True)


@asynccontextmanager
async def lifespan(_app: FastAPI):
    thread = threading.Thread(target=_load, name="load-tone", daemon=True)
    thread.start()
    yield


app = FastAPI(title="aura-asr", lifespan=lifespan)


@app.get("/health")
def health():
    if load_error:
        raise HTTPException(status_code=503, detail=load_error)
    if pipeline is None:
        raise HTTPException(status_code=503, detail="model not loaded")
    return {"status": "ok"}


@app.post("/transcribe")
async def transcribe(request: Request):
    if load_error:
        raise HTTPException(status_code=503, detail=load_error)
    if pipeline is None:
        raise HTTPException(status_code=503, detail="model not loaded")

    filename = request.headers.get("x-filename") or "audio"
    suffix = Path(filename).suffix or ".audio"
    contents = await request.body()
    if not contents:
        raise HTTPException(status_code=400, detail="empty audio file")

    if not process_lock.acquire(blocking=False):
        raise HTTPException(status_code=429, detail="asr is busy")
    try:
        with temp_upload(suffix) as src:
            src.write_bytes(contents)
            prepared = prepare_audio(src)
            return transcribe_pcm(prepared.tone, prepared.diar, prepared.raw)
    except HTTPException:
        raise
    except Exception as exc:
        logger.exception("transcription failed")
        raise HTTPException(status_code=500, detail=str(exc)) from exc
    finally:
        process_lock.release()
