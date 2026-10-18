#!/usr/bin/env python3
"""Exercise the actual Linux installer, intercepting only its HTTPS transport."""

import argparse
import hashlib
import io
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest


ROOT = Path(__file__).resolve().parent.parent
RELEASE_DIR = None
RELEASE_VERSION = None


class InstallerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="crude-installer-", dir=os.environ.get("CRUDE_TEMP_DIR"))
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.home = self.root / "home with spaces"
        self.home.mkdir()
        self.fixtures = self.root / "fixtures"
        self.fixtures.mkdir()
        self.bin = self.root / "tools"
        self.bin.mkdir()
        self.log = self.root / "requests"
        # Production URLs/checksum/extraction/installation stay unchanged. No
        # test endpoint or checksum bypass is added to the shipped installer.
        curl = self.bin / "curl"
        curl.write_text("""#!/usr/bin/env python3
import os, pathlib, shutil, sys
a = sys.argv[1:]
assert '--proto' in a and a[a.index('--proto')+1] == '=https'
assert '--proto-redir' in a and a[a.index('--proto-redir')+1] == '=https'
url = a[-1]
assert url.startswith('https://github.com/')
with open(os.environ['REQUEST_LOG'], 'a') as f: f.write(url+'\\n')
if os.environ.get('NETWORK_FAIL'): sys.exit(22)
if '--head' in a:
    print('https://github.com/uoosef403/crude/releases/tag/'+os.environ['LATEST'], end='')
    sys.exit(0)
source = pathlib.Path(os.environ['FIXTURES']) / url.rsplit('/', 1)[-1]
if not source.exists(): sys.exit(22)
shutil.copyfile(source, a[a.index('--output')+1])
""")
        curl.chmod(0o755)
        self.env = dict(os.environ, HOME=str(self.home), SHELL="/bin/bash", TMPDIR=str(self.root),
                        PATH=str(self.bin) + os.pathsep + os.environ["PATH"],
                        FIXTURES=str(self.fixtures), REQUEST_LOG=str(self.log), LATEST="v0.1.0")
        self.env.pop("CRUDE_INSTALL_DIR", None)
        self.env.pop("ZDOTDIR", None)
        self.dest = self.home / ".local/bin/crude"

    def archive(self, version="v0.1.0", reported=None, extra=None):
        name = f"crude_{version}_linux_amd64.tar.gz"
        with tarfile.open(self.fixtures / name, "w:gz") as archive:
            executable = f"#!/bin/sh\nprintf '%s\\n' 'crude {reported or version} (0123456789abcdef0123456789abcdef01234567; 2026-10-18T00:00:00Z; linux/amd64)'\n"
            members = {"crude": executable, "LICENSE-MIT": "MIT fixture", "LICENSE-APACHE": "Apache fixture",
                       "README.md": "fixture", "licenses/dependency/LICENSE": "dependency fixture"}
            for path, text in members.items():
                data = text.encode()
                member = tarfile.TarInfo(path)
                member.mode = 0o755 if path == "crude" else 0o644
                member.size = len(data)
                archive.addfile(member, io.BytesIO(data))
            if extra:
                member = tarfile.TarInfo(extra)
                member.size = 1
                archive.addfile(member, io.BytesIO(b"!"))
        digest = hashlib.sha256((self.fixtures / name).read_bytes()).hexdigest()
        (self.fixtures / "SHA256SUMS").write_text(f"{digest}  {name}\n")
        return name

    def run_installer(self, *args, success=True):
        result = subprocess.run(["bash", str(ROOT / "install.sh"), "--accept-beta-risk", *args], env=self.env,
                                text=True, capture_output=True, timeout=15)
        if success:
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse(list(self.dest.parent.glob(".crude-install*")))
        self.assertFalse(list(self.root.glob("crude-install.*")))
        return result

    def test_beta_consent_required_and_setup_warnings_visible(self):
        result = subprocess.run(["bash", str(ROOT / "install.sh")], env=self.env,
                                input="", text=True, capture_output=True, timeout=15)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("beta-testing", result.stderr)
        self.assertIn("--accept-beta-risk", result.stderr)
        self.assertFalse(self.log.exists())
        self.archive()
        installed = self.run_installer("--no-path")
        self.assertIn("JetBrainsMono", installed.stderr)
        self.assertIn("ask Crude itself", installed.stderr)
        self.assertIn("active font cannot be verified", installed.stderr)

    def test_fresh_repeat_update_and_path(self):
        (self.home / ".bashrc").write_text("# keep my shell settings\n")
        (self.home / ".crude").mkdir()
        sentinel = self.home / ".crude/config.json"
        sentinel.write_text("do not touch")
        self.archive()
        self.run_installer()
        before = (self.home / ".bashrc").read_text()
        self.assertIn("# keep my shell settings", before)
        self.assertIn("export PATH=", before)
        self.assertIn("v0.1.0", subprocess.check_output([str(self.dest), "--version"], text=True))
        self.run_installer()
        self.assertEqual((self.home / ".bashrc").read_text(), before)
        self.archive("v0.2.0")
        self.env["LATEST"] = "v0.2.0"
        self.run_installer()
        self.assertIn("v0.2.0", subprocess.check_output([str(self.dest), "--version"], text=True))
        self.assertEqual(sentinel.read_text(), "do not touch")
        self.assertTrue(list(self.dest.parent.glob(".crude-licenses/*/dependency/LICENSE")))

    def test_explicit_prerelease_and_custom_path(self):
        self.archive("v0.2.0-rc.1")
        destination = self.home / "custom bin"
        self.run_installer("--version", "v0.2.0-rc.1", "--install-dir", str(destination), "--no-path")
        self.assertTrue((destination / "crude").is_file())
        self.assertFalse((self.home / ".profile").exists())
        self.assertNotIn("releases/latest", self.log.read_text())

    def test_network_or_manifest_failure_preserves_existing_binary(self):
        self.archive()
        self.run_installer("--no-path")
        before = self.dest.read_bytes()
        self.env["NETWORK_FAIL"] = "1"
        self.run_installer(success=False)
        self.env.pop("NETWORK_FAIL")
        self.archive("v0.2.0")
        self.env["LATEST"] = "v0.2.0"
        checksum = self.fixtures / "SHA256SUMS"
        for value in ["0" * 64 + "  crude_v0.2.0_linux_amd64.tar.gz\n", "", checksum.read_text() * 2]:
            checksum.write_text(value)
            self.run_installer(success=False)
            self.assertEqual(self.dest.read_bytes(), before)

    def test_missing_asset_or_wrong_version(self):
        self.run_installer("--version", "v0.1.0", success=False)
        self.archive(reported="v9.9.9")
        self.run_installer(success=False)
        self.assertFalse(self.dest.exists())

    def test_archive_traversal_is_rejected(self):
        self.archive(extra="../escaped")
        self.run_installer(success=False)
        self.assertFalse((self.root / "escaped").exists())
        self.assertFalse(self.dest.exists())

    def test_symlinks_are_rejected(self):
        name = self.archive()
        with tarfile.open(self.fixtures / name, "r:gz") as source:
            members = [(item, source.extractfile(item).read()) for item in source.getmembers()]
        with tarfile.open(self.fixtures / name, "w:gz") as target:
            for item, data in members:
                target.addfile(item, io.BytesIO(data))
            link = tarfile.TarInfo("licenses/linked")
            link.type = tarfile.SYMTYPE
            link.linkname = "/tmp"
            target.addfile(link)
        digest = hashlib.sha256((self.fixtures / name).read_bytes()).hexdigest()
        (self.fixtures / "SHA256SUMS").write_text(f"{digest}  {name}\n")
        self.run_installer(success=False)

    def test_validation_precedes_network(self):
        for args in [("--version", "../bad"), ("--version", "v01.2.3"), ("--version", "v1.2.3-beta.01"), ("--repository", "../other"),
                     ("--install-dir", "relative"), ("--unknown",), ("--version",)]:
            with self.subTest(args=args):
                self.run_installer(*args, success=False)
        self.assertFalse(self.log.exists())

    def test_install_lock_and_final_rename_failure_preserve_existing(self):
        self.archive()
        self.run_installer("--no-path")
        before = self.dest.read_bytes()
        lock = self.dest.parent / ".crude-install.lock"
        lock.mkdir()
        result = subprocess.run(["bash", str(ROOT / "install.sh"), "--accept-beta-risk"], env=self.env, capture_output=True, timeout=15)
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(lock.exists())
        lock.rmdir()
        mv = self.bin / "mv"
        mv.write_text("#!/bin/sh\nexit 1\n")
        mv.chmod(0o755)
        self.run_installer("--no-path", success=False)
        self.assertEqual(self.dest.read_bytes(), before)

    def test_actual_release_binary_install_and_repeat(self):
        if RELEASE_DIR is None:
            self.skipTest("real archive supplied only in release smoke")
        import shutil
        name = f"crude_{RELEASE_VERSION}_linux_amd64.tar.gz"
        shutil.copyfile(RELEASE_DIR / name, self.fixtures / name)
        shutil.copyfile(RELEASE_DIR / "SHA256SUMS", self.fixtures / "SHA256SUMS")
        self.env["LATEST"] = RELEASE_VERSION
        for _ in range(2):
            self.run_installer("--no-path")
            result = subprocess.check_output([str(self.dest), "--version"], env=self.env, text=True)
            self.assertTrue(result.startswith(f"crude {RELEASE_VERSION} ("), result)
        self.assertFalse((self.home / ".crude").exists(), "--version must not open a profile or core")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(add_help=False)
    parser.add_argument("--release-dir", type=Path)
    parser.add_argument("--release-version")
    args, remaining = parser.parse_known_args()
    RELEASE_DIR, RELEASE_VERSION = args.release_dir, args.release_version
    if bool(RELEASE_DIR) != bool(RELEASE_VERSION):
        parser.error("--release-dir and --release-version must be used together")
    unittest.main(argv=[sys.argv[0], *remaining])
