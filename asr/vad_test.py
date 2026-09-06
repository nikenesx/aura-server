from __future__ import annotations

import unittest

from vad import WINDOW_SAMPLES, merge_segments, segments_from_probs


class SegmentsTest(unittest.TestCase):
    def test_keeps_speech_and_drops_silence(self):
        # 32 ms windows: silence, speech, silence, speech, silence
        probs = [0.05] * 4 + [0.9] * 20 + [0.05] * 20 + [0.9] * 16 + [0.05] * 8
        audio_len = len(probs) * WINDOW_SAMPLES
        segs = segments_from_probs(
            probs,
            audio_len,
            threshold=0.5,
            min_speech=WINDOW_SAMPLES,
            min_silence=3 * WINDOW_SAMPLES,
            pad=WINDOW_SAMPLES,
        )
        self.assertEqual(len(segs), 2)
        self.assertLess(segs[0][0], 4 * WINDOW_SAMPLES)
        self.assertGreater(segs[0][1], 24 * WINDOW_SAMPLES)
        self.assertGreater(segs[1][0], segs[0][1])

    def test_empty_when_only_silence(self):
        probs = [0.02] * 30
        segs = segments_from_probs(
            probs,
            len(probs) * WINDOW_SAMPLES,
            threshold=0.5,
            min_speech=WINDOW_SAMPLES,
            min_silence=WINDOW_SAMPLES,
            pad=0,
        )
        self.assertEqual(segs, [])

    def test_merge_overlaps(self):
        self.assertEqual(merge_segments([(0, 10), (8, 20), (30, 40)]), [(0, 20), (30, 40)])


if __name__ == "__main__":
    unittest.main()
