from __future__ import annotations

import subprocess
import tempfile
import wave
from pathlib import Path

import numpy as np
import numpy.typing as npt


def to_tone_pcm(src: Path) -> npt.NDArray[np.int32]:
    with tempfile.NamedTemporaryFile(suffix=".wav", delete=False) as tmp:
        dst = Path(tmp.name)
    try:
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
            "8000",
            "-c:a",
            "pcm_s16le",
            str(dst),
        ]
        proc = subprocess.run(cmd, check=False, capture_output=True, text=True)
        if proc.returncode != 0:
            err = (proc.stderr or proc.stdout or "ffmpeg failed").strip()
            raise RuntimeError(f"ffmpeg: {err}")
        return load_pcm16_mono(dst)
    finally:
        dst.unlink(missing_ok=True)


def load_pcm16_mono(path: Path) -> npt.NDArray[np.int32]:
    with wave.open(str(path), "rb") as wf:
        if wf.getnchannels() != 1:
            raise ValueError("expected mono wav")
        if wf.getsampwidth() != 2:
            raise ValueError("expected 16-bit pcm wav")
        pcm = np.frombuffer(wf.readframes(wf.getnframes()), dtype=np.int16)
    return pcm.astype(np.int32)
