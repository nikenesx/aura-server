from __future__ import annotations

import unittest

from segments import Turn, merge_turns, remap_speakers, split_long_turns, turns_from_output


class SegmentsTest(unittest.TestCase):
    def test_remap_by_first_appearance(self):
        turns = [
            Turn(2.0, 3.0, "SPEAKER_01"),
            Turn(0.5, 1.0, "SPEAKER_00"),
            Turn(4.0, 5.0, "SPEAKER_01"),
        ]
        got = remap_speakers(turns)
        self.assertEqual([item.speaker for item in got], ["speaker_1", "speaker_0", "speaker_1"])

    def test_merge_same_speaker(self):
        turns = [
            Turn(0.0, 1.0, "speaker_0"),
            Turn(1.2, 2.0, "speaker_0"),
            Turn(3.0, 4.0, "speaker_1"),
        ]
        got = merge_turns(turns, gap=0.4)
        self.assertEqual(got, [Turn(0.0, 2.0, "speaker_0"), Turn(3.0, 4.0, "speaker_1")])

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

    def test_split_long(self):
        got = split_long_turns([Turn(0.0, 60.0, "speaker_0")], limit=25.0)
        self.assertEqual(len(got), 3)
        self.assertEqual(got[0], Turn(0.0, 25.0, "speaker_0"))
        self.assertEqual(got[2].end, 60.0)


if __name__ == "__main__":
    unittest.main()
