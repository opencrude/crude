#!/usr/bin/env python3
"""Real loopback OpenSSH acceptance, isolated keys/config/core/tmux; no sudo.

--binary must name a prebuilt Crude binary; --out must be a new evidence directory.
An owned PATH wrapper adds only -F to /usr/bin/ssh: encryption/authentication and
strict known-host verification are performed by real OpenSSH, never simulated.
"""
import argparse
import getpass
import json
import os
from pathlib import Path
import shlex
import socket
import subprocess
import threading
import traceback

from core_shell_acceptance import Harness, Observer, until


class SSHHarness(Harness):
    def __init__(self, args):
        super().__init__(args)
        self.sshd = None
        self.sshd_log = None
        self.result["limitations"] = ["Loopback SSH on one filesystem, not separate hosts", "No desktop screenshots or shared-editor acceptance"]

    def setup_ssh(self):
        # sshd StrictModes checks every ancestor; /tmp is world-writable even
        # when the isolated core HOME itself is private. Keep auth material
        # under the caller's owned evidence tree instead of relaxing security.
        ssh = self.out / "ssh"
        ssh.mkdir(mode=0o700)
        for name in ("host", "identity", "replacement"):
            subprocess.run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", str(ssh / name)], check=True)
        authorized = ssh / "authorized_keys"
        authorized.write_bytes((ssh / "identity.pub").read_bytes())
        authorized.chmod(0o600)
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        wrapper = ssh / "core-relay"
        wrapper.write_text("#!/bin/sh\n[ \"$SSH_ORIGINAL_COMMAND\" = 'crude --core-stdio' ] || exit 77\nexec env " +
                           " ".join(shlex.quote(k + "=" + v) for k, v in self.env.items()) + " " +
                           shlex.quote(self.binary) + " --core-stdio\n")
        wrapper.chmod(0o700)
        config = ssh / "sshd_config"
        config.write_text(f"Port {port}\nListenAddress 127.0.0.1\nHostKey {ssh / 'host'}\nPidFile {ssh / 'sshd.pid'}\n"
                          f"AuthorizedKeysFile {authorized}\nAllowUsers {getpass.getuser()}\nForceCommand {wrapper}\n"
                          "StrictModes yes\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nUsePAM no\n"
                          "PubkeyAuthentication yes\nAllowTcpForwarding no\nAllowAgentForwarding no\nX11Forwarding no\n"
                          "PermitTunnel no\nPermitTTY no\nLogLevel VERBOSE\n")
        self.sshd_log = open(self.out / "sshd.log", "w")
        self.sshd = subprocess.Popen(["/usr/sbin/sshd", "-D", "-e", "-f", str(config)], stdout=self.sshd_log, stderr=self.sshd_log)
        def listening():
            if self.sshd.poll() is not None:
                raise RuntimeError("owned nonroot sshd exited: " + (self.out / "sshd.log").read_text())
            try:
                with socket.create_connection(("127.0.0.1", port), timeout=.2):
                    return True
            except OSError:
                return False
        until("owned loopback sshd", listening, timeout=10)
        known = ssh / "known_hosts"
        known.write_text("")
        client = ssh / "config"
        client.write_text(f"Host fixture\n HostName 127.0.0.1\n Port {port}\n User {getpass.getuser()}\n"
                          f" IdentityFile {ssh / 'identity'}\n IdentitiesOnly yes\n UserKnownHostsFile {known}\n"
                          " GlobalKnownHostsFile /dev/null\n StrictHostKeyChecking yes\n BatchMode yes\n")
        def reject(label):
            p = subprocess.run(["/usr/bin/ssh", "-F", str(client), "fixture", "crude --core-stdio"], capture_output=True, timeout=10)
            (self.out / (label + ".log")).write_bytes(p.stderr)
            assert p.returncode != 0 and b"Host key verification failed" in p.stderr, p.stderr
            self.result["checks"].append(label + " rejected by real OpenSSH")
        reject("unknown-host")
        pub = (ssh / "replacement.pub").read_text().split()
        known.write_text(f"[127.0.0.1]:{port} {pub[0]} {pub[1]}\n")
        reject("replaced-host")
        pub = (ssh / "host.pub").read_text().split()
        known.write_text(f"[127.0.0.1]:{port} {pub[0]} {pub[1]}\n")
        executable = ssh / "ssh"
        executable.write_text("#!/bin/sh\nexec /usr/bin/ssh -F " + shlex.quote(str(client)) + ' "$@"\n')
        executable.chmod(0o700)
        self.env["PATH"] = str(ssh) + os.pathsep + self.env["PATH"]
        self.result["ssh"] = {"port": port, "configuration": str(config), "client_configuration": str(client), "real_binary": "/usr/bin/ssh"}

    def launch(self, name, root, resume=False):
        cmd = [self.binary, "--ssh", "fixture", "--workspace", str(root), "--client-profile", name]
        if resume:
            cmd += ["--resume", "last"]
        command = "exec env " + " ".join(shlex.quote(k + "=" + v) for k, v in self.env.items()) + " " + shlex.join(cmd)
        if not self.windows:
            self.tmux("new-session", "-d", "-s", "acceptance", "-n", name, "-x", "160", "-y", "48", command)
        else:
            self.tmux("new-window", "-d", "-t", "acceptance:", "-n", name, command)
        target = "acceptance:" + name + ".0"
        self.windows[name] = (target, int(self.tmux("display-message", "-p", "-t", target, "#{pane_pid}")))
        until(name + " remote rendered", lambda: "fixture-model" in self.capture(name) if resume else "New Session" in self.capture(name))

    def run(self):
        endpoint = "http://127.0.0.1:" + str(self.provider.server_port)
        threading.Thread(target=self.provider.serve_forever, daemon=True).start()
        self.env["MODELS_DEV_URL"] = endpoint + "/catalog"
        data = self.home / ".crude"
        data.mkdir(mode=0o700)
        config = {"accounts": {"fixture": {"id": "fixture", "provider_id": "openai", "label": "Fixture", "api_key": "fixture", "connection": {"name": "Fixture", "provider_url": endpoint, "api_standard": "openai", "auth_type": "protocol"}}},
                  "agents": {"enabled": True, "active_profile": "default", "profiles": {"default": {"multi_agent": False, "categories": {"default": {"account_id": "fixture", "model": "fixture-model", "model_supports_tools": True}}}}},
                  "local_history": {"enabled": "off"}, "memory": {"auto_recall": "off", "auto_extract": "off", "consolidation": "off"}}
        (data / "config.json").write_text(json.dumps(config))
        (data / "config.json").chmod(0o600)
        project = self.home / "remote-project"
        project.mkdir()
        self.core_log = open(self.out / "core-host.log", "w")
        self.core = subprocess.Popen([self.binary, "--core", "--server"], env=self.env, stdout=self.core_log, stderr=self.core_log)
        until("isolated core", lambda: (data / "corewire/core.sock").exists())
        self.setup_ssh()
        self.observer = Observer(self.home)
        self.launch("ssh-origin", project)
        self.keys("ssh-origin", "C-M-n")
        until("new-session confirmation or model", lambda: "fixture-model" in self.capture("ssh-origin") or "Create" in self.capture("ssh-origin"))
        if "fixture-model" not in self.capture("ssh-origin"):
            self.keys("ssh-origin", "Enter")
        self.visible(["ssh-origin"], "fixture-model")
        workspace = self.observer.call("workspace.attach", {"root": str(project)})["workspace_id"]
        initial = until("root identity", lambda: self.identities(workspace))
        self.launch("ssh-peer", project, resume=True)
        self.submit("ssh-origin", "GENUINE SSH SHARED INPUT")
        self.visible(list(self.windows), "ACCEPTED STREAM PREFIX")
        self.visible(list(self.windows), "GENUINE SSH SHARED INPUT")
        self.count("stream", 1)
        self.kill("ssh-origin")
        self.state["release"].set()
        self.visible(["ssh-peer"], "RETAINED STREAM ANSWER")
        assert self.identities(workspace) == initial
        self.count("stream", 1)
        self.result["checks"].append("two real SSH CLI shells share one stream; origin disconnect preserves execution and identity")
        self.result["passed"] = True

    def cleanup(self):
        try:
            super().cleanup()
        finally:
            if self.sshd is not None:
                self.sshd.terminate()
                try:
                    self.sshd.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    self.sshd.kill()
                    self.sshd.wait(timeout=5)
            if self.sshd_log:
                self.sshd_log.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True)
    parser.add_argument("--out", required=True)
    h = SSHHarness(parser.parse_args())
    try:
        h.run()
    except Exception:
        h.result["failure"] = traceback.format_exc()
    finally:
        h.cleanup()
    print(json.dumps({"passed": h.result["passed"], "result": str(h.out / "result.json")}))
    return 0 if h.result["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
