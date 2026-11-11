#!/usr/bin/env python3
"""Normal and race groups must preserve exact coverage and failure semantics."""

import re
import subprocess
import unittest
from unittest import mock

import test_go


class GoGroupsTests(unittest.TestCase):
    def test_every_case_runs_once(self):
        names = [f"TestCase{i}" for i in range(101)] + ["ExampleUsage", "FuzzParser", *test_go.STRESS_TESTS]
        groups = test_go.partition(names)
        self.assertCountEqual([name for group in groups for name in group], names)
        for name in names:
            self.assertEqual(sum(bool(re.fullmatch(test_go.pattern(group), name)) for group in groups), 1)
        self.assertEqual(groups[-1], ["TestUploadBlackholeRecoveryDurableOwners"])

    def test_empty_and_small_collections(self):
        self.assertEqual(test_go.partition([]), [])
        self.assertEqual(test_go.partition(["TestOne"]), [["TestOne"]])
        self.assertFalse(re.fullmatch(test_go.pattern(["TestOne"]), "TestOneMore"))

    def test_both_modes_run_every_package_and_case_once(self):
        names = ["TestFirst", "TestLast", "ExampleUsage", "FuzzParser", "TestUploadBlackholeRecoveryDurableOwners"]
        for race in (False, True):
            with self.subTest(race=race):
                commands = []
                listing = "\n".join([*names, "BenchmarkNotATest", "ok ignored timing output"])
                outputs = ["crude/a\ncrude/app\ncrude/session\ncrude/tui/viewmodel\ncrude/workspace\ncrude/z\n", listing, listing, listing, listing]
                with mock.patch.object(test_go.subprocess, "check_output", side_effect=outputs), \
                     mock.patch.object(test_go, "run", side_effect=commands.append):
                    test_go.main(["--race"] if race else [])
                self.assertEqual(commands[0][-2:], ["crude/a", "crude/z"])
                for command in commands:
                    self.assertEqual("-race" in command, race)
                    self.assertIn("-count=1", command)
                    self.assertIn("-timeout=300s" if race else "-timeout=180s", command)
                    self.assertNotIn("-skip", command)
                for package in ("./app", "./session", "./tui/viewmodel", "./workspace"):
                    patterns = [command[-1] for command in commands if package in command]
                    for name in names:
                        self.assertEqual(sum(bool(re.fullmatch(pattern, name)) for pattern in patterns), 1)
                    self.assertFalse(any(re.fullmatch(pattern, "BenchmarkNotATest") for pattern in patterns))

    def test_a_failed_group_is_not_retried_or_hidden(self):
        failure = subprocess.CalledProcessError(1, ["go", "test"])
        with mock.patch.object(test_go.subprocess, "check_output", return_value="crude/a\n"), \
             mock.patch.object(test_go, "run", side_effect=failure) as run:
            with self.assertRaises(subprocess.CalledProcessError):
                test_go.main([])
            self.assertEqual(run.call_count, 1)


if __name__ == "__main__":
    unittest.main()
