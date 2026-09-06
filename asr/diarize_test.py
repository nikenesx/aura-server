from __future__ import annotations

import unittest

import numpy as np

from diarize import SPEAKER_DOCTOR, SPEAKER_PATIENT, apply_embeddings, cluster_speakers


class ClusterTest(unittest.TestCase):
    def test_two_separated_groups(self):
        a = np.tile(np.array([1.0, 0.0, 0.0], dtype=np.float32), (3, 1))
        b = np.tile(np.array([0.0, 1.0, 0.0], dtype=np.float32), (3, 1))
        labels = cluster_speakers(np.vstack([a, b]), 2)
        self.assertEqual(len(set(labels[:3].tolist())), 1)
        self.assertEqual(len(set(labels[3:].tolist())), 1)
        self.assertNotEqual(labels[0], labels[3])

    def test_single_speaker(self):
        x = np.ones((4, 3), dtype=np.float32)
        labels = cluster_speakers(x, 1)
        self.assertTrue(np.all(labels == 0))


class ApplyEmbeddingsTest(unittest.TestCase):
    def test_louder_cluster_is_doctor(self):
        sr = 16000
        wav = np.zeros(sr * 4, dtype=np.float32)
        wav[0:sr] = 0.4
        wav[sr * 2 : sr * 3] = 0.05
        phrases = [
            {"text": "hello", "start": 0.1, "end": 0.8},
            {"text": "hi", "start": 2.1, "end": 2.8},
            {"text": "short", "start": 2.85, "end": 3.0},
        ]
        embeddings = [
            np.array([1.0, 0.0], dtype=np.float32),
            np.array([0.0, 1.0], dtype=np.float32),
            None,
        ]
        apply_embeddings(phrases, embeddings, wav, sr, 2)
        self.assertEqual(phrases[0]["speaker"], SPEAKER_DOCTOR)
        self.assertEqual(phrases[1]["speaker"], SPEAKER_PATIENT)
        self.assertEqual(phrases[2]["speaker"], SPEAKER_PATIENT)

    def test_defaults_to_doctor_without_embeddings(self):
        phrases = [{"text": "x", "start": 0.0, "end": 1.0}]
        apply_embeddings(phrases, [None], np.zeros(16000, dtype=np.float32), 16000, 2)
        self.assertEqual(phrases[0]["speaker"], SPEAKER_DOCTOR)


if __name__ == "__main__":
    unittest.main()
