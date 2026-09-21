from __future__ import annotations

import subprocess
import tempfile
import wave
from pathlib import Path

import numpy as np
import numpy.typing as npt

SAMPLE_RATE = 16000


def prepare_wav(src: Path, dest: Path) -> None:
    cmd = [
        "ffmpeg",
        "-hide_banner",
        "-loglevel",
        "error",
        "-y",
        "-i",
        str(src),
        "-vn",
        "-ac",
        "1",
        "-ar",
        str(SAMPLE_RATE),
        "-c:a",
        "pcm_s16le",
        str(dest),
    ]
    run = subprocess.run(cmd, check=False, capture_output=True, text=True)
    if run.returncode != 0:
        err = (run.stderr or run.stdout or "ffmpeg failed").strip()
        raise RuntimeError(f"ffmpeg: {err}")


def pcm_to_float(pcm: npt.NDArray[np.int16]) -> npt.NDArray[np.float32]:
    wav = pcm.astype(np.float32) / 32768.0
    return np.clip(wav, -1.0, 1.0)


def load_pcm16(path: Path) -> npt.NDArray[np.int16]:
    with wave.open(str(path), "rb") as wf:
        if wf.getnchannels() != 1:
            raise ValueError("expected mono wav")
        if wf.getsampwidth() != 2:
            raise ValueError("expected 16-bit pcm wav")
        return np.frombuffer(wf.readframes(wf.getnframes()), dtype=np.int16)


def write_pcm16(path: Path, pcm: npt.NDArray[np.int16]) -> None:
    with wave.open(str(path), "wb") as wf:
        wf.setnchannels(1)
        wf.setsampwidth(2)
        wf.setframerate(SAMPLE_RATE)
        wf.writeframes(np.ascontiguousarray(pcm).tobytes())


def slice_seconds(pcm: npt.NDArray[np.int16], start: float, end: float) -> npt.NDArray[np.int16]:
    a = max(0, int(round(start * SAMPLE_RATE)))
    b = min(len(pcm), int(round(end * SAMPLE_RATE)))
    if b <= a:
        return pcm[0:0]
    return pcm[a:b]


def with_temp_wav(pcm: npt.NDArray[np.int16]) -> Path:
    tmp = tempfile.NamedTemporaryFile(suffix=".wav", delete=False)
    path = Path(tmp.name)
    tmp.close()
    write_pcm16(path, pcm)
    return path
