from __future__ import annotations

import logging
import os
import time

import numpy as np
import numpy.typing as npt

from audio import SAMPLE_RATE, pcm_to_float
from segments import (
    Word,
    assign_speakers,
    frame_energy,
    group_phrases,
    remap_speakers,
    speech_chunks,
    turns_from_output,
)

logger = logging.getLogger("aura-asr")

diarization = None
asr_model = None

MIN_CHUNK_SAMPLES = SAMPLE_RATE // 10


def _env_int(name: str, default: int) -> int | None:
    raw = os.getenv(name)
    if raw is None or raw.strip() == "":
        return default
    if raw.strip().lower() in {"auto", "0"}:
        return None
    return max(1, int(raw))


def load_models() -> None:
    global diarization, asr_model
    import torch
    import gigaam
    from pyannote.audio import Pipeline

    token = os.getenv("HF_TOKEN") or os.getenv("HUGGINGFACE_TOKEN")
    diar_name = os.getenv("DIAR_MODEL", "pyannote/speaker-diarization-3.1")
    asr_name = os.getenv("GIGAAM_MODEL", "v3_e2e_rnnt")
    device = torch.device("cuda" if torch.cuda.is_available() else "cpu")

    logger.info("loading pyannote model=%s device=%s", diar_name, device)
    pipe = Pipeline.from_pretrained(diar_name, token=token)
    if pipe is None:
        raise RuntimeError("failed to load pyannote pipeline; check HF_TOKEN and model access")
    # Unset means "keep what the pretrained config says".
    if os.getenv("DIAR_SEGMENTATION_BATCH"):
        pipe.segmentation_batch_size = int(os.environ["DIAR_SEGMENTATION_BATCH"])
    if os.getenv("DIAR_EMBEDDING_BATCH"):
        pipe.embedding_batch_size = int(os.environ["DIAR_EMBEDDING_BATCH"])
    # Sliding-window step as a share of the window. Embeddings dominate diarization
    # time and scale with 1/step, so 0.2 is about twice as fast as the default 0.1.
    if os.getenv("DIAR_SEGMENTATION_STEP"):
        pipe._segmentation.step = float(os.environ["DIAR_SEGMENTATION_STEP"]) * pipe._segmentation.duration
    pipe.to(device)
    diarization = pipe

    logger.info("loading GigaAM model=%s device=%s", asr_name, device)
    asr_model = gigaam.load_model(asr_name, device=device)
    logger.info("models ready")


def _reset_peak() -> None:
    import torch

    if torch.cuda.is_available():
        torch.cuda.reset_peak_memory_stats()


def _peak_mib() -> float:
    import torch

    if not torch.cuda.is_available():
        return 0.0
    return torch.cuda.max_memory_allocated() / (1 << 20)


def _recognize(
    wav: npt.NDArray[np.float32], chunks: list[tuple[float, float]], batch_size: int
) -> list[Word]:
    """Run GigaAM over chunks in batches and return words on the recording's timeline."""
    import torch
    from gigaam.utils import AudioDataset

    device = next(asr_model.parameters()).device
    audio = torch.from_numpy(wav)
    spans = [(int(start * SAMPLE_RATE), int(end * SAMPLE_RATE)) for start, end in chunks]
    spans = [span for span in spans if span[1] - span[0] >= MIN_CHUNK_SAMPLES]
    # Similar lengths share a batch, so little of it is padding. Longest first,
    # so running out of memory shows up on the first batch.
    spans.sort(key=lambda span: span[0] - span[1])

    words: list[Word] = []
    with torch.inference_mode():
        for k in range(0, len(spans), batch_size):
            batch = spans[k : k + batch_size]
            wav_pad, wav_lens = AudioDataset.collate([audio[a:b] for a, b in batch])
            wav_pad, wav_lens = wav_pad.to(device), wav_lens.to(device)
            encoded, encoded_len = asr_model.forward(wav_pad, wav_lens)
            decoded = asr_model._decode(encoded, encoded_len, wav_lens, word_timestamps=True)
            for (a, _), (_, found) in zip(batch, decoded):
                offset = a / SAMPLE_RATE
                words.extend(Word(offset + w.start, offset + w.end, w.text) for w in found or [])
    words.sort(key=lambda w: w.start)
    return words


def transcribe_pcm(pcm: npt.NDArray[np.int16], metrics: dict | None = None) -> dict:
    if diarization is None or asr_model is None:
        raise RuntimeError("models not loaded")

    import torch

    started = time.perf_counter()
    speakers = _env_int("DIAR_SPEAKERS", 2)
    kwargs = {}
    if speakers is not None:
        kwargs["min_speakers"] = 1
        kwargs["max_speakers"] = speakers

    duration = len(pcm) / SAMPLE_RATE
    wav = pcm_to_float(pcm)

    _reset_peak()
    output = diarization(
        {"waveform": torch.from_numpy(wav).unsqueeze(0), "sample_rate": SAMPLE_RATE, "uri": "audio"},
        **kwargs,
    )
    turns = turns_from_output(output)
    diar_seconds = time.perf_counter() - started
    diar_peak = _peak_mib()
    chunks = speech_chunks(turns, frame_energy(pcm, SAMPLE_RATE), duration)
    logger.info(
        "diarization turns=%d chunks=%d speakers=%s dt=%.2fs",
        len(turns),
        len(chunks),
        speakers,
        diar_seconds,
    )

    _reset_peak()
    asr_started = time.perf_counter()
    words = _recognize(wav, chunks, _env_int("ASR_BATCH_SIZE", 16) or 16)
    asr_seconds = time.perf_counter() - asr_started

    phrases = remap_speakers(group_phrases(assign_speakers(words, turns)))
    items = [
        {"text": p.text, "start": round(p.start, 3), "end": round(p.end, 3), "speaker": p.speaker}
        for p in phrases
    ]
    total_seconds = time.perf_counter() - started
    logger.info(
        "asr words=%d phrases=%d asr=%.2fs total=%.2fs audio=%.0fs",
        len(words),
        len(items),
        asr_seconds,
        total_seconds,
        duration,
    )
    if metrics is not None:
        metrics.update(
            audio_seconds=duration,
            diarization_seconds=diar_seconds,
            asr_seconds=asr_seconds,
            total_seconds=total_seconds,
            diarization_peak_mib=diar_peak,
            asr_peak_mib=_peak_mib(),
            raw_turns=len(turns),
            asr_chunks=len(chunks),
            phrases=len(items),
        )
    return {"text": " ".join(item["text"] for item in items), "phrases": items}
