#!/usr/bin/env python3
"""Network-lab model provider; runs inside the lab's provider container.

It reuses the acceptance fixture's OpenAI-compatible catalog and SSE framing.
A prompt containing ``LAB <tag> STREAM <count>x<milliseconds>`` streams
``<tag>n01 <tag>n02 … <tag>n<count> <tag>end`` at that pace. A prompt containing
``LAB <tag> ASK`` asks one question through the real ``question`` tool, then
answers ``<tag> ANSWERED <option>`` with the option the tool result carries.
The log contains only test tags, plans, chosen options and the actual flush
boundaries; never request bodies.
"""
import json
import re
import sys
import threading
import time

from core_shell_acceptance import Fixture, FixtureServer

PLAN = re.compile(r"\bLAB (P[1-4]) STREAM (\d{1,3})x(\d{1,4})\b")
ASK = re.compile(r"\bLAB (Q[1-4]) ASK\b")
OPTIONS = ("Alpha", "Bravo")
QUESTION = "Pick the lab answer"


def text_of(message):
    content = message.get("content")
    if isinstance(content, list):
        return "".join(part.get("text", "") for part in content if isinstance(part, dict))
    return content if isinstance(content, str) else ""


class LabProvider(Fixture):
    def record(self, kind, execution, tag, **fields):
        with self.fixture.state["lock"], open(self.fixture.state["log"], "a") as log:
            log.write(json.dumps({"event": kind, "id": execution, "tag": tag,
                                  "unix": time.time(), "mono": time.monotonic(), **fields}) + "\n")

    def do_POST(self):
        request = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        messages = request.get("messages", [])
        if self.ask(messages):
            return
        # The newest prompt that no assistant message has answered yet is the
        # one this request executes; turn context may follow it.
        plan, answered = None, False
        for message in reversed(messages):
            if message.get("role") == "assistant":
                answered = True
            elif message.get("role") == "user" and (plan := PLAN.search(text_of(message))):
                break
        if answered:
            plan = None
        tag, count, pace = (plan.group(1), int(plan.group(2)), int(plan.group(3))) if plan else ("", 0, 0)
        with self.fixture.state["lock"]:
            self.fixture.state["next_id"] += 1
            execution = self.fixture.state["next_id"]
        self.record("request", execution, tag, count=count, pace_ms=pace)
        if not tag or not (1 <= count <= 160 and 25 <= pace <= 1000 and count * pace <= 90_000):
            self.send_error(400, "Invalid network lab plan")
            return
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()
        try:
            for i in range(1, count + 1):
                self.event({"content": f"{tag}n{i:02d} "})
                if i == 1:
                    self.record("first", execution, tag)
                time.sleep(pace / 1000)
            self.event({"content": f"{tag}end"})
            self.event({}, "stop")
            self.wfile.write(b"data: [DONE]\n\n")
            self.wfile.flush()
            self.record("finish", execution, tag)
        except (BrokenPipeError, ConnectionResetError):
            self.record("disconnect", execution, tag)


    def ask(self, messages):
        """Serve a question plan; False when the newest prompt is not one."""
        # The newest lab prompt decides; turn context may follow it.
        latest = next((m for m in reversed(messages) if m.get("role") == "user"
                       and (ASK.search(text_of(m)) or PLAN.search(text_of(m)))), None)
        plan = ASK.search(text_of(latest)) if latest else None
        if not plan:
            return False
        tag = plan.group(1)
        after = messages[messages.index(latest) + 1:]
        answers = [text_of(m) for m in after if m.get("role") == "tool"]
        with self.fixture.state["lock"]:
            self.fixture.state["next_id"] += 1
            execution = self.fixture.state["next_id"]
        self.record("request", execution, tag, answered=bool(answers))
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()
        try:
            if not answers:
                args = {"questions": [{"header": "Choice", "question": QUESTION, "options": list(OPTIONS)}]}
                self.event({"tool_calls": [{"index": 0, "id": f"lab-{tag}", "type": "function",
                                            "function": {"name": "question", "arguments": json.dumps(args)}}]}, "tool_calls")
                self.wfile.write(b"data: [DONE]\n\n")
                self.wfile.flush()
                self.record("ask", execution, tag)
                return True
            chosen = [o for o in OPTIONS if o in answers[-1]]
            choice = chosen[0] if len(chosen) == 1 else "unclear"
            self.event({"content": f"{tag} ANSWERED {choice}"})
            self.event({}, "stop")
            self.wfile.write(b"data: [DONE]\n\n")
            self.wfile.flush()
            self.record("finish", execution, tag, choice=choice)
        except (BrokenPipeError, ConnectionResetError):
            self.record("disconnect", execution, tag)
        return True


def main():
    port, log = int(sys.argv[1]), sys.argv[2]
    server = FixtureServer(("0.0.0.0", port), LabProvider)
    server.daemon_threads = True
    server.state = {"lock": threading.Lock(), "log": log, "next_id": 0}
    with open(log, "x"):
        pass
    server.serve_forever()


if __name__ == "__main__":
    main()
