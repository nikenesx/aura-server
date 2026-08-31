from __future__ import annotations

import logging
import os
import tempfile
import threading
from contextlib import asynccontextmanager, contextmanager
from pathlib import Path

from fastapi import FastAPI, HTTPException, Request
from tone import StreamingCTCPipeline
from tone.pipeline import TextPhrase

from audio import to_tone_pcm

logging.basicConfig(
    level=os.getenv("LOG_LEVEL", "info").upper(),
    format="%(asctime)s %(levelname)s %(name)s: %(message)s",
)
logger = logging.getLogger("aura-asr")

pipeline: StreamingCTCPipeline | None = None
load_error: str | None = None
process_lock = threading.Lock()


def load_pipeline() -> StreamingCTCPipeline:
    folder = os.getenv("LOAD_FROM_FOLDER", "/models")
    model = Path(folder) / "model.onnx"
    if model.is_file():
        logger.info("loading T-one from %s", folder)
        return StreamingCTCPipeline.from_local(folder)
    logger.info("loading T-one from Hugging Face")
    return StreamingCTCPipeline.from_hugging_face()


def _load() -> None:
    global pipeline, load_error
    try:
        pipeline = load_pipeline()
        load_error = None
        logger.info("T-one ready")
    except Exception as exc:
        load_error = str(exc)
        logger.exception("failed to load T-one")


def phrases_to_result(phrases: list[TextPhrase]) -> dict:
    items = []
    parts: list[str] = []
    for phrase in phrases:
        text = (phrase.text or "").strip()
        if not text:
            continue
        items.append(
            {
                "text": text,
                "start": round(float(phrase.start_time), 3),
                "end": round(float(phrase.end_time), 3),
            }
        )
        parts.append(text)
    return {"text": " ".join(parts), "phrases": items}


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
            audio = to_tone_pcm(src)
            phrases = pipeline.forward_offline(audio)
            return phrases_to_result(phrases)
    except HTTPException:
        raise
    except Exception as exc:
        logger.exception("transcription failed")
        raise HTTPException(status_code=500, detail=str(exc)) from exc
    finally:
        process_lock.release()
