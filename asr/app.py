from __future__ import annotations

import asyncio
import hashlib
import logging
import os
import tempfile
import threading
from contextlib import asynccontextmanager, contextmanager
from pathlib import Path

from fastapi import FastAPI, HTTPException, Request

from audio import prepare_wav
from pipeline import load_models, transcribe_file

logging.basicConfig(
    level=os.getenv("LOG_LEVEL", "info").upper(),
    format="%(asctime)s %(levelname)s %(name)s: %(message)s",
)
logger = logging.getLogger("aura-asr")

load_error: str | None = None
ready = False
process_lock = threading.Lock()


def _load() -> None:
    global load_error, ready
    try:
        load_models()
        load_error = None
        ready = True
        logger.info("asr ready")
    except Exception as exc:
        load_error = str(exc)
        ready = False
        logger.exception("failed to load models")


@contextmanager
def temp_upload(suffix: str):
    tmp = tempfile.TemporaryDirectory(prefix="aura-asr-")
    folder = Path(tmp.name)
    src = folder / f"upload{suffix or '.audio'}"
    wav = folder / "ready.wav"
    try:
        yield src, wav
    finally:
        tmp.cleanup()


@asynccontextmanager
async def lifespan(_app: FastAPI):
    thread = threading.Thread(target=_load, name="load-models", daemon=True)
    thread.start()
    yield


app = FastAPI(title="aura-asr", lifespan=lifespan)


@app.get("/health")
def health():
    if load_error:
        raise HTTPException(status_code=503, detail=load_error)
    if not ready:
        raise HTTPException(status_code=503, detail="model not loaded")
    return {"status": "ok"}


@app.post("/transcribe")
async def transcribe(request: Request):
    if load_error:
        raise HTTPException(status_code=503, detail=load_error)
    if not ready:
        raise HTTPException(status_code=503, detail="model not loaded")

    filename = request.headers.get("x-filename") or "audio"
    suffix = Path(filename).suffix or ".audio"
    contents = await request.body()
    if not contents:
        raise HTTPException(status_code=400, detail="empty audio file")

    if not process_lock.acquire(blocking=False):
        raise HTTPException(status_code=429, detail="asr is busy")
    try:
        return await asyncio.to_thread(_transcribe_bytes, filename, suffix, contents)
    except HTTPException:
        raise
    except Exception as exc:
        logger.exception("transcription failed")
        raise HTTPException(status_code=500, detail=str(exc)) from exc
    finally:
        process_lock.release()


def _transcribe_bytes(filename: str, suffix: str, contents: bytes) -> dict:
    with temp_upload(suffix) as (src, wav):
        src.write_bytes(contents)
        logger.info(
            "asr request filename=%s bytes=%d sha256=%s",
            filename,
            len(contents),
            hashlib.sha256(contents).hexdigest()[:12],
        )
        prepare_wav(src, wav)
        return transcribe_file(wav)
