"""Benchmark the ASR pipeline on one recording.

    python bench.py recording.m4a --runs 2 --json before.json --transcript before.txt

The first run includes CUDA warm-up; compare the later ones. --tile-minutes
repeats the input up to the given length: good enough for timing, meaningless
for quality.
"""
from __future__ import annotations

import argparse
import json
import logging
import time
from pathlib import Path

import numpy as np

from audio import SAMPLE_RATE, decode_pcm16
import pipeline


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("audio", type=Path)
    parser.add_argument("--runs", type=int, default=2)
    parser.add_argument("--tile-minutes", type=float, default=0.0)
    parser.add_argument("--json", type=Path, help="write per-run metrics here")
    parser.add_argument("--transcript", type=Path, help="write the last transcript here")
    parser.add_argument("--result", type=Path, help="write the last result JSON here, for evaluate.py")
    args = parser.parse_args()

    logging.basicConfig(level=logging.WARNING, format="%(asctime)s %(levelname)s %(name)s: %(message)s")

    import torch

    started = time.perf_counter()
    pcm = decode_pcm16(args.audio)
    ffmpeg_seconds = time.perf_counter() - started
    if args.tile_minutes > 0:
        samples = int(args.tile_minutes * 60 * SAMPLE_RATE)
        pcm = np.tile(pcm, -(-samples // len(pcm)))[:samples]

    started = time.perf_counter()
    pipeline.load_models()
    load_seconds = time.perf_counter() - started

    gpu = torch.cuda.get_device_name(0) if torch.cuda.is_available() else "cpu"
    print(f"device={gpu} ffmpeg={ffmpeg_seconds:.2f}s load={load_seconds:.1f}s")

    runs = []
    result = None
    for i in range(args.runs):
        metrics: dict = {}
        result = pipeline.transcribe_pcm(pcm, metrics=metrics)
        metrics["run"] = i + 1
        runs.append(metrics)
        print(_format(metrics))

    if args.json:
        args.json.write_text(
            json.dumps({"device": gpu, "ffmpeg_seconds": ffmpeg_seconds, "runs": runs}, indent=2),
            encoding="utf-8",
        )
    if args.result and result is not None:
        args.result.write_text(json.dumps(result, ensure_ascii=False, indent=1), encoding="utf-8")
    if args.transcript and result is not None:
        lines = [f"[{p['start']:8.2f}-{p['end']:8.2f}] {p['speaker']}: {p['text']}" for p in result["phrases"]]
        args.transcript.write_text("\n".join(lines) + "\n", encoding="utf-8")


def _format(m: dict) -> str:
    audio = m["audio_seconds"] or 1.0
    return (
        f"run {m['run']}: audio={m['audio_seconds'] / 60:.1f}min "
        f"diar={m['diarization_seconds']:.2f}s asr={m['asr_seconds']:.2f}s "
        f"total={m['total_seconds']:.2f}s rtf={m['total_seconds'] / audio:.4f} "
        f"peak diar={m['diarization_peak_mib']:.0f}MiB asr={m['asr_peak_mib']:.0f}MiB "
        f"raw_turns={m['raw_turns']} chunks={m['asr_chunks']} phrases={m['phrases']}"
    )


if __name__ == "__main__":
    main()
