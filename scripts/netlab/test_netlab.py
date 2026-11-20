"""Focused regressions for the runner's acceptance predicates."""
import threading
import time
import unittest

from netlab import Shell, complete, connection_fields, draft_visible, startup_workspace_ready, stream_visibility, validate_provider
from typeprobe import analyse, observe_frame


class AcceptancePredicates(unittest.TestCase):
    def test_startup_requires_file_tree_and_no_trust_modal(self):
        self.assertFalse(startup_workspace_ready("New Session\nLoading files"))
        self.assertFalse(startup_workspace_ready("New Session\nstartup-proof.txt\nTrust this folder?"))
        self.assertTrue(startup_workspace_ready("New Session\nstartup-proof.txt"))

    def test_numbered_stream_does_not_accept_missing_duplicate_or_reordered_tokens(self):
        words = [f"P2n{n:02d}" for n in range(1, 161)]
        text = " ".join(words + ["P2end"])
        self.assertTrue(complete(text, "P2", 160))
        for bad in (text.replace("P2n100 ", ""),
                    text.replace("P2n100 ", "P2n100 P2n100 "),
                    text.replace("P2n100 P2n101 ", "P2n101 P2n100 "),
                    text + " P2n200"):
            self.assertFalse(complete(bad, "P2", 160))

    def test_composer_does_not_read_the_same_prompt_in_the_transcript(self):
        prompt = "LAB P3 STREAM 5x100"
        self.assertFalse(draft_visible(f"│ {prompt}\n│ ──\n│ \n━━━━\nFooter\n", prompt))
        self.assertTrue(draft_visible(f"│ previous\n│ ──\n│ {prompt}\n━━━━\nFooter\n", prompt))

    def test_connection_has_an_explicit_state_transport_and_round_trip(self):
        body = "│ State        reconnecting │\n│ Transport    QUIC · TLS 1.3 │\n│ Round trip   32 ms · last observation │"
        self.assertIsNone(connection_fields(body))
        self.assertEqual(connection_fields("┌ Connection to core ┐\n" + body),
                         {"State": "reconnecting", "Transport": "QUIC · TLS 1.3",
                          "Round trip": "32 ms · last observation"})

    def test_wait_rejects_a_previous_success_frame(self):
        lab = type("FixtureLab", (), {"cname": staticmethod(str)})
        shell = Shell(lab, "a")
        shell.current = (time.monotonic() - 1, "P1end")
        shell.frames.append(shell.current)
        with self.assertRaisesRegex(AssertionError, "fresh"):
            shell.wait("fresh completion", lambda text: "P1end" in text, .01)

        def publish():
            with shell.lock:
                shell.current = (time.monotonic(), "P1end")
                shell.lock.notify_all()

        timer = threading.Timer(.01, publish)
        timer.start()
        try:
            self.assertEqual(shell.wait("fresh completion", lambda text: "P1end" in text, 1)[0], "P1end")
        finally:
            timer.join()

    def test_extra_or_disconnected_model_execution_fails(self):
        log = [dict(tag="P1", id=1, event=event, count=2, pace_ms=100)
               for event in ("request", "first", "finish")]
        validate_provider(log, {"P1": (2, 100)})
        with self.assertRaises(AssertionError):
            validate_provider(log + log, {"P1": (2, 100)})
        with self.assertRaises(AssertionError):
            validate_provider(log[:2] + [{**log[2], "event": "disconnect"}], {"P1": (2, 100)})

    def test_visibility_measures_viewport_instead_of_provider_progress(self):
        log = [{"tag": "P4", "event": "first", "mono": 2.0},
               {"tag": "P4", "event": "finish", "mono": 2.5}]
        frames = [(1.0, "LAB P4 STREAM 2x100"), (2.2, "P3end"), (5.0, "P4n01"), (7.0, "P4n01 P4n02 P4end")]
        self.assertEqual(stream_visibility(frames, log, {"P4": (2, 100)}),
                         {"P4": {"first_flush_to_first_visible_s": 3.0, "finish_to_end_visible_s": 4.5}})

    def test_key_painted_during_capture_is_not_reported_lost(self):
        now = [1.0]
        def read():
            now[0] = 1.04
            return "xy"
        frame = observe_frame(read, lambda: now[0])
        report = analyse("xy", "", [(0.8, .81), (1.01, 1.02)], [frame])
        self.assertEqual(report["lost"], [])
        self.assertEqual(report["per_key_ms"][-1], 20)

    def test_typing_probe_reports_echo_loss_and_a_momentary_wrong_frame(self):
        baseline = "│ draft: ab│"
        sends = [(1.0, 1.01), (1.1, 1.11), (1.2, 1.21)]
        frames = [(1.05, "│ x│"), (1.15, "│ xy│"), (1.16, "│ │"), (1.25, "│ xy│")]
        result = analyse("xyz", baseline, sends, frames)
        self.assertEqual(result["lost"], [3])
        self.assertEqual(result["per_key_ms"], [40.0, 40.0, None])
        # Both visible prefixes vanished in one frame and came back 90 ms later.
        self.assertEqual([(r["key"], r["ms"]) for r in result["regressions"]], [(1, 90.0), (2, 90.0)])
        # A prefix already painted before typing (here "ab") is not an echo.
        self.assertEqual(analyse("ab", baseline, sends[:2], [(1.05, baseline)])["lost"], [1, 2])
