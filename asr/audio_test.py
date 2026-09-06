from __future__ import annotations

import os
import unittest
from unittest import mock

from audio import DEFAULT_AGC_FILTER, agc_filter


class AgcFilterTest(unittest.TestCase):
    def test_enabled_by_default(self):
        with mock.patch.dict(os.environ, {}, clear=False):
            os.environ.pop("AGC_ENABLED", None)
            os.environ.pop("AGC_FILTER", None)
            self.assertEqual(agc_filter(), DEFAULT_AGC_FILTER)

    def test_can_disable(self):
        with mock.patch.dict(os.environ, {"AGC_ENABLED": "false"}, clear=False):
            os.environ.pop("AGC_FILTER", None)
            self.assertIsNone(agc_filter())

    def test_custom_filter(self):
        with mock.patch.dict(
            os.environ,
            {"AGC_ENABLED": "true", "AGC_FILTER": "highpass=f=100"},
            clear=False,
        ):
            self.assertEqual(agc_filter(), "highpass=f=100")


if __name__ == "__main__":
    unittest.main()
