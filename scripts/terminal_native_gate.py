#!/usr/bin/env python3
"""Opt-in Linux terminal measurement; never claims outer-emulator displayed FPS.

Build in the native cgo environment:
  . scripts/libghostty_env.sh
  go test -c -o "$CRUDE_TEMP_DIR/terminal-gate.test" ./tui/viewmodel
  python3 scripts/terminal_native_gate.py --test-binary ... --out NEW_DIRECTORY

Use --workload mpv --mpv /absolute/path/mpv --video /absolute/path/video to
exercise real mpv --vo=tct. --native-output writes the same renderer output to
the caller's terminal; only scripts/tuishot.sh should own native capture.

strace adds overhead. Its p99 is a conservative instrumented measurement, not
an uninstrumented production claim. No ptrace attach, sudo, or host changes.
"""

import argparse
import json
import math
import os
from pathlib import Path
import re
import select
import shutil
import subprocess
import sys
import tempfile
import time


def child():
    """Real raw PTY workload; synchronized full 200x50 redraws at 60 Hz."""
    import tty

    if os.environ.get("CRUDE_TERMINAL_GATE_WORKLOAD") == "mpv":
        mpv = os.environ["CRUDE_TERMINAL_GATE_MPV"]
        os.execv(mpv, [mpv, "--no-config", "--vo=tct", "--vo-tct-width=200",
                          "--vo-tct-height=50", "--no-audio", "--loop-file=inf",
                          os.environ["CRUDE_TERMINAL_GATE_VIDEO"]])
    tty.setraw(sys.stdin.fileno())
    oracle = Path(os.environ["CRUDE_TERMINAL_GATE_ORACLE"]).open("w", buffering=1)
    frames = Path(os.environ["CRUDE_TERMINAL_GATE_FRAMES"]).open("w", buffering=1)
    os.write(1, b"\x1b[?1049h\x1b[?25l")
    frame = 0
    deadline = time.monotonic()
    while True:
        now = time.monotonic()
        if now >= deadline:
            rows = ["\x1b[?2026h\x1b[H"]
            # Every cell changes truecolor, not only a counter or cursor.
            for y in range(50):
                rows.append(f"\x1b[{y + 1};1H")
                for x in range(200):
                    r = (x * 3 + frame * 7) % 256
                    g = (y * 5 + frame * 11) % 256
                    b = (x + y + frame * 13) % 256
                    rows.append(f"\x1b[48;2;{r};{g};{b}m ")
            rows.append("\x1b[0m\x1b[?2026l")
            data = "".join(rows).encode("ascii")
            while data:
                data = data[os.write(1, data):]
            frame += 1
            frames.write(json.dumps({"completed_unix_ns": time.time_ns(), "frame": frame}) + "\n")
            deadline += 1 / 60
            # Do not replay a delayed frame backlog, but retain missed deadlines
            # in the oracle. A slow source cannot manufacture a passing FPS gate.
            if time.monotonic() - deadline > 1 / 60:
                deadline = time.monotonic()
        readable, _, _ = select.select([0], [], [], max(0, deadline - time.monotonic()))
        if readable:
            data = os.read(0, 4096)
            if not data:
                return
            oracle.write(json.dumps({"received_unix_ns": time.time_ns(),
                                     "input_hex": data.hex(), "frame": frame}) + "\n")


# -ff gives one file per tracee. -ttt uses wall-clock microseconds. Match ONLY
# successful one-byte writes to the PTY master, not encoded IPC/child echoes.
WRITE = re.compile(r'^(\d+)\.(\d+)\s+write\(\d+<(/dev/(?:pts/ptmx|ptmx))>, "j", 1\)\s+= 1(?:\s|$)')


def read_writes(paths):
    writes = []
    for path in paths:
        with path.open(errors="replace") as stream:
            for line in stream:
                match = WRITE.match(line)
                if match:
                    writes.append(int(match[1]) * 10**9 + int(match[2].ljust(9, "0")))
    return sorted(writes)


def summarize(result, writes):
    keys = result["keys"]
    failures = []
    if len(keys) != 100 or len(writes) != len(keys):
        failures.append(f"expected exactly 100 keys and matching PTY writes: keys={len(keys)} writes={len(writes)}")
    latencies = [(write - key["enqueue_unix_ns"]) / 1e6 for key, write in zip(keys, writes)]
    if any(value < 0 for value in latencies):
        failures.append("negative wall-clock latency; invalid clock/matching evidence")
    ordered = sorted(latencies)
    p99 = ordered[max(0, math.ceil(len(ordered) * .99) - 1)] if ordered else None
    duration = (result["end_unix_ns"] - result["start_unix_ns"]) / 1e9
    fps = len(result["content_flush_unix_ns"]) / duration
    limit = 3 if result["workload"] == "flood" else 20
    if p99 is None or p99 >= limit:
        failures.append(f"key enqueue -> PTY write p99 must be <{limit}ms: {p99}")
    if result["shell_active_cpu_seconds"] <= 0 or result["shell_gc_cpu_percent"] >= 5:
        failures.append(f"shell GC CPU must be <5% of active runtime CPU: {result['shell_gc_cpu_percent']}")
    # Writer throughput is necessary, but not sufficient, for presented 30 FPS.
    # There is deliberately no invented tolerance or relaxed presentation gate.
    if fps < 30:
        failures.append(f"content-bearing writer throughput below necessary 30 FPS: {fps}")
    return {"measurement": "instrumented renderer-writer and real PTY syscall",
            "key_origin": "immediately before Program.Send (includes queue admission)",
            "key_sink": "successful one-byte PTY-master write syscall entry (strace -ttt)",
            "key_samples": len(latencies), "key_to_pty_write_p99_ms": p99,
            "key_to_pty_write_max_ms": max(latencies, default=None),
            "key_to_pty_write_samples_ms": latencies,
            "content_writer_fps": fps, "shell_gc_cpu_percent": result["shell_gc_cpu_percent"],
            "instrumented_checks_pass": not failures, "failures": failures,
            "outer_emulator_displayed_fps": None,
            "presentation_gate_status": "UNVERIFIED: native pixel/display evidence required",
            "latency_quantization_us": 1, "strace_overhead_included": True,
            "syscall_filter": "seccomp-bpf: write,writev only; successful PTY-master write correlation unchanged"}


def self_test():
    import unittest

    class Checks(unittest.TestCase):
        def test_exact_master_only(self):
            self.assertIsNotNone(WRITE.match('1790000000.123456 write(9</dev/pts/ptmx>, "j", 1) = 1\n'))
            for line in ['1790000000.123456 write(1</dev/pts/5>, "j", 1) = 1',
                         '1790000000.123456 write(9</dev/pts/ptmx>, "j", 1) = -1 EAGAIN',
                         '1790000000.123456 write(9<socket:[123]>, "j", 1) = 1']:
                self.assertIsNone(WRITE.match(line))

        def test_thresholds_are_strict_and_presentation_unknown(self):
            result = {"keys": [{"enqueue_unix_ns": 0}] * 100, "workload": "flood",
                      "start_unix_ns": 0, "end_unix_ns": 10**9,
                      "content_flush_unix_ns": list(range(30)),
                      "shell_active_cpu_seconds": 1, "shell_gc_cpu_percent": 4.99}
            good = summarize(result, [2_999_000] * 100)
            self.assertTrue(good["instrumented_checks_pass"])
            self.assertIsNone(good["outer_emulator_displayed_fps"])
            self.assertFalse(summarize(result, [3_000_000] * 100)["instrumented_checks_pass"])
            result["shell_gc_cpu_percent"] = 5
            self.assertFalse(summarize(result, [1] * 100)["instrumented_checks_pass"])

        def test_missing_or_preceding_write_fails_closed(self):
            result = {"keys": [{"enqueue_unix_ns": 10}] * 100, "workload": "mpv",
                      "start_unix_ns": 0, "end_unix_ns": 10**9,
                      "content_flush_unix_ns": list(range(30)),
                      "shell_active_cpu_seconds": 1, "shell_gc_cpu_percent": 1}
            self.assertFalse(summarize(result, [1] * 100)["instrumented_checks_pass"])
            self.assertFalse(summarize(result, [11] * 99)["instrumented_checks_pass"])

    suite = unittest.defaultTestLoader.loadTestsFromTestCase(Checks)
    return 0 if unittest.TextTestRunner().run(suite).wasSuccessful() else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--test-binary", type=Path)
    parser.add_argument("--out", type=Path)
    parser.add_argument("--workload", choices=("flood", "mpv"), default="flood")
    parser.add_argument("--mpv", type=Path)
    parser.add_argument("--video", type=Path)
    parser.add_argument("--seconds", type=int, choices=range(6, 31), default=8)
    parser.add_argument("--native-output", action="store_true")
    parser.add_argument("--no-strace", action="store_true", help="diagnostic FPS/GC only; key gate remains unverified and exit is nonzero")
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--child", action="store_true", help=argparse.SUPPRESS)
    args = parser.parse_args()
    if args.child:
        child()
        return 0
    if args.self_test:
        return self_test()
    if not args.test_binary or not args.out:
        parser.error("--test-binary and a new --out directory are required")
    if not args.no_strace and not shutil.which("strace"):
        parser.error("strace is required (or --no-strace for diagnostic-only evidence)")
    if args.workload == "mpv" and (not args.mpv or not args.video):
        parser.error("mpv workload requires --mpv and --video")
    if args.workload == "mpv" and (not args.mpv.is_file() or not args.video.is_file()):
        parser.error("mpv executable/video missing")
    binary = args.test_binary.resolve(strict=True)
    args.out.mkdir(parents=True, exist_ok=False)
    out = args.out.resolve()
    env = dict(os.environ, LC_ALL="C", CRUDE_TERMINAL_GATE="1",
               CRUDE_TERMINAL_GATE_SCRIPT=str(Path(__file__).resolve()),
               CRUDE_TERMINAL_GATE_PYTHON=sys.executable,
               CRUDE_TERMINAL_GATE_RESULT=str(out / "shell.json"),
               CRUDE_TERMINAL_GATE_ORACLE=str(out / "child-input.jsonl"),
               CRUDE_TERMINAL_GATE_FRAMES=str(out / "child-frames.jsonl"),
               CRUDE_TERMINAL_GATE_SECONDS=str(args.seconds),
               CRUDE_TERMINAL_GATE_WORKLOAD=args.workload,
               CRUDE_TERMINAL_GATE_NATIVE_OUTPUT="1" if args.native_output else "0")
    env.pop("CRUDE_TERMINAL_GATE_CORE", None)
    env.pop("CRUDE_TERMINAL_GATE_TRACE", None)
    if not args.no_strace:
        env["CRUDE_TERMINAL_GATE_TRACE"] = str(out / "pty-write.trace")
    if args.mpv:
        env["CRUDE_TERMINAL_GATE_MPV"] = str(args.mpv.resolve())
    if args.video:
        env["CRUDE_TERMINAL_GATE_VIDEO"] = str(args.video.resolve())
    # Short, private socket-bound data; durable evidence stays in --out. Runtime
    # shutdown joins/reaps its owned children before this context removes data.
    with tempfile.TemporaryDirectory(prefix="ctgate-", dir="/tmp") as private:
        env["CRUDE_TERMINAL_GATE_DIR"] = private
        with (out / "test.log").open("wb") as log:
            run = subprocess.run([str(binary), "-test.run=^TestTerminalNativeGate$", "-test.timeout=60s"],
                                 env=env, stdin=None if args.native_output else subprocess.DEVNULL,
                                 stdout=None if args.native_output else log, stderr=log,
                                 timeout=65, check=False)
    if run.returncode:
        print(f"fixture failed ({run.returncode}); inspect {out / 'test.log'}", file=sys.stderr)
        return 1
    result = json.loads((out / "shell.json").read_text())
    summary = summarize(result, read_writes(out.glob("pty-write.trace.*")))
    if args.no_strace:
        summary["key_gate_status"] = "UNVERIFIED: strace explicitly disabled"
        summary["strace_overhead_included"] = False
    if args.workload == "flood":
        oracle = [json.loads(line) for line in (out / "child-input.jsonl").read_text().splitlines()]
        actual = b"".join(bytes.fromhex(event["input_hex"]) for event in oracle)
        if actual != b"j" * 100:
            summary["failures"].append(f"raw child received {actual!r}, expected exactly 100 j bytes")
            summary["instrumented_checks_pass"] = False
    if args.workload == "flood":
        frames = [json.loads(line) for line in (out / "child-frames.jsonl").read_text().splitlines()]
        measured = [frame for frame in frames if result["start_unix_ns"] <= frame["completed_unix_ns"] <= result["end_unix_ns"]]
        summary["source_fullscreen_redraw_hz"] = len(measured) / ((result["end_unix_ns"] - result["start_unix_ns"]) / 1e9)
        summary["source_target_hz"] = 60
        summary["source_boundary"] = "child completed writing one DEC2026 full 200x50 truecolor frame to PTY slave"
        # Floor only handles a measurement window ending partway through one
        # 60Hz period; no percentage tolerance or relaxed latency/GC ceiling.
        expected = math.floor((result["end_unix_ns"] - result["start_unix_ns"]) * 60 / 1e9)
        summary["flood_60hz_condition_verified"] = len(measured) >= expected
        if not summary["flood_60hz_condition_verified"]:
            summary["failures"].append(f"60Hz flood condition not sustained: {len(measured)} completed source redraws, required {expected}")
            summary["instrumented_checks_pass"] = False
    (out / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")
    print(json.dumps(summary, indent=2))
    return 0 if summary["instrumented_checks_pass"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
