#!/usr/bin/env python3
"""Release names, redistribution material and exact two-platform manifests."""

import argparse
import os
from pathlib import Path
import re
import tarfile
import tempfile
import unittest
import zipfile

import release


class ReleaseTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(dir=os.environ.get("CRUDE_TEMP_DIR"))
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.stage = self.root / "stage"
        self.stage.mkdir()
        self.output = self.root / "dist"
        self.output.mkdir()
        for name in ("crude", "crude.exe", "LICENSE-MIT", "LICENSE-APACHE", "README.md"):
            (self.stage / name).write_text(name, encoding="utf-8")
        (self.stage / "licenses/dependency").mkdir(parents=True)
        (self.stage / "licenses/dependency/LICENSE").write_text("fixture", encoding="utf-8")
        self.args = argparse.Namespace(directory=self.output, version="v0.1.0")

    def test_safe_versions(self):
        for version in ("v0.1.0", "v10.20.30", "v0.1.0-beta.1"):
            self.assertEqual(release.validate_version(version), version)
        for version in ("1.0.0", "v1.0", "../v1.0.0", "v1.0.0/x", "v1.0.0\n", "v01.0.0", "v1.0.0-beta.01", "$(bad)"):
            with self.subTest(version=version), self.assertRaises(ValueError):
                release.validate_version(version)

    def test_exact_archive_contract(self):
        linux = release.package(self.stage, self.output, "v0.1.0", "linux")
        windows = release.package(self.stage, self.output, "v0.1.0", "windows")
        self.assertEqual(linux.name, "crude_v0.1.0_linux_amd64.tar.gz")
        self.assertEqual(windows.name, "crude_v0.1.0_windows_amd64.zip")
        with tarfile.open(linux) as archive:
            names = archive.getnames()
            self.assertIn("crude", names)
            self.assertNotIn("crude.exe", names)
            self.assertIn("licenses/dependency/LICENSE", names)
        with zipfile.ZipFile(windows) as archive:
            names = archive.namelist()
            self.assertIn("crude.exe", names)
            self.assertNotIn("crude", names)
            self.assertIn("licenses/dependency/LICENSE", names)
        release.combine(self.args)
        lines = (self.output / "SHA256SUMS").read_text().splitlines()
        self.assertEqual(len(lines), 2)
        self.assertEqual([line[66:] for line in lines], [linux.name, windows.name])
        with self.assertRaises(ValueError):
            release.package(self.stage, self.output, "v0.1.0", "linux")

    def test_reject_missing_extra_and_corrupt_artifacts(self):
        release.package(self.stage, self.output, "v0.1.0", "linux")
        with self.assertRaises(ValueError):
            release.combine(self.args)
        windows = release.package(self.stage, self.output, "v0.1.0", "windows")
        extra = self.output / "surprise.exe"
        extra.write_text("unexpected")
        with self.assertRaises(ValueError):
            release.combine(self.args)
        extra.unlink()
        windows.write_text("corrupted in transit")
        with self.assertRaises(ValueError):
            release.combine(self.args)

    def test_missing_notices_fail_closed(self):
        (self.stage / "LICENSE-APACHE").unlink()
        with self.assertRaises(ValueError):
            release.package(self.stage, self.output, "v0.1.0", "linux")

    def test_workflows_do_not_run_on_push_pull_request_or_tag(self):
        expected = {
            "tui.yml": {"workflow_dispatch"},
            "release.yml": {"workflow_dispatch"},
            "native-preflight.yml": {"workflow_dispatch"},
            "public-install-smoke.yml": {"workflow_dispatch"},
            "verify.yml": {"workflow_call"},
        }
        workflows = release.ROOT / ".github/workflows"
        self.assertEqual({path.name for path in workflows.glob("*.yml")}, set(expected))
        for name, triggers in expected.items():
            with self.subTest(workflow=name):
                text = (workflows / name).read_text()
                events = text.split("\non:\n", 1)[1].split("\npermissions:", 1)[0]
                self.assertEqual(set(re.findall(r"^  ([a-z_]+):", events, re.MULTILINE)), triggers)


if __name__ == "__main__":
    unittest.main()
