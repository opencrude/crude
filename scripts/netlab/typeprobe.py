#!/usr/bin/env python3
"""Paced typing with in-container frame sampling for the network lab.

The runner reaches a client container through ``docker exec``, which costs tens
of milliseconds per call and cannot time one keystroke. This probe runs inside
the client container next to the shell's tmux server. One thread types the
text one character at a time at the requested pace; another captures the pane
as fast as tmux answers and keeps every changed frame with its monotonic time.
Containers share the host's monotonic clock, so the runner can compare these
times with provider events.

Each key is matched by the first frame after its send that shows the typed
prefix more often than the frame before typing began. The probe reports lost
keys, prefixes that disappear again after they were visible (a momentary wrong
frame), per-key echo latency and the sampler's own largest gap, so an
incomplete observation is visible instead of reading as smooth.
"""
import gzip
import json
import subprocess
import sys
import threading
import time

SOCKET = "/tmp/netlab-tmux.sock"


def tmux(*args):
    return subprocess.run(["tmux", "-S", SOCKET, "-f", "/dev/null", *args],
                          capture_output=True, text=True, timeout=5, check=True).stdout


def percentile(values, fraction):
    ordered = sorted(values)
    return ordered[min(len(ordered) - 1, int(fraction * len(ordered)))] if ordered else None


def observe_frame(capture, clock=time.monotonic):
    # The capture may start before a key and return the frame containing it.
    # The end of the read is the first time we can attest to its contents.
    frame = capture()
    return clock(), frame


def analyse(text, baseline, sends, frames):
    """Pure analysis, separate from I/O so the runner's tests can check it."""
    keys, regressions = [], []
    for k in range(1, len(text) + 1):
        prefix = text[:k]
        floor = baseline.count(prefix)
        sent_start, sent_end = sends[k - 1]
        seen = next((t for t, frame in frames if t >= sent_start and frame.count(prefix) > floor), None)
        keys.append({"key": k, "sent": sent_end, "visible": seen,
                     "latency_ms": None if seen is None else round(max(0.0, seen - sent_end) * 1000, 1)})
        if seen is None:
            continue
        # A prefix that was visible must stay visible: later keys only extend
        # it. Record each disappearance and how long it lasted. A capture can
        # land inside one partly written frame, so the runner judges duration.
        missing = None
        for t, frame in frames:
            if t <= seen:
                continue
            if frame.count(prefix) <= floor:
                missing = t if missing is None else missing
            elif missing is not None:
                regressions.append({"key": k, "missing_at": missing, "ms": round((t - missing) * 1000, 1)})
                missing = None
        if missing is not None:
            regressions.append({"key": k, "missing_at": missing, "ms": None})
    latencies = [key["latency_ms"] for key in keys if key["latency_ms"] is not None]
    return {
        "keys": len(text),
        "lost": [key["key"] for key in keys if key["visible"] is None],
        "regressions": regressions,
        "latency_ms": {"p50": percentile(latencies, .5), "p95": percentile(latencies, .95),
                       "max": max(latencies, default=None)},
        "per_key_ms": [key["latency_ms"] for key in keys],
        "sends": sends,
        "frames": len(frames),
    }


def main():
    spec = json.loads(sys.argv[1])
    text, pace, settle = spec["text"], spec["pace_ms"] / 1000, spec["settle_s"]
    frames, stop, lock = [], threading.Event(), threading.Lock()
    gaps, errors = [], []

    def sample():
        last, previous = None, None
        while not stop.is_set():
            started = time.monotonic()
            if previous is not None:
                gaps.append(started - previous)
            previous = started
            try:
                observed, frame = observe_frame(lambda: tmux("capture-pane", "-p", "-t", "lab"))
            except (OSError, subprocess.SubprocessError) as err:
                errors.append(str(err))
                stop.set()
                return
            if frame != last:
                with lock:
                    frames.append((observed, frame))
                last = frame
            time.sleep(.002)

    baseline = tmux("capture-pane", "-p", "-t", "lab")
    sampler = threading.Thread(target=sample, daemon=True)
    sampler.start()
    sends = []
    began = time.monotonic()
    for index, character in enumerate(text):
        due = began + index * pace
        time.sleep(max(0.0, due - time.monotonic()))
        sent = time.monotonic()
        tmux("send-keys", "-t", "lab", "-l", character)
        sends.append((sent, time.monotonic()))
    floor = baseline.count(text)
    deadline = time.monotonic() + settle
    complete = False
    while time.monotonic() < deadline and not complete:
        with lock:
            complete = bool(frames) and frames[-1][1].count(text) > floor
        time.sleep(.01)
    time.sleep(spec.get("tail_s", .5))
    stop.set()
    sampler.join(timeout=5)
    if sampler.is_alive() or errors or not gaps:
        raise RuntimeError(f"Incomplete typing observation: {errors}")
    with lock:
        captured = list(frames)
    result = analyse(text, baseline, sends, captured)
    result.update({"complete": complete, "pace_ms": spec["pace_ms"],
                   "sample_gap_ms": {"p50": round((percentile(gaps, .5) or 0) * 1000, 1),
                                     "max": round(max(gaps, default=0) * 1000, 1)},
                   "began": began, "ended": time.monotonic()})
    if spec.get("frames"):
        with gzip.open(spec["frames"], "wt") as out:
            for t, frame in captured:
                out.write(json.dumps({"t": t, "text": frame}) + "\n")
    print(json.dumps(result))


if __name__ == "__main__":
    main()
