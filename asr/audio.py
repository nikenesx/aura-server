from __future__ import annotations

import subprocess
from pathlib import Path

import numpy as np
import numpy.typing as npt

SAMPLE_RATE = 16000


def decode_pcm16(src: Path) -> npt.NDArray[np.int16]:
    """Decode any audio ffmpeg understands to 16 kHz mono PCM, in memory."""
    cmd = [
        "ffmpeg",
        "-nostdin",
        "-hide_banner",
        "-loglevel",
        "error",
        "-i",
        str(src),
        "-vn",
        "-ac",
        "1",
        "-ar",
        str(SAMPLE_RATE),
        "-f",
        "s16le",
        "-c:a",
        "pcm_s16le",
        "-",
    ]
    run = subprocess.run(cmd, check=False, capture_output=True)
    if run.returncode != 0:
        err = run.stderr.decode(errors="replace").strip() or "ffmpeg failed"
        raise RuntimeError(f"ffmpeg: {err}")
    return np.frombuffer(run.stdout, dtype=np.int16)


def pcm_to_float(pcm: npt.NDArray[np.int16]) -> npt.NDArray[np.float32]:
    wav = pcm.astype(np.float32) / 32768.0
    return np.clip(wav, -1.0, 1.0)
