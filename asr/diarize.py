from __future__ import annotations

import os
from dataclasses import dataclass

import numpy as np
import numpy.typing as npt

SPEAKER_DOCTOR = "doctor"
SPEAKER_PATIENT = "patient"
DIAR_SAMPLE_RATE = 16000


@dataclass(frozen=True)
class DiarConfig:
    enabled: bool
    emb_model: str
    num_speakers: int
    num_threads: int
    min_embed_ms: int

    @classmethod
    def from_env(cls) -> DiarConfig:
        return cls(
            enabled=_env_bool("DIAR_ENABLED", True),
            emb_model=os.getenv("DIAR_EMB_MODEL", "/models/speaker_embedding.onnx"),
            num_speakers=max(1, _env_int("DIAR_SPEAKERS", 2)),
            num_threads=max(1, _env_int("DIAR_THREADS", 2)),
            min_embed_ms=max(200, _env_int("DIAR_MIN_EMBED_MS", 400)),
        )


class SpeakerDiarizer:
    def __init__(self, cfg: DiarConfig):
        import sherpa_onnx

        config = sherpa_onnx.SpeakerEmbeddingExtractorConfig(
            model=cfg.emb_model,
            num_threads=cfg.num_threads,
            provider="cpu",
        )
        if not config.validate():
            raise RuntimeError("diarization config is invalid; check embedding model path")
        self._extractor = sherpa_onnx.SpeakerEmbeddingExtractor(config)
        self.min_embed_s = cfg.min_embed_ms / 1000.0
        self.num_speakers = cfg.num_speakers
        self.sample_rate = DIAR_SAMPLE_RATE

    def embed(self, wav: npt.NDArray[np.float32]) -> npt.NDArray[np.float32] | None:
        if wav.size < int(self.min_embed_s * self.sample_rate):
            return None
        stream = self._extractor.create_stream()
        stream.accept_waveform(self.sample_rate, np.ascontiguousarray(wav, dtype=np.float32))
        stream.input_finished()
        if not self._extractor.is_ready(stream):
            return None
        return np.asarray(self._extractor.compute(stream), dtype=np.float32)

    def label_phrases(
        self,
        phrases: list[dict],
        diar_wav: npt.NDArray[np.float32],
        raw_wav: npt.NDArray[np.float32],
    ) -> None:
        embeddings: list[npt.NDArray[np.float32] | None] = []
        for phrase in phrases:
            start = float(phrase["start"])
            end = float(phrase["end"])
            embeddings.append(self.embed(_slice(diar_wav, start, end, self.sample_rate)))
        apply_embeddings(phrases, embeddings, raw_wav, self.sample_rate, self.num_speakers)


def apply_embeddings(
    phrases: list[dict],
    embeddings: list[npt.NDArray[np.float32] | None],
    raw_wav: npt.NDArray[np.float32],
    sample_rate: int,
    num_speakers: int,
) -> None:
    if not phrases:
        return

    indexed = [(i, emb) for i, emb in enumerate(embeddings) if emb is not None and emb.size]
    if not indexed:
        for phrase in phrases:
            phrase["speaker"] = SPEAKER_DOCTOR
        return

    matrix = np.stack([emb for _, emb in indexed])
    clusters = cluster_speakers(matrix, num_speakers)
    phrase_cluster: dict[int, int] = {i: int(c) for (i, _), c in zip(indexed, clusters)}

    rms_by_cluster: dict[int, list[float]] = {}
    for i, cluster in phrase_cluster.items():
        rms_by_cluster.setdefault(cluster, []).append(
            _rms(raw_wav, float(phrases[i]["start"]), float(phrases[i]["end"]), sample_rate)
        )
    means = {cid: sum(vals) / len(vals) for cid, vals in rms_by_cluster.items()}
    doctor_id = max(means, key=means.get)
    names = {cid: SPEAKER_DOCTOR if cid == doctor_id else SPEAKER_PATIENT for cid in means}

    for i, phrase in enumerate(phrases):
        cluster = phrase_cluster.get(i)
        if cluster is None:
            cluster = _nearest_cluster(i, phrases, phrase_cluster)
        phrase["speaker"] = names.get(cluster, SPEAKER_DOCTOR) if cluster is not None else SPEAKER_DOCTOR


def cluster_speakers(embeddings: npt.NDArray[np.float32], k: int) -> npt.NDArray[np.int32]:
    n = len(embeddings)
    if n == 0:
        return np.zeros(0, dtype=np.int32)
    if k <= 1 or n == 1:
        return np.zeros(n, dtype=np.int32)

    x = embeddings.astype(np.float64, copy=False)
    x = x / (np.linalg.norm(x, axis=1, keepdims=True) + 1e-8)
    sim = x @ x.T
    np.fill_diagonal(sim, 1.0)
    i, j = np.unravel_index(int(np.argmin(sim)), sim.shape)
    centers = np.stack([x[i], x[j]])

    labels = np.zeros(n, dtype=np.int32)
    for _ in range(15):
        d0 = np.linalg.norm(x - centers[0], axis=1)
        d1 = np.linalg.norm(x - centers[1], axis=1)
        new_labels = (d1 < d0).astype(np.int32)
        if np.array_equal(new_labels, labels):
            break
        labels = new_labels
        if labels.min() == labels.max():
            break
        for cid in (0, 1):
            members = x[labels == cid]
            if len(members) == 0:
                continue
            centers[cid] = members.mean(axis=0)
            centers[cid] /= np.linalg.norm(centers[cid]) + 1e-8
    return labels


def _nearest_cluster(index: int, phrases: list[dict], assigned: dict[int, int]) -> int | None:
    if not assigned:
        return None
    mid = (float(phrases[index]["start"]) + float(phrases[index]["end"])) / 2
    best_id: int | None = None
    best_dist: float | None = None
    for i, cluster in assigned.items():
        other = (float(phrases[i]["start"]) + float(phrases[i]["end"])) / 2
        dist = abs(mid - other)
        if best_dist is None or dist < best_dist:
            best_dist = dist
            best_id = cluster
    return best_id


def _slice(wav: npt.NDArray[np.float32], start: float, end: float, sample_rate: int) -> npt.NDArray[np.float32]:
    a = max(0, int(start * sample_rate))
    b = min(len(wav), int(end * sample_rate))
    if b <= a:
        return wav[0:0]
    return wav[a:b]


def _rms(wav: npt.NDArray[np.float32], start: float, end: float, sample_rate: int) -> float:
    chunk = _slice(wav, start, end, sample_rate)
    if chunk.size == 0:
        return 0.0
    values = chunk.astype(np.float64, copy=False)
    return float(np.sqrt(np.mean(np.square(values))))


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
