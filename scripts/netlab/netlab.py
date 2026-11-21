#!/usr/bin/env python3
"""Container network lab: real Crude shells use a real remote core over an
impaired link.

Topology per scenario, on two private internal Docker networks:

    client --(access)-- router --(server)-- server (sshd, core) + provider

The router presents the remote service at a private address on the access
bridge and NATs it to the server bridge. It impairs both directions with Linux
netem (egress toward the server is the uplink, egress toward the client the
downlink) and an iptables chain for
outages. Shells are the real `crude --ssh server` CLI in tmux; the core is
started by the real `crude --core-stdio` helper over OpenSSH with strict host
keys. The model provider is an in-lab fixture on the server side, so only the
shell-core link is impaired.

Use a local rootful Linux/amd64 Docker daemon with the lab image already built.
The runner does not pull images or reach external hosts. Do not publish ports.
It stages the binary and a same-build observer in an isolated volume and labels
all resources. Captured tmux frames are sampled text, not a latency SLO.

Run: python3 scripts/netlab/netlab.py --binary /abs/crude \\
        --observer /abs/observer --out /new/evidence
"""
import argparse
import concurrent.futures
from contextlib import contextmanager
import dataclasses
import gzip
import hashlib
import json
import os
from pathlib import Path
import random
import re
import shlex
import signal
import subprocess
import sys
import threading
import time
import traceback

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent))
from core_shell_acceptance import Observer, painted_click  # noqa: E402

# Provenance of the official Ubuntu base used by the Dockerfile.
BASE_SHA256 = "e77b6f10c2590cef872b33ee9f635a0e3fd1f57fb074c0e52b5c7f56147a0c86"
PREFIX = "crude-netlab"
LABEL = "crude.netlab.run"
PROVIDER_PORT = 8080
DIRECT_PORT = 7340
ROUTER_HOST = 2
ENDPOINT_HOST = 10
TMUX = "/tmp/netlab-tmux.sock"
PROJECT = "/home/crude/project"
QUEUE = "limit 4096"  # packets; sized for delay x rate with headroom (netem default is 1000)
VIEWPORT = (160, 48)
TRANSPORT = {"ssh": "SSH · encrypted relay", "quic": "QUIC · TLS 1.3", "tls": "TCP · TLS 1.3"}
STOP = threading.Event()


class Cancelled(Exception):
    pass


def delay(seconds):
    if STOP.wait(max(0, seconds)):
        raise Cancelled("Lab cancelled")


class Docker:
    """The Docker CLI; falls back to non-interactive sudo for the local socket."""

    def __init__(self):
        self.base = ["docker"]
        endpoint = os.environ.get("DOCKER_HOST") or subprocess.run(
            [*self.base, "context", "inspect", "--format", "{{.Endpoints.docker.Host}}"],
            check=True, capture_output=True, text=True, timeout=10,
        ).stdout.strip()
        if endpoint != "unix:///var/run/docker.sock":
            raise RuntimeError("Select the local rootful Docker context before you run the lab")
        self.base += ["--host", endpoint]
        probe = self.run_raw(["version", "--format", "{{.Server.Version}}"], timeout=60)
        if probe.returncode and "permission denied" in probe.stderr:
            self.base = ["sudo", "-n", "docker", "--host", endpoint]
            probe = self.run_raw(["version", "--format", "{{.Server.Version}}"], timeout=60)
        if probe.returncode:
            raise RuntimeError("Docker is not usable: " + probe.stderr.strip())
        self.version = probe.stdout.strip()

    def run_raw(self, args, input=None, timeout=20, binary=False):
        return subprocess.run(self.base + list(args), input=input, capture_output=True, timeout=timeout, text=not binary)

    def __call__(self, *args, input=None, check=True, timeout=20, binary=False):
        p = self.run_raw(args, input=input, timeout=timeout, binary=binary)
        if check and p.returncode:
            err = p.stderr if isinstance(p.stderr, str) else p.stderr.decode(errors="replace")
            raise RuntimeError(f"docker {' '.join(args[:4])} failed ({p.returncode}): {err[-3000:]}")
        return p

    def sh(self, container, script, user=None, input=None, check=True, timeout=15):
        args = ["exec"] + (["-i"] if input is not None else []) + (["-u", user] if user else [])
        return self(*args, container, "sh", "-c", script, input=input, check=check, timeout=timeout).stdout

def default_image():
    base = f"{PREFIX}-base:{BASE_SHA256[:12]}"
    return f"{PREFIX}:{hashlib.sha256(base.encode() + (HERE / 'Dockerfile').read_bytes()).hexdigest()[:12]}"


@dataclasses.dataclass(frozen=True)
class Scenario:
    """One lab level. up/down are netem options for the router's egress toward
    the server and toward the client, applied before the shell starts. Events
    are (seconds after the measured stream starts, action, argument); a level
    that degrades an established session starts with a "shape" event."""
    name: str
    summary: str
    up: str = ""
    down: str = ""
    events: tuple = ()
    stream: tuple = (60, 250)  # P2 token count and pace in milliseconds
    recovery: float = 55
    warm_shape: bool = False  # test the impaired active connection, not its cold handshake


B = "delay 15ms rate 10mbit"
WIFI = "delay 30ms 10ms distribution normal rate {rate} slot 2ms 8ms packets {packets} loss gemodel 0.5% 25% 100% 0%"
GEO = "delay 375ms 30ms distribution normal rate 1mbit loss random 3.5%"
TYPED = "LAB P3 STREAM 5x100"

LEVELS = {s.name: s for s in [
    Scenario("L0", "clean link"),
    Scenario("L1", "regional broadband, 30 ms RTT", B, B),
    Scenario("L2", "slow 4G (Lighthouse), 150 ms RTT, 768/1638 kbit/s", "delay 75ms rate 768kbit", "delay 75ms rate 1638kbit"),
    Scenario("L3", "intercontinental, 264 ms RTT", "delay 132ms rate 10mbit", "delay 132ms rate 10mbit"),
    Scenario("L4", "2% random loss each way", B + " loss random 2%", B + " loss random 2%"),
    Scenario("L5", "busy Wi-Fi: jitter, burst delivery, Gilbert-Elliott loss",
             WIFI.format(rate="2mbit", packets=4), WIFI.format(rate="10mbit", packets=8)),
    Scenario("L6", "LEO-style fades every 15 s", "delay 25ms rate 10mbit", "delay 25ms rate 10mbit",
             events=tuple(e for t in (5, 20, 35) for e in ((t, "shape", ("delay 55ms rate 2mbit", "delay 55ms rate 2mbit")),
                                                         (t + .25, "shape", ("delay 25ms rate 10mbit", "delay 25ms rate 10mbit")))),
             stream=(160, 250)),
    Scenario("L7", "established link at 750 ms RTT, jitter, 3.5% loss, 1 Mbit/s", GEO, GEO,
             stream=(30, 500), recovery=90, warm_shape=True),
    Scenario("L8", "5 s outage mid-stream", B, B, events=((3, "drop", "both"), (8, "restore", None))),
    Scenario("L9", "30 s uplink-only outage, then three 5 s flaps", B, B,
             events=((3, "drop", "up"), (8, "type", TYPED), (10, "enter", None), (33, "restore", None),
                     (38, "drop", "both"), (43, "restore", None), (48, "drop", "both"), (53, "restore", None),
                     (58, "drop", "both"), (63, "restore", None))),
    Scenario("L10", "NAT rebinding mid-stream", B, B, events=((3, "rebind", None),)),
    Scenario("L11", "120 s sleep: shell frozen and link down", B, B,
               events=((3, "drop", "both"), (4, "pause", None), (124, "unpause", None), (124.5, "restore", None)), recovery=70),
    Scenario("L12", "60 s of 29% loss each way, typed input, then recovery", B, B,
             events=((0, "shape", (B + " loss random 29%", B + " loss random 29%")), (5, "type", TYPED), (7, "enter", None),
                       (60, "shape", (B, B))), stream=(30, 500), recovery=70),
    Scenario("L13", "established QUIC falls back to pinned TCP/TLS with UDP blocked", B, B,
               events=((3, "deny_udp", None),), recovery=70),
    Scenario("L14", "45 s outage with a draft kept through reconnection", B, B,
               events=((3, "drop", "both"), (6, "type", TYPED), (9, "watch_loss", None), (48, "restore", None)), recovery=70),
    Scenario("L15", "cold SSH attach at 750 ms RTT, jitter, 3.5% loss, 1 Mbit/s", GEO, GEO,
             stream=(30, 500), recovery=90),
    Scenario("L16", "cold SSH at 1200 ms RTT: exceeds the former one-second close deadline",
             "delay 600ms rate 10mbit", "delay 600ms rate 10mbit", stream=(30, 500), recovery=90),
]}
COLD_SLOW = {"L15", "L16"}


class Network:
    def __init__(self, name, prefix):
        self.name, self.prefix = name, prefix

    def host(self, n):
        return f"{self.prefix}.{n}"


class Shell:
    """One rendered CLI in a client container's tmux, with a frame sampler."""

    def __init__(self, lab, client):
        self.lab, self.client, self.container = lab, client, lab.cname("client-" + client)
        self.frames, self.lock, self.stop = [], threading.Condition(), threading.Event()
        self.current = (0, "")
        self.dead = None
        self.thread = None

    def tmux(self, *args, check=True):
        return self.lab.docker("exec", "-u", "crude", self.container, "tmux", "-S", TMUX, "-f", "/dev/null", *args,
                                 check=check, timeout=5).stdout

    def launch(self, argv, capture_startup=False):
        command = "exec env HOME=/home/crude CRUDE_HOME=/home/crude TERM=xterm-256color LANG=C.UTF-8 " + shlex.join(argv)
        self.tmux("new-session", "-d", "-s", "lab", "-x", str(VIEWPORT[0]), "-y", str(VIEWPORT[1]), command)
        self.tmux("set-window-option", "-t", "lab", "remain-on-exit", "on")
        if capture_startup:
            # The shell can write its startup error and leave the alternate
            # screen between two samples. Keep this cold lane's exact PTY bytes.
            self.tmux("pipe-pane", "-O", "-t", "lab", "cat >/tmp/netlab-startup.tty")
        self.pid = int(self.tmux("display-message", "-p", "-t", "lab", "#{pane_pid}").strip())
        self.thread = threading.Thread(target=self.sample, daemon=True)
        self.thread.start()

    def sample(self):
        while not self.stop.is_set():
            captured = time.monotonic()
            try:
                raw = self.tmux("display-message", "-p", "-t", "lab", "#{pane_dead}:#{pane_dead_status}",
                                ";", "capture-pane", "-p", "-t", "lab")
                status, text = raw.split("\n", 1)
            except Exception:
                pass  # paused container or transient exec failure
            else:
                with self.lock:
                    if not self.frames or self.frames[-1][1] != text:
                        self.frames.append((captured, text))
                    self.current = captured, text
                    if status.startswith("1:"):
                        self.dead = status.removeprefix("1:") or "unknown"
                    self.lock.notify_all()
            self.stop.wait(.2)

    def latest(self):
        with self.lock:
            return self.current[1]

    def wait(self, description, predicate, timeout):
        """Require a fresh viewport sample, never a previously matching frame."""
        started = time.monotonic()
        with self.lock:
            while (remaining := started + timeout - time.monotonic()) > 0:
                if STOP.is_set():
                    raise Cancelled("Lab cancelled")
                if self.dead is not None:
                    raise AssertionError(f"{self.client}: shell exited ({self.dead}) while waiting for {description}")
                captured, text = self.current
                if captured >= started and (match := predicate(text)):
                    return text, match
                self.lock.wait(min(remaining, .25))
        raise AssertionError(f"{self.client}: timeout after {timeout:.0f}s waiting for {description}")

    def palette(self, name):
        self.keys("C-M-p")
        self.wait("command palette", lambda s: "Command Palette" in s, 5)
        self.type(name)
        self.wait(name + " command", lambda s: s.lower().count(name.lower()) >= 2, 5)
        self.keys("Enter")

    @contextmanager
    def connection(self):
        """Open the real Connection Details command, without using its color."""
        self.palette("Connection Details")
        try:
            self.wait("Connection to core popover", connection_fields, 5)
            yield
        finally:
            self.keys("Escape")
            self.wait("connection popover closed", lambda s: "Connection to core" not in s, 5)

    def keys(self, *keys):
        self.tmux("send-keys", "-t", "lab", *keys)

    def settled_click(self, description, text, row=None, timeout=15, settle=.3):
        """Click a painted control once it has held still for settle seconds,
        as a person would. Returns each earlier move of the control so a
        layout jump is evidence, not a missed click."""
        moves, place, since = [], None, 0.0
        first = deadline = time.monotonic() + timeout
        while True:
            frame, _ = self.wait(description, lambda text_: text_, max(.1, deadline - time.monotonic()))
            now = time.monotonic()
            try:
                click = painted_click(frame, text, row)
            except AssertionError:
                click = None
            if click is not None:
                first = min(first, now)
            if click != place:
                if place is not None:
                    moves.append({"after_ms": round((now - first) * 1000), "from": place[2:], "to": (click or "")[2:]})
                place, since = click, now
            elif click is not None and now - since >= settle:
                self.type(click)
                return moves
            if now > deadline:
                raise AssertionError(f"{self.client}: {description} did not hold still: {moves}")

    def probe(self, name, text, pace_ms=70, settle_s=15):
        """Type text at a human pace and time each key's echo in the container."""
        path = f"/tmp/probe-{name}.jsonl.gz"
        spec = {"text": text, "pace_ms": pace_ms, "settle_s": settle_s, "frames": path}
        out = self.lab.docker("exec", "-u", "crude", self.container, "python3", "/opt/crude/typeprobe.py", json.dumps(spec),
                              timeout=len(text) * pace_ms / 1000 + settle_s + 20).stdout
        self.lab.docker("cp", f"{self.container}:{path}", str(self.lab.out / f"probe-{name}.jsonl.gz"))
        return json.loads(out)

    def type(self, text):
        self.tmux("send-keys", "-t", "lab", "-l", text)

    def close(self):
        self.stop.set()
        if self.thread:
            self.thread.join(timeout=6)
            assert not self.thread.is_alive(), "Frame sampler did not stop"


def tokens(text, tag):
    return re.findall(rf"\b{re.escape(tag)}(n\d+|end)\b", text)


def complete(text, tag, count):
    return tokens(text, tag) == [f"n{i:02d}" for i in range(1, count + 1)] + ["end"]


def connection_fields(text):
    if "Connection to core" not in text:
        return None
    fields = dict(re.findall(r"\b(State|Transport|Round trip)\s{2,}([^│\n]+)", text))
    fields = {key: value.strip() for key, value in fields.items()}
    return fields if len(fields) == 3 else None


def draft_visible(text, prompt):
    # The bounded viewport always has a single-line composer above the footer.
    # Match only there, never a historical user message in the transcript.
    return any(line.rsplit("│", 1)[-1].strip() == prompt for line in text.splitlines()[-3:-2])


class Lab:
    def __init__(self, docker, image, volume, run, scenario, carrier, out, grace, seed, case=None):
        self.docker, self.image, self.volume = docker, image, volume
        self.name = f"{PREFIX}-{run}-{(case or f'{scenario.name}-{carrier}').lower()}"
        self.scenario, self.carrier, self.out, self.grace = scenario, carrier, out, grace
        self.clients: tuple[str, ...] = ("a",)
        self.nets, self.ifaces, self.shells = {}, {}, {}
        self.rebinds = 0
        self.observer = None
        self.workspace = None
        self.seed = seed
        self.seed_supported = False
        self.seed_probe = ""
        self.paused = set()

    def cname(self, role):
        return f"{self.name}-{role}"

    # Provisioning -----------------------------------------------------------
    def network(self, role):
        name = self.cname(role)
        for _ in range(64):
            prefix = f"10.231.{random.randrange(256)}"
            p = self.docker("network", "create", "--driver", "bridge", "--internal", "--subnet", prefix + ".0/24",
                            "--label", f"{LABEL}={self.name}", "-o", "com.docker.network.bridge.enable_ip_masquerade=false",
                            name, check=False)
            if p.returncode == 0:
                return Network(name, prefix)
            if "overlap" not in p.stderr and "in use" not in p.stderr:
                raise RuntimeError("network create failed: " + p.stderr)
        raise RuntimeError("no free 10.231.x.0/24 subnet for " + name)

    def container(self, role, net, ip, caps=("NET_ADMIN",), sysctls=(), hosts=None):
        args = ["run", "-d", "--pull", "never", "--init", "--name", self.cname(role), "--hostname", role, "--label", f"{LABEL}={self.name}",
                "--network", net.name, "--ip", ip, "-v", f"{self.volume}:/opt/crude:ro",
                "--sysctl", "net.ipv6.conf.all.disable_ipv6=1", "--sysctl", "net.ipv6.conf.default.disable_ipv6=1"]
        for cap in caps:
            args += ["--cap-add", cap]
        for sysctl in sysctls:
            args += ["--sysctl", sysctl]
        for host, address in (hosts or {}).items():
            args += ["--add-host", f"{host}:{address}"]
        self.docker(*args, self.image, "sleep", "infinity")

    def write(self, role, path, content, owner="root", mode="600"):
        self.docker.sh(self.cname(role), f"cat > {path} && chown {owner}:{owner} {path} && chmod {mode} {path}",
                       input=content)

    def up(self):
        srv = self.nets["srv"] = self.network("srv")
        self.container("provider", srv, srv.host(20), caps=())
        self.container("server", srv, srv.host(ENDPOINT_HOST), hosts={"provider": srv.host(20)})
        for i, c in enumerate(self.clients):
            net = self.nets[c] = self.network("cli-" + c)
            router = "router-" + c
            self.container(router, net, net.host(ROUTER_HOST), caps=("NET_ADMIN", "NET_RAW"), sysctls=("net.ipv4.ip_forward=1",))
            self.docker("network", "connect", "--ip", srv.host(ROUTER_HOST + i), srv.name, self.cname(router))
            self.container("client-" + c, net, net.host(ENDPOINT_HOST), hosts={"server": net.host(ROUTER_HOST)})
            names = self.interfaces(router)
            self.ifaces[c] = {"client": names[net.host(ROUTER_HOST)], "server": names[srv.host(ROUTER_HOST + i)]}
        for role in ["server", "provider"] + [r + c for c in self.clients for r in ("router-", "client-")]:
            if role != "provider":
                self.docker.sh(self.cname(role), "set -e; for i in /sys/class/net/eth*; do "
                               'ethtool -K "${i##*/}" tso off gso off gro off; done')
        for c in self.clients:
            i = self.ifaces[c]
            net = self.nets[c]
            for chain in ("LAB_UDP", "LAB_FAULT"):
                self.docker.sh(self.router(c), f"iptables -w -N {chain} && iptables -w -I FORWARD 1 -j {chain}")
            # Docker's internal bridges can reject off-subnet IP packets at
            # the host bridge even when the next-hop MAC is on the bridge.
            # Keep both legs on-subnet. Only the router namespace translates
            # the private service address and forwards it to the other leg.
            self.docker.sh(self.router(c), "iptables -w -t nat -A PREROUTING "
                           f"-i {i['client']} -s {net.host(ENDPOINT_HOST)} -d {net.host(ROUTER_HOST)} "
                           f"-j DNAT --to-destination {srv.host(ENDPOINT_HOST)} && "
                           f"iptables -w -t nat -A POSTROUTING {self.nat_match(c)} -j MASQUERADE")
            # Probe the exact qdisc on this router, not a host interface.
            probe = self.docker("exec", self.router(c), "tc", "qdisc", "replace", "dev", i["server"],
                                "root", "netem", "delay", "1ms", "1ms", "seed", str(self.seed), check=False)
            if probe.returncode:
                if not re.search(r'What is "seed"|invalid argument|not supported', probe.stderr, re.I):
                    raise RuntimeError("netem seed probe failed: " + probe.stderr.strip())
                self.seed_probe = probe.stderr.strip()
            else:
                self.seed_supported = True
            self.shape(c, "", "")
            if self.carrier == "tls":
                self.act("deny_udp", None, c)
        self.provision_ssh()
        if len(self.clients) > 1:
            self.write("server", "/usr/local/bin/marksman", '#!/bin/sh\nexport DOTNET_SYSTEM_GLOBALIZATION_INVARIANT=1\nexec /opt/crude/marksman "$@"\n', mode="755")
            self.write("server", PROJECT + "/.marksman.toml", "", owner="crude")
            self.write("server", PROJECT + "/Links.md", "[jump](Guide.md#destination)\n\n[missing](dead.md)\n", owner="crude")
            self.write("server", PROJECT + "/Guide.md", "# Guide\n\n## Destination\n\nPeer document.\n", owner="crude")
        self.provision_core()
        self.docker("exec", "-d", "-u", "crude", self.cname("provider"), "sh", "-c",
                    f"cd /opt/crude && exec python3 provider.py {PROVIDER_PORT} /tmp/provider.jsonl > /tmp/provider.log 2>&1")
        self.wait_ready("provider", "python3 -c \"import urllib.request; urllib.request.urlopen("
                        f"'http://provider:{PROVIDER_PORT}/catalog', timeout=2)\"")

    def interfaces(self, role):
        out = self.docker.sh(self.cname(role), "ip -o -4 addr show")
        return {m.group(2): m.group(1) for m in re.finditer(r"^\d+: (\S+)\s+inet (\S+)/", out, re.M)}

    def wait_ready(self, what, script, timeout=30):
        deadline = time.monotonic() + timeout
        while self.docker("exec", self.cname("server"), "sh", "-c", script, check=False, timeout=15).returncode:
            if time.monotonic() > deadline:
                raise RuntimeError(what + " did not become ready")
            delay(.3)

    def provision_ssh(self):
        server = self.cname("server")
        self.docker.sh(server, "ssh-keygen -q -t ed25519 -N '' -f /etc/ssh/ssh_host_ed25519_key && usermod -p '*' crude && "
                       "install -d -o crude -g crude -m 700 /home/crude/.ssh /home/crude/.crude && "
                       f"install -d -o crude -g crude -m 755 {PROJECT} && "
                       f"printf 'Network lab project.\\n' > {PROJECT}/README.md && chown crude:crude {PROJECT}/README.md")
        self.write("server", "/etc/ssh/sshd_config", "\n".join([
            "Port 22", "HostKey /etc/ssh/ssh_host_ed25519_key", "PidFile /run/sshd.pid",
            "AuthorizedKeysFile .ssh/authorized_keys", "AllowUsers crude", "PubkeyAuthentication yes",
            "PasswordAuthentication no", "KbdInteractiveAuthentication no", "UsePAM no", "AllowTcpForwarding no",
            "AllowAgentForwarding no", "X11Forwarding no", "PermitTunnel no", "LogLevel VERBOSE",
            f"SetEnv CRUDE_HOME=/home/crude MODELS_DEV_URL=http://provider:{PROVIDER_PORT}/catalog", ""]), mode="644")
        host_key = self.docker.sh(server, "cat /etc/ssh/ssh_host_ed25519_key.pub").split()
        authorized = []
        for c in self.clients:
            client = self.cname("client-" + c)
            self.docker.sh(client, "install -d -o crude -g crude -m 700 /home/crude/.ssh")
            self.docker.sh(client, "ssh-keygen -q -t ed25519 -N '' -C lab-" + c + " -f /home/crude/.ssh/id_ed25519", user="crude")
            authorized.append(self.docker.sh(client, "cat /home/crude/.ssh/id_ed25519.pub"))
            self.write("client-" + c, "/home/crude/.ssh/config",
                       "Host server\n User crude\n IdentityFile ~/.ssh/id_ed25519\n IdentitiesOnly yes\n", owner="crude")
            self.write("client-" + c, "/home/crude/.ssh/known_hosts", f"server {host_key[0]} {host_key[1]}\n", owner="crude")
        self.write("server", "/home/crude/.ssh/authorized_keys", "".join(authorized), owner="crude")
        self.docker("exec", "-d", server, "sh", "-c", "/usr/sbin/sshd -D -e 2>&1 | "
                    "while IFS= read -r line; do echo \"$(date +%s.%N) $line\"; done >> /var/log/sshd.log")
        self.wait_ready("sshd", "test -s /run/sshd.pid")

    def provision_core(self):
        endpoint = f"http://provider:{PROVIDER_PORT}"
        network = {"session_grace_seconds": self.grace}
        if self.carrier != "ssh":
            network.update(remote_access_enabled=True, remote_quic_address=f"0.0.0.0:{DIRECT_PORT}",
                           remote_tls_address=f"0.0.0.0:{DIRECT_PORT}")
        config = {"accounts": {"fixture": {"id": "fixture", "provider_id": "openai", "label": "Fixture", "api_key": "fixture",
                                           "connection": {"name": "Fixture", "provider_url": endpoint, "api_standard": "openai",
                                                          "auth_type": "protocol"}}},
                  "agents": {"enabled": True, "active_profile": "default", "profiles": {"default": {"multi_agent": False,
                             "categories": {"default": {"account_id": "fixture", "model": "fixture-model", "model_supports_tools": True}}}}},
                  "local_history": {"enabled": "off"}, "lsp": {"enabled": False}, "network": network,
                  "memory": {"auto_recall": "off", "auto_extract": "off", "consolidation": "off"}}
        if len(self.clients) > 1:
            config["local_history"] = {"enabled": "on"}
            config["lsp"] = {"enabled": True, "auto_detect": True, "start_on_open": True, "goto_definition": True, "rename": True,
                             "diagnostics": True, "ask_errand": "off", "project_check": "off"}
        self.write("server", "/home/crude/.crude/config.json", json.dumps(config, indent=1), owner="crude")

    # Link control -----------------------------------------------------------
    def router(self, client="a"):
        return self.cname("router-" + client)

    def nat_match(self, client):
        return (f"-s {self.nets[client].host(ENDPOINT_HOST)} -d {self.nets['srv'].host(ENDPOINT_HOST)} "
                f"-o {self.ifaces[client]['server']}")

    def shape_before_attach(self, client):
        level = self.scenario
        self.shape(client, *((B, B) if level.warm_shape else (level.up, level.down)))

    def shape_after_attach(self, client):
        level = self.scenario
        return self.act("shape", (level.up, level.down), client) if level.warm_shape else None

    def shape(self, client, up, down, evidence=False):
        i = self.ifaces[client]
        def command(dev, spec, seed):
            opts = f" seed {seed}" if self.seed_supported and (" loss " in f" {spec} " or " distribution " in f" {spec} ") else ""
            return f"tc qdisc replace dev {dev} root netem {spec or 'delay 0ms'} {QUEUE}{opts}"

        script = " && ".join(command(dev, spec, self.seed + index)
                             for index, (dev, spec) in enumerate(((i["server"], up), (i["client"], down))))
        if not evidence:
            self.docker.sh(self.router(client), script)
            return None
        report = self.docker.sh(self.router(client), "tc -s -d qdisc show && " + script +
                                " && printf '\\nLab shape applied ' && date +%s.%N && tc -s -d qdisc show")
        before, applied = report.split("\nLab shape applied ", 1)
        unix, after = applied.split("\n", 1)
        return {"before": before, "applied_unix": float(unix), "after": after}

    def rules(self, chain, direction, extra="", client="a"):
        i = self.ifaces[client]
        src, dst = self.nets[client].host(ENDPOINT_HOST), self.nets["srv"].host(ENDPOINT_HOST)
        paths = {"up": (i["client"], i["server"], src, dst), "down": (i["server"], i["client"], dst, src)}
        script = []
        for side in ("up", "down") if direction == "both" else (direction,):
            inp, out, source, dest = paths[side]
            script.append(f"iptables -w -A {chain} -i {inp} -o {out} -s {source} -d {dest} "
                          f"{extra} -m comment --comment {side} -j DROP")
        self.docker.sh(self.router(client), " && ".join(script))

    def counters(self, chain, client="a"):
        report = self.docker.sh(self.router(client), f"iptables -w -n -v -x -L {chain}")
        return {m[3]: {"packets": int(m[1]), "bytes": int(m[2])}
                for m in re.finditer(r"^\s*(\d+)\s+(\d+)\s+DROP .*?/\* (up|down) \*/", report, re.M)}

    def tuple_filter(self, client, carrier):
        net = self.nets[client]
        return f"{self.transport_match(carrier)} -s {net.host(ENDPOINT_HOST)} -d {net.host(ROUTER_HOST)}"

    @staticmethod
    def transport_match(carrier):
        proto, port = ("udp" if carrier == "quic" else "tcp"), (22 if carrier == "ssh" else DIRECT_PORT)
        return f"-p {proto} --dport {port}"

    def tuples(self, client="a", carrier=None):
        return self.docker.sh(self.router(client), f"conntrack -L {self.tuple_filter(client, carrier or self.carrier)}").splitlines()

    def link_stats(self):
        return self.docker.sh(self.router(), "tc -s -d qdisc show")

    def act(self, action, argument, client="a"):
        router = self.router(client)
        if action == "shape":
            return self.shape(client, *argument, evidence=True)
        elif action == "drop":
            self.rules("LAB_FAULT", argument, client=client)
            return self.counters("LAB_FAULT", client)
        elif action == "restore":
            before = self.counters("LAB_FAULT", client)
            self.docker.sh(router, "iptables -w -F LAB_FAULT")
            return before
        elif action == "deny_udp":
            self.rules("LAB_UDP", "both", "-p udp", client)
            return self.counters("LAB_UDP", client)
        elif action == "rebind":
            self.rebinds += 1
            low = 20000 + 1000 * (self.rebinds % 20)
            high = low + 999
            self.rules("LAB_FAULT", "both", client=client)
            try:
                before = self.tuples(client)
                assert before, "No active carrier tuple to rebind"
                self.docker.sh(router, f"iptables -w -t nat -I POSTROUTING 1 {self.nat_match(client)} "
                               f"{self.transport_match(self.carrier)} "
                               f"-j MASQUERADE --to-ports {low}-{high} && "
                               f"conntrack -D {self.tuple_filter(client, self.carrier)}")
                assert not self.tuples(client), "Old carrier tuple survived deletion"
            finally:
                self.docker.sh(router, "iptables -w -F LAB_FAULT")
            return {"before": before, "ports": [low, high]}
        elif action in ("pause", "unpause"):
            name = self.cname("client-" + client)
            self.docker(action, name)
            if action == "pause":
                self.paused.add(name)
            else:
                self.paused.remove(name)
            state = self.docker("inspect", "--format", "{{.State.Paused}}", name).stdout.strip()
            assert state == ("true" if action == "pause" else "false"), ("pause state", state)
            return {"paused": state == "true"}
        elif action == "type":
            self.shells[client].type(argument)
        elif action == "enter":
            self.shells[client].keys("Enter")
        else:
            raise ValueError(action)

    def ping(self, client="a", count=20):
        p = self.docker("exec", self.cname("client-" + client), "ping", "-c", str(count), "-i", "0.2",
                        "-q", "-W", "2", "server", check=False, timeout=12)
        if p.returncode not in (0, 1):
            raise RuntimeError("Path ping failed: " + p.stderr.strip())
        out = p.stdout
        match = re.search(r"= [\d.]+/([\d.]+)/[\d.]+/([\d.]+) ms", out)
        loss = re.search(r"([\d.]+)% packet loss", out)
        return {"avg_ms": float(match.group(1)) if match else None, "mdev_ms": float(match.group(2)) if match else None,
                "loss_percent": float(loss.group(1)) if loss else None, "raw": out.strip().splitlines()[-2:]}

    def prove_path(self):
        """The client reaches the server only through its router: shaping adds
        the configured delay and a router drop stops all traffic."""
        client = self.cname("client-a")
        route = self.docker.sh(client, "ip route get \"$(getent hosts server | cut -d' ' -f1)\"")
        assert route.split()[0] == self.nets["a"].host(ROUTER_HOST) and "via" not in route.split(), route
        self.shape("a", "delay 40ms", "delay 40ms")
        shaped = self.ping(count=5)
        assert shaped["avg_ms"] and shaped["avg_ms"] >= 75, ("router delay not on path", shaped)
        proof = {}
        for direction in ("up", "down"):
            self.act("drop", direction)
            dropped = self.ping(count=3)
            counters = self.act("restore", None)
            assert dropped["loss_percent"] == 100, (f"{direction} router drop not on path", dropped)
            assert counters and set(counters) == {direction} and counters[direction]["packets"] >= 3, (direction, counters)
            proof[direction] = {"ping": dropped, "rule": counters}
        self.shape("a", "", "")
        return {"route": route.strip(), "shaped_ping": shaped, "directional_drops": proof,
                "router_interfaces": self.ifaces["a"]}

    # Observation ------------------------------------------------------------
    def observe(self, method, payload=None, workspace=""):
        if self.observer is None:
            self.observer = Observer(None, argv=self.docker.base + ["exec", "-i", "-u", "crude", "-e", "CRUDE_HOME=/home/crude",
                                                                    self.cname("server"), "/opt/crude/observer"])
        return self.observer.call(method, payload, workspace)

    def identity(self):
        if self.workspace is None:
            self.workspace = self.observe("workspace.attach", {"root": PROJECT})["workspace_id"]
        snapshot = self.observe("workspace.snapshot", workspace=self.workspace)
        core = self.observe("core.status")
        assert not core.get("partial"), "Core status was incomplete"
        return {"workspace": [snapshot[k] for k in ("id", "root", "started_at")],
                "root_agents": sorted([a["ID"], a["SessionID"]] for a in (snapshot["agents"] or []) if not a["ParentID"]),
                "core": [core[k] for k in ("pid", "build_id", "started_at")]}

    def provider_requests(self):
        out = self.docker.sh(self.cname("provider"), "cat /tmp/provider.jsonl", timeout=5)
        return [json.loads(line) for line in out.splitlines() if line.strip()]

    def ssh_authentications(self):
        return len(self.ssh_authentication_times())

    def ssh_authentication_times(self):
        log = self.docker.sh(self.cname("server"), "cat /var/log/sshd.log")
        return [float(ts) for ts in re.findall(r"(?m)^(\d+\.\d+) Accepted publickey for crude ", log)]

    def file_text(self, path):
        return self.docker.sh(self.cname("server"), f"cat {shlex.quote(path)}", user="crude", timeout=10)

    def shell_identity(self, shell):
        stat = self.docker.sh(shell.container, f"cat /proc/{shell.pid}/stat", timeout=5).rsplit(")", 1)[1].split()
        assert stat[0] != "Z", "Shell became a zombie"
        return [shell.pid, int(stat[19])]

    # Evidence and teardown ----------------------------------------------------
    def collect(self):
        out = self.out
        for c, shell in self.shells.items():
            with gzip.open(out / f"frames-{c}.jsonl.gz", "wt") as f:
                for t, text in shell.frames:
                    f.write(json.dumps({"t": round(t, 3), "text": text}) + "\n")
            (out / f"final-{c}.txt").write_text(shell.latest())
        existing = set(self.docker("ps", "--format", "{{.Names}}", "--filter", f"label={LABEL}={self.name}").stdout.splitlines())
        if self.cname("provider") in existing:
            (out / "provider.json").write_text(json.dumps(self.provider_requests(), indent=2))
        if self.scenario.name in COLD_SLOW and self.shells.get("a") and self.cname("client-a") in existing:
            p = self.docker("exec", self.cname("client-a"), "tail", "-c", "65536", "/tmp/netlab-startup.tty", binary=True)
            (out / "startup-a.tty").write_bytes(p.stdout)
        if self.cname("server") in existing:
            (out / "server-sockets.txt").write_text(self.docker.sh(self.cname("server"), "ss -tun"))
            (out / "sshd.txt").write_text(self.docker.sh(self.cname("server"), "tail -n 100 /var/log/sshd.log"))
        if self.router() in existing:
            (out / "router-a.txt").write_text(self.docker.sh(
                self.router(), "set -e; tc -V; tc -s -d qdisc show; iptables -w -L -v -n -x; "
                "iptables -w -t nat -L -v -n -x; conntrack -L"))

    def scan_failures(self):
        """Report crash/fatal log filenames only. Never scan or archive profiles."""
        found = {}
        for role in ["server"] + ["client-" + c for c in self.clients]:
            out = self.docker.sh(self.cname(role), "for f in /home/crude/.crude/core.log /home/crude/.crude/crashes/*.log; do "
                                 'if test -f "$f"; then grep -lE \'^(panic:|fatal error:)\' "$f"; '
                                 'status=$?; if test "$status" -gt 1; then exit "$status"; fi; fi; done')
            if out and out.strip():
                found[role] = out.split()
        return found

    def stop_observers(self):
        for shell in self.shells.values():
            shell.close()
        if self.observer:
            self.observer.close()

    def down(self):
        for name in sorted(self.paused):
            self.docker("unpause", name)
        ids = self.docker("ps", "-aq", "--filter", f"label={LABEL}={self.name}").stdout.split()
        if ids:
            self.docker("rm", "-f", *ids, timeout=40)
        nets = self.docker("network", "ls", "-q", "--filter", f"label={LABEL}={self.name}").stdout.split()
        if nets:
            self.docker("network", "rm", *nets)


def until(description, read, timeout):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        delay(0)
        if result := read():
            return result
        delay(.2)
    raise AssertionError(f"Timeout after {timeout}s waiting for {description}")


def validate_provider(log, plans):
    assert len(log) == 3 * len(plans), "Extra, missing, or disconnected model execution"
    for tag, (count, pace) in plans.items():
        row = [e for e in log if e["tag"] == tag]
        assert [e["event"] for e in row] == ["request", "first", "finish"], (tag, row)
        assert len({e["id"] for e in row}) == 1, (tag, row)
        assert (row[0]["count"], row[0]["pace_ms"]) == (count, pace), (tag, row[0])


def stream_visibility(frames, log, plans):
    """Compare provider events and visible output on the shared lab clock."""
    provider = {(e["tag"], e["event"]): e["mono"] for e in log}
    result = {}
    for tag in plans:
        first = next((t for t, text in frames if "n01" in tokens(text, tag)), None)
        end = next((t for t, text in frames if "end" in tokens(text, tag)), None)
        result[tag] = {
            "first_flush_to_first_visible_s": None if first is None else round(first - provider[tag, "first"], 3),
            "finish_to_end_visible_s": None if end is None else round(end - provider[tag, "finish"], 3),
        }
    return result


def case_name(scenario, carrier, attempt, repeat):
    """One evidence directory and resource name per case; repeats are numbered."""
    return f"{scenario.name}-{carrier}" + (f"-r{attempt + 1}" if repeat > 1 else "")


README = "Network lab project.\n"
EDIT_MARK = "Zq8 edit probe 7"
OFFLINE_MARK = "Zq9 offline edit 3"
TERMINAL_COMMAND = "echo LABT$((6*7))"


def local_echo(probe, name):
    """A local edit echoes at UI speed on every link class; budgets are generous
    until repeated comparable runs calibrate them."""
    assert probe["complete"] and not probe["lost"], (f"{name}: lost or unfinished keys", probe["lost"])
    assert probe["sample_gap_ms"]["max"] <= 150, (f"{name}: observation has a gap", probe["sample_gap_ms"])
    lasting = [r for r in probe["regressions"] if r["ms"] is None or r["ms"] >= 50]
    assert not lasting, (f"{name}: typed text disappeared after it was visible", lasting)
    assert probe["latency_ms"]["p95"] <= 150 and probe["latency_ms"]["max"] <= 400, (f"{name}: slow local echo", probe["latency_ms"])


def workflow_lane(lab, shell, result, wait, submit, stream_event, scenario, geo):
    """Overlapping IDE work through the real UI: composer, a concurrent agent
    stream with editor typing and save, an edit and save during an outage, an
    agent question answered by the user and a remote terminal command."""
    m, probes, plans = result["metrics"], result.setdefault("probes", {}), {}
    readme = PROJECT + "/README.md"

    probes["composer"] = shell.probe("composer", "LAB P1 STREAM 20x100")
    local_echo(probes["composer"], "composer")
    shell.keys("Enter")
    plans["P1"] = (20, 100)
    frame, _ = wait("p1_done_s", lambda s: complete(s, "P1", 20), 40)
    agent = re.search(r"(\S+) ×  \+", frame)
    assert agent, "No agent tab in the tab bar"
    agent = agent[1]

    # The editor works while the agent streams over the same link.
    plans["P2"] = scenario.stream
    submit("P2", plans["P2"])
    stream_event("P2", "first", 40)
    shifts = result.setdefault("layout_shifts", {})
    wait("file_tree_s", lambda s: s.count("README.md") == 1, 10)
    shifts["file tree"] = shell.settled_click("README.md in the file tree", "README.md")
    wait("editor_open_s", lambda s: "Network lab project." in s and "README.md ×" in s, 25)
    shell.keys("C-End")
    probes["editor"] = shell.probe("editor", EDIT_MARK)
    local_echo(probes["editor"], "editor")
    saved = time.monotonic()
    shell.keys("C-s")
    until("edit saved on the server", lambda: EDIT_MARK in lab.file_text(readme), 45 if geo else 25)
    m["save_durable_s"] = round(time.monotonic() - saved, 3)
    assert lab.file_text(readme) == README + EDIT_MARK, ("Saved bytes differ", lab.file_text(readme))

    # An outage while the user edits and saves: the text stays, and the
    # save that could not reach the core completes after the link returns.
    # On the cold GEO SSH profile, exceed the carrier liveness window. An
    # eight-second cut can be absorbed by TCP without an SSH reauthentication.
    outage_seconds = 30 if scenario.name == "L15" else 8
    auth_before = lab.ssh_authentications() if scenario.name == "L15" else None
    result["outage"] = {"drop": lab.act("drop", "both")}
    began = time.monotonic()
    shell.keys("Enter")
    probes["editor_offline"] = shell.probe("editor_offline", OFFLINE_MARK)
    local_echo(probes["editor_offline"], "editor during outage")
    shell.keys("C-s")
    delay(max(0, began + outage_seconds - time.monotonic()))
    assert OFFLINE_MARK not in lab.file_text(readme), "A save crossed a blocked link"
    result["outage"]["restore"] = lab.act("restore", None)
    m["outage_s"] = round(time.monotonic() - began, 3)
    restored = time.monotonic()
    until("offline save after reconnection", lambda: OFFLINE_MARK in lab.file_text(readme), 60 if geo else 40)
    result["outage"]["save_completed_without_retry"] = True
    m["offline_save_after_restore_s"] = round(time.monotonic() - restored, 3)
    assert lab.file_text(readme) == README + EDIT_MARK + "\n" + OFFLINE_MARK, ("Saved bytes differ", lab.file_text(readme))

    if auth_before is not None:
        auth_after = lab.ssh_authentications()
        assert auth_after > auth_before, "Long GEO outage did not force a fresh SSH authentication"
        result["outage"]["ssh_authentications_after_cut"] = auth_after - auth_before

    # Back to the agent: the stream that ran under the editor is exact.
    shifts["agent tab"] = shell.settled_click("agent tab", agent, row="README.md ×")
    wait("p2_done_s", lambda s: complete(s, "P2", plans["P2"][0]), 60 if geo else 40)

    # The agent asks; the user answers in the product UI.
    shell.type("LAB Q1 ASK")
    wait("q1_draft_s", lambda s: draft_visible(s, "LAB Q1 ASK"), 5)
    shell.keys("Enter")
    asked = stream_event("Q1", "ask", 40)
    wait("question_visible_s", lambda s: "Pick the lab answer" in s and "○ Bravo" in s, 30)
    m["ask_to_visible_s"] = round(time.monotonic() - asked["mono"], 3)
    shifts["question choice"] = shell.settled_click("question choice", "○ Bravo")
    wait("answer_review_s", lambda s: "Submit" in s and "Answer:" in s, 15)
    shifts["question submit"] = shell.settled_click("question Submit", "Submit")
    answered = time.monotonic()
    wait("answer_result_s", lambda s: "Q1 ANSWERED Bravo" in s, 45 if geo else 25)
    m["answer_to_result_s"] = round(time.monotonic() - answered, 3)

    # A command in the remote terminal; its output proves it ran there.
    shell.keys("C-M-t")
    wait("terminal_prompt_s", lambda s: "crude@" in s and "$" in s, 30)
    # First establish that the foreground process really echoes on this line.
    # Predicting before this confirmation can disclose input at a hidden prompt.
    confirm = time.monotonic()
    shell.type(TERMINAL_COMMAND[0])
    wait("terminal_echo_confirmation_s", lambda s: "$ e" in s, 15)
    m["terminal_echo_confirmation_s"] = round(time.monotonic() - confirm, 3)
    probes["terminal"] = shell.probe("terminal", TERMINAL_COMMAND[1:], settle_s=30)
    if scenario.name in {"L3", "L7"}:
        local_echo(probes["terminal"], "terminal after confirmed echo")
    assert probes["terminal"]["complete"] and not probes["terminal"]["lost"], ("terminal keys lost", probes["terminal"]["lost"])
    ran = time.monotonic()
    shell.keys("Enter")
    wait("terminal_output_s", lambda s: "LABT42" in s, 30)
    m["terminal_output_s"] = round(time.monotonic() - ran, 3)

    shell.type("IFS= read -rs -p 'Hidden prompt: ' reply; echo; echo SIZE=${#reply}")
    shell.keys("Enter")
    wait("hidden_terminal_prompt_s", lambda s: any(line.rstrip().endswith("Hidden prompt:") for line in s.splitlines()), 20)
    hidden = shell.probe("terminal_hidden", "~~~~~~~~", settle_s=1)
    assert hidden["lost"] == list(range(1, 9)) and not hidden["complete"], "The shell displayed hidden terminal input"
    shell.keys("Enter")
    wait("hidden_input_received_s", lambda s: "SIZE=8" in s, 20)

    log = lab.provider_requests()
    validate_provider([e for e in log if e["tag"].startswith("P")], plans)
    q1 = [e for e in log if e["tag"] == "Q1"]
    assert [e["event"] for e in q1] == ["request", "ask", "request", "finish"] and q1[-1]["choice"] == "Bravo", q1
    moved = {name: moves for name, moves in shifts.items() if moves}
    assert not moved, ("Controls moved after they were painted", moved)
    result["checks"] += ["Composer and editor keys echoed locally without loss, also during an outage",
                         "Clicked controls held still once painted",
                         "The editor saved exact bytes while the agent streamed, and again after the outage",
                         "The concurrent stream was exact; the agent question resolved once with the user's answer",
                         "A remote terminal command ran and printed its output"]
    return plans


def native_views(lab, shell, scene="shared-editor"):
    """Present the actual client's tmux session in the repository capture driver."""
    wrapper = lab.out / "view-client"
    wrapper.write_text("#!/bin/sh\nexec " + shlex.join(lab.docker.base + ["exec", "-it", "-u", "crude", shell.container,
                       "tmux", "-S", TMUX, "attach-session", "-t", "lab"]) + "\n")
    wrapper.chmod(0o700)
    try:
        for cols, rows in (VIEWPORT, (84, 26)):
            out = lab.out / f"native-{cols}x{rows}"
            env = dict(os.environ, CRUDE_BIN=str(wrapper), CRUDE_ARGS="", COLS=str(cols), ROWS=str(rows), KEEP_OPEN="0")
            with (lab.out / f"capture-{cols}.log").open("w") as log:
                subprocess.run([HERE.parent / "tuishot.sh", out, f"shot:{scene}"], env=env, check=True,
                               stdout=log, stderr=subprocess.STDOUT, timeout=90)
            assert list(out.glob("*.png")), "Native capture produced no images"
    finally:
        wrapper.unlink()
        shell.tmux("resize-window", "-t", "lab", "-x", str(VIEWPORT[0]), "-y", str(VIEWPORT[1]))


def startup_workspace_ready(frame):
    return "Trust this folder?" not in frame and "New Session" in frame and "startup-proof.txt" in frame


def startup_lane(lab, result, native):
    """Measure actual startup on three lifetimes, not an attach response or a spinner."""
    server, client = lab.cname("server"), lab.cname("client-a")
    lab.write("server", PROJECT + "/startup-proof.txt", "Startup proof.\n", owner="crude")
    result["ping"] = lab.ping(count=6)
    metrics = result["metrics"]
    started = time.monotonic()
    lab.docker.sh(client, "ssh -T -o BatchMode=yes -o StrictHostKeyChecking=yes "
                  "-o ControlMaster=no -o ControlPath=none server true", user="crude", timeout=30)
    metrics["plain_ssh_s"] = round(time.monotonic() - started, 3)
    auths = lab.ssh_authentications()
    assert auths == 1, "The reference SSH command did not authenticate exactly once"
    identities = []
    for phase in ("cold", "warm", "restart"):
        result["startup_phase"] = phase
        if phase == "restart":
            lab.docker.sh(server, "CRUDE_HOME=/home/crude crude --core-stop", user="crude", timeout=30)
        shell = lab.shells["a"] = Shell(lab, "a")
        launched = time.monotonic()
        wall_offset = time.time() - time.monotonic()
        shell.launch(["crude", "--ssh", "server", "--workspace", PROJECT, "--client-profile", "lab-a"],
                     capture_startup=lab.scenario.name in COLD_SLOW)
        trust_times = None
        if phase == "cold":
            trust, _ = shell.wait("first trust prompt", lambda s: "Trust this folder?" in s, 60)
            prompted = time.monotonic()
            (lab.out / "cold-trust.txt").write_text(trust)
            if native:
                native_views(lab, shell, "workspace-trust")
            trust_times = (prompted, time.monotonic())
            shell.keys("Enter")  # The safe default persists Restricted, not Trusted.
        screen, _ = shell.wait(f"{phase} file tree and launcher", startup_workspace_ready, 60)
        opened = time.monotonic()
        (lab.out / f"{phase}-workspace.txt").write_text(screen)
        times = lab.ssh_authentication_times()
        assert len(times) == auths + 1, (f"{phase} requires exactly one authenticated SSH connection", len(times) - auths)
        auths = len(times)
        authenticated = times[-1] - wall_offset
        assert launched <= authenticated <= opened, "Startup observations are out of order"
        metrics[f"{phase}_auth_s"] = round(authenticated - launched, 3)
        metrics[f"{phase}_auth_to_workspace_s"] = round(opened - authenticated, 3)
        if trust_times is not None:
            prompted, accepted = trust_times
            metrics["cold_auth_to_trust_prompt_s"] = round(prompted - authenticated, 3)
            metrics["cold_trust_choice_wait_s"] = round(accepted - prompted, 3)
            metrics["cold_trust_choice_to_workspace_s"] = round(opened - accepted, 3)
        with shell.lock:
            if phase != "cold":
                assert all("Trust this folder?" not in frame for _, frame in shell.frames), f"{phase} asked again"
        shell.settled_click("Open the startup proof from the file tree", "startup-proof.txt")
        shell.wait("startup proof in the editor", lambda s: "Startup proof." in s, 30)
        shell.keys("C-w")
        shell.wait("workspace after closing the editor", startup_workspace_ready, 15)
        identity = lab.observe("core.status")
        assert not identity.get("partial"), "Core identity is incomplete"
        identities.append((identity["pid"], identity["started_at"]))
        if lab.observer:
            lab.observer.close()
            lab.observer = None
        shell.keys("C-q")
        until(f"{phase} shell exit", lambda: shell.dead is not None, 15)
        assert shell.dead in {"0", "unknown"}, f"{phase} shell did not close cleanly: {shell.dead}"
        shell.close()
        shell.tmux("kill-session", "-t", "lab")
    assert identities[0] == identities[1] != identities[2], "Warm reopen or core restart used the wrong owner"
    result["startup_identities"] = identities
    result["checks"] += [
        "Cold start showed the trust choice, then loaded the actual file tree",
        "Warm reopen reused the core and did not ask for trust again",
        "Restart used a new core without losing the saved trust choice",
        "The file opened from the tree and showed the remote bytes in every phase",
    ]


def peer_lane(lab, shell, result, submit, stream_event, native):
    """Two client namespaces and independent leases. Only A loses its link."""
    peer = lab.shells["b"] = Shell(lab, "b")
    level = lab.scenario
    lab.shape_before_attach("b")
    peer.launch(["crude", "--ssh", "server", "--workspace", PROJECT, "--client-profile", "lab-b"])
    peer.wait("second workspace", lambda s: "Guide.md" in s, 75)
    with peer.connection():
        _, fields = peer.wait("second carrier", lambda s: (f := connection_fields(s)) and
                              f["State"] == "connected" and f["Transport"] == TRANSPORT[lab.carrier] and f, 30)
        result["connections"].append({"label": "peer_connected", "client": "b", "mono": time.monotonic(), **fields})
    result["peer_warm_shape"] = lab.shape_after_attach("b")
    result["peer_ping"] = lab.ping("b", 10)
    if level.warm_shape:
        assert 670 <= (result["peer_ping"]["avg_ms"] or 0) <= 850, ("Peer GEO RTT not observed", result["peer_ping"])
    submit("P2", level.stream)
    stream_event("P2", "first", 40)
    peer.settled_click("peer project file", "Guide.md")
    peer.wait("peer editor", lambda s: "Peer document." in s, 30)
    shell.settled_click("source of Markdown link", "Links.md")
    shell.wait("link in editor", lambda s: "[jump](Guide.md#destination)" in s, 30)
    # No server stub or raw LSP invocation: resolution, trust, didOpen and
    # the selected definition all belong to the running product.
    shell.palette("Problems")
    shell.wait("Marksman diagnosed the broken Markdown link", lambda s: "dead.md" in s and
               ("non-existent" in s.lower() or "does not exist" in s.lower() or "broken link" in s.lower()), 50)
    shell.keys("Escape")
    shell.wait("source editor after reading diagnostic", lambda s: "[jump](Guide.md#destination)" in s, 10)
    shell.settled_click("link destination under caret", "Guide.md#destination")
    shell.keys("M-.")
    shell.wait("definition opened the target", lambda s: "Peer document." in s, 35)
    if native:
        native_views(lab, shell)
    result["peer_outage"] = lab.act("drop", "both")
    peer.keys("C-End")
    peer.type("Peer edited while A was offline.")
    peer.keys("C-s")
    path = PROJECT + "/Guide.md"
    txt = "# Guide\n\n## Destination\n\nPeer document.\nPeer edited while A was offline."
    until("peer saved during A's outage", lambda: lab.file_text(path) == txt, 35)
    assert "Peer edited while A was offline." not in shell.latest()
    result["peer_restore"] = lab.act("restore", None)
    shell.wait("peer edit on restored shell", lambda s: "Peer edited while A was offline." in s, 45)
    shell.keys("C-End")
    shell.type(" Both see this.")
    peer.wait("unsaved shared text", lambda s: "Both see this." in s, 45)
    assert lab.file_text(path) == txt
    peer.keys("C-s")
    until("saved another shell's shared edits", lambda: lab.file_text(path) == txt + " Both see this.", 35)
    # Review a real multi-file language-server edit. Cancel once to prove
    # preview has no effect, then apply; both the disk and the peer must agree.
    def review_heading():
        shell.wait("local save indicator cleared", lambda s: "Guide.md ×" in s and "● Guide.md" not in s, 30)
        shell.settled_click("target heading", "# Destination")
        shell.keys("F2")
        shell.wait("rename prompt", lambda s: "Rename symbol" in s, 15)
        shell.keys("C-a")
        shell.type("Renamed")
        shell.keys("Enter")
        try:
            shell.wait("multi-file rename review", lambda s: "Review rename" in s and "Apply rename" in s and "Links.md" in s, 30)
        except AssertionError:
            if "Notices" in shell.latest():
                shell.settled_click("read the refusal", "Notices")
            raise
    review_heading()
    shell.keys("Escape")
    assert lab.file_text(path) == txt + " Both see this."
    review_heading()
    shell.settled_click("apply reviewed rename", "Apply rename")
    peer.wait("peer saw reviewed edit", lambda s: "# Renamed" in s, 30)
    assert lab.file_text(path) == (txt + " Both see this.").replace("# Destination", "# Renamed")
    assert "#renamed" in lab.file_text(PROJECT + "/Links.md").lower()
    shell.palette("Local History")
    shell.wait("history loaded", lambda s: "Local History" in s and "Before LSP rename" in s and "kept for safety" in s, 20)
    shell.keys("Escape")
    shell.palette("Application Settings")
    shell.wait("settings window", lambda s: "Settings" in s and "Accounts" in s, 15)
    shell.settled_click("settings search", "Search settings")
    shell.type("Remote access")
    frame, _ = shell.wait("remote administration is read-only", lambda s: "Unavailable: remote access administration" in s, 15)
    setting = re.search(r"Allow remote access\s+‹ (On|Off)\s+›", frame)
    assert setting, "Missing remote access setting"
    shell.settled_click("remote access switch", "Allow remote access")
    shell.keys("Space")
    shell.wait("remote cannot change host listener policy", lambda s: bool(re.search(
               r"Allow remote access\s+‹ " + setting[1] + r"\s+›", s)) and "Unavailable: remote access administration" in s, 5)
    shell.keys("Escape")
    stream_event("P2", "finish", 25)
    validate_provider(lab.provider_requests(), {"P2": level.stream})
    result["checks"] += ["Marksman definition opened its exact file through the editor",
                         "The peer edited and saved while A was cut off; A caught up without reopening",
                         "One client saved the other's unsaved text while the same agent ran",
                         "Problems showed the broken link; the reviewed rename changed both files only on approval",
                         "Local History retained the pre-rename snapshot; remote access settings stayed read-only"]


def run_scenario(docker, image, volume, run, scenario, carrier, root, grace, seed, keep, case=None, lane="stream", native=False):
    case = case or f"{scenario.name}-{carrier}"
    out = root / case
    out.mkdir()
    lab = Lab(docker, image, volume, run, scenario, carrier, out, grace, seed, case)
    if lane == "peer":
        lab.clients = ("a", "b")
    result = {"scenario": dataclasses.asdict(scenario), "carrier": carrier, "case": case, "lane": lane, "passed": False,
              "checks": [], "metrics": {}, "connections": [], "events": [], "resources": lab.name}
    m = result["metrics"]
    started = time.monotonic()
    phase = "provision"
    try:
        delay(0)
        lab.up()
        result["netem_seed"] = {"up": seed, "down": seed + 1} if lab.seed_supported else "unsupported"
        if lab.seed_probe:
            result["netem_seed_reason"] = lab.seed_probe
        result["anchors"] = {"mono": time.monotonic(), "unix": time.time()}
        result["path"] = lab.prove_path()
        lab.shape_before_attach("a")
        result["before_launch"] = {"tc": lab.link_stats(), "udp_drop": lab.counters("LAB_UDP")}
        if lane == "startup":
            phase = "startup"
            startup_lane(lab, result, native)
            result["passed"] = True
            return result
        geo = scenario.name in {"L7", "L15"}
        cold_slow = scenario.name in COLD_SLOW

        def measure_link():
            result["ping"] = lab.ping(count=10)
            if geo:
                assert 670 <= (result["ping"]["avg_ms"] or 0) <= 850, ("GEO RTT not observed", result["ping"])
            if scenario.name == "L16":
                assert (result["ping"]["avg_ms"] or 0) > 1000, ("Path does not exceed one second", result["ping"])

        if cold_slow:
            measure_link()
        shell = lab.shells["a"] = Shell(lab, "a")

        def wait(description, predicate, timeout):
            began = time.monotonic()
            frame, hit = shell.wait(description, predicate, timeout)
            m[description] = round(time.monotonic() - began, 3)
            return frame, hit

        def connection(label, predicate, timeout):
            with shell.connection():
                _, observed = wait(label, lambda s: (fields := connection_fields(s)) and predicate(fields) and fields, timeout)
                result["connections"].append({"label": label, "mono": time.monotonic(), **observed})
                return observed

        def connected(label, allowed, timeout):
            transports = {TRANSPORT[c] for c in allowed}
            return connection(label, lambda f: f["State"] == "connected" and f["Transport"] in transports, timeout)

        def submit(tag, plan):
            prompt = f"LAB {tag} STREAM {plan[0]}x{plan[1]}"
            shell.type(prompt)
            wait(f"{tag}_draft_s", lambda s: draft_visible(s, prompt), 5)
            shell.keys("Enter")

        def stream_event(tag, event, timeout):
            return until(f"provider {tag} {event}", lambda: next((e for e in lab.provider_requests()
                           if e["tag"] == tag and e["event"] == event), None), timeout)

        phase = "attach"
        launch = time.monotonic()
        result["launched_unix"] = time.time()
        shell.launch(["crude", "--ssh", "server", "--workspace", PROJECT, "--client-profile", "lab-a"],
                     capture_startup=cold_slow)
        wait("trust_prompt_s", lambda s: "Trust this folder?" in s, 45 if scenario.name == "L15" else 75)
        if cold_slow:
            auth = lab.ssh_authentications()
            assert auth == 1, ("Cold startup needs one authenticated SSH connection", auth)
            result["startup_ssh_authentications"] = auth
            shell.tmux("pipe-pane", "-t", "lab")
        if lane == "peer":
            shell.settled_click("Trust the lab project", "Trust folder")
        else:
            shell.keys("Enter")
        wait("trusted_launcher_s", lambda s: "New Session" in s and "Trust this folder?" not in s, 15)
        # Read the actual connected state before asking this attachment to
        # create its first session. A launcher can paint before its topics
        # and connection details are ready.
        connected("initial_connection_s", {carrier}, 28)
        frame, _ = wait("launcher_s", lambda s: "New Session" in s and "Trust this folder?" not in s, 15)
        shell.type(painted_click(frame, "New Session"))
        wait("session_s", lambda s: "fixture-model" in s, 25)
        m["attach_to_session_s"] = round(time.monotonic() - launch, 3)
        result["identity_before"] = lab.identity()
        result["shell_before"] = lab.shell_identity(shell)
        assert len(result["identity_before"]["root_agents"]) == 1, "Expected one root agent and session"
        if carrier == "tls":
            assert lab.counters("LAB_UDP").get("up", {}).get("packets", 0) > 0, "No QUIC attempt hit the pre-attach UDP block"
        if scenario.warm_shape:
            result["warm_shape"] = lab.shape_after_attach("a")
        if not cold_slow:
            measure_link()

        def same_owners():
            assert lab.identity() == result["identity_before"], "Workspace, session, agent, or core changed"
            assert lab.shell_identity(shell) == result["shell_before"], "Shell process changed"
            result["checks"].append("The same shell, workspace, core, root agent and session completed the work")
            failures = lab.scan_failures()
            assert not failures, ("Panic or fatal error in logs", failures)

        if lane == "peer":
            phase = "peer"
            peer_lane(lab, shell, result, submit, stream_event, native)
            same_owners()
            result["passed"] = True
            return result

        if lane == "workflow":
            phase = "workflow"
            workflow_lane(lab, shell, result, wait, submit, stream_event, scenario, geo)
            same_owners()
            result["passed"] = True
            return result

        phase = "control turn"
        plans = {"P1": (20, 100), "P2": scenario.stream}
        submit("P1", plans["P1"])
        wait("p1_done_s", lambda s: complete(s, "P1", plans["P1"][0]), 40)

        phase = "start impaired turn"
        submit("P2", plans["P2"])
        first = stream_event("P2", "first", 40)
        t0 = first["mono"]
        assert 0 <= time.monotonic() - t0 < 3, "Provider timestamp is not on the lab monotonic clock"
        result["p2_first"] = first
        typed = None
        sent_offline = False
        active_drop = None
        for offset, action, argument in scenario.events:
            phase = f"event {action} at +{offset}s"
            delay(t0 + offset - time.monotonic())
            event = {"planned_s": offset, "start_s": round(time.monotonic() - t0, 3),
                     "action": action, "argument": argument}
            result["events"].append(event)
            assert abs(event["start_s"] - offset) < 1.5, ("Missed event schedule", event)
            if action == "watch_loss":
                connection("link_loss_s", lambda f: f["State"] in {"unavailable", "offline", "reconnecting"},
                           max(1, t0 + 45 - time.monotonic()))
                if typed:
                    wait("draft_during_loss_s", lambda s: draft_visible(s, typed), 4)
            else:
                if action == "deny_udp":
                    event["quic_before"] = lab.tuples(carrier="quic")
                    assert event["quic_before"], "No QUIC tuple before the UDP block"
                if scenario.name == "L9" and action == "restore" and active_drop == "up":
                    assert not any(e["tag"] == "P3" for e in lab.provider_requests()), "The draft bypassed the uplink block"
                event["evidence"] = lab.act(action, argument)
                if scenario.name == "L9" and action == "drop" and argument == "both":
                    # These flaps can fall between product heartbeats after
                    # both turns have finished. Verify the cut even if no
                    # product packet needed the path in this five-second slot.
                    lab.docker("exec", "-d", lab.cname("client-a"), "ping", "-n", "-c", "1", "-W", "1", "server")
                    event["probe"] = "ICMP echo during flap"
            event["end_s"] = round(time.monotonic() - t0, 3)
            if action == "drop":
                active_drop = argument
            elif action == "restore":
                counters = event["evidence"] or {}
                expected_directions = {"up", "down"} if active_drop == "both" else {active_drop}
                assert set(counters) == expected_directions and any(d["packets"] > 0 for d in counters.values()), ("No traffic hit the scoped drop", event)
                active_drop = None
            elif action == "type":
                typed = argument
                plans["P3"] = (5, 100)
                wait("draft_during_fault_s", lambda s: draft_visible(s, typed), 5)
            elif action == "enter":
                sent_offline = True

        phase = "recovery"
        restored = time.monotonic()
        recovery_deadline = restored + scenario.recovery
        allowed = {"tls"} if scenario.name == "L13" else {carrier}
        if scenario.name in {"L9", "L11", "L12", "L14"} and carrier != "ssh":
            # A sustained fault may exhaust the direct dial budget. Resuming
            # on the verified SSH path is product behavior, not data loss.
            allowed |= {"ssh", "tls"}
        result["allowed_recovery_carriers"] = sorted(allowed)
        if typed and not sent_offline:
            connected("reconnected_with_draft_s", allowed, scenario.recovery)
            assert not any(e["tag"] == "P3" for e in lab.provider_requests()), "Unsent draft was executed"
            wait("draft_after_reconnect_s", lambda s: draft_visible(s, typed), 5)
            shell.keys("Enter")
        expected = {tag: plan[0] for tag, plan in plans.items() if tag != "P1"}
        # Each stage shares one finite recovery deadline. The entire current
        # P2/P3 viewport and then fresh connection state must both be correct;
        # a healthy badge sampled before the failure was detected is not enough.
        wait("p2_and_draft_done_s", lambda s: all(complete(s, tag, n) for tag, n in expected.items()),
             max(0, recovery_deadline - time.monotonic()))
        def recovered(fields):
            if fields["State"] != "connected" or fields["Transport"] not in {TRANSPORT[c] for c in allowed}:
                return False
            if geo:
                rtt = re.fullmatch(r"(\d+) ms", fields["Round trip"])
                return rtt is not None and 500 <= int(rtt[1]) <= 1200
            return True

        final = connection("reconnected_s", recovered, max(0, recovery_deadline - time.monotonic()))
        m["after_last_event_s"] = round(time.monotonic() - restored, 3)
        finish = stream_event("P2", "finish", 5)
        result["p2_finish"] = finish
        for event in result["events"]:
            # The L9 flaps deliberately continue after the original stream.
            early_fault = event["action"] in {"drop", "pause", "rebind", "deny_udp", "shape"} and event["planned_s"] < 38
            if early_fault:
                assert t0 <= t0 + event["start_s"] < finish["mono"], ("Fault missed provider stream", event)
        if scenario.name == "L11":
            pause = next(e for e in result["events"] if e["action"] == "pause")
            unpause = next(e for e in result["events"] if e["action"] == "unpause")
            assert pause["end_s"] <= finish["mono"] - t0 < unpause["start_s"], "Provider did not finish while the client was frozen"

        phase = "fresh post-recovery turn"
        plans["P4"] = (5, 100)
        submit("P4", plans["P4"])
        p4_deadline = time.monotonic() + (65 if geo else 40)
        result["p4_first"] = stream_event("P4", "first", 25)
        wait("p4_done_s", lambda s: complete(s, "P4", plans["P4"][0]) and complete(s, "P2", plans["P2"][0])
             and (not typed or complete(s, "P3", plans["P3"][0])), max(0, p4_deadline - time.monotonic()))
        stream_event("P4", "finish", 5)
        log = lab.provider_requests()
        validate_provider(log, plans)
        with shell.lock:
            frames = list(shell.frames)
        result["stream_visibility"] = stream_visibility(frames, log, plans)
        result["checks"].append("Each expected stream is exact on the shell and executed once at the provider")
        if typed:
            result["checks"].append("The local draft appeared in the composer and was delivered without retyping")
        if carrier == "tls" or scenario.name == "L13":
            result["udp_drop_final"] = lab.counters("LAB_UDP")
            assert result["udp_drop_final"].get("up", {}).get("packets", 0) > 0, "No traffic hit the UDP block"
        if final["Transport"] == TRANSPORT["tls"]:
            result["tls_tuple"] = lab.tuples(carrier="tls")
            assert result["tls_tuple"], "No TCP/TLS tuple after recovery"
        if scenario.name == "L10":
            rebind = result["events"][0]["evidence"]
            rebind["after"] = lab.tuples()
            ports = lambda rows: {int(p[-1]) for row in rows if len(p := re.findall(r"\bdport=(\d+)", row)) == 2}
            changed = ports(rebind["after"]) - ports(rebind["before"])
            low, high = rebind["ports"]
            assert any(low <= p <= high for p in changed), ("No changed server-visible source port", rebind)
        same_owners()
        result["passed"] = True
    except Exception:
        result["phase"] = phase
        result["failure"] = traceback.format_exc()
        if phase == "attach" and "trusted_launcher_s" in m:
            # Distinguish a rejected action from a created session that the
            # shell did not receive. Observe only after the failed UI check.
            try:
                result["identity_at_failed_launch"] = lab.identity()
            except Exception as err:
                result["identity_observation_error"] = str(err)
    finally:
        m["elapsed_s"] = round(time.monotonic() - started, 1)
        for label, operation in (("stop_observer", lab.stop_observers), ("collect", lab.collect),
                                 ("cleanup", (lambda: None) if keep else lab.down)):
            try:
                operation()
            except Exception:
                result[label + "_failure"] = traceback.format_exc()
                result["passed"] = False
        (out / "result.json").write_text(json.dumps(result, indent=2))
    return result


def main():
    if not __debug__:
        raise RuntimeError("Run the lab without Python optimization so the checks stay active")
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--binary", required=True, help="prebuilt linux/amd64 Crude executable")
    parser.add_argument("--out", required=True, help="new evidence directory")
    parser.add_argument("--observer", required=True, help="prebuilt typed observer from the same source build")
    parser.add_argument("--image", default=default_image(), help="existing local lab image; never pulled")
    parser.add_argument("--levels", default="all", help="comma-separated levels, for example L0,L8")
    parser.add_argument("--carriers", help="comma-separated carriers (default: ssh,quic,tls; startup: ssh)")
    parser.add_argument("--parallel", type=int, default=1, choices=range(1, 13), metavar="1..12", help="concurrent scenarios")
    parser.add_argument("--grace", type=int, default=900, help="core session grace seconds")
    parser.add_argument("--seed", type=int, default=68170, help="netem RNG seed when the router supports it")
    parser.add_argument("--keep", action="store_true", help="keep this run's isolated containers, networks and volume")
    parser.add_argument("--lane", choices=("stream", "workflow", "peer", "startup"), default="stream",
                        help="stream: agent delivery; workflow: overlapping IDE work; peer: shared editing and LSP; startup: SSH launch")
    parser.add_argument("--repeat", type=int, default=1, choices=range(1, 51), metavar="1..50",
                        help="run each case this many times; a passing retry never hides an earlier failure")
    parser.add_argument("--marksman", type=Path, help="Marksman linux-x64 executable for the peer/LSP lane")
    parser.add_argument("--native", action="store_true", help="Capture the peer or startup journey using scripts/tuishot.sh")
    args = parser.parse_args()
    if (args.lane == "peer") != bool(args.marksman):
        parser.error("The peer lane requires --marksman; other lanes do not use a language server")
    if args.native and (args.lane not in {"peer", "startup"} or args.parallel != 1):
        parser.error("Native capture needs a serial peer or startup run")
    binary = Path(args.binary).resolve(strict=True)
    observer = Path(args.observer).resolve(strict=True)
    steady = [name for name, level in LEVELS.items() if not level.events]
    default_levels = ["L0"] if args.lane == "startup" else (steady if args.lane != "stream" else list(LEVELS))
    levels = default_levels if args.levels == "all" else list(dict.fromkeys(args.levels.split(",")))
    default_carriers = "ssh" if args.lane == "startup" else "ssh,quic,tls"
    carriers = list(dict.fromkeys((args.carriers or default_carriers).split(",")))
    if set(levels) - LEVELS.keys() or set(carriers) - TRANSPORT.keys():
        parser.error("Use known levels and carriers: " + ", ".join(TRANSPORT))
    if args.lane == "startup" and carriers != ["ssh"]:
        parser.error("The startup lane measures SSH; use --carriers ssh")
    if args.lane != "stream" and set(levels) - set(steady):
        parser.error("Choose steady levels for this lane: " + ",".join(steady))
    if args.lane == "startup" and any(LEVELS[level].warm_shape for level in levels):
        parser.error("Choose a profile applied before startup. Use L15 for a GEO-like startup.")
    if not 1 <= args.seed < 2**32 - 2 - 100 * args.repeat or not 180 <= args.grace <= 3600:
        parser.error("Use a seed between 1 and 4294967294 minus 100 for each repeat, and grace between 180 and 3600 seconds")
    specialized = {"L13": "quic", **dict.fromkeys(COLD_SLOW, "ssh")}
    jobs = [(LEVELS[level], carrier, attempt) for attempt in range(args.repeat) for level in levels for carrier in carriers
            if specialized.get(level, carrier) == carrier]
    if not jobs:
        parser.error("No matching cases. L13 requires QUIC. L15 and L16 require SSH.")
    # A lost control session (SIGHUP) stops the run like Ctrl+C, so its exact
    # resources are still removed. Start unattended runs detached instead.
    for stop_signal in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
        signal.signal(stop_signal, lambda *_: STOP.set())
    docker = Docker()
    inspected = docker("image", "inspect", "--format", "{{.Id}} {{.Os}}/{{.Architecture}}", args.image).stdout.split()
    if len(inspected) != 2 or inspected[1] != "linux/amd64":
        raise RuntimeError("Use a local linux/amd64 lab image")
    image = inspected[0]
    root = Path(args.out).resolve()
    root.mkdir(parents=True, exist_ok=False, mode=0o700)
    run = time.strftime("%m%d%H%M") + "-" + os.urandom(4).hex()
    run_label = f"{PREFIX}-{run}"
    volume = f"{run_label}-bin"
    manifest = {"run": run, "label": run_label, "host": os.uname().nodename, "kernel": os.uname().release,
                "machine": os.uname().machine, "docker_version": docker.version, "image": args.image, "image_id": image,
                "binary": str(binary), "binary_sha256": file_sha(binary), "observer": str(observer),
                "observer_sha256": file_sha(observer), "runner_sha256": file_sha(Path(__file__)),
                "provider_sha256": file_sha(HERE / "provider.py"), "levels": levels, "carriers": carriers,
                "jobs": [case_name(s, c, k, args.repeat) for s, c, k in jobs], "repeat": args.repeat, "lane": args.lane,
                "grace_seconds": args.grace, "seed": args.seed,
                "viewport": VIEWPORT, "limits": "Sampled tmux text at approximately 5 Hz, not a latency SLO. Same-host Linux network namespaces, not physical hosts."}
    if args.marksman:
        manifest["marksman_sha256"] = file_sha(args.marksman)
    (root / "manifest.json").write_text(json.dumps(manifest, indent=2))
    helper = f"{run_label}-stage"
    results = []
    docker("volume", "create", "--label", f"{LABEL}={run_label}", volume)
    try:
        docker("create", "--pull", "never", "--network", "none", "--name", helper, "--label", f"{LABEL}={run_label}",
               "-v", f"{volume}:/opt/crude", image, "true")
        for source, name in ((binary, "crude"), (observer, "observer"), (HERE / "provider.py", "provider.py"),
                             (HERE / "typeprobe.py", "typeprobe.py"),
                             (HERE.parent / "core_shell_acceptance.py", "core_shell_acceptance.py")):
            delay(0)
            docker("cp", str(source), f"{helper}:/opt/crude/{name}", timeout=180)
        if args.marksman:
            docker("cp", str(args.marksman.resolve(strict=True)), f"{helper}:/opt/crude/marksman", timeout=180)
        docker("rm", "-f", helper)
        with concurrent.futures.ThreadPoolExecutor(max_workers=args.parallel) as pool:
            # Each repeat samples another loss pattern where the router honours netem seeds.
            futures = {pool.submit(run_scenario, docker, image, volume, run, s, c, root, args.grace, args.seed + 100 * k,
                                   args.keep, case_name(s, c, k, args.repeat), args.lane, args.native): (s, c, k)
                       for s, c, k in jobs}
            for future in concurrent.futures.as_completed(futures):
                s, c, k = futures[future]
                r = future.result()
                results.append(r)
                print(f"{'PASS' if r['passed'] else 'FAIL'} {case_name(s, c, k, args.repeat)} {s.summary}: {r['metrics']}", flush=True)
                for key, value in r.items():
                    if key.endswith("failure"):
                        print(f"  {r.get('phase', key)}: {value.strip().splitlines()[-1]}", flush=True)
    except Cancelled:
        pass  # finally still closes this exact staging container and volume
    finally:
        try:
            ids = docker("ps", "-aq", "--filter", f"label={LABEL}={run_label}").stdout.split()
            if ids:
                docker("rm", "-f", *ids)
            if not args.keep:
                docker("volume", "rm", volume)
        finally:
            (root / "summary.json").write_text(json.dumps(results, indent=2))
    passed = sum(r["passed"] for r in results)
    print(json.dumps({"passed": passed, "total": len(jobs), "evidence": str(root), "cancelled": STOP.is_set()}))
    return 130 if STOP.is_set() else (0 if passed == len(jobs) else 1)


def file_sha(path):
    with path.open("rb") as file:
        return hashlib.file_digest(file, "sha256").hexdigest()


if __name__ == "__main__":
    raise SystemExit(main())
