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


WAVEFORM_POINTS = 1000


def waveform_peaks(pcm: npt.NDArray[np.int16], points: int = WAVEFORM_POINTS) -> list[float]:
    """Loudness peaks across the recording, 0..1, for drawing the player's waveform.

    Computed here because the PCM is already decoded; a client would have to
    decode an hour of audio to draw the same thing. Scaled to the loudest
    peak, so a quiet recording still shows its shape.
    """
    if len(pcm) == 0:
        return []
    points = min(points, len(pcm))
    step = len(pcm) // points
    # A view, not a copy: an hour of PCM is 115 MB.
    view = pcm[: step * points].reshape(points, step)
    peaks = np.maximum(view.max(axis=1).astype(np.float32), -view.min(axis=1).astype(np.float32))
    top = peaks.max()
    if top > 0:
        peaks /= top
    return [round(float(v), 3) for v in peaks]
