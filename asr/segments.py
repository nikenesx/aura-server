from __future__ import annotations

import bisect
from dataclasses import dataclass, replace

import numpy as np
import numpy.typing as npt

CHUNK_MAX_SECONDS = 22.0
CHUNK_MIN_SECONDS = 15.0
CHUNK_HARD_SECONDS = 25.0
CHUNK_PAD_SECONDS = 0.2
PHRASE_GAP_SECONDS = 1.0
PHRASE_MAX_SECONDS = 30.0
ENERGY_FRAME_SECONDS = 0.01

_SENTENCE_END = (".", "?", "!", "…")


@dataclass(frozen=True)
class Turn:
    start: float
    end: float
    speaker: str


@dataclass(frozen=True)
class Word:
    start: float
    end: float
    text: str


@dataclass(frozen=True)
class Phrase:
    start: float
    end: float
    speaker: str
    text: str


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


def remap_speakers(items: list) -> list:
    """Rename speakers to speaker_0, speaker_1, ... in order of first appearance."""
    order: list[str] = []
    for item in sorted(items, key=lambda it: (it.start, it.end)):
        if item.speaker not in order:
            order.append(item.speaker)
    names = {raw: f"speaker_{i}" for i, raw in enumerate(order)}
    return [replace(item, speaker=names[item.speaker]) for item in items]


def frame_energy(pcm: npt.NDArray[np.int16], sample_rate: int) -> npt.NDArray[np.float32]:
    frame = int(sample_rate * ENERGY_FRAME_SECONDS)
    n = len(pcm) // frame
    if n == 0:
        return np.zeros(0, dtype=np.float32)
    frames = pcm[: n * frame].astype(np.float32).reshape(n, frame)
    return np.sqrt((frames**2).mean(axis=1))


def _quietest(energy: npt.NDArray[np.float32], lo: float, hi: float) -> float:
    a = int(lo / ENERGY_FRAME_SECONDS)
    b = min(len(energy), int(hi / ENERGY_FRAME_SECONDS))
    if b <= a:
        return hi
    return (a + int(np.argmin(energy[a:b]))) * ENERGY_FRAME_SECONDS


def split_region(
    start: float,
    end: float,
    energy: npt.NDArray[np.float32],
    limit: float = CHUNK_HARD_SECONDS,
) -> list[tuple[float, float]]:
    """Cut speech longer than `limit` at its quietest moments, not mid-word."""
    out: list[tuple[float, float]] = []
    while end - start > limit:
        cut = _quietest(energy, start + limit * 0.5, start + limit)
        out.append((start, cut))
        start = cut
    out.append((start, end))
    return out


def speech_chunks(
    turns: list[Turn],
    energy: npt.NDArray[np.float32],
    duration: float,
    max_seconds: float = CHUNK_MAX_SECONDS,
    min_seconds: float = CHUNK_MIN_SECONDS,
    hard_seconds: float = CHUNK_HARD_SECONDS,
    pad: float = CHUNK_PAD_SECONDS,
) -> list[tuple[float, float]]:
    """Group speech into ASR chunks of one speaker each, cutting in pauses.

    GigaAM punctuates a chunk as one text, so a chunk never spans a speaker
    change. Within one speaker a chunk grows while it stays under
    `max_seconds`, and closes once it is past `min_seconds`. Chunks are
    widened by `pad` into the surrounding silence so diarization boundaries
    do not clip word edges.
    """
    pieces = [
        (start, end, turn.speaker)
        for turn in sorted(turns, key=lambda t: t.start)
        for start, end in split_region(turn.start, turn.end, energy, hard_seconds)
    ]
    grouped: list[tuple[float, float, str]] = []
    for start, end, speaker in pieces:
        if grouped:
            c_start, c_end, c_speaker = grouped[-1]
            if c_speaker == speaker and c_end - c_start < min_seconds and end - c_start <= max_seconds:
                grouped[-1] = (c_start, end, speaker)
                continue
        grouped.append((start, end, speaker))
    chunks = [(start, end) for start, end, _ in grouped]

    # Neighbours split the silence between them, so no audio lands in two chunks.
    padded: list[tuple[float, float]] = []
    for i, (start, end) in enumerate(chunks):
        lo = (chunks[i - 1][1] + start) / 2 if i > 0 else 0.0
        hi = (end + chunks[i + 1][0]) / 2 if i + 1 < len(chunks) else duration
        padded.append((max(lo, start - pad, 0.0), min(hi, end + pad, duration)))
    return padded


def assign_speakers(words: list[Word], turns: list[Turn]) -> list[tuple[Word, str]]:
    """Give each word the speaker whose turn overlaps it most, else the nearest one."""
    if not turns:
        return []
    ordered = sorted(turns, key=lambda t: t.start)
    starts = [t.start for t in ordered]
    longest = max(t.end - t.start for t in ordered)
    out: list[tuple[Word, str]] = []
    for word in words:
        # Only turns starting before the word ends and after start - longest can overlap it.
        i = bisect.bisect_left(starts, word.end)
        best, best_overlap = None, 0.0
        j = i - 1
        while j >= 0 and ordered[j].start >= word.start - longest:
            overlap = min(word.end, ordered[j].end) - max(word.start, ordered[j].start)
            if overlap > best_overlap:
                best, best_overlap = ordered[j].speaker, overlap
            j -= 1
        if best is None:
            near = [ordered[k] for k in (i - 1, i) if 0 <= k < len(ordered)]
            best = min(near, key=lambda t: max(t.start - word.end, word.start - t.end, 0.0)).speaker
        out.append((word, best))
    return out


def group_phrases(
    labelled: list[tuple[Word, str]],
    gap: float = PHRASE_GAP_SECONDS,
    max_seconds: float = PHRASE_MAX_SECONDS,
) -> list[Phrase]:
    """Join consecutive words of one speaker into phrases.

    A new phrase starts on a speaker change, after a pause longer than `gap`,
    or at a sentence end once the phrase is longer than `max_seconds`.
    """
    phrases: list[Phrase] = []
    current: list[Word] = []
    speaker = ""

    def flush() -> None:
        if current:
            text = " ".join(w.text for w in current)
            phrases.append(Phrase(current[0].start, current[-1].end, speaker, text))
            current.clear()

    for word, who in sorted(labelled, key=lambda item: item[0].start):
        if current and (
            who != speaker
            or word.start - current[-1].end > gap
            or (current[-1].end - current[0].start > max_seconds and current[-1].text.endswith(_SENTENCE_END))
        ):
            flush()
        speaker = who
        current.append(word)
    flush()
    return phrases
