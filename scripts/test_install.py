#!/usr/bin/env python3
"""Exercise the actual Linux installer, intercepting only its HTTPS transport."""

import argparse
import hashlib
import io
import os
from pathlib import Path
import shlex
import subprocess
import sys
import tarfile
import tempfile
import unittest


ROOT = Path(__file__).resolve().parent.parent
RELEASE_DIR = None
RELEASE_VERSION = None

WINDOW_VERSION = "20240203-110809-5046fc22"


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
        # Production URLs, pinned checksums, extraction and installation stay
        # unchanged. No test endpoint or checksum bypass exists in the shipped
        # installer, so this harness only replaces the network client.
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
    print('https://github.com/opencrude/crude/releases/tag/'+os.environ['LATEST'], end='')
    sys.exit(0)
source = pathlib.Path(os.environ['FIXTURES']) / url.rsplit('/', 1)[-1]
if not source.exists(): sys.exit(22)
shutil.copyfile(source, a[a.index('--output')+1])
""")
        curl.chmod(0o755)
        self.env = dict(os.environ, HOME=str(self.home), SHELL="/bin/bash", TMPDIR=str(self.root),
                        PATH=str(self.bin) + os.pathsep + os.environ["PATH"],
                        FIXTURES=str(self.fixtures), REQUEST_LOG=str(self.log), LATEST="v0.1.0")
        for name in ("CRUDE_INSTALL_DIR", "ZDOTDIR", "XDG_DATA_HOME"):
            self.env.pop(name, None)
        self.dest = self.home / ".local/bin/crude"
        self.window = self.home / f".local/share/crude/window/wezterm-{WINDOW_VERSION}"

    # ---- Fixtures ----------------------------------------------------------
    def archive(self, version="v0.1.0", reported=None, extra=None, icon=False):
        name = f"crude_{version}_linux_amd64.tar.gz"
        with tarfile.open(self.fixtures / name, "w:gz") as archive:
            executable = f"#!/bin/sh\nprintf '%s\\n' 'crude {reported or version} (0123456789abcdef0123456789abcdef01234567; 2026-10-18T00:00:00Z; linux/amd64)'\n"
            members = {"crude": executable, "LICENSE-MIT": "MIT fixture", "LICENSE-APACHE": "Apache fixture",
                       "README.md": "fixture", "licenses/dependency/LICENSE": "dependency fixture"}
            if icon:
                members["crude-icon.png"] = "png fixture"
                members["crude-icon.svg"] = "svg fixture"
                members["crude-icon.ico"] = "ico fixture"
            for path, text in members.items():
                data = text.encode()
                member = tarfile.TarInfo(path)
                member.mode = 0o755 if path == "crude" else (0o444 if path.startswith("licenses/") else 0o644)
                member.size = len(data)
                archive.addfile(member, io.BytesIO(data))
            if extra:
                member = tarfile.TarInfo(extra)
                member.size = 1
                archive.addfile(member, io.BytesIO(b"!"))
        digest = hashlib.sha256((self.fixtures / name).read_bytes()).hexdigest()
        (self.fixtures / "SHA256SUMS").write_text(f"{digest}  {name}\n")
        return name

    def installed_window(self, runs=True):
        """A window install that is already present, so no download is needed."""
        gui = self.window / "usr/bin/wezterm"
        gui.parent.mkdir(parents=True)
        if runs:
            gui.write_text("#!/bin/sh\nprintf 'wezterm %s\\n' '" + WINDOW_VERSION + "'\n")
        else:
            gui.write_text("#!/bin/sh\nexit 1\n")
        gui.chmod(0o755)
        fonts = self.home / ".local/share/fonts/JetBrainsMonoNerdFont"
        fonts.mkdir(parents=True)
        (fonts / "JetBrainsMonoNerdFont-Regular.ttf").write_bytes(b"font")

    def run_installer(self, *args, success=True, yes=True):
        command = ["bash", str(ROOT / "install.sh")]
        if yes:
            command.append("--yes")
        result = subprocess.run([*command, *args], env=self.env, text=True, capture_output=True, timeout=60)
        if success:
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse(list(self.dest.parent.glob(".crude-install*")))
        self.assertFalse(list(self.root.glob("crude-install.*")))
        return result

    def assert_no_window(self):
        self.assertFalse((self.home / ".local/bin/crude-window").exists())
        self.assertFalse((self.home / ".local/share/applications/crude.desktop").exists())

    # ---- Consent and reporting --------------------------------------------
    def test_consent_and_quiet_reporting(self):
        result = subprocess.run(["bash", str(ROOT / "install.sh")], env=self.env, input="", text=True,
                                capture_output=True, timeout=30)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Add --yes to accept it", result.stderr)
        self.assertIn("Nothing was changed", result.stderr)
        self.assertFalse(self.log.exists(), "a refused install must not use the network")

        self.archive()
        installed = self.run_installer("--no-gui")
        self.assertIn("Version v0.1.0", installed.stderr)
        self.assertIn("SHA-256 matches", installed.stderr)
        self.assertIn("Next: open a new terminal, then run: crude", installed.stderr)
        self.assertNotIn("beta-testing", installed.stderr)
        self.assertNotIn("WARNING:", installed.stderr)
        self.assertNotIn("SETUP WARNING", installed.stderr)
        self.assertTrue((self.dest.parent / ".crude-licenses").is_dir())

    def test_path_is_added_once_and_updates_replace_only_the_program(self):
        (self.home / ".bashrc").write_text("# keep my shell settings\n")
        (self.home / ".crude").mkdir()
        (self.home / ".crude/config.json").write_text("do not touch")
        self.archive()
        self.run_installer("--no-gui")
        before = (self.home / ".bashrc").read_text()
        self.assertIn("# keep my shell settings", before)
        self.assertIn("export PATH=", before)
        self.assertEqual(before.count("export PATH="), 1)
        self.run_installer("--no-gui")
        self.assertEqual((self.home / ".bashrc").read_text(), before)
        self.archive("v0.2.0")
        self.env["LATEST"] = "v0.2.0"
        self.run_installer("--no-gui")
        self.assertIn("v0.2.0", subprocess.check_output([str(self.dest), "--version"], text=True))
        self.assertEqual((self.home / ".crude/config.json").read_text(), "do not touch")

    # ---- Refusals ---------------------------------------------------------
    def test_damaged_archives_change_nothing(self):
        self.archive("v0.1.0", extra="tool")
        result = self.run_installer("--no-gui", success=False)
        self.assertIn("unexpected file", result.stderr)
        self.assertFalse(self.dest.exists())

        self.archive("v0.1.0", reported="v9.9.9")
        result = self.run_installer("--no-gui", success=False)
        self.assertIn("not v0.1.0", result.stderr)
        self.assertFalse(self.dest.exists())

        name = self.archive()
        (self.fixtures / "SHA256SUMS").write_text(f"{'0' * 64}  {name}\n")
        result = self.run_installer("--no-gui", success=False)
        self.assertIn("does not match its checksum", result.stderr)
        self.assertFalse(self.dest.exists())

        self.env["NETWORK_FAIL"] = "1"
        result = self.run_installer("--no-gui", success=False)
        self.assertIn("cannot", result.stderr)
        self.assertFalse(self.dest.exists())

    # ---- A running Crude --------------------------------------------------
    def start_running_crude(self):
        """The installed path itself is busy, which is what an update faces."""
        busy = f"cp /bin/sleep {shlex.quote(str(self.dest))} && exec {shlex.quote(str(self.dest))} 120 &"
        subprocess.run(["sh", "-c", busy], check=True, timeout=15)
        for _ in range(100):
            for entry in Path("/proc").iterdir():
                if not entry.name.isdigit():
                    continue
                try:
                    if os.readlink(entry / "exe") == str(self.dest):
                        return int(entry.name)
                except OSError:
                    continue
            subprocess.run(["sleep", "0.1"], check=True)
        self.fail("the busy program did not start")

    def alive(self, pid):
        return Path(f"/proc/{pid}").exists()

    def test_a_running_crude_is_never_stopped_without_an_answer(self):
        self.archive()
        self.run_installer("--no-gui")
        pid = self.start_running_crude()
        try:
            result = self.run_installer("--no-gui", success=False)
            self.assertIn("Crude is still running", result.stderr)
            self.assertIn("--stop", result.stderr)
            self.assertTrue(self.alive(pid), "a refusal must not stop the running program")

            kept = self.run_installer("--no-gui", "--no-stop")
            self.assertIn("stays open", kept.stderr)
            self.assertTrue(self.alive(pid))

            stopped = self.run_installer("--no-gui", "--stop")
            self.assertIn("Crude is closed", stopped.stderr)
            self.assertFalse(self.alive(pid))
        finally:
            if self.alive(pid):
                os.kill(pid, 9)

    def test_a_refused_stop_leaves_the_old_program_in_place(self):
        self.archive()
        self.run_installer("--no-gui")
        pid = self.start_running_crude()
        before = self.dest.read_bytes()
        try:
            self.archive("v0.2.0")
            self.env["LATEST"] = "v0.2.0"
            result = self.run_installer("--no-gui", success=False)
            self.assertIn("Crude is still running", result.stderr)
            self.assertTrue(self.alive(pid))
            self.assertEqual(self.dest.read_bytes(), before, "a refused update must not replace the program")
            self.assertEqual(len(list((self.dest.parent / ".crude-licenses").iterdir())), 1,
                             "a refused update must not publish the new notices")
        finally:
            if self.alive(pid):
                os.kill(pid, 9)

    # ---- The desktop window -----------------------------------------------
    def test_window_starts_crude_and_appears_in_the_menu(self):
        self.archive(icon=True)
        self.installed_window()
        self.run_installer("--gui")
        launcher = self.home / ".local/bin/crude-window"
        self.assertTrue(launcher.exists())
        self.assertTrue(os.access(launcher, os.X_OK))
        text = launcher.read_text()
        self.assertIn(f"{self.window}/usr/bin/wezterm", text)
        self.assertIn("--config-file", text)
        self.assertIn("start --always-new-process --class crude", text)
        config = self.home / ".local/share/crude/window/wezterm.lua"
        self.assertIn(f"default_prog = {{ '{self.home}/.local/bin/crude' }}", config.read_text())
        self.assertIn("JetBrainsMono Nerd Font", config.read_text())
        desktop = (self.home / ".local/share/applications/crude.desktop").read_text()
        self.assertIn(f'Exec="{launcher}"', desktop)
        self.assertIn("StartupWMClass=crude", desktop)
        self.assertIn(f"Icon={self.home}/.local/share/crude/icons/crude.png", desktop)
        self.assertEqual((self.home / ".local/share/crude/icons/crude.png").read_text(), "png fixture")

    def test_window_skips_without_gui_or_when_it_cannot_start(self):
        self.archive()
        self.run_installer("--no-gui")
        self.assert_no_window()
        self.installed_window(runs=False)
        broken = self.run_installer("--gui")
        self.assertIn("cannot start", broken.stderr)
        self.assert_no_window()

    def test_a_window_without_an_icon_names_no_missing_file(self):
        self.archive()
        self.installed_window()
        self.run_installer("--gui")
        desktop = (self.home / ".local/share/applications/crude.desktop").read_text()
        self.assertNotIn("Icon=", desktop)
        self.assertIn('Exec="' + str(self.home / ".local/bin/crude-window") + '"', desktop)

    def test_a_window_download_that_fails_a_checksum_installs_no_window(self):
        self.archive()
        window = self.fixtures / f"wezterm-{WINDOW_VERSION}.Ubuntu22.04.tar.xz"
        window.write_bytes(b"not the pinned window")
        result = self.run_installer("--gui")
        self.assertIn("does not match its pinned checksum", result.stderr)
        self.assert_no_window()
        self.assertFalse(self.window.exists(), "a failed window download must leave no partial folder")
        self.assertTrue(self.dest.exists(), "the program install must survive a failed window download")

    def test_explicit_prerelease_and_custom_path(self):
        self.archive("v0.2.0-rc.1")
        destination = self.home / "custom bin"
        self.run_installer("--version", "v0.2.0-rc.1", "--install-dir", str(destination), "--no-path", "--no-gui")
        self.assertTrue((destination / "crude").is_file())
        self.assertFalse((self.home / ".profile").exists())
        self.assertNotIn("releases/latest", self.log.read_text())

    def test_network_or_manifest_failure_preserves_existing_binary(self):
        self.archive()
        self.run_installer("--no-gui")
        before = self.dest.read_bytes()
        self.env["NETWORK_FAIL"] = "1"
        self.run_installer("--no-gui", success=False)
        self.env.pop("NETWORK_FAIL")
        self.archive("v0.2.0")
        self.env["LATEST"] = "v0.2.0"
        checksum = self.fixtures / "SHA256SUMS"
        for value in ["0" * 64 + "  crude_v0.2.0_linux_amd64.tar.gz\n", "", checksum.read_text() * 2]:
            checksum.write_text(value)
            self.run_installer("--no-gui", success=False)
            self.assertEqual(self.dest.read_bytes(), before)

    def test_missing_asset_or_wrong_version(self):
        self.run_installer("--version", "v0.1.0", "--no-gui", success=False)
        self.archive(reported="v9.9.9")
        self.run_installer("--no-gui", success=False)
        self.assertFalse(self.dest.exists())

    def test_archive_traversal_is_rejected(self):
        self.archive(extra="../escaped")
        self.run_installer("--no-gui", success=False)
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
        self.run_installer("--no-gui", success=False)

    def test_validation_precedes_network(self):
        for args in [("--version", "../bad"), ("--version", "v01.2.3"), ("--version", "v1.2.3-beta.01"),
                     ("--repository", "../other"), ("--install-dir", "relative"), ("--unknown",), ("--version",)]:
            with self.subTest(args=args):
                self.run_installer(*args, "--no-gui", success=False)
        self.assertFalse(self.log.exists())

    def test_install_lock_and_final_rename_failure_preserve_existing(self):
        self.archive()
        self.run_installer("--no-gui")
        before = self.dest.read_bytes()
        lock = self.dest.parent / ".crude-install.lock"
        lock.mkdir()
        (lock / "owner").write_text(f"{os.getpid()}\n")
        result = subprocess.run(["bash", str(ROOT / "install.sh"), "--yes"], env=self.env, capture_output=True,
                                text=True, timeout=30)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(f"another installer (process {os.getpid()}) is running", result.stderr)
        self.assertTrue(lock.exists())
        subprocess.run(["rm", "-rf", str(lock)], check=True)
        stale = self.dest.parent / ".crude-install.lock"
        stale.mkdir()
        (stale / "owner").write_text("999999\n")
        self.run_installer("--no-gui")
        self.assertFalse(stale.exists(), "a lock from a stopped installer must not block the update")
        mv = self.bin / "mv"
        mv.write_text("#!/bin/sh\nexit 1\n")
        mv.chmod(0o755)
        self.run_installer("--no-gui", success=False)
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
            self.run_installer("--no-path", "--no-gui")
            result = subprocess.check_output([str(self.dest), "--version"], env=self.env, text=True)
            self.assertTrue(result.startswith(f"crude {RELEASE_VERSION} ("), result)
        self.assertFalse((self.home / ".crude").exists(), "--version must not open a profile or core")


def main():
    global RELEASE_DIR, RELEASE_VERSION
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--release-dir", help="also install a combined release artifact set")
    parser.add_argument("--version", "--release-version", dest="version", help="version of that artifact set")
    parser.add_argument("-v", "--verbose", action="store_true", help="list each test")
    args = parser.parse_args()
    RELEASE_DIR, RELEASE_VERSION = args.release_dir, args.version
    suite = unittest.defaultTestLoader.loadTestsFromTestCase(InstallerTests)
    return 0 if unittest.TextTestRunner(verbosity=2 if args.verbose else 1).run(suite).wasSuccessful() else 1


if __name__ == "__main__":
    sys.exit(main())
