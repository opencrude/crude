#!/usr/bin/env python3
"""Race sharding must preserve exact coverage and separate upload stress."""

import re
import unittest

import test_race


class RaceShardsTests(unittest.TestCase):
    def test_every_case_runs_once(self):
        names = [f"TestCase{i}" for i in range(101)] + ["ExampleUsage", "FuzzParser", *test_race.STRESS_TESTS]
        groups = test_race.partition(names)
        self.assertCountEqual([name for group in groups for name in group], names)
        for name in names:
            self.assertEqual(sum(bool(re.fullmatch(test_race.pattern(group), name)) for group in groups), 1)
        self.assertEqual(groups[-1], ["TestUploadBlackholeRecoveryDurableOwners"])

    def test_empty_and_small_collections(self):
        self.assertEqual(test_race.partition([]), [])
        self.assertEqual(test_race.partition(["TestOne"]), [["TestOne"]])
        self.assertFalse(re.fullmatch(test_race.pattern(["TestOne"]), "TestOneMore"))


if __name__ == "__main__":
    unittest.main()
