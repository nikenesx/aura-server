from __future__ import annotations

import logging
import os
import time
from pathlib import Path

from audio import SAMPLE_RATE, load_pcm16, pcm_to_float, slice_seconds, with_temp_wav
from segments import merge_turns, remap_speakers, split_long_turns, turns_from_output

logger = logging.getLogger("aura-asr")

diarization = None
asr_model = None


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
    pipe.to(device)
    diarization = pipe

    logger.info("loading GigaAM model=%s device=%s", asr_name, device)
    asr_model = gigaam.load_model(asr_name, device=device)
    logger.info("models ready")


def transcribe_file(wav_path: Path) -> dict:
    if diarization is None or asr_model is None:
        raise RuntimeError("models not loaded")

    started = time.perf_counter()
    speakers = _env_int("DIAR_SPEAKERS", 2)
    kwargs = {}
    if speakers is not None:
        kwargs["min_speakers"] = 1
        kwargs["max_speakers"] = speakers

    import torch

    pcm = load_pcm16(wav_path)
    waveform = torch.from_numpy(pcm_to_float(pcm)).unsqueeze(0)
    output = diarization(
        {
            "waveform": waveform,
            "sample_rate": SAMPLE_RATE,
            "uri": f"{wav_path.resolve()}-{len(pcm)}-{time.time_ns()}",
        },
        **kwargs,
    )
    raw = turns_from_output(output)

    turns = split_long_turns(merge_turns(remap_speakers(raw)))
    logger.info(
        "diarization turns=%d raw=%d speakers=%s dt=%.2fs",
        len(turns),
        len(raw),
        speakers,
        time.perf_counter() - started,
    )
    items: list[dict] = []
    asr_started = time.perf_counter()
    for turn in turns:
        clip = slice_seconds(pcm, turn.start, turn.end)
        if clip.size < 1600:
            continue
        clip_path = with_temp_wav(clip)
        try:
            result = asr_model.transcribe(str(clip_path))
            text = (getattr(result, "text", None) or str(result) or "").strip()
        finally:
            clip_path.unlink(missing_ok=True)
        if not text:
            continue
        items.append(
            {
                "text": text,
                "start": round(turn.start, 3),
                "end": round(turn.end, 3),
                "speaker": turn.speaker,
            }
        )

    logger.info(
        "asr phrases=%d asr=%.2fs total=%.2fs",
        len(items),
        time.perf_counter() - asr_started,
        time.perf_counter() - started,
    )
    return {"text": " ".join(item["text"] for item in items), "phrases": items}
