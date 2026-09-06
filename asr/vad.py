from __future__ import annotations

import os
from dataclasses import dataclass

import numpy as np
import numpy.typing as npt

SAMPLE_RATE = 8000
WINDOW_SAMPLES = 256  # 32 ms at 8 kHz
CONTEXT_SAMPLES = 32
STATE_SHAPE = (2, 1, 128)
MIN_TONE_SAMPLES = 2400  # T-one chunk is 300 ms


@dataclass(frozen=True)
class VADConfig:
    enabled: bool
    model_path: str
    threshold: float
    min_speech_ms: int
    min_silence_ms: int
    pad_ms: int

    @classmethod
    def from_env(cls) -> VADConfig:
        return cls(
            enabled=_env_bool("VAD_ENABLED", True),
            model_path=os.getenv("VAD_MODEL", "/models/silero_vad.onnx"),
            threshold=_env_float("VAD_THRESHOLD", 0.5),
            min_speech_ms=_env_int("VAD_MIN_SPEECH_MS", 300),
            min_silence_ms=_env_int("VAD_MIN_SILENCE_MS", 400),
            pad_ms=_env_int("VAD_PAD_MS", 300),
        )


class SileroVAD:
    def __init__(self, model_path: str):
        import onnxruntime as ort

        opts = ort.SessionOptions()
        opts.intra_op_num_threads = 1
        opts.inter_op_num_threads = 1
        self.session = ort.InferenceSession(
            model_path,
            sess_options=opts,
            providers=["CPUExecutionProvider"],
        )
        self.reset()

    def reset(self) -> None:
        self._state = np.zeros(STATE_SHAPE, dtype=np.float32)
        self._context = np.zeros((1, CONTEXT_SAMPLES), dtype=np.float32)

    def probability(self, chunk: npt.NDArray[np.float32]) -> float:
        if chunk.shape != (WINDOW_SAMPLES,):
            raise ValueError(f"expected {WINDOW_SAMPLES} samples, got {chunk.shape}")
        x = np.concatenate([self._context, chunk.reshape(1, -1)], axis=1)
        out, state = self.session.run(
            None,
            {
                "input": x.astype(np.float32, copy=False),
                "state": self._state,
                "sr": np.array(SAMPLE_RATE, dtype=np.int64),
            },
        )
        self._state = state
        self._context = x[:, -CONTEXT_SAMPLES:]
        return float(np.asarray(out).reshape(-1)[0])


def pcm_to_float(pcm: npt.NDArray[np.int32]) -> npt.NDArray[np.float32]:
    wav = pcm.astype(np.float32) / 32768.0
    return np.clip(wav, -1.0, 1.0)


def speech_probs(wav: npt.NDArray[np.float32], model: SileroVAD) -> list[float]:
    model.reset()
    probs: list[float] = []
    for start in range(0, len(wav), WINDOW_SAMPLES):
        chunk = wav[start : start + WINDOW_SAMPLES]
        if len(chunk) < WINDOW_SAMPLES:
            chunk = np.pad(chunk, (0, WINDOW_SAMPLES - len(chunk)))
        probs.append(model.probability(chunk))
    return probs


def segments_from_probs(
    probs: list[float],
    audio_len: int,
    *,
    threshold: float,
    min_speech: int,
    min_silence: int,
    pad: int,
) -> list[tuple[int, int]]:
    if not probs or audio_len <= 0:
        return []

    neg_threshold = max(threshold - 0.15, 0.01)
    triggered = False
    start = 0
    temp_end = 0
    raw: list[tuple[int, int]] = []

    for i, prob in enumerate(probs):
        cur = i * WINDOW_SAMPLES
        if prob >= threshold and not triggered:
            triggered = True
            start = cur
            temp_end = 0
            continue
        if triggered and prob >= threshold:
            temp_end = 0
            continue
        if triggered and prob < neg_threshold:
            if not temp_end:
                temp_end = cur
            if cur - temp_end < min_silence:
                continue
            end = temp_end
            if end - start >= min_speech:
                raw.append((start, end))
            triggered = False
            temp_end = 0

    if triggered and audio_len - start >= min_speech:
        raw.append((start, audio_len))

    padded: list[tuple[int, int]] = []
    for seg_start, seg_end in raw:
        padded.append((max(0, seg_start - pad), min(audio_len, seg_end + pad)))
    return merge_segments(padded)


def merge_segments(segments: list[tuple[int, int]]) -> list[tuple[int, int]]:
    if not segments:
        return []
    ordered = sorted(segments)
    merged = [ordered[0]]
    for start, end in ordered[1:]:
        prev_start, prev_end = merged[-1]
        if start <= prev_end:
            merged[-1] = (prev_start, max(prev_end, end))
        else:
            merged.append((start, end))
    return merged


def speech_segments(pcm: npt.NDArray[np.int32], model: SileroVAD, cfg: VADConfig) -> list[tuple[int, int]]:
    wav = pcm_to_float(pcm)
    probs = speech_probs(wav, model)
    return segments_from_probs(
        probs,
        len(pcm),
        threshold=cfg.threshold,
        min_speech=_ms_to_samples(cfg.min_speech_ms),
        min_silence=_ms_to_samples(cfg.min_silence_ms),
        pad=_ms_to_samples(cfg.pad_ms),
    )


def pad_for_tone(chunk: npt.NDArray[np.int32]) -> npt.NDArray[np.int32]:
    if len(chunk) >= MIN_TONE_SAMPLES:
        return chunk
    return np.pad(chunk, (0, MIN_TONE_SAMPLES - len(chunk)))


def _ms_to_samples(ms: int) -> int:
    return max(0, int(SAMPLE_RATE * ms / 1000))


def _env_bool(name: str, default: bool) -> bool:
    raw = os.getenv(name)
    if raw is None or raw.strip() == "":
        return default
    return raw.strip().lower() in {"1", "true", "yes", "on"}


def _env_int(name: str, default: int) -> int:
    raw = os.getenv(name)
    if raw is None or raw.strip() == "":
        return default
    return int(raw)


def _env_float(name: str, default: float) -> float:
    raw = os.getenv(name)
    if raw is None or raw.strip() == "":
        return default
    return float(raw)
