"""Fills a recording profile's workspace with 30 days of made-up use.

    python record/seed-dashboard.py WORKSPACE_DIR [--seed 1969]

WORKSPACE_DIR is the workspace data folder of a recording profile, for example
PROFILE_DIR/.crude/workspaces/<id>; run Crude in it once first. The script
copies the schema of a real session database into closed sessions and
sub-agents whose only content is their request history (the usage_requests
table the Workspace panel reads for cost), and adds placeholder files for the
Storage page. It never changes the existing sessions. Use it only on a
throwaway profile.

The models and prices are invented for the film; they are not price data.
"""

import argparse
import datetime as dt
import os
import random
import sqlite3
import uuid

# model id, provider id, input / cached input / output USD per million tokens
MODELS = {
    "claude-opus-5-5": ("anthropic", 5.00, 0.50, 25.00),
    "gpt-6.2-astra": ("openai", 2.50, 0.25, 12.00),
    "gemini-neon": ("google", 1.25, 0.125, 10.00),
    "glm-5.4": ("z-ai", 0.60, 0.11, 2.20),
    "deepseek-v4.1-flash:1017": ("ollama-cloud", 0.15, 0.015, 0.60),
}
# Which model leads a session, and which models its sub-agents use.
LEADS = ["claude-opus-5-5"] * 5 + ["gpt-6.2-astra"] * 2 + ["gemini-neon"]
HELPERS = ["glm-5.4", "glm-5.4", "deepseek-v4.1-flash:1017", "gemini-neon", "gpt-6.2-astra"]
MB = 1 << 20


def template(workspace):
    """Returns the path of the session database with the fewest messages."""
    folder = os.path.join(workspace, "sessions")
    best = None
    for name in sorted(os.listdir(folder)):
        if not name.endswith(".db"):
            continue
        db = sqlite3.connect(os.path.join(folder, name))
        try:
            n = db.execute("SELECT COUNT(*) FROM messages").fetchone()[0]
        except sqlite3.Error:
            continue
        finally:
            db.close()
        if best is None or n < best[0]:
            best = (n, os.path.join(folder, name))
    if best is None:
        raise SystemExit("no session database found; run Crude in this workspace once first")
    return best[1]


def request(rng, session, agent, parent, model, at):
    provider, price_in, price_cached, price_out = MODELS[model]
    prompt = rng.randint(18_000, 140_000)
    cached = int(prompt * rng.uniform(0.72, 0.96))
    output = rng.randint(250, 4_800)
    cost = ((prompt - cached) * price_in + cached * price_cached + output * price_out) / 1e6
    ns = int(at.timestamp() * 1e9)
    return (uuid.uuid4().hex, session, agent, parent, model, provider, prompt, output, cached,
            min(prompt + output, 1_048_576), 1_048_576, cost, ns, ns)


INSERT = """INSERT INTO usage_requests (request_id, session_id, agent_id, parent_agent_id, model_id,
    provider_id, prompt_tokens, completion_tokens, cached_tokens, context_used, context_window,
    cost_usd, created_at, updated_at, cost_known, cost_source, cache_read_known, request_outcome,
    account_id) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,1,'catalog',1,'completed','demo')"""


def database(path, source, session, model, at, rows, parent=None, name=None):
    """Writes a closed session (or sub-agent) database with only its requests."""
    os.makedirs(os.path.dirname(path), exist_ok=True)
    src, db = sqlite3.connect(source), sqlite3.connect(path)
    try:
        src.backup(db)
        with db:
            tables = [r[0] for r in db.execute(
                "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")]
            for table in tables:
                db.execute(f'DELETE FROM "{table}"')
            ns = int(at.timestamp() * 1e9)
            db.execute("""INSERT INTO sessions (id, project_path, project_name, model, provider, account_id,
                created_at, closed_at, message_count, turn_count, parent_session_id, agent_category,
                agent_name, depth) VALUES (?,?,?,?,?,'demo',?,?,0,0,?,?,?,?)""",
                (session, "C:\\dev\\orbit", "orbit", model, MODELS[model][0], ns, ns + 3_600_000_000_000,
                 parent, "crafter" if parent else None, name, 1 if parent else 0))
            db.executemany(INSERT, rows)
        db.execute("VACUUM")
    finally:
        src.close()
        db.close()


def filler(path, size, when):
    """Creates a placeholder of size bytes without writing its content."""
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "wb") as f:
        f.truncate(size)
    os.utime(path, (when.timestamp(), when.timestamp()))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("workspace")
    parser.add_argument("--seed", type=int, default=1969)
    args = parser.parse_args()
    rng = random.Random(args.seed)
    source = template(args.workspace)
    sessions_dir = os.path.join(args.workspace, "sessions")
    now = dt.datetime.now()
    today = now.replace(hour=0, minute=0, second=0, microsecond=0)
    totals = {}
    count = 0
    for back in range(29, -1, -1):
        day = today - dt.timedelta(days=back)
        weekend = day.weekday() >= 5
        busy = 0.35 if weekend else rng.uniform(0.8, 1.35)
        if back == 9:
            busy = 2.6  # the release day: the chart's highest bar
        if back == 0:
            busy = 0.55
        for _ in range(1 if weekend or back == 0 else rng.choice([1, 1, 2])):
            start = day + dt.timedelta(hours=rng.uniform(1 if back == 0 else 8, 3 if back == 0 else 20))
            session = start.strftime("%Y-%m-%d_%H%M%S_") + uuid.uuid4().hex[:8]
            lead = rng.choice(LEADS)
            agent = session + "_orch_1"
            rows = [request(rng, session, agent, "", lead, start + dt.timedelta(seconds=40 * i))
                    for i in range(int(rng.randint(28, 55) * busy))]
            database(os.path.join(sessions_dir, session + ".db"), source, session, lead, start, rows)
            folder = os.path.join(sessions_dir, session)
            filler(os.path.join(folder, "turns", "turns.log"), int(rng.uniform(14, 34) * MB), start)
            filler(os.path.join(folder, "tmp", "build-cache.bin"), int(rng.uniform(4, 30) * MB), start)
            for child in range(rng.choice([0, 1, 2, 3])):
                helper = rng.choice(HELPERS)
                child_id = f"{session}_child_{child + 1}"
                child_rows = [request(rng, session, child_id, agent, helper, start + dt.timedelta(seconds=55 * i + 20))
                              for i in range(int(rng.randint(10, 30) * busy))]
                database(os.path.join(folder, "subagents", child_id + ".db"), source, child_id, helper, start,
                         child_rows, parent=session, name=f"Helper {child + 1}")
                filler(os.path.join(folder, "subagents", child_id + ".log"), int(rng.uniform(2, 9) * MB), start)
                rows += child_rows
            for row in rows:
                totals[row[4]] = totals.get(row[4], 0) + row[11]
            count += 1
    chunks = os.path.join(args.workspace, "local-history", "objects", "chunks")
    for i in range(160):
        name = uuid.UUID(int=rng.getrandbits(128)).hex * 2
        when = today - dt.timedelta(days=rng.uniform(0, 29))
        filler(os.path.join(chunks, name[:2], name), int(rng.uniform(2, 8) * MB), when)
    print(f"{count} sessions")
    for model, cost in sorted(totals.items(), key=lambda kv: -kv[1]):
        print(f"  {model:28s} ${cost:8.2f}")


if __name__ == "__main__":
    main()
