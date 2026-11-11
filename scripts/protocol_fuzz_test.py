"""Exercise the real campaign driver with a recorded native-tool boundary."""

import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
FAKE_BINARY = """#!/usr/bin/env python3
import json, os, sys
args = sys.argv[1:]
with open(os.environ["FAKE_CALLS"], "a") as f:
    f.write(json.dumps(args) + "\\n")
target = os.environ["FUZZ_TARGETS"]
if "-test.list" in args:
    print(target)
elif "-test.fuzz" in args:
    if os.environ.get("FAKE_FAIL") == "1":
        print("FAILING_INPUT_RETAINED")
        sys.exit(42)
    if os.environ.get("FAKE_NO_MUTATIONS") != "1":
        print("fuzz: gathering baseline coverage complete, now fuzzing with 2 workers")
    print("PASS")
else:
    for i in range(int(os.environ.get("FAKE_SEEDS", "3"))):
        print("=== RUN   " + target + "/seed#" + str(i))
    print("PASS")
"""
FAKE_GO = """#!/usr/bin/env python3
import os, pathlib, sys
args = sys.argv[1:]
if args == ["env", "CGO_ENABLED"]:
    print("1")
elif args[:2] == ["test", "-c"]:
    path = pathlib.Path(args[args.index("-o") + 1])
    path.write_text(os.environ["FAKE_BINARY"])
    path.chmod(0o755)
else:
    raise SystemExit("unexpected go invocation: " + repr(args))
"""


class ProtocolCampaignTests(unittest.TestCase):
    def drive(self, target="FuzzMessageReader", **overrides):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "go").write_text(FAKE_GO)
            (root / "go").chmod(0o755)
            calls = root / "calls.jsonl"
            env = {**os.environ, "PATH": directory + os.pathsep + os.environ["PATH"],
                   "CRUDE_TEMP_DIR": directory, "FUZZ_TARGETS": target,
                   "FAKE_CALLS": str(calls), "FAKE_BINARY": FAKE_BINARY}
            for key in ("FUZZ_CASES", "FAKE_FAIL", "FAKE_NO_MUTATIONS", "FAKE_SEEDS"):
                env.pop(key, None)
            env.update(overrides)
            result = subprocess.run(["bash", str(ROOT / "scripts/protocol_fuzz.sh")],
                                    env=env, text=True, capture_output=True, timeout=20)
            commands = [json.loads(line) for line in calls.read_text().splitlines()] if calls.exists() else []
            return result, commands

    def test_generated_budget_is_added_after_the_complete_seed_inventory(self):
        result, calls = self.drive(target="FuzzEveryWireDecoder", FAKE_SEEDS="1201", FUZZ_CASES="256")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        fuzz = [args for args in calls if "-test.fuzz" in args]
        self.assertEqual(len(fuzz), 1)
        self.assertEqual(fuzz[0][fuzz[0].index("-test.fuzztime") + 1], "1457x")
        self.assertIn("1201 seeds + 256 generated executions", result.stdout)
        self.assertIn("-test.timeout", fuzz[0])

    def test_slow_owner_targets_have_an_explicit_count_not_a_shorter_timer(self):
        result, calls = self.drive(target="FuzzCoreFeatureDocuments")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        fuzz = next(args for args in calls if "-test.fuzz" in args)
        self.assertEqual(fuzz[fuzz.index("-test.fuzztime") + 1], "67x")

    def test_failure_is_propagated_without_retry(self):
        result, calls = self.drive(FAKE_FAIL="1")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("FAILING_INPUT_RETAINED", result.stdout + result.stderr)
        self.assertEqual(sum("-test.fuzz" in args for args in calls), 1)

    def test_seed_only_success_is_not_fuzz_acceptance(self):
        result, _ = self.drive(FAKE_NO_MUTATIONS="1")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("stopped before generated-input fuzzing", result.stdout + result.stderr)

    def test_missing_seeds_and_invalid_bounds_fail_closed(self):
        result, calls = self.drive(FAKE_SEEDS="0")
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any("-test.fuzz" in args for args in calls))
        for bound in ("0", "-1", "bad", "1000001", "99999999999"):
            with self.subTest(bound=bound):
                result, calls = self.drive(FUZZ_CASES=bound)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(calls)


if __name__ == "__main__":
    unittest.main()
