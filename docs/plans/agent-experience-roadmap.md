# Agent Experience Roadmap

> Status: IN PROGRESS — vision and ordered roadmap for frictionless, survivable,
> cost-effective agent work in Crude. Written 2026-10-15 from a session audit;
> the "Done" section lists what has landed. "Remaining" is the open work.

## Vision

Crude is the place an agent lives while it works: it has a name, a lineage, a
memory, a body of tools and a metabolism of tokens. A good harness feels to the
agent the way a good IDE feels to a person: every action does what it says,
every failure explains itself and how to recover, nothing important is
forgotten, and the agent never has to fight its environment to do its job.

Four properties follow from that, in priority order:

1. **Survivable.** Weak, cheap, rate-limited or flaky models (often deliberately
   chosen for background Aeron work) and mid-run model switches never break a
   workflow or an agentic loop. Degradation is silent and bounded; work goes on.
2. **Accurate.** Context compression, delegation and recovery never lose a
   requirement or replay an effect. Correctness is never traded for tokens.
3. **Economical.** Every token sent earns its place. Long contexts are a cost
   and a quality risk, not a free resource, even when cached.
4. **Frictionless and fast.** Tools are forgiving where intent is clear and
   precise where it matters; errors name the exact fix; latency-sensitive paths
   use the fastest tier the user allows.

The harness should get *simpler* as it gets better: each item below should
remove a mechanism, a round trip or a rule an agent has to remember.

## Evidence

Source: six root sessions and 67 child-agent databases for this repository,
2026-10-09 to 2026-10-14. Crude is in active development, so this is guidance,
not ground truth; each defect below was confirmed in current code before it was
fixed.

| Signal | Measurement |
|---|---|
| Tool volume | `read_file` 2,685 calls, `execute_command` 920, `grep_files` 800, `apply_patch` 523, `update_plan` 239 |
| Avoidable tool failures | `read_file` 7% (guessed paths; sibling-batch wildcard errors), `agent_start` 15% (claim conflicts with the parent's own claims), `remember` 47% (schema did not match validation), `search_history`/`wait` "unknown tool" in children |
| Guidance deferrals | ~17 tool calls deferred because a 146 KB `AGENTS.md` changed during runs |
| Tool schema overhead | ~11.7K tokens of tool definitions on every request (46 tools) |
| Root context economy | gpt-6.1-sol root: 2,635 requests, 586M prompt tokens (97% cached), average ~222K tokens per request, peak 899K |
| Compaction | 3 runs, all triggered near 900K of a 1.05M window (85% default); 368 s and 540 s with a flash-class Aeron model; four sequential full-source stages |
| Child runs | 207 succeeded, 29 cancelled, 15 launch-aborted |
| Search | HTML scraping of Brave/DuckDuckGo/Startpage plus Exa via an `EXA_API_KEY` environment variable; transport errors could expose the key |

## Done (2026-10-15)

- `AGENTS.md` cut from 146 KB to 14 KB with a topic map; detailed contracts
  moved to references; the docs gate now rejects cited paths and tests that do
  not exist.
- Memory tools: evidence schema matches validation, `session_id` defaults to
  the caller, lessons ground by origin identity (the recorded synopsis is
  stored, not the caller's paraphrase), and every rejection names the field.
- One call's glob operand no longer fails its whole batch; `grep_files` accepts
  glob paths; grep/glob output is workspace-relative; `read_file` misses list the
  nearest real candidates (the existing hint never fired because the error did
  not wrap `fs.ErrNotExist`).
- Children keep `publish_report`, `artifact_read` and `agent_send` under a
  narrowed tool list; delegating a path the parent holds transfers its claim
  atomically.
- A failed automatic compaction that still leaves room for the next request
  defers and continues instead of stopping the run.
- The provider-reported `service_tier` is recorded, and Fast/priority requests
  report their cost as unknown instead of understating it at the standard rate.
- Exa API keys are redacted from transport errors; non-200 search responses are
  errors rather than empty results.

- Web search, fetch and arXiv moved to a built-in in-process MCP connection
  (`web`), keyed providers in quality order with keyless fallback, paged and
  cached fetches bounded by the transport, keys in Crude's secret store
  (`secret:NAME`) set from Settings or self control.
- MCP client: legacy HTTP+SSE transport and automatic fallback, background
  reconnection of dropped and durably granted connections with backoff,
  explicit connects join in-flight attempts.
- `capabilities.*` self-control commands; approval prompts mask secrets and
  render one argument per line; command discovery matches by words.
- Adaptive compaction: no fixed fullness or retention defaults; physical,
  economic (ski-rental) and explicit triggers; background preparation adopted
  late with the recent turns verbatim and old re-runnable results stubbed;
  combined single-pass extraction with staged fallback; main-model and
  model-free decay as last resorts at capacity; planner and prepared
  checkpoints survive across runs; resumed runs seed their first projection
  from provider-reported usage (it used to fall back to local estimates that
  were half the real size on dense content).
- Shuffled, unbounded agent names and optional parent-chosen nicknames.
- Guidance deferral runs pure reads; parents get a children-status notice on
  change; claim changes need only the new paths; per-role service tier and
  `/fast`; children launch in repositories without commits.

## Design rules for everything below

- **Aeron work is best-effort and adaptive.** Compaction, memory extraction,
  guidance compilation and review run on whatever the user chose, possibly a
  cheap model with low concurrency, small windows and small output caps. They
  learn each connection's limits (as output ceilings already are in
  `model-catalog.db`), shrink their requests, serialize when concurrency is
  refused, and fall back to "keep working on the full context" rather than stop.
  They stop a run only when no request can be made at all.
- **Crude's checkpoint is the portable authority.** Models change mid-run;
  provider-native state (encrypted compaction items, reasoning blobs, response
  IDs) is bound to the producing connection and is an accelerator, never the
  only copy.
- **Windows is a first-class platform.** Paths, process trees, locks, sockets
  and case-insensitive filesystems are designed and tested for Windows as well
  as Unix; cross-compilation is the floor, not the acceptance.
- **Every tool error is a lesson.** It names the field, the reason and the next
  action. A model-visible error that does not tell the agent what to do next is
  a defect.

## Request shape: what is sent on every request

Chat Completions, Anthropic Messages and Ollama are stateless: the system
prompt and every tool schema must be sent on every request. Responses
`previous_response_id` keeps earlier input items on the server, but tools and
instructions are not carried over, so Crude resends tools there too. Sending
them is correct; what matters is that they stay byte-stable so prompt caching
serves them (the audit shows 96-97% cached input):

| Situation | What happens today | Cache effect |
|---|---|---|
| Ordinary step | Tool list rebuilt each step by `PrepareStep`; built-ins are fixed lists, MCP connections sorted by ID | Stable; prefix cached |
| Capability activated or session toggle changed | New tools appear in the list | One cold request, then cached |
| Automatic compaction | Same tools; system message rebuilt from the current guidance; history replaced by the checkpoint | One cold request (the history changed anyway) |
| Compaction extraction requests | Reuse the agent's own system message and tool list | Share the conversation's cached prefix |
| Child agent | Own category tool set and child system prompt | First request cold, then cached |
| Model or protocol switch mid-run | Schemas re-projected for the new model (for example `apply_patch`/`exec_command` for GPT) | Cold; caches are per model regardless |

## Roadmap

### Remaining

1. **Provider-native compaction as an accelerator.** When the next request goes
   to the same provider and model, use native compaction: OpenAI Responses
   `context_management` (`type: "compaction"`, `compact_threshold`) or
   `/responses/compact`, and Anthropic compaction. The returned item is opaque
   and connection-bound, so treat it like opaque reasoning replay: only for
   that connection, with Crude's portable checkpoint (already prepared in the
   background) kept for model switches, resume and children. Needs live
   verification against both APIs before it may replace a Crude checkpoint.
2. **Agent-reported friction.** A `report_friction` tool (tool, call, error,
   suggestion) appended next to the session databases and raised as a Notice.
3. **Agent identity and continuity.** Named agents keep a durable profile:
   personal memory, the work they did, their lineage, preferences learned from
   feedback. A resumed or re-spawned agent recognizes its own history.
4. **Model routing by role.** Route each role (root reasoning, child coding,
   compaction, memory, review, search summarization) to a connection by
   capability, cost and latency, with survivable fallbacks the user approves.
5. **Usage ledger per value.** Attribute tokens to outcomes (tool round,
   compaction, child, guidance) so users see which activities are worth their
   cost, and so the adaptive policies can be checked against data.
6. **Schema weight.** ~11K schema tokens ride on every request (cached).
   Choose families per role once per run (never lazily mid-run, which breaks
   the cache) and trim the largest descriptions without losing contract text.
7. **Names after failed launches.** A launch that fails before running keeps
   its registered name, so a repeated nickname skips a numeral. Retire
   never-run registrations instead.

## Acceptance

Each item lands with a regression test that fails for the original reason, a
measured before/after on a dense resumed session, Windows and Unix coverage, an
update to its reference document, and removal from this plan when complete.
