#!/usr/bin/env python3
"""Two independently rendered Linux CLI editors; prebuilt binary, isolated HOME.

All document actions enter through real tree/palette/keyboard input, never RPC.
Text captures are evidence, not desktop screenshots. --out-new must not exist.
Default cleanup stops only this harness's private tmux/core; state is retained.
"""
import argparse
import json
import os
import signal
import subprocess
import traceback

from core_shell_acceptance import Harness, Observer, process_alive, until


class EditorHarness(Harness):
    def __init__(self, args):
        args.out = args.out_new
        super().__init__(args)
        self.result["limitations"] = ["tmux text captures, not desktop screenshots",
                                       "Origin death is checked after peer rendering proves canonical acceptance; not a forced in-flight RPC death"]
        self.result["binary"] = self.binary

    def literal(self, name, text):
        self.tmux("send-keys", "-t", self.windows[name][0], "-l", text)

    def paste_edit(self, name, text):
        # A bracketed paste is one real editor undo group. send-keys -l emits
        # individual keystrokes and correctly creates multiple undo commands.
        buffer = "editor-acceptance-" + name
        subprocess.run(["tmux", "-S", self.socket, "load-buffer", "-b", buffer, "-"],
                       input=text, text=True, env=self.env, check=True, timeout=15)
        self.tmux("paste-buffer", "-p", "-d", "-b", buffer, "-t", self.windows[name][0])

    def palette(self, name, action):
        self.keys(name, "C-M-p")
        until("palette open", lambda: "Command Palette" in self.capture(name))
        self.literal(name, action)
        until("palette action " + action, lambda: action.lower() in self.capture(name).lower())
        self.keys(name, "Enter")

    def launch_ready(self, frame, resume):
        # A second editor can restore the document directly, without ever
        # painting the launcher or configuring an inference account.
        return self.path.name in frame

    def open_file(self, name):
        if not self.result.get("restricted"):
            self.visible([name], "Open in restricted mode")
            self.click_text(name, "Open in restricted mode")
            until("restricted choice accepted", lambda: "Trust this folder?" not in self.capture(name))
            self.result["restricted"] = True
        if "EDITOR BASELINE" not in self.capture(name):
            self.click_text(name, self.path.name)
        self.visible([name], "EDITOR BASELINE")
        self.click_text(name, "EDITOR BASELINE")  # focus source even on restore

    def detach_editor(self, name):
        # OS window close, not Discard: Bubble Tea handles SIGTERM as orderly
        # teardown. Wait for the exact shell and verify the SDK close below.
        identity = self.processes[name]
        assert process_alive(identity), (name, identity)
        os.kill(identity[0], signal.SIGTERM)
        until(name + " detached", lambda: not process_alive(identity))
        self.tmux("kill-window", "-t", self.windows[name][0], check=False)
        del self.windows[name]

    def dirty(self, names):
        def check():
            for name in names:
                lines = self.capture(name).splitlines()
                if not any("sample.txt" in row and ("●" in row or "*" in row or "•" in row) for row in lines):
                    return False
            return True
        until("dirty tab indicator on every shell", check)

    def assert_disk(self, expected):
        actual = self.path.read_text()
        assert actual == expected, (actual, expected)

    def stage(self, label):
        for name in self.windows:
            (self.out / (label + "-" + name + ".txt")).write_text(self.capture(name))

    def start_core(self):
        self.core_log = open(self.out / "core-host.log", "a")
        self.core = subprocess.Popen([self.binary, "--core", "--server"], env=self.env,
                                     stdout=self.core_log, stderr=self.core_log)
        until("owned isolated core socket", lambda: (self.home / ".crude/corewire/core.sock").exists())
        self.observer = until("core responds", self.connect_observer)

    def connect_observer(self):
        try:
            return Observer(self.home)
        except (OSError, EOFError):
            return None

    def stop_core(self):
        if self.observer:
            self.observer.close()
            self.observer = None
        if self.core and self.core.poll() is None:
            stop = subprocess.run([self.binary, "--core-stop", "--force"], env=self.env,
                                  capture_output=True, text=True, timeout=15)
            self.result.setdefault("core_stops", []).append({"code": stop.returncode, "output": stop.stdout + stop.stderr})
            self.core.wait(timeout=15)
        if self.core:
            self.core_log.close()
            self.core = None

    def run(self):
        data = self.home / ".crude"
        data.mkdir(mode=0o700)
        config = {"agents": {"enabled": False},
                  "onboarding": {"introduction_completed": True, "ai_setup_reminder_hidden": True},
                  "local_history": {"enabled": "off"},
                  "lsp": {"enabled": False},
                  "memory": {"auto_recall": "off", "auto_extract": "off", "consolidation": "off"}}
        (data / "config.json").write_text(json.dumps(config))
        os.chmod(data / "config.json", 0o600)
        self.project = self.home / "project"
        self.project.mkdir()
        self.path = self.project / "sample.txt"
        baseline = ("EDITOR BASELINE\nSECOND LINE\n"
                    '    #figure(caption: [پژوهش Active SLAM می\u200cشود.])\n'
                    '    table.header([#strong[שלום]], [世界 e\u0301])\n')
        self.path.write_text(baseline)
        self.start_core()
        for name in ("editor-a", "editor-b"):
            self.launch(name, self.project)
            self.open_file(name)
        until("two independent attached editors", lambda: self.status()["clients"] == 2)
        self.result["checks"].append("two real CLI processes open sample via tree and render baseline")

        self.keys("editor-a", "C-Home")
        self.paste_edit("editor-a", "AUTHOR_A ")
        self.visible(list(self.windows), "AUTHOR_A EDITOR BASELINE")
        self.keys("editor-b", "C-Home", "Down", "Home")
        self.paste_edit("editor-b", "AUTHOR_B ")
        self.visible(list(self.windows), "AUTHOR_B SECOND LINE")
        self.dirty(list(self.windows))
        self.assert_disk(baseline)
        self.stage("shared-unsaved")
        self.result["checks"].append("disjoint edits visible in both dirty tabs; disk unchanged")

        self.keys("editor-a", "C-z")
        until("A own undo leaves B intact", lambda: all("AUTHOR_A" not in self.capture(n) and "AUTHOR_B SECOND LINE" in self.capture(n) for n in self.windows))
        self.assert_disk(baseline)
        self.keys("editor-b", "C-s")
        saved = baseline.replace("SECOND LINE", "AUTHOR_B SECOND LINE", 1)
        until("explicit Ctrl+S commits exact shared bytes", lambda: self.path.read_text() == saved)
        self.stage("author-undo-and-save")
        self.result["checks"].append("A undo preserves B disjoint edit; B Ctrl+S commits exact shared bytes")

        self.keys("editor-a", "C-Home")
        self.paste_edit("editor-a", "RETAINED_ORIGIN ")
        self.visible(["editor-b"], "RETAINED_ORIGIN EDITOR BASELINE")
        self.kill("editor-a")
        self.visible(["editor-b"], "RETAINED_ORIGIN EDITOR BASELINE")
        self.dirty(["editor-b"])
        self.assert_disk(saved)
        self.stage("origin-killed")
        self.result["checks"].append("origin SIGKILL retains peer-rendered canonical unsaved edit")
        self.result["crash_boundary"] = {"retained_clients": self.status()["clients"], "live_shells": 1}
        # Use the same real profile to replace its crash-left retained session;
        # then native quit closes both owners without changing the v2 grace.
        self.launch("editor-a", self.project)
        self.open_file("editor-a")
        self.visible(["editor-a"], "RETAINED_ORIGIN EDITOR BASELINE")
        for name in ("editor-a", "editor-b"):
            self.keys(name, "C-w")
            self.visible([name], "Unsaved changes")
            self.keys(name, "Escape")  # keep the document, never choose Discard
            until(name + " close cancelled", lambda: "Unsaved changes" not in self.capture(name))
            self.visible([name], "RETAINED_ORIGIN EDITOR BASELINE")
            self.assert_disk(saved)
            self.detach_editor(name)
        until("zero retained shell clients after graceful quit", lambda: self.status()["clients"] == 0)
        self.stop_core()
        self.start_core()
        self.launch("editor-b", self.project)  # same stable private client profile
        self.open_file("editor-b")
        self.visible(["editor-b"], "RETAINED_ORIGIN EDITOR BASELINE")
        self.visible(["editor-b"], "AUTHOR_B SECOND LINE")
        self.dirty(["editor-b"])
        self.assert_disk(saved)
        self.stage("restart-restored")
        self.result["checks"].append("zero-client core restart and same-profile actual reopen restore unsaved canonical bytes")

        # Compete for the same insertion point as the unsaved prefix. A
        # mostly disjoint replacement can legitimately auto-merge and save.
        external = "Ω " + saved
        self.path.write_text(external)
        self.visible(["editor-b"], "Changed on disk. Conflicts")
        self.keys("editor-b", "C-s")
        self.visible(["editor-b"], "File changed on disk")
        self.assert_disk(external)
        self.stage("external-conflict")
        self.keys("editor-b", "Escape")
        until("conflict dialog cancelled", lambda: "File changed on disk" not in self.capture("editor-b"))
        self.visible(["editor-b"], "RETAINED_ORIGIN EDITOR BASELINE")
        self.assert_disk(external)
        self.result["external_conflict_observation"] = {"disk_preserved": True, "local_text_visible": True}
        self.result["checks"].append("external dirty change + Ctrl+S shows conflict, preserves local text and external disk; Esc does not write")
        self.result["passed"] = True

    def cleanup(self):
        try:
            self.tmux("kill-server", check=False)
        except Exception as exc:
            self.result["cleanup_tmux_error"] = str(exc)
        try:
            self.stop_core()
        except Exception as exc:
            self.result["cleanup_core_error"] = str(exc)
            if self.core:
                self.core.kill()  # only the exact child process created above
                self.core.wait(timeout=10)
                self.core_log.close()
        self.provider.server_close()  # inherited fixture is never started/used
        core_log = self.home / ".crude/core.log"
        if core_log.exists():
            (self.out / "core.log").write_bytes(core_log.read_bytes())
        (self.out / "result.json").write_text(json.dumps(self.result, indent=2))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True)
    parser.add_argument("--out-new", required=True, help="new evidence directory (must not exist)")
    args = parser.parse_args()
    harness = EditorHarness(args)
    try:
        harness.run()
    except Exception:
        harness.result["failure"] = traceback.format_exc()
        try:
            harness.stage("failure")
        except Exception as exc:
            harness.result["failure_capture_error"] = str(exc)
    finally:
        harness.cleanup()
    print(json.dumps({"passed": harness.result["passed"], "result": str(harness.out / "result.json")}))
    return 0 if harness.result["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
