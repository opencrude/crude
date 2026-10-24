#!/usr/bin/env python3
"""Focused wrapper tests; set CRUDE_ACCEPTANCE_BINARY for real v2 process checks."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

from core_shell_acceptance import Observer, process_alive, process_identity, until


class ObserverWrapperTest(unittest.TestCase):
    def setUp(self):
        self.scratch = tempfile.TemporaryDirectory(dir=os.environ.get("CRUDE_TEMP_DIR"))
        self.addCleanup(self.scratch.cleanup)
        self.root = Path(self.scratch.name)
        self.helper = self.root / "helper"
        self.helper.write_text('''#!/usr/bin/env python3
import json, sys
print('{"ready":true}', flush=True)
for line in sys.stdin:
    request = json.loads(line)
    if request["method"] == "workspace.snapshot":
        result = {"agents": [{"ID":"root", "SessionID":"session", "ParentID":""}]}
        print(json.dumps({"payload":result}), flush=True)
    elif request["method"] == "core.status":
        print(json.dumps({"payload":{"clients":0}}), flush=True)
    else:
        print(json.dumps({"error":"observer method not allowed"}), flush=True)
''')
        self.helper.chmod(0o700)

    def test_persistent_json_lines_and_exact_eof_shutdown(self):
        observer = Observer(self.root, binary=self.helper)
        self.addCleanup(observer.close)
        owned = process_identity(observer.process.pid)
        self.assertEqual(observer.call("core.status"), {"clients": 0})
        snapshot = observer.call("workspace.snapshot", workspace="workspace")
        self.assertEqual(snapshot["agents"][0], {"ID": "root", "SessionID": "session", "ParentID": ""})
        self.assertEqual(observer.process.pid, owned[0])
        observer.close()
        self.assertEqual(observer.process.returncode, 0)
        self.assertFalse(process_alive(owned))

    def test_errors_do_not_become_successful_payloads(self):
        observer = Observer(self.root, binary=self.helper)
        self.addCleanup(observer.close)
        with self.assertRaisesRegex(AssertionError, "method not allowed"):
            observer.call("agent.submit", {"text": "must not run"})
        self.assertEqual(observer.call("core.status"), {"clients": 0})


@unittest.skipUnless(os.environ.get("CRUDE_ACCEPTANCE_BINARY"), "requires current prebuilt core")
class TypedObserverProcessTest(unittest.TestCase):
    def test_actual_typed_methods_and_clean_session_close(self):
        # This starts ONLY an isolated, prebuilt core; no shell input/provider,
        # credentials or manually encoded protocol is involved.
        binary = str(Path(os.environ["CRUDE_ACCEPTANCE_BINARY"]).resolve(strict=True))
        with tempfile.TemporaryDirectory(prefix="cobs-", dir="/tmp") as root:
            home = Path(root)
            data = home / ".crude"
            data.mkdir(mode=0o700)
            config = {"agents": {"enabled": False}, "lsp": {"enabled": False},
                      "local_history": {"enabled": "off"},
                      "memory": {"auto_recall": "off", "auto_extract": "off", "consolidation": "off"}}
            (data / "config.json").write_text(json.dumps(config))
            project = home / "project"
            project.mkdir()
            env = {k: os.environ[k] for k in ("PATH", "LANG", "LD_LIBRARY_PATH") if k in os.environ}
            env.update(HOME=str(home), CRUDE_HOME=str(home), USERPROFILE=str(home),
                       GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL="/dev/null")
            with tempfile.TemporaryFile(mode="w+t") as log:
                core = subprocess.Popen([binary, "--core", "--server"], env=env, stdout=log, stderr=log)
                observers = []
                try:
                    until("test core socket", lambda: (data / "corewire/core.sock").exists())
                    first = Observer(home)
                    observers.append(first)
                    self.assertEqual(first.call("core.status")["pid"], core.pid)
                    self.assertEqual(first.call("core.status")["clients"], 0)
                    inspected = first.call("startup.inspect", {"root": str(project)})
                    self.assertEqual(inspected["root"], str(project))
                    self.assertFalse(first.call("core.status")["workspaces"])
                    attached = first.call("workspace.attach", {"root": str(project)})
                    workspace = attached["workspace_id"]
                    snapshot = first.call("workspace.snapshot", workspace=workspace)
                    self.assertEqual(snapshot["id"], workspace)
                    self.assertEqual(snapshot["root"], str(project))
                    self.assertFalse(snapshot["agents"])
                    for method, payload in (("core.shutdown", {}), ("agent.submit", {}),
                                            ("workspace.attach", {"root": str(project), "unknown": True})):
                        with self.assertRaises(AssertionError):
                            first.call(method, payload)
                    second = Observer(home)
                    observers.append(second)
                    self.assertEqual(second.call("core.status")["clients"], 1)
                    owned = process_identity(first.process.pid)
                    first.close()
                    self.assertFalse(process_alive(owned))
                    self.assertEqual(second.call("core.status")["clients"], 0)
                    second.close()
                finally:
                    for observer in observers:
                        observer.close()
                    if core.poll() is None:
                        stop = subprocess.run([binary, "--core-stop", "--force"], env=env,
                                              capture_output=True, text=True, timeout=15)
                        try:
                            core.wait(timeout=15)
                        except subprocess.TimeoutExpired:
                            core.kill()
                            core.wait(timeout=5)
                        self.assertEqual(stop.returncode, 0, stop.stdout + stop.stderr)
                    self.assertIsNotNone(core.returncode)


if __name__ == "__main__":
    unittest.main()
