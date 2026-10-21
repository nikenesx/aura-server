"""How far the current transcription has got, for GET /progress.

Stages get a share of the bar by how long they really take: on a 60-minute
recording speaker embeddings are ~80% of the time, recognition ~10%.
"""
from __future__ import annotations

import threading

# Where each stage starts and ends on the 0..1 bar.
STAGES = {
    "decoding": (0.00, 0.02),
    "segmentation": (0.02, 0.12),
    "embeddings": (0.12, 0.85),
    "clustering": (0.85, 0.88),
    "recognition": (0.88, 1.00),
}


class Progress:
    """Thread-safe progress of the one transcription in flight. The bar never moves back."""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._active = False
        self._stage = "idle"
        self._fraction = 0.0

    def start(self) -> None:
        with self._lock:
            self._active, self._stage, self._fraction = True, "decoding", 0.0

    def stage(self, name: str, done: float = 0.0) -> None:
        """Move to stage `name`, `done` (0..1) of the way through it."""
        lo, hi = STAGES[name]
        value = lo + (hi - lo) * min(max(done, 0.0), 1.0)
        with self._lock:
            if value >= self._fraction:
                self._stage, self._fraction = name, value

    def finish(self) -> None:
        with self._lock:
            self._active, self._stage, self._fraction = False, "idle", 1.0

    def snapshot(self) -> dict:
        with self._lock:
            return {"active": self._active, "stage": self._stage, "fraction": round(self._fraction, 4)}

    def diarization_hook(self):
        """A pyannote pipeline hook that reports segmentation and embedding batches."""

        def hook(step_name, step_artifact=None, file=None, total=None, completed=None):
            if step_name in ("segmentation", "embeddings"):
                if total:
                    self.stage(step_name, (completed or 0) / total)
                elif step_artifact is not None:
                    # Called once more with the result when the step is over.
                    self.stage(step_name, 1.0)
            elif step_name == "speaker_counting":
                # Comes between segmentation and embeddings, not after them.
                self.stage("segmentation", 1.0)
            elif step_name == "discrete_diarization":
                self.stage("clustering", 1.0)

        return hook
