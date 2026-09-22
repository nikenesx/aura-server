"""Score a transcript against a hand-labelled reference.

    python evaluate.py reference.json result.json

Both files hold {"segments" or "phrases": [{"start", "end", "speaker", "text"}]};
result.json is what bench.py --result writes (the API result format).

WER   - recognition errors, speakers ignored.
cpWER - words compared per speaker after mapping hypothesis speakers onto the
        reference, so words given to the wrong speaker count as errors too.
DER   - diarization error rate with a 0.25 s collar.

Text is lowercased, ё folded to е and punctuation dropped. Numbers are not
normalised: "пять" against "5" is an error, so keep references in the same
style the model writes.
"""
from __future__ import annotations

import argparse
import json
import re
from pathlib import Path

from pyannote.core import Annotation, Segment
from pyannote.metrics.diarization import DiarizationErrorRate

_PUNCT = re.compile(r"[^\w\s]", re.UNICODE)


def words(text: str) -> list[str]:
    return _PUNCT.sub(" ", text.lower().replace("ё", "е")).split()


def edit_distance(ref: list[str], hyp: list[str]) -> int:
    prev = list(range(len(hyp) + 1))
    for i, r in enumerate(ref, 1):
        cur = [i] + [0] * len(hyp)
        for j, h in enumerate(hyp, 1):
            cur[j] = min(prev[j] + 1, cur[j - 1] + 1, prev[j - 1] + (r != h))
        prev = cur
    return prev[-1]


def load(path: Path) -> list[dict]:
    data = json.loads(path.read_text(encoding="utf-8"))
    data = data.get("result", data)
    return sorted(data.get("segments") or data.get("phrases") or [], key=lambda s: s["start"])


def annotation(items: list[dict]) -> Annotation:
    ann = Annotation()
    for i, item in enumerate(items):
        ann[Segment(item["start"], item["end"]), i] = item["speaker"]
    return ann


def score(ref: list[dict], hyp: list[dict]) -> dict:
    ref_words = [w for s in ref for w in words(s["text"])]
    hyp_words = [w for s in hyp for w in words(s["text"])]
    wer = edit_distance(ref_words, hyp_words) / max(1, len(ref_words))

    metric = DiarizationErrorRate(collar=0.25)
    ref_ann, hyp_ann = annotation(ref), annotation(hyp)
    der = metric(ref_ann, hyp_ann)
    mapping = metric.optimal_mapping(ref_ann, hyp_ann)

    errors = 0
    for speaker in {s["speaker"] for s in ref} | {mapping.get(s["speaker"], s["speaker"]) for s in hyp}:
        r = [w for s in ref if s["speaker"] == speaker for w in words(s["text"])]
        h = [w for s in hyp if mapping.get(s["speaker"], s["speaker"]) == speaker for w in words(s["text"])]
        errors += edit_distance(r, h)
    cpwer = errors / max(1, len(ref_words))

    return {"wer": wer, "cpwer": cpwer, "der": der, "ref_words": len(ref_words), "hyp_words": len(hyp_words)}


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("reference", type=Path)
    parser.add_argument("result", type=Path)
    args = parser.parse_args()
    s = score(load(args.reference), load(args.result))
    print(
        f"WER={s['wer']:.2%} cpWER={s['cpwer']:.2%} DER={s['der']:.2%} "
        f"ref_words={s['ref_words']} hyp_words={s['hyp_words']}"
    )


if __name__ == "__main__":
    main()
