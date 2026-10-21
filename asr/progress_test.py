from __future__ import annotations

import unittest

import numpy as np

from audio import waveform_peaks
from progress import STAGES, Progress


class ProgressTest(unittest.TestCase):
    def test_stages_cover_the_bar_in_order(self):
        edges = list(STAGES.values())
        self.assertEqual(edges[0][0], 0.0)
        self.assertEqual(edges[-1][1], 1.0)
        for (_, hi), (lo, _) in zip(edges, edges[1:]):
            self.assertEqual(hi, lo)

    def test_never_moves_back(self):
        p = Progress()
        p.start()
        p.stage("embeddings", 0.5)
        p.stage("segmentation", 1.0)
        self.assertEqual(p.snapshot()["stage"], "embeddings")
        self.assertAlmostEqual(p.snapshot()["fraction"], 0.485, places=3)

    def test_pyannote_hook(self):
        p = Progress()
        p.start()
        hook = p.diarization_hook()
        hook("segmentation", None, file={}, total=10, completed=5)
        self.assertAlmostEqual(p.snapshot()["fraction"], 0.07, places=3)
        # pyannote counts speakers before embeddings; that must not skip them.
        hook("speaker_counting", object(), file={})
        hook("embeddings", None, file={}, total=4, completed=2)
        self.assertAlmostEqual(p.snapshot()["fraction"], 0.485, places=3)
        hook("embeddings", None, file={}, total=4, completed=4)
        self.assertAlmostEqual(p.snapshot()["fraction"], 0.85, places=3)
        hook("discrete_diarization", object(), file={})
        self.assertAlmostEqual(p.snapshot()["fraction"], 0.88, places=3)

    def test_finish(self):
        p = Progress()
        p.start()
        p.finish()
        self.assertEqual(p.snapshot(), {"active": False, "stage": "idle", "fraction": 1.0})


class WaveformTest(unittest.TestCase):
    def test_peaks_scaled_to_loudest(self):
        pcm = np.zeros(1000, dtype=np.int16)
        pcm[100] = 1000
        pcm[900] = -32768
        peaks = waveform_peaks(pcm, points=10)
        self.assertEqual(len(peaks), 10)
        self.assertEqual(peaks[9], 1.0)
        self.assertAlmostEqual(peaks[1], 1000 / 32768, places=3)
        self.assertEqual(peaks[5], 0.0)

    def test_short_and_silent(self):
        self.assertEqual(waveform_peaks(np.zeros(0, dtype=np.int16)), [])
        self.assertEqual(waveform_peaks(np.zeros(3, dtype=np.int16), points=10), [0.0, 0.0, 0.0])


if __name__ == "__main__":
    unittest.main()
