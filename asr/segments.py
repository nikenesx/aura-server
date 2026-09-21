from __future__ import annotations

from dataclasses import dataclass

MAX_ASR_SECONDS = 25.0
MERGE_GAP_SECONDS = 0.4


@dataclass(frozen=True)
class Turn:
    start: float
    end: float
    speaker: str


def turns_from_output(output) -> list[Turn]:
    annotation = getattr(output, "exclusive_speaker_diarization", None) or getattr(
        output, "speaker_diarization", output
    )
    if hasattr(annotation, "itertracks"):
        return [
            Turn(start=float(turn.start), end=float(turn.end), speaker=str(speaker))
            for turn, _, speaker in annotation.itertracks(yield_label=True)
        ]
    raise TypeError(f"unsupported diarization output: {type(output)!r}")


def remap_speakers(turns: list[Turn]) -> list[Turn]:
    order: list[str] = []
    for turn in sorted(turns, key=lambda item: (item.start, item.end)):
        if turn.speaker not in order:
            order.append(turn.speaker)
    names = {raw: f"speaker_{i}" for i, raw in enumerate(order)}
    return [
        Turn(start=turn.start, end=turn.end, speaker=names.get(turn.speaker, turn.speaker))
        for turn in turns
    ]


def merge_turns(turns: list[Turn], gap: float = MERGE_GAP_SECONDS) -> list[Turn]:
    if not turns:
        return []
    ordered = sorted(turns, key=lambda item: (item.start, item.end, item.speaker))
    merged = [ordered[0]]
    for turn in ordered[1:]:
        prev = merged[-1]
        if turn.speaker == prev.speaker and turn.start - prev.end <= gap:
            merged[-1] = Turn(start=prev.start, end=max(prev.end, turn.end), speaker=prev.speaker)
        else:
            merged.append(turn)
    return merged


def split_long_turns(turns: list[Turn], limit: float = MAX_ASR_SECONDS) -> list[Turn]:
    if limit <= 0:
        return list(turns)
    out: list[Turn] = []
    for turn in turns:
        start = turn.start
        while turn.end - start > limit:
            out.append(Turn(start=start, end=start + limit, speaker=turn.speaker))
            start += limit
        if turn.end - start > 0.02:
            out.append(Turn(start=start, end=turn.end, speaker=turn.speaker))
    return out
