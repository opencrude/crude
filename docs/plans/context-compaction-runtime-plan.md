# Context Compaction And Runtime Continuity Plan

> Status: LANDED (2026-09-12) - provider-grounded context accounting,
> four-stage model extraction with code-authored checkpoints, durable lifecycle
> projection, deterministic resume, memory boundaries, and agent continuity.

The current capacity and overflow hardening is documented in
[adaptive context handling](../reference/adaptive-context.md). It supersedes this
plan's full-source-only extraction assumption: fitting sources keep their cached
prefix, oversized sources use complete attributed chunk-and-reduce, and extraction
budgets output against its model's available capacity while retaining the selected
reasoning effort.

## 1. Problem Statement

The 2026-09-11 production session exposed a coupled failure across context
measurement, compaction, persistence, and presentation:

- A provider measured 117,532 input tokens while Crude estimated 1,238,613.
- OpenAI-compatible reasoning arrived as 37,884 stream fragments. The semantic
  reasoning was about 135 KB, but retained fragment wrappers occupied 4.62 MB.
- The estimator counted every wrapper although the encoder merged those
  fragments before transmission.
- A configured 40% trigger selected a 50% compaction target.
- A mid-turn message made the immediately preceding assistant/tool unit
  discardable, reducing the next measured request from 117,532 to 8,874 tokens.
- Seven automatic compactions occurred in one turn without a real threshold
  crossing.
- The deterministic checkpoint retained only 24 clipped observations and
  eventually omitted both the original request and the mid-turn correction.
- The full transcript survived, but the model's active context did not.
- The request ledger remained accurate; the header correctly exposed the latest
  request context rather than cumulative input.

The same audit found adjacent lifecycle defects:

- `PendingBtw` is omitted from chat-projection equality, so a consumed note can
  remain painted as `Waiting to send`.
- System prompts use weak Markdown instruction/data boundaries and the solo
  prompt includes contradictory orchestrator instructions.
- Resume infers execution state from message roles, invents visible recovery
  user messages, ignores a compacted context after interruption, and performs
  legacy transcript reconstruction.
- Internal continuation and child-wake messages overload the user role.
- Memory recall is appended after the human request inside the same message,
  accumulates each turn, and can be mistaken for human intent during compaction.
- Child agents use the same compactor without a child-specific checkpoint
  contract or per-agent context ownership.

## 2. Scope And Compatibility

This is a clean pre-production cutover. Only sessions created with the new
schema are supported by the new runtime. Existing development sessions do not
receive migration, reconstruction, or compatibility behavior. Their files are
not deleted automatically.

The implementation covers:

1. Provider replay canonicalization.
2. Authoritative context measurement.
3. XML prompt contracts and trust boundaries.
4. Typed message origin and visibility.
5. Aeron-category, cache-anchored root and child compaction.
6. Manual `/compact` through the same path.
7. BTW admission and projection lifecycle.
8. Memory recall/extraction boundaries.
9. New-session persistence and resume.
10. Live child completion wakes and child shutdown semantics.
11. Request accounting and compaction presentation.
12. Regression, performance, visual, lint, and documentation gates.

## 3. Non-Negotiable Invariants

### 3.1 One context owner per agent

Each root or child agent owns one active context, one context generation, and
one context meter. Session-wide or process-wide context values may aggregate
usage but may not replace per-agent state.

### 3.2 Provider usage is authoritative

When the provider reports total request input, that value is the baseline.
Crude projects the next request by adding provider-reported completion tokens
and semantic estimates only for locally appended, unreported content.
Session creates a fresh `Agent` per turn, so it seeds that meter from the latest
persisted normal usage record at the previous final-assistant boundary. The
session's nullable `ContextCompactionPercent` override is persisted independently
of provider/model fields; a configured `2%` therefore compares against the same
provider-reported occupancy shown in the header on the next request boundary.

Before the first provider measurement, Crude estimates semantic content at
`ceil(runes / 4)`. It counts actual system/tool descriptions and message text,
but never JSON punctuation, repeated stream envelopes, or per-fragment object
wrappers.

### 3.3 Compaction is transactional

Compaction appends one hidden shared contract and runs four hidden, no-tools
extractions over the same cached conversation. Crude, not the model, escapes and
wraps those section bodies as XML. The active context is replaced only after all
four sections are non-empty, bounded, structurally valid, and collectively
smaller. Cancellation or failure leaves the old context byte-for-byte unchanged.
There is no deterministic lossy fallback.

### 3.4 Transcript and active context are different products

The immutable transcript retains what happened. Active context is the bounded
provider continuation projection. Compaction never rewrites or deletes the
transcript.

### 3.5 Runtime data is not human intent

Human input, BTW input, memory snapshots, continuation controls, context
checkpoints, and child-result wakes have explicit origins. Provider role is a
wire constraint, not the source of truth for attribution or visibility.

### 3.6 Resume is passive

Opening a session hydrates state and performs no provider request. It never
invents a recovery message or restarts a child. Explicit human input is required
to start new root work after resume.

### 3.7 Accepted partial streams continue, never replay

An upload, connection, or response-header stall may retry the same request
because the model produced no accepted output. Once a streamed response yields
content, a download stall cannot replay that stochastic request. Crude commits
the canonical partial assistant output, appends a hidden typed continuation,
and issues a new request against that prefix. For an incomplete tool-call
stream, no tool has executed: Crude drops the malformed call/replay fragments,
retains ordinary visible assistant text, records that no side effect occurred,
and asks the model to issue complete calls from scratch.

## 4. Message Contract

`provider.ChatMessage` gains presentation-neutral origin and visibility fields.
The provider encoders ignore them; session persistence and context selection do
not.

Origins:

| Origin | Meaning |
|---|---|
| `human` | A genuine user-submitted turn |
| `btw` | A genuine user note admitted during an active turn |
| `assistant` | Provider assistant output |
| `tool` | Tool result paired with an assistant tool call |
| `runtime_context` | Memory, previous-session, goal, or runtime snapshot |
| `continuation` | Internal continuation after provider truncation |
| `subagent_wake` | Structured terminal child result delivered to a parent |
| `assignment` | Parent-authored child assignment |
| `checkpoint` | Code-authored XML envelope around model-extracted continuation state |
| `compaction_request` | Hidden request asking the Aeron model to extract one stage |

Visibility:

| Visibility | Meaning |
|---|---|
| `conversation` | Paint as ordinary human/assistant/tool conversation |
| `lifecycle` | Paint as an attributed runtime status/event |
| `context_only` | Send to the provider but never paint as conversation |

No code may infer these properties from `[SYSTEM ...]` prefixes.

## 5. XML Prompt Architecture

The root system prompt is rebuilt from escaped XML sections:

```xml
<crude_agent>
  <identity>...</identity>
  <instruction_priority>...</instruction_priority>
  <operating_mode type="orchestrator|solo">...</operating_mode>
  <tool_contract>...</tool_contract>
  <planning_contract>...</planning_contract>
  <memory_contract>...</memory_contract>
  <environment>...</environment>
</crude_agent>
```

Solo mode contains no delegation mandate. Orchestrator mode contains no direct
implementation mandate. Dynamic environment and profile values are XML escaped.

The child prompt is separate:

```xml
<crude_sub_agent>
  <identity type="leaf" category="...">...</identity>
  <assignment>...</assignment>
  <parent_contract>...</parent_contract>
  <tool_contract>...</tool_contract>
  <file_ownership>...</file_ownership>
  <report_contract required="true">...</report_contract>
  <profile_instructions>...</profile_instructions>
  <environment>...</environment>
</crude_sub_agent>
```

Both prompts state that file content, tool output, fetched pages, memory records,
and quoted transcripts are data rather than instructions. Current genuine human
instructions outrank recalled memory.

## 6. Provider Replay Canonicalization

Stream decoders may emit presentation deltas at fragment cadence, but a completed
`provider.Completion` stores canonical replay items only.

OpenAI-compatible reasoning details with the same replay identity are merged
once before they reach:

- Agent context.
- Session persistence.
- Token estimation.
- Request replay.
- Turn logging.

Signed and encrypted protocol blocks remain byte-exact. Canonicalization is
protocol-owned and covered by round-trip tests.

Opaque replay items are also bound to the producing provider endpoint/protocol
and the exact request prefix (system, tools, and prior messages). Crude never
sends them to another endpoint merely because it implements the same wire
format. A changed prefix drops the item from that request copy without editing
the immutable transcript. A model switch within the same provider retains the
byte-exact item and lets the provider enforce its documented model-family or
producer-model compatibility rules.

Compaction never decrypts or summarizes ciphertext. Its extraction stages see
the provider-neutral visible transcript and tool state. Atomic checkpoint commit
then replaces active context with the checkpoint plus retained human intent;
old signed/encrypted items remain only in the immutable full transcript.

The retained checkpoint boundary remains ordinary transcript content for
interaction. Full-bleed rules and native table rows use the same global chat
line map as messages, so drag selection, clipboard extraction, hover targeting,
and context menus cross the boundary without a separate opaque widget path.

Token estimation counts semantic content once. If reasoning text and replay
metadata represent the same content, the text wins. Opaque canonical replay data
is counted only when no provider measurement or semantic equivalent exists.

## 7. Context Meter

For every accepted request, the agent records:

- Request ID and kind.
- Context generation.
- Number/generation of messages included.
- Provider-measured input tokens, when available.
- Provider-reported completion tokens.
- Semantic local additions after that request.

The next-request projection is:

```text
last measured request input
+ reported completion tokens
+ semantic local additions not covered by that report
```

If no provider measurement exists, the full canonical request is estimated at
`ceil(runes / 4)`, including semantic system instructions and tool schemas.

The effective trigger is the lower of the configured percentage and the main
model's capacity minus its output and safety reserves. Extraction separately
plans each request for its own model, chunking large sources. Zero disables
automatic compaction. There is no repeated-compaction hysteresis loop.

## 8. Compaction Request

Automatic compaction runs at the next legal provider boundary. Every assistant
tool call receives its tool result first. Pending BTW messages are admitted
before summary generation.

For sources that fit the extraction connection, the extraction transaction uses:

- The resolved Aeron provider and model.
- The same system prompt.
- No tool schemas and one stable source prefix shared by the extraction stages.
- One shared hidden extraction contract followed by four stage-specific hidden
  requests over the same immutable source: intent, work, state, continuation.
- `tool_choice: none` where supported.
- The Aeron model's configured reasoning effort, including thinking mode.
- Generation capacity independent of checkpoint text size; the final checkpoint
  is validated against the continuation model's actual remaining context.

Oversized sources, including individual large tool results and arguments, are
fully fragmented into chronological source data carrying role/origin/tool
identity. Each stage extracts every bounded chunk and merges the extractions;
recursive reduction must shrink and has a fixed depth cap. Provider context
rejections reduce the extraction budget before a changed request is attempted.
Reported usage also calibrates its budget for later requests in the transaction.
The continuation model's budget controls checkpoint output and verbatim retention.

Each stage emits plain telegraphic text, never XML. Crude escapes each body and
wraps it in the deterministic version-3 envelope below. Successful prior stages
remain in memory while only a failed stage retries. Retryable transport,
download-stall, empty-output, and malformed-text failures receive at most three
stage-local recoveries; cancellation never retries. Exhaustion fails the whole
transaction closed, with the original active context unchanged.

All four requests use the workspace/session's resolved Aeron category model;
the checkpoint role still describes the context owner (`root` or `child`). The
boundary header records the actual Aeron provider/model attribution. The four
requests share the old large prefix for provider caching. This spends
more cached input than a single summary request but isolates failures, provides
truthful progress units, and substantially improves small-model format
reliability. The first ordinary request after rotation necessarily builds the
new small cache; no design can retain the removed prefix as the new conversation.

## 9. Root Checkpoint Contract

```xml
<context_checkpoint role="root" version="3">
  <intent>escaped model extraction</intent>
  <work>escaped model extraction</work>
  <state>escaped model extraction</state>
  <continuation>escaped model extraction</continuation>
</context_checkpoint>
```

The summary accounts for every genuine human message. Active requests,
corrections, and security constraints remain verbatim. Older resolved requests
may be concise. A bounded recent genuine-user tail of at most 20K tokens is kept
beside the checkpoint; unbounded verbatim retention would eventually defeat
compaction.

The model compresses language, not evidence: dense fragments and `key: value`
notation replace narrative prose while exact paths, symbols, commands, errors,
IDs, numbers, constraints, and unresolved choices remain intact. Each fact has
one owning stage; duplicated forensic narrative is forbidden. Files are recorded
by path, state, load-bearing symbols, and why they must be reread. Bulk source
already on disk is not copied unless otherwise unrecoverable.

## 10. Child Checkpoint Contract

```xml
<context_checkpoint role="child" version="3">
  <intent>escaped model extraction</intent>
  <work>escaped model extraction</work>
  <state>escaped model extraction</state>
  <continuation>escaped model extraction</continuation>
</context_checkpoint>
```

Children preserve the exact delegated assignment, not parent conversation they
never received. The report state says whether `write_report` remains mandatory
or has already succeeded and records its path. Researcher, crafter, and aeron
share the deterministic envelope; stage prompts remain role-specific and their
cached category contracts remain distinct.

## 11. Memory Boundaries

Memory recall runs exactly once for a genuine human turn. The recalled snapshot
is a separate hidden runtime-context message placed before the human message so
the actual request remains most recent.

Compaction treats memories as provenance-bearing data:

- Preserve only records that materially affect unfinished work.
- Retain memory IDs and whether facts were verified.
- Same-turn `update_memory` and `forget_memory` results override recalled data.
- Never promote unverified memory into human intent or verified decisions.
- Never run extraction or maintenance for a compaction request.

Runtime child wakes and continuation controls do not perform recall or enter
memory extraction. The next genuine human turn performs fresh recall rather than
retaining raw previous-turn memory blocks.

## 12. BTW Lifecycle

Admission remains request-correlated:

```text
queued -> included in request -> request accepted -> consumed
```

`PendingBtw` participates in chat-projection equality. Consumed and cleared
events invalidate the exact row, and tests assert both projection state and
painted output. A note received during compaction waits for the next ordinary
request; it cannot mutate an in-flight summary transaction.

## 13. Persistence And Resume

The new session schema stores exact message origin/visibility and one active
context row per agent. Active-context serialization uses the same lossless wire
message codec as transcript persistence, including provider replay metadata and
multipart content.

Authoritative turn state replaces role inference:

```text
agent_turns(session_id, agent_id, generation, origin, state, started_at, ended_at)
agent_context_state(session_id, agent_id, generation, reason, messages, updated_at)
agent_context_generations(session_id, agent_id, generation, reason, messages, created_at)
context_compactions(operation_id, session_id, agent_id, trigger, role, state,
                    stage, completed_stages, retry, tokens, timestamps)
```

Before extraction, the exact source context and nonterminal operation begin in
one SQLite transaction; this includes in-flight turn state that has not reached
the normal transcript flush. Context installation, previous-generation archival,
and terminal compaction state later commit in one transaction. Resume performs no provider work.
Persisted nonterminal compactions become retained `interrupted` boundaries with
the old active context authoritative. Persisted running turns become interrupted
presentation state. Root work waits for human input. Running children become
cancelled/interrupted results and are never restarted. Completed unconsumed child
results remain visible and are injected only into the next explicit root turn.

The legacy recovery prefix, role-based interruption detection, legacy child DB
scan, and transcript result reconstruction are deleted for the new schema.

## 14. Live Child Wakes

An asynchronous child may still wake a live parent after the parent yielded.
The wake is a typed lifecycle/runtime message, not human input. It does not run
memory recall or extraction. The UI states why the parent resumed.

Root and child compaction, context meters, request usage, events, and persisted
state remain keyed by agent ID. A root compaction cannot mutate child context and
a child compaction cannot mutate root context.

## 15. Usage And Presentation

Request usage gains a request kind (`normal` or `compaction`). Compaction input,
output, cache, and cost contribute to cumulative totals. The active-context
header uses the latest ordinary provider measurement; a full-context compaction
request does not masquerade as post-compaction occupancy.

Compaction is a first-class retained context boundary, not a fake assistant
message or transient turn status. Active and terminal forms share one compact
four-row table on the normal chat background, bounded by full-bleed rules in
the theme's active-rail accent color:

```text
────────────────────────────────────────────────────────────────
Crude provider/aeron-model                           today at 14:32
● Compaction                                       active · manual
Capturing state                               ■ ■ ● □  3 / 4
156K source · full history retained             18s · esc cancel
────────────────────────────────────────────────────────────────
```

The stage count is real completed work, not an estimated wall-clock percentage.
Terminal boundaries remain visible and resume as `completed`, `cancelled`,
`failed`, or `interrupted`; all state explicitly says whether the original
context or installed checkpoint is authoritative. Cancellation is quiet: no
error toast and no retry. Status Overview may independently show measured
context occupancy against the automatic threshold.

## 16. Implementation Order

1. DONE - freeze forensic regressions and BTW paint regression.
2. DONE - add message origin/visibility and new-session schema.
3. DONE - canonicalize provider replay metadata and replace semantic estimation.
4. DONE - rebuild root and child prompts as escaped XML.
5. DONE - separate runtime context from genuine human input.
6. DONE - add per-agent context meter and request kind.
7. DONE - implement Aeron-model root/child compaction and validation.
8. DONE - route manual `/compact` through the same transaction.
9. DONE - replace resume heuristics with authoritative passive hydration.
10. DONE - correct live child wake attribution and memory gates.
11. DONE - add compaction lifecycle projection and the BTW equality fix.
12. DONE - delete deterministic compaction and legacy recovery paths.
13. DONE - focused, package, race, build, lint, and docs gates pass; visual and
    benchmark evidence is recorded during final acceptance.

## 17. Acceptance Gates

- The 37,884-fragment forensic response is stored proportional to semantic
  content and does not compact at the provider-measured 117.5K occupancy.
- One real threshold crossing causes one model summary request.
- The summary request preserves the old request prefix and tool schemas.
- Cancellation, invalid XML, empty content, or tool calls retain the old active
  context byte-for-byte.
- Root checkpoints preserve human intent and child state.
- Child checkpoints preserve assignment, policy, ownership, and report state.
- Solo prompts contain no delegation requirement.
- Dynamic prompt fields are XML escaped.
- Memory is never attributed as human input and compaction never extracts it.
- `Waiting to send` disappears in the presented frame after consumption.
- Synthetic continuation and wake messages never render as human messages.
- New-session resume performs zero provider calls and restarts zero children.
- Context state round-trips canonical provider items for each persisted agent.
- Compaction cost is cumulative but does not replace normal active occupancy.
- Session log volume is proportional to semantic content, not stream fragments.
- A typed download stall after accepted partial content continues autonomously
  without replaying the first request; upload stalls retain request-level retry,
  and partial tool-call stalls recover without executing malformed calls.
- `go build ./...`, `go test ./...`, `golangci-lint run ./...`, `make docs-check`,
  and applicable benchmark/visual gates pass.

## 18. User Acceptance Workloads

After implementation, acceptance should include real agent tasks shaped to test
the product rather than only internals:

1. A long codebase audit with repeated large reasoning streams and a BTW
   correction immediately before a threshold crossing.
2. A root agent that launches blocking and asynchronous children, compacts while
   one child remains active, then consumes both terminal results.
3. A memory-dependent task that corrects a recalled fact before compaction and
   verifies the corrected fact survives while the stale one does not.
4. A session stopped during active work, reopened, inspected without automatic
   provider activity, and explicitly continued by a new human message.
5. A child task that compacts before `write_report` and still produces the
   required report exactly once.
