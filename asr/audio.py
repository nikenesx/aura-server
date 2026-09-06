from __future__ import annotations

import os
import subprocess
import tempfile
import wave
from pathlib import Path

import numpy as np
import numpy.typing as npt

# High-pass rumble, then even out doctor vs distant patient before 8 kHz.
# dynaudnorm boosts quiet frames without clipping loud ones; alimiter is a safety cap.
DEFAULT_AGC_FILTER = (
    "highpass=f=80,"
    "dynaudnorm=f=250:g=11:p=0.95:m=20:s=8:r=0.1:b=1:t=0.002,"
    "alimiter=limit=0.95"
)


def agc_filter() -> str | None:
    raw = os.getenv("AGC_ENABLED", "true")
    if raw.strip().lower() in {"0", "false", "no", "off"}:
        return None
    custom = os.getenv("AGC_FILTER", "").strip()
    return custom or DEFAULT_AGC_FILTER


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
        ]
        filt = agc_filter()
        if filt:
            cmd.extend(["-af", filt])
        cmd.extend(
            [
                "-ac",
                "1",
                "-ar",
                "8000",
                "-c:a",
                "pcm_s16le",
                str(dst),
            ]
        )
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
