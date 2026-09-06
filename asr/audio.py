from __future__ import annotations

import os
import subprocess
import tempfile
from dataclasses import dataclass
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

DIAR_SAMPLE_RATE = 16000
TONE_SAMPLE_RATE = 8000


@dataclass(frozen=True)
class PreparedAudio:
    tone: npt.NDArray[np.int32]
    diar: npt.NDArray[np.float32]
    raw: npt.NDArray[np.float32]


def agc_filter() -> str | None:
    raw = os.getenv("AGC_ENABLED", "true")
    if raw.strip().lower() in {"0", "false", "no", "off"}:
        return None
    custom = os.getenv("AGC_FILTER", "").strip()
    return custom or DEFAULT_AGC_FILTER


def prepare_audio(src: Path) -> PreparedAudio:
    with tempfile.TemporaryDirectory() as tmp:
        raw16 = Path(tmp) / "raw16.wav"
        diar16 = Path(tmp) / "diar16.wav"
        tone8 = Path(tmp) / "tone.wav"
        filt = agc_filter()
        if filt:
            filter_complex = (
                f"[0:a]aformat=channel_layouts=mono,asplit=2[raw][proc];"
                f"[raw]aresample={DIAR_SAMPLE_RATE}[a16raw];"
                f"[proc]{filt},asplit=2[ag][tn];"
                f"[ag]aresample={DIAR_SAMPLE_RATE}[a16agc];"
                f"[tn]aresample={TONE_SAMPLE_RATE}[a8]"
            )
            cmd = _ffmpeg_base(src, filter_complex) + [
                "-map",
                "[a16raw]",
                "-c:a",
                "pcm_s16le",
                str(raw16),
                "-map",
                "[a16agc]",
                "-c:a",
                "pcm_s16le",
                str(diar16),
                "-map",
                "[a8]",
                "-c:a",
                "pcm_s16le",
                str(tone8),
            ]
        else:
            filter_complex = (
                f"[0:a]aformat=channel_layouts=mono,asplit=2[raw][proc];"
                f"[raw]aresample={DIAR_SAMPLE_RATE}[a16];"
                f"[proc]aresample={TONE_SAMPLE_RATE}[a8]"
            )
            cmd = _ffmpeg_base(src, filter_complex) + [
                "-map",
                "[a16]",
                "-c:a",
                "pcm_s16le",
                str(raw16),
                "-map",
                "[a8]",
                "-c:a",
                "pcm_s16le",
                str(tone8),
            ]
        run = subprocess.run(cmd, check=False, capture_output=True, text=True)
        if run.returncode != 0:
            err = (run.stderr or run.stdout or "ffmpeg failed").strip()
            raise RuntimeError(f"ffmpeg: {err}")
        raw = pcm_to_float(load_pcm16_mono(raw16))
        tone = load_pcm16_mono(tone8)
        diar = pcm_to_float(load_pcm16_mono(diar16)) if filt else raw
    return PreparedAudio(tone=tone, diar=diar, raw=raw)


def _ffmpeg_base(src: Path, filter_complex: str) -> list[str]:
    return [
        "ffmpeg",
        "-hide_banner",
        "-loglevel",
        "error",
        "-y",
        "-i",
        str(src),
        "-vn",
        "-filter_complex",
        filter_complex,
    ]


def to_tone_pcm(src: Path) -> npt.NDArray[np.int32]:
    return prepare_audio(src).tone


def load_pcm16_mono(path: Path) -> npt.NDArray[np.int32]:
    import wave

    with wave.open(str(path), "rb") as wf:
        if wf.getnchannels() != 1:
            raise ValueError("expected mono wav")
        if wf.getsampwidth() != 2:
            raise ValueError("expected 16-bit pcm wav")
        pcm = np.frombuffer(wf.readframes(wf.getnframes()), dtype=np.int16)
    return pcm.astype(np.int32)


def pcm_to_float(pcm: npt.NDArray[np.int32]) -> npt.NDArray[np.float32]:
    wav = pcm.astype(np.float32) / 32768.0
    return np.clip(wav, -1.0, 1.0)
