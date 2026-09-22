from __future__ import annotations

import unittest

import numpy as np

from segments import (
    ENERGY_FRAME_SECONDS,
    Phrase,
    Turn,
    Word,
    assign_speakers,
    frame_energy,
    group_phrases,
    remap_speakers,
    speech_chunks,
    split_region,
    turns_from_output,
)


def flat_energy(seconds: float, quiet_at: float | None = None):
    energy = np.ones(int(seconds / ENERGY_FRAME_SECONDS), dtype=np.float32)
    if quiet_at is not None:
        energy[int(quiet_at / ENERGY_FRAME_SECONDS)] = 0.0
    return energy


class SegmentsTest(unittest.TestCase):
    def test_remap_by_first_appearance(self):
        turns = [
            Turn(2.0, 3.0, "SPEAKER_01"),
            Turn(0.5, 1.0, "SPEAKER_00"),
            Turn(4.0, 5.0, "SPEAKER_01"),
        ]
        got = remap_speakers(turns)
        self.assertEqual([item.speaker for item in got], ["speaker_1", "speaker_0", "speaker_1"])

    def test_remap_phrases(self):
        got = remap_speakers([Phrase(0.0, 1.0, "SPEAKER_01", "да")])
        self.assertEqual(got, [Phrase(0.0, 1.0, "speaker_0", "да")])

    def test_turns_from_diarize_output(self):
        class FakeTrack:
            def __init__(self, start, end):
                self.start = start
                self.end = end

        class FakeAnnotation:
            def itertracks(self, yield_label=False):
                yield FakeTrack(0.1, 1.2), None, "SPEAKER_00"

        class FakeOutput:
            exclusive_speaker_diarization = FakeAnnotation()
            speaker_diarization = None

        self.assertEqual(turns_from_output(FakeOutput()), [Turn(0.1, 1.2, "SPEAKER_00")])

    def test_frame_energy(self):
        pcm = np.concatenate([np.zeros(160, dtype=np.int16), np.full(160, 1000, dtype=np.int16)])
        np.testing.assert_allclose(frame_energy(pcm, 16000), [0.0, 1000.0])

    def test_split_region_cuts_at_quietest_point(self):
        got = split_region(0.0, 40.0, flat_energy(40.0, quiet_at=18.0), limit=25.0)
        self.assertEqual(len(got), 2)
        self.assertAlmostEqual(got[0][1], 18.0)
        self.assertEqual(got[1][1], 40.0)

    def test_split_region_keeps_short(self):
        self.assertEqual(split_region(1.0, 10.0, flat_energy(20.0)), [(1.0, 10.0)])

    def test_chunks_never_cross_speakers(self):
        turns = [Turn(0.0, 2.0, "A"), Turn(2.0, 4.0, "B"), Turn(4.5, 6.0, "A")]
        got = speech_chunks(turns, flat_energy(10.0), 10.0, pad=0.0)
        self.assertEqual(got, [(0.0, 2.0), (2.0, 4.0), (4.5, 6.0)])

    def test_chunks_group_one_speaker(self):
        turns = [Turn(0.0, 5.0, "A"), Turn(5.5, 10.0, "A"), Turn(10.5, 30.0, "A")]
        got = speech_chunks(turns, flat_energy(40.0), 40.0, pad=0.0)
        # 0-10 stays under min, but adding 10.5-30 would pass max.
        self.assertEqual(got, [(0.0, 10.0), (10.5, 30.0)])

    def test_chunks_padding_stays_in_silence(self):
        turns = [Turn(1.0, 2.0, "A"), Turn(2.1, 3.0, "B")]
        got = speech_chunks(turns, flat_energy(5.0), 5.0, pad=0.2)
        self.assertEqual(got[0][0], 0.8)
        self.assertLessEqual(got[0][1], got[1][0])
        self.assertAlmostEqual(got[1][1], 3.2)

    def test_assign_by_overlap(self):
        turns = [Turn(0.0, 2.0, "A"), Turn(2.0, 4.0, "B")]
        words = [Word(0.5, 1.0, "да"), Word(1.8, 2.6, "нет"), Word(3.0, 3.5, "ага")]
        got = [speaker for _, speaker in assign_speakers(words, turns)]
        self.assertEqual(got, ["A", "B", "B"])

    def test_assign_nearest_when_no_overlap(self):
        turns = [Turn(0.0, 1.0, "A"), Turn(5.0, 6.0, "B")]
        words = [Word(1.2, 1.4, "ну"), Word(4.5, 4.8, "так")]
        got = [speaker for _, speaker in assign_speakers(words, turns)]
        self.assertEqual(got, ["A", "B"])

    def test_assign_inside_long_turn(self):
        turns = [Turn(0.0, 100.0, "A"), Turn(50.0, 51.0, "B")]
        got = assign_speakers([Word(80.0, 80.5, "слово")], turns)
        self.assertEqual(got[0][1], "A")

    def test_phrases_split_on_speaker_and_gap(self):
        labelled = [
            (Word(0.0, 0.5, "Привет."), "A"),
            (Word(0.6, 1.0, "Как"), "A"),
            (Word(1.1, 1.5, "дела?"), "A"),
            (Word(1.7, 2.0, "Хорошо."), "B"),
            (Word(5.0, 5.5, "Правда."), "B"),
        ]
        got = group_phrases(labelled, gap=1.0)
        self.assertEqual(
            got,
            [
                Phrase(0.0, 1.5, "A", "Привет. Как дела?"),
                Phrase(1.7, 2.0, "B", "Хорошо."),
                Phrase(5.0, 5.5, "B", "Правда."),
            ],
        )

    def test_long_phrase_splits_at_sentence_end(self):
        labelled = [(Word(float(i), i + 0.5, "слово"), "A") for i in range(40)]
        labelled[35] = (Word(35.0, 35.5, "конец."), "A")
        got = group_phrases(labelled, max_seconds=30.0)
        self.assertEqual([p.end for p in got], [35.5, 39.5])


if __name__ == "__main__":
    unittest.main()
