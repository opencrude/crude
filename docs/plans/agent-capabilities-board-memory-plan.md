# Agent Capabilities, Shared Board, and Evidence-Based Memory Plan

> Status: IN PROGRESS — design proposal only. Skills, MCP acquisition, the shared
> board, and the memory changes below are proposed; this document does not
> describe shipped behavior.

## Goal and source boundaries

Give agents access to relevant skills and MCP integrations from configured,
credible sources; let cooperating agents exchange useful findings during work;
and make durable workspace memory more accurate and useful over time.

Two external references inform different parts of the design:

- [Kilo Code's shared board](https://github.com/Kilo-Org/kilocode/tree/d13d50df600fe97b272b88a377551bd1e505a080/packages/opencode/src/kilocode/board)
  informs communication: `board_post`, `board_read`, persisted messages, small
  activity notices, explicit reads, and posts that do not wake workers.
- [Google Research RRSI](https://github.com/google-research/rrsi/tree/be50316e1db05914068a973f322770ef08ed7ba1)
  informs memory quality: scoped episodes, evidence for successful and failed
  attempts, incremental updates, and evaluating benefit against cost. RRSI does
  **not** implement a peer message board. Its helpful/harmful playbook counters
  are a [suggested pattern](https://github.com/google-research/rrsi/tree/be50316e1db05914068a973f322770ef08ed7ba1/domains/workspace),
  not a scoring feature of its example memory store.

The first release should offer all four capabilities through the existing
`app/` → `workspace/` → `tui/` architecture. Agents remain responsible for
their work and reports. Board posts supplement delegation and completion;
memories supplement the current request and verified workspace state.

## Existing Crude seams and changes required

| Area | Existing behavior | Required change |
|---|---|---|
| Tool activation | [`Agent.PrepareStep`](../../agent/agent.go) refreshes root tool definitions between model requests; runtime notices have a separate admission hook. | Include agent-specific capabilities and board notices at request boundaries. Supply the same contract to child agents. Keep an in-flight request and parallel tool batch on one snapshot. |
| Tool policy | [`PolicyAllows`](../../agent/policy.go) defaults unknown names to allowed. Children also filter named tools. | Register explicit effect and scope metadata for dynamic tools; unknown dynamic operations fail closed. Apply root, child, category, and user limits at definition and execution time. |
| Agent ownership | [`SubAgentRunner`](../../session/subagent.go) creates its own file ownership registry. | Move enforcement to a workspace authority before several independent roots coordinate on edits. The board may show claims but cannot enforce them. |
| Memory | [`memory/`](../../memory/types.go) has workspace SQLite, FTS5 retrieval, history search, background extraction, consolidation, and an audit journal. | Add evidence, scoped attempts, immutable revisions, version checks, and separate exposure from demonstrated usefulness. Retain existing FTS and history search as the retrieval foundation. |
| Memory ranking | [Recall](../../memory/retrieve.go) rewards usage and the latest of update or last-use time; [auto recall](../../session/memory.go) calls `Touch` whenever it shows a record. | Prevent selection from reinforcing itself. Use applicability and evidence when learning whether advice helped. |
| Memory extraction | The [turn mirror](../../session/memory_pipeline.go) retains user and assistant summaries, tool names, and paths; the extraction prompt does not receive underlying tool results. | Select bounded, relevant observations and link extracted claims to source messages or tool calls. |

The project is pre-alpha: replace affected internal schemas and contracts
directly, without migration or compatibility paths for old development data.
Preserve unrelated user data and files during implementation.

## 1. Shared capability catalog

### Source and trust model

One catalog indexes both skills and MCP servers. Each entry records publisher,
source, exact revision or remote endpoint, package identity, content digest
when applicable, license, supported platform/runtime, requested access,
health, and installation state. Source adapters should start with selected
vendor repositories, [Anthropic skills](https://github.com/anthropics/skills),
supported contents of [OpenAI plugins](https://github.com/openai/plugins),
and MCP registry metadata. Allow user-configured private repositories and
registries under the same policy.

The [MCP Registry](https://modelcontextprotocol.io/registry/about) verifies
publisher namespaces and hosts metadata, but does not certify that server
code is safe. Discovery must never become a permission grant. A plugin adapter
can expose recognized skills and MCP definitions without executing unrelated
plugin hooks, agents, commands, or connectors.

`capability_search(query, kind?, limit?)` searches installed entries first,
then configured external sources and returns concise candidates with access
requirements. `capability_activate(id)` verifies, acquires, and enables an
entry for the calling agent. The usual path is: identify a concrete missing
capability → search → select → acquire → activate → use. Do not require
external searches for ordinary tasks already covered by installed tools.

Use a shared immutable artifact cache and a workspace lock of selected
versions. Record the effective version in each agent session. Deduplicate
concurrent acquisitions; verify before activation; never expose an incomplete
download. Cache failed searches briefly and bound retries. An accepted source
and access grant makes routine acquisition quiet; missing credentials or new
access use the existing owner-correlated permission flow.

Local artifacts can be pinned to bytes. A remote MCP endpoint can change
behind its URL: retain its endpoint identity and observed tool-schema
fingerprint, surface material changes, and reassess permissions. Existing
requests and tool batches keep their prior capability snapshot. Resume does
not automatically download, update, or start anything.

### Skills

Implement the [Agent Skills specification](https://agentskills.io/specification):
validate `SKILL.md` frontmatter, support optional scripts, references and
assets, and resolve referenced paths within the acquired skill. Index compact
names and descriptions. Load instructions on activation and resources only as
needed, so an expanding catalog does not flood provider context. Include
locally configured project/user skills under the same provenance and grant
rules. An explicit user request for a skill should resolve to that skill.

Skill prose and `allowed-tools` requests are instructions from a source,
subordinate to the user and runtime policy. Scripts execute through Crude's
normal execution controls. Validate compatibility and dependencies before
advertising a skill as usable; report unavailable requirements precisely.

### MCP servers

Use the [official Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk)
for managed stdio and remote Streamable HTTP connections. Crude owns startup,
authentication, credentials, cancellation, timeouts, health, reconnect, and
shutdown. Adapt server tools under stable names such as
`mcp__github__list_issues`, retaining their schemas. Expose relevant tool
definitions and available resources/prompts on demand, with concise discovery
rather than every schema in every model request.

Define an effect and scope contract for every dynamic operation. A server's
own read-only annotation is descriptive; Crude decides the grant. At execution,
intersect the user grant, workspace policy, agent/category ceiling, and
resource scope. Unknown or changed operations fail closed. A local server
gets only the environment, filesystem access, and network access covered by
its grant; automatic local acquisition is offered only when Crude can enforce
the required launch policy. Remote credentials are bound to the intended
endpoint and never published as tool output.

A disconnected mutating call with an unknown result is not retried blindly.
Direct MCP filesystem writes are outside Crude's native mutation journal and
ownership guarantees unless explicitly integrated with that path. Show this
limit in a capability's access details and permission decision.

## 2. Shared agent board

### Membership, data, and tools

Store boards in the workspace database. A collaboration has an explicit board
ID and membership for participating root agents; children inherit membership
from their parent. Keep unrelated sessions out, and validate project identity
at every read and post. Agents have stable runtime IDs plus display names.
One member can address another by ID, address the root coordinator as `main`
when a coordinator exists, or use `ALL` for genuinely team-wide updates.
Addressing affects notices; every board member can read the discussion.

Start with two tools:

```text
board_post(to, type, body, reply_to?, references?)
board_read(since?, thread?, limit?)
```

Types are `INFO`, `ASK`, `RESULT`, `HOLD`, and `VETO`. `HOLD` and `VETO`
communicate peer concerns; they do not pause the scheduler, reserve files,
approve requests, or change assignments. Replies attach corrections and
resolutions to the originating message. `board_read` returns a roster,
messages, a cursor, and pagination state. The UI offers search; add a separate
agent-facing search tool if incremental reads prove insufficient.

Commit each post transactionally with a monotonic board sequence and a stable
message ID. Deduplicate retries by originating agent, provider message, and
tool-call identity; reject retries with altered arguments. Store sender and
recipient identities, type, body, reply target, evidence references, and
timestamp. Bound formatted messages (initially 4 KiB), reads (initially
32 KiB), and active board history. Archive older threads into searchable
history with stable IDs, so a cap never silently discards evidence.

### Notices and lifecycle

A post is **stored**, not proof of delivery or reading. At the next safe
model-request boundary, a participating agent receives a small fixed runtime
notice when addressed activity is pending; the agent explicitly calls
`board_read` for the body. Coalesce notices. Keep notification position
separate from successful read position. Advance read position only after the
tool result is durably accepted into the recipient's context; partial pages,
concurrent posts, failed reads, cancellation, and resume must preserve unread
items. Duplicate notices are preferable to losing a message.

**Posts do not wake, assign, restart, or resume workers.** Finished agents
remain finished; report recipient availability at posting time as active,
finished, or unknown. Child terminal results retain their existing structured
parent wake and handoff path. Peer text enters context only through explicit
reads and is attributed as peer data, including posts claiming user approval.
It cannot override the current user request or expand permissions.

Board access is a separate communication capability: a read-only researcher
can post findings without a filesystem write grant. The workspace file
ownership service enforces claims where Crude controls writes. Ordinary
command and external MCP side effects need their own access policy. Plans
remain concise declarative lifecycle banners, not task scheduling or board
messages.

Kilo's [store](https://github.com/Kilo-Org/kilocode/blob/d13d50df600fe97b272b88a377551bd1e505a080/packages/opencode/src/kilocode/board/store.ts),
[tools](https://github.com/Kilo-Org/kilocode/blob/d13d50df600fe97b272b88a377551bd1e505a080/packages/opencode/src/kilocode/tool/board.ts),
and [live tests](https://github.com/Kilo-Org/kilocode/blob/d13d50df600fe97b272b88a377551bd1e505a080/packages/opencode/test/kilocode/board-live.test.ts)
are the reference for the two-tool, explicit-read behavior. Crude's
workspace board membership deliberately extends beyond Kilo's one-root
session tree.

## 3. Evidence-driven workspace memory

### Records, revisions, and evidence

Keep separate durable **knowledge** and **episodes**. Knowledge covers facts,
decisions, preferences, conventions, references, and confirmed lessons.
Episodes capture a situation, attempted method, observed outcome (including
unmeasured or cancelled), conditions, and supporting evidence. Preserve a
failed hypothesis as scoped negative evidence even if no fix was found;
generalize only after evidence supports broader applicability.

Each knowledge record has a stable ID independent of its title, an immutable
revision history, current revision pointer, source and actor, scope and
`applies_when`, evidence references, and active/retired state. Revisions use
compare-and-swap against an expected revision. A late extraction or
consolidation based on an old version cannot overwrite a newer correction or
revive retired advice. Explicit user statements can establish preferences;
technical lessons require observed support. A reference stores the source
identity and a bounded synopsis, including enough evidence context to remain
auditable if a session is removed. Do not persist secrets or raw confidential
tool output in memory records.

This fixes a current contract mismatch: [`update_memory`](../../session/memory.go)
promises an auditable old version, but [same-title updates](../../memory/store.go)
currently revise the row in place. Deduplication by title or content becomes
a suggestion to merge or attach evidence, never permission to overwrite a
different assertion. Forget retires the specific revision without deleting
its history.

### Extraction, feedback, and retrieval

Keep the existing background Memory Agent and cadence. Feed it a bounded,
redacted set of relevant tool outcomes and user corrections alongside the
turn mirror. Reference the actual originating messages and tool calls. The
curator may save zero records. Store a verified observation as an episode,
and promote a lesson only when the test actually supports the claimed fix.
Automatic extraction and watermark updates commit coherently; a failed write
does not mark unsaved turns processed.

Track **exposure** (shown in automatic context or returned by recall)
separately from **useful**, **misleading**, **contradicted**, and **not
applicable** feedback tied to exact revisions and evidence. Lack of feedback
is unknown, and a successful turn is not proof that all injected memories
helped. Stop allowing exposure alone to boost ranking or refresh knowledge
freshness; adjust inactivity/archive rules to account for this distinction.
Only introduce a bounded usefulness ranking signal after evaluation.

Keep FTS5/BM25, path relevance, scoped applicability, source, importance,
budgeted recall, and explicit history search. Recall should return compact
source/evidence indicators and allow drill-down. Consolidation performs
small version-checked operations on complete candidate records. Resolve
contradictions using applicable conditions and evidence rather than merely
preferring the newest text. Retain unresolved disagreements, exceptions,
user pins, and provenance. Never promote a board post into verified memory
solely because another agent posted it.

Retain the current tool family with version-aware parameters, and add one
bounded batched feedback operation:

```text
remember(..., applies_when?, evidence?)
recall_memory(query, limit?)
update_memory(id, expected_revision, ...)
forget_memory(id, expected_revision)
search_history(query, limit?)
memory_feedback(items)
```

Most feedback comes from existing observations and explicit corrections, not
recurring user rating prompts. RRSI's [history](https://github.com/google-research/rrsi/blob/be50316e1db05914068a973f322770ef08ed7ba1/rrsi/history.py)
motivates retaining rejected attempts and their conditions; its example
[memory helper](https://github.com/google-research/rrsi/blob/be50316e1db05914068a973f322770ef08ed7ba1/third_party/archipelago/harness_workspace/mechanisms.py)
motivates bounded, filtered reads. Its entity-free restriction prevents
benchmark leakage across test trials. Crude needs project-specific paths,
symbols, and user decisions within the correct workspace scope.

## Service ownership and user experience

The companion [workspace tools and capability TUI plan](workspace-tools-capabilities-tui-plan.md)
audits the current rail, settings catalogs, workspace dock, session decks, and
runtime capability gaps. Revision 2 incorporates the user's direction: no
Terminals rail entry, calm Git navigation with main-area diffs, workspace Memory
in the footer, and complete Skills/MCP browsing and manual management in Settings.
It also recommends separate Skills/MCP rail entries and `/skills`, `/mcp`
manager commands over one shared catalog. Detailed controls and layouts remain
proposed; the examples below are only the original surface outline.

| Owner | Proposed responsibility |
|---|---|
| `app/` | Global source defaults, artifact cache location, credential references, and application policy. |
| `workspace/` | Compose catalog, MCP, board, memory, workspace file ownership, lifecycle, and workspace events. Workspace settings select sources and access limits. |
| `capability/`, `skills/`, `mcpclient/` | Neutral catalog/acquisition, skill loading, and MCP transport/tool adaptation. No TUI imports. |
| `board/`, `memory/` | Board membership/persistence and memory revisions/evidence/retrieval respectively. No TUI imports. |
| `session/`, `agent/` | Per-agent activation, policy checks, stable tool snapshots, notices, and execution attribution. Session state records selected versions. |
| `tui/` | Render neutral workspace views and route actions; perform network, disk, and subprocess work through background services. |

Three surfaces make the features understandable without taking over chat:

```text
Capabilities
  PDF extraction    Skill   Anthropic   Active in Mira
  GitHub            MCP     GitHub      Connected
  Database          MCP     Internal    Needs sign-in

Board · Parser work
  Mira → Theo   RESULT   Parser now returns byte offsets
    ↳ Theo      HOLD     Renderer still assumes rune offsets
    ↳ Mira      INFO     Conversion fixed · evidence attached

Memory
  Resize diagnosis   Episode   Observed · 2 evidence links
  Paint contract     Lesson    Verified · pinned
  Old API behavior   Fact      Contradicted · inspect revision
```

Capabilities lists source, version, permissions, connection health, active
agents, update, and disable. The Board sits with agent navigation and shows
threads, unread activity, participants, evidence, and archive/search. Memory
shows applicability, source, evidence, revisions, correction, pin, and retire.
Missing dependencies, unavailable credentials, empty boards, offline servers,
failed acquisition, stale evidence, and partial reads need clear states.
Background events must not switch tabs, steal focus, or block the TUI update
goroutine. Switching workspaces cancels or generations out old requests.

## Delivery increments and acceptance

1. **Runtime foundation.** Explicit effect and scope metadata for native and
   dynamic tools; consistent root/child policy and request snapshots; stable
   agent identities and workspace write ownership. Demonstrate that a newly
   activated tool cannot bypass a read-only child or user denial.
2. **Memory correctness.** Evidence-aware extraction, scoped episodes,
   immutable revision updates, corrections, and exposure/usefulness separation.
   Demonstrate that late extraction cannot replace a correction, unsupported
   claims are not promoted, and retired claims stay retired.
3. **Configured skills and MCPs.** Standard skill loader and managed MCP client
   with explicit configuration, per-agent activation, connection lifecycle,
   and Capabilities UI. Demonstrate root and child use under distinct grants,
   including failure, cancellation, and resume.
4. **Automatic acquisition.** Curated and user-configured source adapters,
   pinned cache, integrity checks, lock state, policy, and update flow.
   Demonstrate acquisition and use during the task, with no partial active
   install or duplicate download on retry.
5. **Shared board.** Membership across collaborating roots and children,
   post/read tools, durable cursors, fixed notices, workspace events, and Board
   UI. Demonstrate useful parent, sibling, and root-to-root exchanges without
   extra agent invocations; completion remains a separate lifecycle event.
6. **Evaluate and tune.** Measure task completion, repeated failed approaches,
   stale advice, missed coordination, latency, provider tokens, and permission
   interruptions. Keep additions that earn their cost on held-out tasks.

Memory and board implementation can proceed independently after runtime
identity and tool-policy contracts are fixed. For memory evaluation, restrict
each task to knowledge available **before** it began; otherwise old answers
leak into the trial. Compare on the same model and workloads, repeat noisy
measurements, and hold out tasks or time periods. RRSI's overall gains do
not isolate the value of its memory example. Board evaluation should measure
whether shared information changes decisions, rather than number of posts.

Run focused tests, full build and test gates, docs checks, and full-repository
lint on implementation. Verify real UI paths at normal and narrow terminal
sizes. Include concurrent acquisition, ambiguous MCP writes, permission
changes, partial board reads, retry deduplication, workspace switching,
cancelled agents, stale memory extraction, and passive resume.

## Deliberate later work

Add automatic board wakes, subscriptions, remote agent federation/A2A,
general plugin-hook execution, embeddings, and memory feedback weighting only
after measurement shows they help. The initial board uses explicit reads and
quiet notices; the initial memory work prioritizes traceable observations
and safe corrections.
