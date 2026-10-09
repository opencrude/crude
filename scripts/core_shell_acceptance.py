#!/usr/bin/env python3
"""Linux real-process acceptance; never builds or uses the user's core/tmux.

Run: python3 scripts/core_shell_acceptance.py --binary /absolute/crude --out /new/evidence
Captures are tmux text evidence, NOT screenshots. Desktop capture is a separate,
sequential scripts/tuishot.sh responsibility. Failures retain isolated state.
"""
import argparse
import http.server
import json
import os
from pathlib import Path
import shlex
import socket
import struct
import subprocess
import tempfile
import threading
import time
import traceback
import uuid


def until(description, check, timeout=45):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        result = check()
        if result:
            return result
        time.sleep(.1)
    raise AssertionError("timeout: " + description)


class Fixture(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_GET(self):
        body = json.dumps({"data": [{"id": "fixture-model"}], "models": []}).encode()
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def event(self, delta, finish=None):
        data = {"choices": [{"delta": delta, "finish_reason": finish}]}
        self.wfile.write(("data: " + json.dumps(data) + "\n\n").encode())
        self.wfile.flush()

    def do_POST(self):
        request = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        state = self.server.state
        with state["lock"]:
            mode = state["mode"]
            state["requests"].append({"mode": mode, "body": request})
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()
        try:
            if mode == "question":
                tools = [m for m in request.get("messages", []) if m.get("role") == "tool"]
                if tools:
                    # Check the actual tool result, not just a provider completion.
                    assert "Yes" in json.dumps(tools), tools
                    self.event({"content": "PEER DECISION ACCEPTED"})
                else:
                    args = {"questions": [{"header": "Choice", "question": "Choose a fixture answer", "options": ["Yes", "No"]}]}
                    self.event({"tool_calls": [{"index": 0, "id": "fixture-question", "type": "function", "function": {"name": "question", "arguments": json.dumps(args)}}]}, "tool_calls")
                    self.wfile.write(b"data: [DONE]\n\n")
                    self.wfile.flush()
                    return
            else:
                self.event({"content": "ACCEPTED " + mode.upper() + " PREFIX"})
                while not state["release"].wait(.25):
                    if state["shutdown"].is_set():
                        return
                    # Heartbeats reveal peer Stop/disconnect without manufacturing text.
                    self.wfile.write(b": heartbeat\n\n")
                    self.wfile.flush()
                self.event({"content": " RETAINED " + mode.upper() + " ANSWER"})
            self.event({}, "stop")
            self.wfile.write(b"data: [DONE]\n\n")
            self.wfile.flush()
            state["completed"][mode].set()
        except (BrokenPipeError, ConnectionResetError):
            pass
        except Exception as exc:
            with state["lock"]:
                state["errors"].append(str(exc))


class Observer:
    """Read-only identity/status observer; all product actions use rendered shells."""
    def __init__(self, home):
        self.sock = socket.socket(socket.AF_UNIX)
        self.sock.settimeout(15)
        self.sock.connect(str(home / ".crude/corewire/core.sock"))
        self.client = "acceptance-observer-" + uuid.uuid4().hex
        self.send({"version": 1, "kind": "hello", "client_id": self.client, "payload": {"max_frame": 8 << 20}})
        assert self.read()["kind"] == "welcome"
        self.owner = self.read()["payload"]
        self.sequence = 0

    def send(self, frame):
        data = json.dumps(frame).encode()
        self.sock.sendall(struct.pack(">I", len(data)) + data)

    def read(self):
        def exact(n):
            data = b""
            while len(data) < n:
                part = self.sock.recv(n - len(data))
                if not part:
                    raise EOFError("core disconnected observer")
                data += part
            return data
        n = struct.unpack(">I", exact(4))[0]
        assert 0 < n <= 8 << 20
        return json.loads(exact(n))

    def call(self, method, payload=None, workspace=""):
        self.sequence += 1
        ident = str(self.sequence)
        self.send({"version": 1, "kind": "request", "request_id": ident, "client_id": self.client,
                   "epoch": self.owner["epoch"], "workspace_id": workspace, "method": method, "payload": payload or {}})
        while True:
            frame = self.read()
            if frame.get("request_id") == ident:
                assert not frame.get("error"), frame
                return frame["payload"]

    def close(self):
        self.sock.close()


def proc(pid):
    root = Path("/proc") / str(pid)
    stat = (root / "stat").read_text().rsplit(")", 1)[1].split()
    return {"pid": pid, "ticks": int(stat[11]) + int(stat[12]),
            "rss_bytes": int(stat[21]) * os.sysconf("SC_PAGE_SIZE"),
            "fds": len(list((root / "fd").iterdir()))}


class Harness:
    def __init__(self, args):
        self.binary = str(Path(args.binary).resolve(strict=True))
        self.out = Path(args.out).resolve()
        self.out.mkdir(parents=True, exist_ok=False)
        # Short unique HOME avoids sockaddr_un limits even with deep evidence paths.
        self.home = Path(tempfile.mkdtemp(prefix="cracc-", dir="/tmp"))
        # Do not forward unrelated credentials/environment into process argv,
        # fixture children or saved evidence. Terminal transport needs only these
        # nonsecret platform/search-path facts plus its explicitly isolated roots.
        self.env = {k: os.environ[k] for k in ("PATH", "LANG", "LC_ALL", "LD_LIBRARY_PATH", "SHELL", "USER", "LOGNAME") if k in os.environ}
        self.env.update(HOME=str(self.home), XDG_CONFIG_HOME=str(self.home / "config"),
                        XDG_DATA_HOME=str(self.home / "data"), XDG_CACHE_HOME=str(self.home / "cache"),
                        TMPDIR=str(self.home), TERM="xterm-256color")
        self.socket = str(self.home / "tmux.sock")
        self.windows = {}
        self.observer = None
        self.core = None
        self.result = {"passed": False, "home": str(self.home), "tmux_socket": self.socket,
                       "checks": [], "limitations": ["No desktop screenshots; tmux captures are text evidence", "Shared-editor journey is not run by this harness"]}
        self.state = {"lock": threading.Lock(), "mode": "stream", "release": threading.Event(),
                      "shutdown": threading.Event(), "requests": [], "errors": [],
                      "completed": {m: threading.Event() for m in ("stream", "question", "stop", "zero")}}
        self.provider = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Fixture)
        self.provider.daemon_threads = True
        self.provider.state = self.state

    def tmux(self, *args, check=True):
        return subprocess.run(["tmux", "-S", self.socket, *args], env=self.env, check=check,
                              capture_output=True, text=True, timeout=15).stdout.strip()

    def launch(self, name, root, resume=False):
        cmd = [self.binary, "--client-profile", name]
        if resume:
            cmd += ["--resume", "last"]
        # exec makes pane_pid the exact independently rendered crude process.
        command = "exec env " + " ".join(shlex.quote(k + "=" + v) for k, v in self.env.items()) + " " + shlex.join(cmd)
        if not self.windows:
            self.tmux("new-session", "-d", "-s", "acceptance", "-n", name, "-x", "160", "-y", "48", "-c", str(root), command)
        else:
            self.tmux("new-window", "-d", "-t", "acceptance:", "-n", name, "-c", str(root), command)
        target = "acceptance:" + name + ".0"
        pid = int(self.tmux("display-message", "-p", "-t", target, "#{pane_pid}"))
        self.windows[name] = (target, pid)
        until(name + " rendered", lambda: "fixture-model" in self.capture(name) if resume else
              "New Session" in self.capture(name) or "fixture-model" in self.capture(name))

    def capture(self, name):
        text = self.tmux("capture-pane", "-p", "-t", self.windows[name][0])
        (self.out / (name + ".txt")).write_text(text)
        return text

    def keys(self, name, *keys):
        self.tmux("send-keys", "-t", self.windows[name][0], *keys)

    def submit(self, name, text):
        self.tmux("send-keys", "-t", self.windows[name][0], "-l", text)
        self.keys(name, "Enter")

    def kill(self, name):
        os.kill(self.windows[name][1], 9)
        self.tmux("kill-window", "-t", self.windows[name][0], check=False)
        del self.windows[name]

    def visible(self, names, text):
        until("all shells show " + text, lambda: all(text in self.capture(n) for n in names))
        stage = "-".join(text.lower().split())
        for name in names:
            (self.out / (stage + "-" + name + ".txt")).write_text(self.capture(name))

    def mode(self, mode):
        self.state["mode"] = mode
        self.state["release"] = threading.Event()

    def count(self, mode, want):
        with self.state["lock"]:
            actual = sum(r["mode"] == mode for r in self.state["requests"])
            assert actual == want, (mode, actual, want)
            assert not self.state["errors"], self.state["errors"]

    def status(self):
        return self.observer.call("core.status")

    def identities(self, workspace):
        snapshot = self.observer.call("workspace.snapshot", workspace=workspace)
        roots = [(a["ID"], a["SessionID"]) for a in snapshot["agents"] if not a["ParentID"]]
        assert len(roots) == 1, roots
        return roots

    def run(self):
        endpoint = "http://127.0.0.1:" + str(self.provider.server_port)
        threading.Thread(target=self.provider.serve_forever, daemon=True).start()
        self.env["MODELS_DEV_URL"] = endpoint + "/catalog"
        data = self.home / ".crude"
        data.mkdir(mode=0o700)
        config = {"accounts": {"fixture": {"id": "fixture", "provider_id": "openai", "label": "Fixture", "api_key": "fixture",
                   "connection": {"name": "Fixture", "provider_url": endpoint, "api_standard": "openai", "auth_type": "protocol"}}},
                  "agents": {"enabled": True, "active_profile": "default", "profiles": {"default": {"multi_agent": False,
                    "categories": {"default": {"account_id": "fixture", "model": "fixture-model", "model_supports_tools": True}}}}},
                   "local_history": {"enabled": "off"}, "lsp": {"enabled": False},
                  "memory": {"auto_recall": "off", "auto_extract": "off", "consolidation": "off"}}
        (data / "config.json").write_text(json.dumps(config))
        os.chmod(data / "config.json", 0o600)
        project = self.home / "project"
        project.mkdir()
        (project / "sample.txt").write_text("SHARED EDITOR BASELINE\n")
        log = open(self.out / "core-host.log", "w")
        self.core_log = log
        self.core = subprocess.Popen([self.binary, "--core", "--server"], env=self.env, stdout=log, stderr=log)
        until("isolated core socket", lambda: (data / "corewire/core.sock").exists())
        self.observer = Observer(self.home)
        self.launch("shell-0", project)
        self.keys("shell-0", "C-M-n")
        self.visible(["shell-0"], "fixture-model")
        attached = self.observer.call("workspace.attach", {"root": str(project)})
        workspace = attached["workspace_id"]
        until("created root published", lambda: len(self.observer.call("workspace.snapshot", workspace=workspace)["agents"]) == 1)
        initial = self.identities(workspace)
        self.result["identity"] = initial
        for i in range(1, 10):
            self.launch("shell-" + str(i), project, resume=True)
        # core.status excludes its requesting administrative observer.
        until("ten attached shells",lambda:self.status()["clients"]==10)
        self.submit("shell-0", "TEN SHELL INPUT")
        self.visible(list(self.windows), "ACCEPTED STREAM PREFIX")
        self.visible(list(self.windows), "TEN SHELL INPUT")
        self.count("stream", 1)
        self.result["checks"].append("ten independent rendered shells share accepted input/output")
        self.kill("shell-0")
        self.state["release"].set()
        self.visible(list(self.windows), "RETAINED STREAM ANSWER")
        assert self.identities(workspace) == initial
        self.count("stream", 1)
        self.result["checks"].append("origin kill retains root/session and one provider execution")
        self.mode("question")
        self.submit("shell-1", "ASK PEER QUESTION")
        self.visible(list(self.windows), "Choose a fixture answer")
        self.keys("shell-2", "Enter")  # real first Yes row activation/submit
        self.visible(list(self.windows), "PEER DECISION ACCEPTED")
        # The completed tool evidence intentionally retains Q1/A1 in chat. Check
        # the actual pending block and blocking deck, not absence of history text.
        def question_resolved():
            snapshot = self.observer.call("workspace.snapshot", workspace=workspace)
            return all(not a.get("Prompt") and not a.get("PendingBlockID") for a in snapshot["agents"]) and all(
                "Choose a fixture answer" not in "\n".join(row for row in self.capture(n).splitlines() if "Q1:" not in row)
                for n in self.windows)
        until("question dismissed on peers", question_resolved)
        self.count("question", 2)
        self.result["checks"].append("peer Yes resolves question once with checked tool result")
        self.mode("stop")
        self.submit("shell-1", "INPUT RETAINED AFTER STOP")
        self.visible(list(self.windows), "ACCEPTED STOP PREFIX")
        self.keys("shell-2", "Escape")
        self.visible(list(self.windows), "Stopped")
        self.visible(list(self.windows), "INPUT RETAINED AFTER STOP")
        self.visible(list(self.windows), "ACCEPTED STOP PREFIX")
        self.count("stop", 1)
        self.result["checks"].append("peer Stop retains accepted input and output")
        self.mode("zero")
        self.submit("shell-1", "ZERO SHELL INPUT")
        self.visible(list(self.windows), "ACCEPTED ZERO PREFIX")
        for name in list(self.windows):
            self.kill(name)
        until("only observer remains attached", lambda: self.status()["clients"] == 0)
        self.observer.close()
        self.observer = None
        # No observer at the zero-shell boundary. Response is released while
        # there are genuinely no clients; reattach must not restart the request.
        self.state["release"].set()
        until("zero-shell fixture completion", lambda: self.state["completed"]["zero"].is_set())
        assert self.core.poll() is None
        self.launch("shell-return", project, resume=True)
        self.visible(["shell-return"], "RETAINED ZERO ANSWER")
        self.observer = Observer(self.home)
        self.observer.call("workspace.attach", {"root": str(project)})
        assert self.identities(workspace) == initial
        self.count("zero", 1)
        self.result["checks"].append("server execution survives zero clients and passive reattach")
        for i in range(19):
            root = self.home / ("project-" + str(i))
            root.mkdir()
            name = "workspace-" + str(i)
            self.launch(name, root)
            until("workspace retained", lambda: any(w["root"] == str(root) for w in self.status()["workspaces"]))
            self.kill(name)
        assert len(self.status()["workspaces"]) == 20
        # Add independent idle views, then report core separately from shells.
        for i in range(9):
            self.launch("idle-" + str(i), project, resume=True)
        self.observer.close()
        self.observer = None
        pids = {"core": self.core.pid, **{n: p for n, (_, p) in self.windows.items()}}
        before = {n: proc(p) for n, p in pids.items()}
        start = time.monotonic()
        time.sleep(2)  # measurement interval, not synchronization/acceptance proof
        elapsed = time.monotonic() - start
        after = {n: proc(p) for n, p in pids.items()}
        for n, value in after.items():
            value["cpu_seconds"] = (value["ticks"] - before[n]["ticks"]) / os.sysconf("SC_CLK_TCK")
            value["cpu_percent_one_core"] = value["cpu_seconds"] / elapsed * 100
        self.result["resources"] = {"retained_workspaces": 20, "sample_seconds": elapsed, "processes": after,
                                    "shell_rss_sum": sum(v["rss_bytes"] for n, v in after.items() if n != "core")}
        self.result["checks"].append("twenty retained workspaces and ten idle rendered shell resource sample")
        self.count("stream", 1)
        self.count("question", 2)
        self.count("stop", 1)
        self.count("zero", 1)
        self.result["passed"] = True

    def cleanup(self):
        if self.observer:
            self.observer.close()
        try:
            self.tmux("kill-server", check=False)
        except (OSError, subprocess.TimeoutExpired) as exc:
            self.result["cleanup_tmux_error"] = str(exc)
        if self.core:
            try:
                stop = subprocess.run([self.binary, "--core-stop", "--force"], env=self.env,
                                      capture_output=True, text=True, timeout=15)
                self.result["cleanup_stop"] = {"code": stop.returncode, "output": stop.stdout + stop.stderr}
                self.core.wait(timeout=15)
            except (subprocess.TimeoutExpired, OSError):
                self.core.kill()  # exact process we started, never name matching
                self.core.wait(timeout=10)
            self.core_log.close()
        self.state["shutdown"].set()
        self.state["release"].set()
        self.provider.shutdown()
        self.provider.server_close()
        core_log = self.home / ".crude/core.log"
        if core_log.exists():
            (self.out / "core.log").write_bytes(core_log.read_bytes())
        self.result["provider_requests"] = self.state["requests"]
        self.result["provider_errors"] = self.state["errors"]
        (self.out / "result.json").write_text(json.dumps(self.result, indent=2))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, help="prebuilt crude executable")
    parser.add_argument("--out", required=True, help="new isolated evidence directory")
    args = parser.parse_args()
    harness = Harness(args)
    try:
        harness.run()
    except Exception:
        harness.result["failure"] = traceback.format_exc()
    finally:
        harness.cleanup()
    print(json.dumps({"passed": harness.result["passed"], "result": str(harness.out / "result.json")}))
    return 0 if harness.result["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
