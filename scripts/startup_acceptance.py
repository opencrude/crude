#!/usr/bin/env python3
"""Bounded Linux startup acceptance using real, privately isolated CLI processes.

Run only after building: python3 scripts/startup_acceptance.py \
    --binary /absolute/crude --out /new/evidence

The six scenarios retain their short /tmp profiles and all text evidence, including
on failure. tmux captures are NOT screenshots; sequential tuishot remains separate.
No credentials, model executions, user's tmux server or user's core are used.
"""
import argparse
import hashlib
import http.server
import json
import os
from pathlib import Path
import re
import shlex
import signal
import subprocess
import sys
import tempfile
import threading
import time
import traceback

from core_shell_acceptance import Observer, proc, until


class Catalog(http.server.BaseHTTPRequestHandler):
    """Deterministic loopback catalog; any provider execution fails acceptance."""

    def log_message(self, *_):
        pass

    def do_GET(self):
        self.server.requests.append({"method": "GET", "path": self.path})
        body = b"{}"
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        self.server.requests.append({"method": "POST", "path": self.path})
        self.send_error(503, "Provider execution forbidden in startup acceptance")


def identity(pid):
    """Pin process lifetime, not merely a potentially reused PID."""
    stat = (Path("/proc") / str(pid) / "stat").read_text().rsplit(")", 1)[1].split()
    return {"pid": pid, "start_ticks": int(stat[19])}


def alive(owned):
    try:
        root = Path("/proc") / str(owned["pid"])
        stat = (root / "stat").read_text().rsplit(")", 1)[1].split()
        return stat[0] != "Z" and identity(owned["pid"]) == owned
    except (FileNotFoundError, ProcessLookupError):
        return False


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


class Profile:
    def __init__(self, harness, name):
        self.harness = harness
        self.name = name
        self.home = Path(tempfile.mkdtemp(prefix="csu-", dir="/tmp"))
        self.data = self.home / ".crude"
        self.config = self.data / "config.json"
        self.out = harness.out / name
        self.out.mkdir()
        self.socket = str(self.home / "tmux.sock")
        self.env = {k: os.environ[k] for k in
                    ("PATH", "LANG", "LC_ALL", "LD_LIBRARY_PATH", "SHELL", "USER", "LOGNAME")
                    if k in os.environ}
        self.env.update(HOME=str(self.home), USERPROFILE=str(self.home), CRUDE_HOME=str(self.home),
                        XDG_CONFIG_HOME=str(self.home / "config"),
                        XDG_DATA_HOME=str(self.home / "data"),
                        XDG_CACHE_HOME=str(self.home / "cache"),
                        XDG_STATE_HOME=str(self.home / "state"),
                        XDG_RUNTIME_DIR=str(self.home / "runtime"),
                        TMPDIR=str(self.home), TERM="xterm-256color",
                        GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL="/dev/null",
                        MODELS_DEV_URL=harness.endpoint + "/catalog")
        (self.home / "runtime").mkdir(mode=0o700)
        self.project = self.home / "Desktop" / "child-project"
        self.project.mkdir(parents=True)
        self.sample = self.project / "sample.txt"
        self.sample.write_text("STARTUP EDITOR ACCEPTANCE SENTINEL\n")
        self.shells = {}
        self.owned = []
        self.observer = None
        self.sequence = 0
        self.info = {"name": name, "home": str(self.home), "tmux_socket": self.socket,
                     "processes": self.owned, "captures": [], "status": [], "cleanup": []}
        harness.result["profiles"].append(self.info)

    def tmux(self, *args, check=True):
        result = subprocess.run(["tmux", "-S", self.socket, "-f", "/dev/null", *args],
                                env=self.env, check=check, capture_output=True, text=True, timeout=8)
        return result.stdout.strip()

    def launch(self, name, root=None, workspace=None):
        cmd = [self.harness.binary, "--client-profile", "startup-acceptance"]
        if workspace is not None:
            cmd += ["--workspace", str(workspace)]
        command = "exec env " + " ".join(shlex.quote(k + "=" + v) for k, v in self.env.items())
        command += " " + shlex.join(cmd)
        # Retain exited panes too, so early product errors remain inspectable.
        if not self.shells:
            self.tmux("new-session", "-d", "-s", "acceptance", "-n", name,
                      "-x", "180", "-y", "54", "-c", str(root or self.home), command)
            self.tmux("set-option", "-g", "remain-on-exit", "on")
        else:
            self.tmux("new-window", "-d", "-t", "acceptance:", "-n", name,
                      "-c", str(root or self.home), command)
        target = "acceptance:" + name + ".0"
        pid = int(self.tmux("display-message", "-p", "-t", target, "#{pane_pid}"))
        owned = identity(pid)
        self.shells[name] = (target, owned)
        self.owned.append({**owned, "role": "shell", "name": name})
        return name

    def capture(self, name, label=None):
        text = self.tmux("capture-pane", "-p", "-t", self.shells[name][0])
        (self.out / (name + "-latest.txt")).write_text(text + "\n")
        if label:
            self.sequence += 1
            file = "%02d-%s-%s.txt" % (self.sequence, name, label)
            (self.out / file).write_text(text + "\n")
            self.info["captures"].append(file)
        return text

    def visible(self, name, label, *present, absent=()):
        # The selection-page explanation also says "Choose a project folder";
        # its text is not proof that the native picker is mounted yet.
        if "Choose a project folder" in present:
            present = (*present, "Ctrl+L")
        def check():
            text = self.capture(name)
            return all(p in text for p in present) and all(p not in text for p in absent)
        until(self.name + "/" + name + ": " + label, check, timeout=30)
        return self.capture(name, label)

    def keys(self, name, *keys):
        self.tmux("send-keys", "-t", self.shells[name][0], *keys)

    def text(self, name, text):
        self.tmux("send-keys", "-t", self.shells[name][0], "-l", str(text))

    def click_text(self, name, text):
        rows = self.capture(name).splitlines()
        matches = [(row.index(text) + max(1, len(text) // 2) + 1, y + 1)
                   for y, row in enumerate(rows) if text in row]
        assert len(matches) == 1, (text, matches)
        x, y = matches[0]
        self.tmux("send-keys", "-t", self.shells[name][0], "-l",
                  f"\x1b[<0;{x};{y}M\x1b[<0;{x};{y}m")

    def navigate(self, name, path, label, error=None):
        self.keys(name, "C-l", "C-a")
        self.text(name, path)
        self.keys(name, "Enter")
        if error:
            return self.visible(name, label, "Choose a project folder", error)
        # The typed field is not evidence of navigation. Require the last
        # rendered breadcrumb segment and its completed folder count.
        def completed():
            text = self.capture(name)
            return any("⌂" in row and Path(path).name in row.split("▸")[-1]
                       and re.search(r"\d+ folders?\s*│", row)
                       for row in text.splitlines()) and not any(
                           error in text for error in ("Loading", "not a directory", "no such file"))
        until(self.name + ": completed path navigation " + str(path), completed, timeout=30)
        return self.capture(name, label)

    def connect(self):
        if self.observer:
            self.observer.close()
            self.observer = None

        def attempt():
            try:
                self.observer = Observer(self.home)
                return True
            except (OSError, EOFError):
                return False
        until(self.name + ": private observer", attempt, timeout=25)
        status = self.status("connected")
        peer = status["pid"]
        env = (Path("/proc") / str(peer) / "environ").read_bytes().split(b"\0")
        assert ("CRUDE_HOME=" + str(self.home)).encode() in env, "non-private core refused"
        owned = identity(peer)
        if not any(p["pid"] == peer and p["start_ticks"] == owned["start_ticks"] for p in self.owned):
            self.owned.append({**owned, "role": "core"})
        assert status["pid"] == peer, status
        self.info["core_resources"] = proc(peer)
        return owned

    def status(self, label):
        status = self.observer.call("core.status")
        self.info["status"].append({"label": label, "value": status})
        return status

    def no_project(self, label):
        status = self.status(label)
        assert not status["workspaces"], status
        assert not list((self.data / "workspaces").glob("*")), "startup retained workspace files"

    def no_sessions(self, label):
        status = self.status(label)
        assert all(w["sessions"] == 0 and w["agents"] == 0 for w in status["workspaces"]), status
        artifacts = [str(p) for p in (self.data / "workspaces").glob("*/sessions/*")]
        assert not artifacts, artifacts
        assert self.sample.read_bytes() == b"STARTUP EDITOR ACCEPTANCE SENTINEL\n"
        assert list(self.project.iterdir()) == [self.sample], "unexpected project artifact"
        return status

    def kill_exact(self, owned, sig=signal.SIGKILL):
        if alive(owned):
            os.kill(owned["pid"], sig)
            self.info["cleanup"].append({"signal": sig.name, **owned})
        until("owned process stopped: " + str(owned), lambda: not alive(owned), timeout=8)

    def stop_shell(self, name):
        self.capture(name, "before-shell-sigkill")
        self.kill_exact(self.shells[name][1])

    def close(self):
        # If startup failed before connect(), discover only this private endpoint.
        if self.observer is None and (self.data / "corewire/core.sock").exists():
            try:
                self.connect()
            except Exception as exc:
                self.info["cleanup"].append({"observer_error": str(exc)})
        if self.observer:
            self.observer.close()
            self.observer = None
        for item in self.owned:
            owned = {k: item[k] for k in ("pid", "start_ticks")}
            try:
                self.kill_exact(owned, signal.SIGTERM)
            except Exception:
                self.kill_exact(owned)
        self.tmux("kill-server", check=False)
        self.info["survivors"] = [p for p in self.owned if alive({k: p[k] for k in ("pid", "start_ticks")})]
        assert not self.info["survivors"], self.info["survivors"]
        log = self.data / "core.log"
        if log.exists():
            (self.out / "core.log").write_bytes(log.read_bytes())


class Harness:
    def __init__(self, args):
        binary = Path(args.binary)
        if not binary.is_absolute() or not binary.is_file() or not os.access(binary, os.X_OK):
            raise ValueError("--binary must be an absolute existing executable")
        self.binary = str(binary.resolve(strict=True))
        self.out = Path(args.out).resolve()
        self.out.mkdir(parents=True, exist_ok=False)
        self.result = {"passed": False, "binary": self.binary, "binary_sha256": digest(binary),
                       "scenarios": [], "profiles": [], "limitations": [
                           "Linux only; no native Windows process acceptance",
                           "Text captures are not screenshots; parent owns sequential tuishot visual acceptance",
                           "No account submission, external credentials, provider calls or AI turns",
                           "Core crash checks durable choices/files and explicit startup reconnect, not in-flight run recovery"]}
        self.provider = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Catalog)
        self.provider.daemon_threads = True
        self.provider.requests = []
        self.endpoint = "http://127.0.0.1:%d" % self.provider.server_port
        self.profiles = []
        threading.Thread(target=self.provider.serve_forever, daemon=True).start()

    def profile(self, name, config=None):
        profile = Profile(self, name)
        self.profiles.append(profile)
        if config is not None:
            profile.data.mkdir(mode=0o700)
            profile.config.write_bytes(config if isinstance(config, bytes) else json.dumps(config).encode())
            profile.config.chmod(0o600)
        return profile

    def scenario(self, name, action):
        check = {"name": name, "passed": False}
        self.result["scenarios"].append(check)
        start = time.monotonic()
        try:
            action()
            check["passed"] = True
        except Exception:
            check["failure"] = traceback.format_exc()
        check["seconds"] = round(time.monotonic() - start, 3)
        self.save()
        return check["passed"]

    def save(self):
        (self.out / "result.json").write_text(json.dumps(self.result, indent=2) + "\n")

    def fresh(self):
        p = self.fresh_profile = self.profile("fresh")
        n = p.launch("fresh")
        p.visible(n, "introduction", "Welcome to Crude", "Let's go!")
        p.connect()
        p.no_project("introduction-no-workspace")
        p.keys(n, "Enter")
        p.visible(n, "lets-go-picker", "Choose a project folder", "Ctrl+L")
        p.no_project("picker-no-home-workspace")
        p.navigate(n, p.project, "direct-project-path")
        p.keys(n, "C-o")
        p.visible(n, "account-reminder", "No AI account is configured.", "Do not show this again", "OK")
        p.no_sessions("reminder-no-session")
        p.keys(n, "Enter")
        p.visible(n, "native-add-account", "Application Settings", "Add account", "Authentication", "API key")
        p.keys(n, "Escape")
        p.visible(n, "account-draft-cancelled", "No accounts yet", absent=("API key", "Set up AI"))
        p.no_sessions("account-draft-cancel-no-session")
        p.keys(n, "Escape")
        p.visible(n, "account-cancelled", "New Session", absent=("Set up AI", "Application Settings"))
        p.no_sessions("account-cancel-no-session")
        p.stop_shell(n)

    def established(self):
        p = self.fresh_profile
        n = p.launch("established", p.project)
        p.visible(n, "established-no-introduction", "No AI account is configured.", absent=("Welcome to Crude",))
        p.keys(n, "Left", "Enter")
        p.visible(n, "reminder-suppressed", "New Session", absent=("Set up AI",))
        until("durable reminder suppression", lambda: json.loads(p.config.read_text()).get("onboarding", {}).get("ai_setup_reminder_hidden") is True, timeout=15)
        p.info["suppressed_config_sha256"] = digest(p.config)
        p.no_sessions("suppression-no-session")
        p.stop_shell(n)
        n = p.launch("suppressed-restart", p.project)
        p.visible(n, "suppression-persists", "New Session", absent=("Welcome to Crude", "Set up AI"))
        assert json.loads(p.config.read_text())["onboarding"]["ai_setup_reminder_hidden"] is True
        p.no_sessions("restart-no-session")
        p.stop_shell(n)

    def boundaries(self):
        p = self.profile("boundaries", {"onboarding": {"introduction_completed": True, "ai_setup_reminder_hidden": True}})
        n = p.launch("explicit-home", workspace=p.home)
        p.visible(n, "explicit-home-navigation-only", "home folder", "Open Workspace")
        p.connect()
        p.no_project("explicit-home")
        p.keys(n, "Enter")
        p.visible(n, "home-picker", "Choose a project folder")
        p.navigate(n, p.home / "Desktop", "desktop-navigation")
        p.keys(n, "C-o")
        p.visible(n, "desktop-not-project", "personal folder", "Choose a project folder")
        p.no_project("desktop-no-project")
        p.navigate(n, p.sample, "typed-file-error", error="not a directory")
        p.no_project("typed-file-no-parent-fallback")
        missing = p.home / "missing-project"
        p.navigate(n, missing, "typed-missing-error", error="no such file")
        p.no_project("typed-missing-no-parent-fallback")
        p.navigate(n, p.project, "typed-error-recovery")
        p.keys(n, "C-o")
        p.visible(n, "child-project-editor", "New Session", absent=("Choose a project folder", "Set up AI"))
        status = p.no_sessions("child-project-allowed")
        assert [w["root"] for w in status["workspaces"]] == [str(p.project)], status
        p.stop_shell(n)
        # Explicit CLI file/missing roots must expose errors, never attach parent.
        for name, path, error in (("cli-file", p.sample, "not a directory"),
                                  ("cli-missing", missing, "no such file")):
            n = p.launch(name, workspace=path)
            p.visible(n, name + "-visible-error", error, "Open Workspace")
            status = p.no_sessions(name + "-no-parent-fallback")
            assert [w["root"] for w in status["workspaces"]] == [str(p.project)], status
            p.keys(n, "Enter")
            p.visible(n, name + "-picker", "Choose a project folder")
            p.navigate(n, p.project, name + "-recovered-path")
            p.keys(n, "C-o")
            p.visible(n, name + "-recovered-editor", "New Session", absent=("Choose a project folder",))
            p.no_sessions(name + "-recovered-no-session")
            p.stop_shell(n)

    def corrupt(self):
        original = b'{"onboarding": {BROKEN STARTUP CONFIG\n'
        p = self.profile("corrupt", original)
        n = p.launch("corrupt")
        text = p.visible(n, "top-level-config-issue", "Configuration needs repair", "configuration must contain one JSON object", "Open Workspace")
        assert "Welcome to Crude" not in text
        p.connect()
        p.no_project("corrupt-config-no-workspace")
        inspected = p.observer.call("startup.inspect", {"root": str(p.home)})
        (p.out / "startup-inspect.json").write_text(json.dumps(inspected, indent=2))
        assert inspected.get("issues") and "configuration must contain one JSON object" in json.dumps(inspected["issues"]), inspected
        assert p.config.read_bytes() == original
        p.keys(n, "Enter")
        p.visible(n, "corrupt-config-browse", "Choose a project folder", "Configuration needs repair")
        p.navigate(n, p.project, "corrupt-config-project")
        p.keys(n, "C-o")
        p.visible(n, "attached-config-issue", "Configuration needs repair", "Edit config file", "Close")
        p.keys(n, "Escape")
        p.visible(n, "config-issue-dismissed", "New Session", absent=("Edit config file", "Application Settings"))
        # Dismiss any subsequent reminder instead of accepting forced setup.
        p.keys(n, "Escape")
        p.visible(n, "editor-accessible-without-setup", "New Session", absent=("Application Settings",))
        self.open_sample(p, n)
        assert p.config.read_bytes() == original, "corrupt config changed without consent"
        p.info["original_config_sha256"] = hashlib.sha256(original).hexdigest()
        p.no_sessions("corrupt-config-editor-no-session")
        p.stop_shell(n)

    @staticmethod
    def open_sample(p, name):
        # Open via the real tree; contextual pane actions are intentionally not
        # offered by the application-global command palette.
        p.visible(name, "sample-file-tree", "sample.txt")
        p.click_text(name, "sample.txt")
        p.visible(name, "sample-editor", "STARTUP EDITOR ACCEPTANCE SENTINEL")

    def core_crash(self):
        p = self.profile("core-crash", {"onboarding": {"introduction_completed": True, "ai_setup_reminder_hidden": True}})
        n = p.launch("picker")
        p.visible(n, "before-crash-selection", "Open Workspace")
        core = p.connect()
        p.keys(n, "Enter")
        p.visible(n, "before-core-crash-picker", "Choose a project folder")
        p.no_project("before-core-crash")
        before = p.config.read_bytes()
        sample = p.sample.read_bytes()
        p.observer.close()
        p.observer = None
        p.kill_exact(core)
        p.capture(n, "after-exact-core-sigkill")
        assert alive(p.shells[n][1]), "startup shell died with core"
        # This explicit user navigation, not an observer mutation, restarts/dials.
        p.navigate(n, p.home / "Desktop", "explicit-navigation-reconnect")
        replacement = p.connect()
        assert replacement != core, (replacement, core)
        p.no_project("after-core-crash-still-detached")
        assert p.config.read_bytes() == before and p.sample.read_bytes() == sample
        p.navigate(n, p.project, "post-crash-project")
        p.keys(n, "C-o")
        p.visible(n, "post-crash-editor-shell", "New Session", absent=("Set up AI", "Welcome to Crude"))
        p.no_sessions("post-crash-no-session")
        assert p.config.read_bytes() == before
        p.stop_shell(n)

    def shell_crash(self):
        p = self.profile("shell-crash", {"onboarding": {"introduction_completed": True, "ai_setup_reminder_hidden": True}})
        n = p.launch("lock-owner", p.project)
        p.visible(n, "locked-profile-editor", "New Session", absent=("Set up AI",))
        p.connect()
        self.open_sample(p, n)
        before = p.config.read_bytes()
        p.no_sessions("before-shell-crash")
        p.stop_shell(n)
        n = p.launch("same-profile-return", p.project)
        p.visible(n, "crash-left-lock-released", "New Session", absent=("profile is already", "Set up AI", "Welcome to Crude"))
        assert p.config.read_bytes() == before
        self.open_sample(p, n)
        p.no_sessions("after-shell-crash")
        p.stop_shell(n)

    def run(self):
        scenarios = (("fresh-intro-picker-account-cancel", self.fresh),
                             ("established-restart-reminder-suppression", self.established),
                             ("navigation-boundaries-invalid-path-recovery", self.boundaries),
                             ("corrupt-config-preserved-editor-access", self.corrupt),
                             ("exact-core-sigkill-explicit-reconnect", self.core_crash),
                             ("exact-shell-sigkill-profile-lock-takeover", self.shell_crash))
        for index, (name, action) in enumerate(scenarios):
            if not self.scenario(name, action):
                self.result["scenarios"].extend(
                    {"name": remaining, "passed": False, "skipped": "prior concrete blocker"}
                    for remaining, _ in scenarios[index + 1:])
                break  # concrete blocker; cleanup rather than cascade dependent failures
        self.result["passed"] = len(self.result["scenarios"]) == 6 and all(s["passed"] for s in self.result["scenarios"])

    def close(self):
        for p in self.profiles:
            try:
                p.close()
            except Exception:
                self.result["passed"] = False
                p.info["cleanup_failure"] = traceback.format_exc()
        self.provider.shutdown()
        self.provider.server_close()
        self.result["loopback_requests"] = self.provider.requests
        if any(r["method"] != "GET" for r in self.provider.requests):
            self.result["passed"] = False
            self.result["provider_execution_failure"] = "unexpected provider request"
        self.save()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, help="absolute prebuilt executable; never builds")
    parser.add_argument("--out", required=True, help="new evidence directory; never overwritten/deleted")
    args = parser.parse_args()
    harness = Harness(args)
    try:
        harness.run()
    except Exception:
        harness.result["failure"] = traceback.format_exc()
    finally:
        harness.close()
    print(json.dumps({"passed": harness.result["passed"], "result": str(harness.out / "result.json")}))
    return 0 if harness.result["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
