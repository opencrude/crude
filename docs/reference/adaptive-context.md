# Adaptive context handling

> Status: Current - connection-scoped capacity, reasoning-aware extraction, transactional recovery, and owner-authored continuity.

Prompt ownership, guidance budgets and development rules are defined in
[model prompts and project guidance](prompt-architecture.md). This reference owns
context admission, extraction and recovery, not a second set of prompt templates.

## Failure and ownership

The reported manual-compaction failure sent about 498K estimated input tokens to
an extraction connection using a 256K window. The old implementation sent the
entire history to each of four stages, then retried the same impossible input.
Automatic compaction used only the main model's window, and explicit Aeron
category capacity was not bound to its connection.

Provider request usage remains the ordinary-context authority. The per-agent
meter adds reported completion and semantic local deltas, including changes to
tool schemas during the turn. Normal request admission consumes that projection
instead of contradicting it with a fresh full-history estimate. A provider or
account switch cannot reuse another connection's persisted usage baseline.

Each explicitly configured context window is scoped to its category connection.
Unconfigured connections use discovered metadata or the existing default; that
default is a planning estimate, not proof of actual provider capacity.

## Authoritative context publication

`sessiondb.PublishContext` is the runtime publication transaction for a context
owner. It checks the source revision and atomically commits transcript deltas,
active context, immutable message identities, recovery and delivery receipts,
and compaction state as applicable. Full immutable identities retain facts absent
from the presentation transcript. Transcript comparisons use its actual persisted
representation; they never replace the full identity check or restamp an existing
message's metadata, turn, owner or timestamp.

Root publishers serialize publication and live-state installation under the
session message lock. A checkpoint can add accepted output between committed
deliveries only if it retains every committed source message in order. It cannot
relabel an old snapshot with a newly loaded revision. Failed finalization cannot
roll back a later admission. Complete tool rounds are published together after
partial checkpoints, never through successively shorter prefixes. There is no
root write buffer or child whole-transcript replacement writer.

Completed tool results obtain one stable message identity before their first
partial-round checkpoint and retain it at final publication. An unfinished native
parallel wrapper is different: its changing leaf evidence is stored in the recovery
envelope's `InFlightTools`, not committed as an immutable provider tool message.
Finishing the wrapper publishes its final result once and clears that evidence.
Passive interruption materializes captured completed leaves with an explicitly
unknown wrapper outcome; it does not replay the batch or erase attachments.

Root and child publication both reconcile independently admitted exact lifecycle
controls. Child revision conflicts re-read and validate the authoritative source
with a bounded retry; changed or missing committed non-control messages still fail
closed. The recovery lock spans publication and live installation, preventing a
late acknowledgement from overwriting a newer snapshot. Runtime-notice admission
commits its receipts first, then checkpoints the entire admitted batch together.
Rejected reservations are never prematurely checkpointed and subsequently removed.

Compaction retains the revision captured when its immutable source is published.
Installation fails closed if that source changed during extraction. Stopped
children use authoritative current context, retain newer facts and original
transcript metadata, and commit their stop boundary and recovery state together.
Parent result/mirror publication is a separate checked transaction; a mirror
failure leaves an actionable retry rather than acknowledging unfinished work.

Accepted local input consists of its immutable original plus an input-owned,
hidden preparation suffix. Reference bytes and exact source identity, supplied
ContextParts, current plan, recall, prior-session and runtime facts are captured
before acknowledgement. Duplicate admission returns the same receipt; Continue
reuses successful preparation rather than reloading mutable references. Invalid
ownership, configuration or storage preserves the original and selected identities
in the checkpoint and blocks unsafe advancement. Missing/empty pasted-image captures
and mismatched image bytes instead exclude that exact attachment, record immutable
unavailable-attachment evidence and publish a warning. The original human text,
good attachments and stored evidence remain intact; normal sends and mid-run
steering continue without replay or manual history editing.
MCP selection is not a grant, and external
context remains attributed data. Explicit empty model modalities remain disabled,
not unspecified. Storage errors during enabled recall fail closed.

Closing rejects new reservations and preserves prepared inputs without enqueueing
execution after the shutdown boundary. Stop changes execution ownership, not
accepted input ownership. Neither action may delete a submitted human input,
return it to the composer, or overwrite a newer unsubmitted draft.

New accepted wire messages receive their stable identity before archive, active
context and checkpoint installation. Finalization retries reuse those identities
and authoritative committed facts; rendered final text is not a second source
from which to manufacture another assistant message. A durable turn mirror must
succeed before completion is acknowledged. Input's owning turn and generation
are reserved before publication, and first/last human-prompt metadata commits in
the same transaction as the transcript row.

Existing development databases are aligned explicitly outside product code, not
upgraded by opening them. Backups must include committed SQLite WAL state and
associated configuration, references and Local History. Validate full typed
context/checkpoint sources before populating an identity registry; the lossy
transcript projection alone is not authoritative. Preserve existing IDs, unknown
historical identities, metadata, timestamps, provenance, pins, receipts and
history. A newly established revision baseline is not evidence of past publication
counts. Stage and verify conversion before replacing any live data, fence the
replacement against unchanged preimages, and verify passive resume without
provider/tool/child replay. One-off alignment tools do not ship in the product.

## Layered policy

1. Admit BTW and runtime child-result notices before measuring pressure. During
   an ordinary provider upload, a newly queued BTW note cancels and rebuilds
   only that request while no model output has started and the uploaded body is
   at most 90 percent complete. The agent drains every ready BTW note, reruns
   context-pressure admission, and uploads one replacement request; this internal
   rebase is not a user cancellation or a successful provider request. Once the
   upload is past 90 percent, accepted, or streaming output, notes retain the
   normal next-request behavior. A queued note is rechecked if the transport
   starts another upload attempt with the old body.
2. Decide when to compact with the adaptive policy below (or the user's
   explicit percentage, bounded by the main model's normal response and
   safety reserves). Extraction plans each request independently against the
   resolved Aeron connection's capacity; a small extraction model uses smaller
   source chunks instead of forcing early compaction of a larger main model.
3. Persist the exact source before any extraction. A fitting source shares its
   immutable prefix with the conversation's cached prefix. Oversized sources use
   chronological fragments;
   individual large messages and tool arguments/results are split on Unicode
   code-point boundaries without deleting their text. Every fragment includes
   source role, origin, and tool-result identity as data, not instructions.
4. Extract all four sections (intent, work, state, continuation) in one
   combined request that reads the source once. A section the model omits, a
   truncated response, an output cap the provider over-advertises, or a
   rejected request shape falls back to extracting the missing sections one at
   a time on the same compaction model; that fallback is learned per
   connection and model (`combinedCompactionWorks`). Each section extraction
   runs over all source chunks, then merges its fragments. The selected model's configured reasoning remains active
   on every extraction request. The response allowance follows its model output
   capability and each request's available context, including hidden reasoning;
   it is independent of checkpoint text length. On truncation, the extractor
   reduces and reprocesses the complete source in smaller regions so the next
   request has more response room. It never repeats an identical truncated
   request or installs partial text. Recursive reduction is bounded and must
   shrink its input.
   A typed invalid-request rejection naming the exact sent `max_tokens`,
   `max_completion_tokens`, or `max_output_tokens` and an allowed upper bound
   instead tunes the output allowance immediately without changing the source,
   reasoning, or completed stages. That bound applies to all following stages.
   The app-owned `model-catalog.db` durably stores the lowest confirmed ceiling
   under the Aeron agent profile, exact provider endpoint/protocol, account,
   and model. New sessions, workspaces, and process restarts start below the
   previously rejected value; other agent profiles or connections do not share
   it. Learned limits expire after seven days so a changed provider cap can
   be probed again. This is separate from the user-configured output ceiling,
   which remains an upper bound.
5. Tighten extraction estimates when reported usage exceeds the estimate. A
   canonical context-limit rejection cuts the input budget to at most two-thirds
   of the rejected size and replans; it never retries that impossible input.
6. Size the final XML checkpoint against the actual continuation model's
   remaining context after its system prompt and tool schemas. There is no
   independent 16K checkpoint ceiling or fixed per-stage quota. A complete
   extraction that exceeds the remaining space is refined with the same model;
   older verbatim human input is retained newest-first up to the size of the
   checkpoint itself (an explicit agent setting of 1,000 to 200,000 estimated
   tokens replaces that bound), then further bounded by whatever continuation
   space actually remains. Recent turns stay verbatim as the tail (below). Answered
   human requirements use the same limit inside their marked context-only history
   block. Oversized individual
   inputs are represented in the checkpoint rather than retained unboundedly
   alongside it; the original source remains archived. Reject empty,
   tool-calling, control-invalid, truncated, nonshrinking, or over-budget results.
7. Commit the complete checkpoint, previous generation, and terminal lifecycle
   atomically. Only then consume notices and BTW admission. Cancellation or any
   pre-commit failure retains the source and installs no partial checkpoint.
8. A normal provider request rejected for canonical context overflow triggers
   bounded compaction and retry only when no output was accepted. Recovery
   targets at most half the projected input, with at most three recoveries before
   a successful normal response. Authorization, malformed schema, cancellation,
   and generic payload-size errors are not misclassified as context overflow.
9. The installed context ends with a fresh code-authored continuity handoff, not
   a retained historical prompt. Known durable human-turn state takes precedence
   when deciding whether the request was answered; the standalone path uses
   message-order evidence only when that state is unavailable. An answered
   request is represented by the checkpoint rather than appended as new-looking
   user input. Pending human input remains verbatim within the configured retention
   budget. A child's latest assignment is not reduced to that recent-input limit:
   it may use all available continuation space, but still falls back to extraction
   when it cannot fit safely.
10. Runtime owners are sampled again after extraction. The handoff carries the
    current plan and session selection plus exact async handle/command/cwd/PID,
    status, output length and observed cursor, and child identity/status. A
    running command has no exit code. Snapshotting never advances output,
    signals or restarts work; model extraction cannot override fresher owner data.

Command output admission is separately bounded before it can create pressure:
`read_async_output` returns one raw-byte page (32 KiB by default, 64 KiB maximum)
with `next_byte`, `total_bytes` and `has_more`. Completed synchronous commands
larger than the inline budget return a head/tail excerpt and a retained handle.
Agents are given a session-owned cross-platform `CRUDE_TEMP_DIR` for noisy logs
and intermediate artifacts. A tracked background command (explicit or promoted
after the foreground window) that completes, fails or times out on its own is
delivered to its owning agent as one durable runtime `command_outcome` event:
admitted at the next safe boundary or waking the idle agent, so agents never
poll for completion. Explicit kills, inline-reported exits and retained output
of already-settled commands are not reported again. Compaction continuity records only owner-observed
output length and cursor; it never reads the skipped bytes.

Extraction contracts, stage wording, attributed source-fragment envelopes and
checkpoint assembly live in `prompt/compaction.go`; `agent/` retains admission,
chunk/retry execution and validation. `prompt/runtime.go` renders the continuity
handoff from session/file-owner facts, not model-authored estimates of live work.

Across normal turns, earlier runtime handoffs remain in the accepted prefix and
fresh owner facts append with explicit supersession. This preserves prompt-cache
and [Responses delta-upload](prompt-architecture.md#responses-delta-uploads)
eligibility without making an old snapshot current. Compaction still replaces
the active context transactionally and starts a fresh transport chain. Context
admission and reported usage always use full inherited context, not delta bytes.

`ContextCompactionPercent: 0` still disables automatic pressure and overflow
recovery; manual `/compact` uses the same extractor. Checkpoints keep four
sections whether they were extracted together or one at a time; chunk progress is additional truthful detail within a stage.
The chat boundary and the agent activity strip report the same operation:
while manual compaction is active, its progress replaces the paused turn's
plan activity; after success the strip says the context is ready to continue.
The transcript remains archived, while the checkpoint replaces the active
context. On resume, the durable boundary supplies the settled status until a
new turn starts. Automatic compaction resumes its interrupted turn instead of
displaying a manual completion prompt.
Manual operations retain their own exact operation identity independently of a
completed/stopped model turn. Its terminal fence still prevents old output from
reactivating work, while compaction lifecycle/progress reaches every connected
shell. Duplicate or late progress cannot reopen a settled operation.

Input submitted during manual compaction is acknowledged only after it enters
the session's durable ordered queue. Both shells see one pending human row;
only the submitting shell may consume its exact unchanged draft. The pending
marker is part of the replicated message contract. Natural completion admits
the next prompt; Stop cancels the exact compaction and preserves queued input
without starting a new turn. Queue removal, context admission and its recovery
checkpoint commit in one SQLite transaction before execution or settlement is
announced. A failed dequeue preserves the queue and starts nothing. Queue-only
sessions are never pruned as empty; cold resume restores pending rows passively
and rejects corrupt queue data instead of overwriting it. Loading occurs in the
session creation/resume worker, not UI preparation, hydration or painting.

Core job capacity and durable receipt replay checks precede owner preparation,
so a refused/repeated request cannot leave compaction ownership reserved.
Shutdown cancels extraction and joins its owner before closing persistence;
final session metadata is mirrored from the settled on-disk database, never a
closed borrowed handle. Follow-up turns retaining an unchanged checkpoint do
not re-archive its evolving generation under an immutable archive identity.
The active plan lives in the session's plan store, outside the compacted
conversation. Each new human turn receives its current revision and complete
step snapshot as hidden runtime context; the stable system planning contract
asks the model to compare it with the new request and revise or close stale
work. The runtime snapshot is data and is never attributed as a user message.
Supplied steps replace the current ordered snapshot exactly; omitted obsolete
steps survive only in immutable revision history. A materially superseding goal
uses explicit `replace=true` with objective and complete steps, atomically resetting
step provenance while retaining the plan identity and its audit history. Generic
and native interfaces share this contract. Milestone updates and honest final
closure remain model obligations, not inferred successes by the runtime.

The September 29 failure sent roughly 598K input tokens with a 3,936-token
generation cap derived from a 16K checkpoint split. The DeepSeek extraction
model used high reasoning, consuming much of that response allowance before
the work stage returned `length`. The former workaround disabled thinking;
that quality regression has been removed. Request capacity and checkpoint size
are now calculated separately, preserving the user's selected reasoning mode.

## Request boundaries and provider replay

Responses delta uploads and inline-image replay recovery are specified in
[prompt architecture](prompt-architecture.md#responses-delta-uploads).

- Wire/model/account/config switches are ordinary request boundaries. Preserve
  the canonical transcript and exact tool identities; the destination adapter
  owns schema-valid projection and only outgoing opaque replay is filtered.
  Never clear history, force a producing protocol, or add transport-level tool
  vetoes for forms an adapter can encode. Cold transition requests are expected;
  unchanged subsequent projections must preserve their warm prefix. Guard every
  registered protocol pair through resume, real tool loops and compound settings
  changes, not only empty-session protocol switches.
- Provider-reported request input is the context authority. Project the next
  request from that baseline plus reported completion and semantic local deltas;
  fallback estimation is `ceil(runes/4)` over canonical content, never stream
  envelopes or repeated provider-item wrappers.
- Completed provider replay metadata is canonicalized before estimation,
  persistence, replay, and logging. OpenAI reasoning fragments merge once;
  signed or encrypted protocol blocks remain byte-exact.
- Opaque replay state is bound to the producing provider endpoint/protocol and
  exact request prefix. Request-local filtering never mutates the transcript:
  cross-provider or changed-prefix turns omit opaque reasoning, while a model
  switch on the same provider preserves it for the provider's documented
  compatibility check. Compaction summarizes visible/tool state only and never
  attempts to inspect ciphertext.
- Established partial stream interruptions never replay a stochastic request. Commit
  canonical partial assistant output and append one hidden continuation request;
  if tool-call construction was interrupted, execute none of those calls, drop
  malformed protocol fragments, and ask the model to issue complete calls from
  scratch. Upload/header stalls may retry the original request because no model
  output was accepted.
- Provider-reported output-parameter ceilings are distinct from context-window
  errors. A typed 400/422 naming the exact sent output token value and valid
  upper bound reduces that stage's allowance, not its source or thinking mode.
  App-wide `model-catalog.db` retains the lowest confirmed ceiling keyed by
  Aeron agent profile, provider endpoint/protocol, account, and model across
  new sessions/workspaces and restarts, expiring after seven days. Never tie
  this learned transport capacity to one session DB or retry a known-invalid
  output request for every stage. Recursive source reduction still recomputes
  the response room for each smaller request under the learned ceiling.

## When to compact: adaptive policy and background preparation

`agent/precompaction.go` owns the decision; there is no fixed fullness default.

- **Physical.** The next request must fit the window with the model's full
  output allowance and safety reserve (the hard limit). Reaching it, or a
  provider context-overflow rejection, requires a checkpoint before sending.
- **Economic (default).** With catalog prices for the main and compaction
  models, each accepted request adds the share of its input spend that a
  checkpoint would have removed. Compaction is due when that accumulated
  excess reaches the price of compacting (extraction plus the cold cache of
  the next request): the ski-rental rule, never worse than twice the best
  choice made with hindsight. Learned compaction cost and checkpoint size
  replace the first estimate. Unknown prices count only uncached tokens, and
  missing cache reporting is read as cached, so missing data never compacts
  sooner. The planner lives in the agent's precompaction slot, so short human
  turns accumulate across runs.
- **Quality floor.** A checkpoint is lossy, a cost the spend accounting cannot
  see, so the economic rule never compacts below half of the hard limit
  (`economicFloor`), and a prepared checkpoint is not adopted below it either.
  Without the floor a cheap Aeron model next to an expensive main model
  compacted a 1M-token window at ~90K tokens after a few steps. Below the
  floor only the physical trigger or an explicit percentage compacts.
- **Explicit.** A user percentage is a boundary: past it the next request
  waits for a checkpoint. `0` disables automatic compaction.
- **Preparation leads.** Background preparation starts halfway to the trigger
  (a quarter of the excess once three quarters of the quality floor is
  reached, halfway in tokens for capacity and explicit percentages) and runs while the agent keeps working, at most one job per
  agent. A ready checkpoint is adopted when the trigger fires; in adaptive
  mode it is adopted as soon as the accumulated excess covers the cold cache
  of the next request, because its extraction is already paid. The job lives
  in a slot owned by the session (roots) or the child loop, so it survives the
  end of a run and the next run adopts it when its source is still the start
  of the context (`contextDigest` over message identities). A connection
  that refused concurrent requests (learned in `model-catalog.db`) compacts
  synchronously instead.
- **What is adopted.** The fresh system prompt and tools, the checkpoint of the
  older context, a refreshed runtime continuity handoff, and the recent turns
  verbatim. `verbatimTailCut` keeps the current and previous human turns,
  else recent tool rounds, never more than the part being summarized, and
  never splits a tool call from its result. Re-runnable tool results older
  than the latest round in that tail (file reads, searches, listings, web
  pages, async output) become one-line stubs that say how to fetch them
  again (`decayVerbatimTail`); command output, edits and external results stay
  verbatim. Decay happens only at a checkpoint, where the cached prefix is
  rebuilt anyway.
- **Survival.** Background failures back off exponentially and never block.
  The capacity margin is the largest step growth since the last checkpoint,
  so one large paste cannot keep the agent near capacity. At capacity, after
  the compaction model's adaptive handling (chunking, staged sections,
  learned output ceilings, retries) has failed because the connection is
  unavailable (credentials, missing model, outage), the agent's own
  connection writes that one checkpoint; if no model can, deterministic decay
  of the whole context frees room. Only when none of these fits does the run
  stop, recoverably.
- **Visibility.** Preparation is reported as `context_precompaction`
  (`started`, `progress`, `ready`, `failed`); the agent header shows
  "checkpoint…" or "checkpoint ready". Adoption emits the normal compaction
  boundary with trigger `background` (or `decay`).

## Compaction operations

- Automatic and manual compaction use the resolved Aeron category model for
  a combined no-tools extraction of four sections (intent, work, state,
  continuation) over one immutable durable source, falling back to one request
  per section when needed. Fitting sources share the cached prefix; oversized
  histories or individual tool inputs/results use complete chronological,
  origin-attributed text fragments and bounded model-backed chunk-and-reduce,
  never oldest-history deletion. Each fragment repeats its source role/origin
  and tool-result identity instead of sending orphan protocol calls.
  The retained boundary records the actual model and real chunk progress.
  Context windows are connection-scoped when explicitly configured; ordinary
  pressure reserves normal output and includes runtime notices before admission.
  Every extraction request plans against its own model's available context.
  Provider-confirmed overflow retries only after checkpoint reduction; extraction
  shrinks its input budget on provider evidence. Preserve the configured thinking
  mode during extraction: hidden reasoning counts toward the response allowance,
  so checkpoint text size must never cap generation. Use the actual available
  continuation space for checkpoint size; refine complete sections if necessary.
  A truncated extraction reduces and reprocesses the complete source in smaller
  regions; never replay the identical exhausted request or install a partial
  checkpoint. Bounded genuine-input retention and continuation capacity are
  validated before installation.
  Models emit terse plain text; Crude escapes and wraps the four bodies in one
  deterministic XML checkpoint. Retry only the current stage, at most three
  recoveries; cancellation never retries. Previous-generation archival,
  checkpoint installation, and terminal operation state commit atomically.
  The exact source context is durable before extraction starts, including
  in-flight turn state. Failure, cancellation, or pre-commit crash leaves that
  source authoritative.
- Automatic compaction is best-effort while the next request still fits. Below
  the hard limit (window minus the normal output reserve and safety reserve), a
  failed attempt, or a checkpoint that does not leave room for a response, keeps
  the full context, emits a deferred `context_compaction_failed` boundary, and
  continues; the next attempt waits until the context grows by the largest
  step growth seen since the last checkpoint (at least 2% of the window). A checkpoint that reduces the
  context but stays above the threshold is committed and defers the next attempt
  the same way. Only provider context-overflow evidence or reaching the hard
  limit makes a compaction failure stop the run.
- Compaction presentation is one retained, operation-ID-keyed context-boundary
  table on the normal chat background with full-bleed top/bottom rules using
  the active-rail accent color. It shows
  real stage units, never fabricated completion percentages. Active, retrying,
  completed, cancelled, failed, and resumed-interrupted forms share geometry;
  generic turn cleanup must never consume these durable operation rows.
- Manual compaction is a separate exact-ID operation after a completed/stopped
  model turn, never permission to reactivate old output. Connected composers
  queue input durably with replicated pending rows; dequeue, context and recovery
  commit atomically before execution. Stop preserves the queue, cold resume is
  passive, and queue-only sessions are not empty. Core capacity/receipt checks
  precede owner preparation; shutdown cancels extraction before joining workers
  and mirrors final session metadata without reading a closed SQL handle.
- Context-boundary tables are ordinary chat content for interaction: native
  rows, full-bleed rules, and body cells all participate in drag selection,
  clipboard copy, hover/hit testing, and row context menus. Their distinct
  visual treatment must never create a second opaque interaction path.

## Continuity handoff

- Message origin and visibility, not provider role or string prefixes,
  distinguish human/BTW input, runtime context, continuation controls, child
  wakes, assignments, and checkpoints. Runtime or memory data is never
  attributed as human intent.
- A compacted context ends with one fresh code-authored `runtime_context`
  handoff. It states that compaction is not a new user turn, carries the latest
  durable human-turn state, current plan, session/model/account/capability
  selection, child identities/statuses, and command-owner snapshots. Extraction
  may describe those facts but never owns them. Refresh the handoff after all
  extraction stages because commands and children may settle during compaction.
- Retain a verbatim human tail as input only for a request that is still
  pending. If a later terminal assistant answer already completed the latest
  human request, never append it as a pending user turn (the next invocation
  would answer it again); carry those requests verbatim in one code-authored,
  context-only `<answered_human_requests>` history block so their requirements
  stay binding without being re-answered. A child's latest assignment may use
  all available continuation space, not only the recent-input cap; only an
  assignment that cannot fit at all is left to extraction.
- In-run automatic compaction rebuilds the continuation system message from the
  current guidance render. Guidance updates admitted during the run live in
  runtime messages that the checkpoint replaces; never continue on the run's
  initial render.
- Async continuity uses exact owner data: handle, command, cwd, PID, start time,
  running/terminal status, terminal exit only when known, total output bytes,
  and the last observed byte cursor. Taking a continuity snapshot must not read
  output, advance a cursor, signal a process, restart a command, or treat a
  running command as exit zero. Compaction preserves and observes existing
  handles; it never relaunches work to rediscover state.
- New-format resume is passive: restore exact per-agent active context and turn
  state, mark interrupted work, issue zero provider calls, and restart zero
  children. Existing development sessions have no compatibility path.
- Root checkpoints preserve human intent and child-result ownership; child
  checkpoints preserve assignment, policy, file ownership, evidence, and the
  `publish_report` obligation. Compaction requests never enter memory extraction.

## Memory jobs and shutdown

- Memory extraction and maintenance are session-owned cancellable jobs. Closing
  cancels and joins them before the workspace DB closes, without launching new
  provider work. Durable turn mirrors finish, and pending extraction watermarks
  recover on next startup, including a resume of the same session.
- Workspace memory and the session registry are mandatory session dependencies.
  `CreateSession`/`ResumeSession` retry a transient workspace-DB startup failure
  and wire the recovered store before returning; never launch an agent with a
  nil memory store and a merely logged `db_open_failed` warning.
- Turn finalization writes its one batched transcript mirror synchronously on the
  off-UI orchestrator goroutine before releasing the turn. Never register a
  flush WaitGroup after observers may already be waiting; that races shutdown,
  manual compaction, and persistence acceptance tests.
- Async output length and cursors are owner facts, not permission to read the
  bytes during continuity or compaction. Snapshotting never drains output.

## Harness research and decisions

Primary sources were reviewed at pinned commits rather than assuming an older
implementation still matched the product:

- **Codex**, `1983c48fd186614529e4fad418ac38a1ae00c6b6`:
  [context manager](https://github.com/openai/codex/blob/1983c48fd186614529e4fad418ac38a1ae00c6b6/codex-rs/core/src/context_manager/history.rs)
  uses measured usage plus a local tail; live tool-output truncation is separate
  from full rollout persistence.
  [Local compaction](https://github.com/openai/codex/blob/1983c48fd186614529e4fad418ac38a1ae00c6b6/codex-rs/core/src/compact.rs)
  removes oldest paired history on overflow. Crude adopts accounting, reserves,
  and archival separation, but not silent source deletion during extraction.
- **Kilo Code**, `e9948b6000697757ceea194403750cd80effe70c`:
  the current implementation is the OpenCode-derived runtime, not the older Roo
  extension. [Overflow preflight](https://github.com/Kilo-Org/kilocode/blob/e9948b6000697757ceea194403750cd80effe70c/packages/opencode/src/kilocode/session/overflow.ts)
  combines a measured baseline with additions; [chunk extraction](https://github.com/Kilo-Org/kilocode/blob/e9948b6000697757ceea194403750cd80effe70c/packages/opencode/src/kilocode/session/compaction-chunks.ts)
  budgets and reduces oversized history. Its fixed estimate inflation and clipped
  transcript/tool excerpts are not copied; Crude submits complete attributed text
  fragments and learns a smaller budget from provider evidence.
- **DeepSeek Harness**, `639ed015397290b3745d163aafe02ffee4aa3f84`:
  [compaction backend](https://github.com/deepseek-ai/deepseek-harness/blob/639ed015397290b3745d163aafe02ffee4aa3f84/packages/compaction/compaction-basic/src/index.ts)
  reserves output/headroom and allows overflow retries only after surface
  progress. Its [token meter](https://github.com/deepseek-ai/deepseek-harness/blob/639ed015397290b3745d163aafe02ffee4aa3f84/packages/llm/token-meter/README.md#readme)
  scopes usage to the routed envelope. Crude adopts progress-gated recovery and
  connection-scoped capacity while keeping its stricter all-or-nothing checkpoint
  transaction; optional pruning is not a partial commit here.

## Validation and limits

Regressions cover an approximately 540K-token source against a 256K extractor,
real `Session.HandleCompact` histories above 350K with 64K/128K Aeron connections,
huge tool arguments/results, UTF-8 boundaries, late child wakes, provider capacity
rejections, stage-local retries, reasoning-preserving output-cap recovery,
checkpoint refinement, typed output-ceiling learning across new sessions and
catalog reopen, connection/profile isolation, expiry, truncated output,
cancellation, exact source archival, and passive persisted checkpoint
restoration, answered-request non-replay, live async-state refresh, and bounded
project-guidance compilation. Name-allocation tests
also exhaust the full first-name/surname tier without duplicate numeric labels.

Connected regression journeys complete a real initial turn, start `/compact`,
hold the extraction request, and submit through both shell composers. They
assert painted compaction and pending rows, ordered single execution, no duplicate
answers, unchanged newer/peer drafts, exact Stop, passive cold resume, receipt
replay, and shutdown of a live extraction. Storage-failure tests inject a rejected
dequeue transaction and verify no context, execution, or false acknowledgement.

This is not a promise of perfect recall: model summaries can omit facts, token
estimation without reported usage remains approximate, and media contents are not
text fragments. Oversized text extraction keeps media descriptors while the
exact media-bearing source remains archived. System/tool envelopes that alone
exhaust a model, unavailable providers, and reductions that cannot converge fail
closed with the source retained. General model-visible tool-output offload and
provider-native remote compaction are separate future work, not implied by this
implementation.

Complete project-guidance intake is a separate pipeline, not conversation
compaction or permission to load every referenced document. Its 64 MiB source
ceiling, target-model token budget, model-backed verification limits and
session-local cache are documented in the
[prompt architecture contract](prompt-architecture.md#complete-guidance-intake).
Neither pipeline's per-request context bound is a total cost guarantee.
