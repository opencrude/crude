# Adaptive context handling

> Status: Current - connection-scoped capacity, reasoning-aware extraction, transactional recovery, and owner-authored continuity.

Prompt ownership, guidance budgets and development rules are defined in
[model prompts and project guidance](prompt-architecture.md). This reference owns
context admission, extraction and recovery, not a second set of prompt templates.

## Product rules

These are the user's decisions. Every change in this area keeps them; the
sections below describe how.

1. **Native first, then the whole history.** Crude tries the provider's
   [native compaction](#native-compaction-on-custom-connections) on the
   selected account. When it is not available or fails, Crude writes a portable
   checkpoint of the whole history, as manual `/compact` does. It never trims
   or drops history to fit.
2. **A running agent keeps its momentum.** Automatic compaction happens at a
   safe request boundary and does not stop the run. After the checkpoint the
   agent continues the same step: the turn in progress, its steering, running
   commands and children stay current, and answered requests are marked as
   answered.
3. **The continuation keeps its contract.** Every request after a checkpoint
   carries the current system prompt and all tool definitions. Only the
   history is replaced.
4. **The user's own words survive.** The checkpoint keeps earlier human
   prompts verbatim, newest first, up to the retention budget (by default the
   size of the checkpoint; the agent setting allows 1,000 to 200,000 tokens).
   The prompt and steering of the turn in progress come first and stay pending.
   Prompts that were already answered stay verbatim in one marked
   `<answered_human_requests>` block, so their requirements still bind but are
   not answered again. A child keeps its whole latest assignment when it fits.
   An input too large for the budget is carried by the checkpoint text, and the
   full original history stays archived.
5. **No raw history behind a checkpoint.** A checkpoint written by Errand or by
   the serving model replaces the old history. Crude does not attach earlier
   raw messages or tool output behind it: that costs money, and the checkpoint
   already carries the state.
6. **Manual compaction is authoritative.** A compact order from the user or the
   main agent runs at the next loop boundary, also while the agent is running.
   Running work never makes Crude refuse it, and the user always sees the
   outcome.
7. **Recover instead of stopping.** A recoverable failure (rate limit,
   transient outage, rejected native artifact, output ceiling) is retried or
   adapted inside the loop. Only a run-level fault stops the run, and the run
   stays recoverable. User authority and short, plain messages stay intact.

## Failure and ownership

The reported manual-compaction failure sent about 498K estimated input tokens to
an extraction connection using a 256K window. The old implementation sent the
entire history to each of four stages, then retried the same impossible input.
Automatic compaction used only the main model's window, and explicit Errand
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
Provider replay items in that transcript comparison use the JSON encoding written
to their column, not the provider's original JSON whitespace or HTML escaping.
This does not normalize or mutate opaque replay data in the active context, and
changed replay values, scope, order or provenance still fail the immutable checks.

Root publishers serialize publication and live-state installation under the
session message lock. A checkpoint can add accepted output between committed
deliveries only if it retains every committed source message in order. It cannot
relabel an old snapshot with a newly loaded revision. Failed finalization cannot
roll back a later admission. Complete tool rounds are published together after
partial checkpoints, never through successively shorter prefixes. There is no
root write buffer or child whole-transcript replacement writer.

The accepted admission checkpoint is a source for its execution, not a second
live revision owner. A journal control can commit between acknowledgement and
execution startup or preparation. Startup reconciles those exact facts into the
checkpoint under the message lock; it never assigns the checkpoint's older
revision or active-context snapshot back over the current owner.

Completed tool results obtain one stable message identity before their first
partial-round checkpoint and retain it at final publication. An unfinished native
parallel wrapper is different: its changing leaf evidence is stored in the recovery
envelope's `InFlightTools`, not committed as an immutable provider tool message.
Finishing the wrapper publishes its final result once and clears that evidence.
Typed tool outcomes are optional immutable message evidence. Ordinary history
does not allocate inline result strings on checkpoint copies. Tool-call metadata
is keyed by Crude's internal call ID, so a reused provider ID cannot rewrite a
prior result's duration, operands or disclosure evidence. Display snapshots keep
factual content separately from model-only repair advice in the existing metadata.
Passive interruption materializes captured completed leaves with an explicitly
unknown wrapper outcome; it does not replay the batch or erase attachments.

Root and child publication both reconcile independently admitted exact lifecycle
controls. Child revision conflicts re-read and validate the authoritative source
with a bounded retry; changed or missing committed non-control messages still fail
closed. The recovery lock spans publication and live installation, preventing a
late acknowledgement from overwriting a newer snapshot. Runtime-notice admission
commits its receipts first, then checkpoints the entire admitted batch together.
Rejected reservations are never prematurely checkpointed and subsequently removed.

Active request order and accepted archive order are different projections. Late
steering, its hidden preparation, controls and compaction can legitimately place
shared identities differently. Active-context reconciliation preserves committed
request order; archive reconciliation preserves its own existing chronology and
validates shared identities by exact bytes and uniqueness, not relative position.
Neither projection may reorder the other or turn a valid Stop into a storage
incident. Resume hydrates provider facts from full immutable identities, never
rebuilds canonical recovery input from the lossy display projection. Compaction
mints identities for new checkpoint/history/continuity messages at creation;
retained human messages keep their original identities.

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
   resolved Errand connection's capacity; a small extraction model uses smaller
   source chunks instead of forcing early compaction of a larger main model.
3. Persist the exact source before any extraction. A fitting source preserves message order; extraction on a different model, endpoint, or no-tools envelope need not share a cache entry. Oversized sources use
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
   under the Errand agent profile, exact provider endpoint/protocol, account,
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
   space actually remains. Answered
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
checkpoint assembly live in `agent/prompt/compaction.go`; `agent/` retains admission,
chunk/retry execution and validation. `agent/prompt/runtime.go` renders the continuity
handoff from session/file-owner facts, not model-authored estimates of live work.

Across normal turns, earlier runtime handoffs remain in the accepted prefix and
fresh owner facts append with explicit supersession. This preserves prompt-cache
and [Responses delta-upload](prompt-architecture.md#responses-delta-uploads)
eligibility without making an old snapshot current. Compaction still replaces
the active context transactionally and starts a fresh transport chain. Context
admission and reported usage always use full inherited context, not delta bytes.

`ContextCompactionPercent: 0` still disables automatic pressure and overflow
recovery; manual `/compact` uses the same extractor, and a `/compact` accepted
by a running turn compacts at its next step regardless of that setting. That
request ends the turn's waits at once and always summarizes the complete
current source; extraction treats an earlier checkpoint in the source as older
state that later messages supersede. Checkpoints keep four
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
  Errand agent profile, provider endpoint/protocol, account, and model across
  new sessions/workspaces and restarts, expiring after seven days. Never tie
  this learned transport capacity to one session DB or retry a known-invalid
  output request for every stage. Recursive source reduction still recomputes
  the response room for each smaller request under the learned ceiling.

## When to compact: adaptive policy and background preparation

`agent/turnloop/precompaction.go` owns the decision; there is no fixed fullness default.

- **Physical.** The next request must fit the window with the model's full
  output allowance and safety reserve (the hard limit). Reaching it, or a
  provider context-overflow rejection, requires a checkpoint before sending.
- **Economic (default).** With catalog prices for the main and compaction
  models, each accepted request adds the share of its input spend that a
  checkpoint would have removed. Compaction is due when that accumulated
  excess reaches the price of compacting (extraction plus the cold cache of
  the next request): a ski-rental heuristic, not a guarantee about actual billing. Learned compaction cost and checkpoint size
  replace the first estimate. Prices and usage are optional provider data and
  a general rule covers their absence. Unknown prices use the common relative
  prices (uncached input 1, cached read 0.1, output 4, both models alike), so a
  cached context is cheap but never free and still earns a checkpoint past the
  quality floor instead of riding to the capacity cliff. Missing usage or cache
  reports read every previously seen token as cached and only the growth since
  the previous request as new: the cheapest physically possible reading, so
  missing data never compacts sooner than reported data and never switches the
  rule off. The planner lives in the agent's precompaction slot, so short human
  turns accumulate across runs.
- **Quality floor.** A checkpoint is lossy, a cost the spend accounting cannot
  see, so the economic rule never compacts below half of the hard limit
  (`economicFloor`), and a prepared checkpoint is not adopted below it either.
  Without the floor a cheap Errand model next to an expensive main model
  compacted a 1M-token window at ~90K tokens after a few steps. Below the
  floor only the physical trigger or an explicit percentage compacts.
- **Explicit.** A user percentage is a boundary: past it the next request
  waits for a checkpoint, unless a checkpoint that could not get below it
  deferred the next attempt (see below). `0` disables automatic compaction.
- **Live choice.** Before every step the root run reads the session's current
  passive settings (`agent.LiveSettings`): compaction percentage, retention,
  maximum output and the Errand compaction connection. They change no system
  prompt, tool list or history bytes, so a change in Settings while a turn
  works applies at its next step without invalidating the cached prefix. The provider
  still decides the actual cache hit. Manual compaction and capacity protection
  remain separate from the economic rule. Connection and tool-list changes
  use their own request or attempt boundaries. A child follows its launch role's passive settings, including named profile
  overrides. This does not change its tool permissions or file ownership.
- **Preparation leads for the portable path.** Background preparation starts halfway to the trigger
  (a quarter of the excess once three quarters of the quality floor is
  reached, halfway in tokens for capacity and explicit percentages) and runs while the agent keeps working, at most one job per
  agent. A ready checkpoint is adopted when the trigger fires; in adaptive
  mode it is adopted as soon as the accumulated excess covers the cold cache
  of the next request plus any outstanding reduction. Only the prepared prefix is already paid for. The job lives
  in a slot owned by the session (roots) or the child loop, so it survives the
  end of a run and the next run adopts it when its source is still the start
  of the context (`contextDigest` over message identities). A connection
  that refused concurrent requests (learned in `model-catalog.db`), or whose
  next checkpoint will try [native compaction](#native-compaction-on-custom-connections),
  compacts synchronously instead.
- **What is adopted.** Every model-written checkpoint covers the entire history at the boundary. A background snapshot covers complete rounds. If new messages arrive, finish the checkpoint from the prepared summary and the new messages, without rereading the old prefix. The full original history remains authoritative for archival and bounded human retention. Only the checkpoint, retained input, fresh runtime handoff, current system instructions and tools continue. No raw tool tail is attached behind a rewritten prefix. Manual and overflow requests use the complete current source directly. See the [cache evidence and seven-wire comparison](../issues/2026-11-24-compaction-cache-misses.md).
- **Survival.** Background failures back off exponentially and never block.
  The capacity margin is the largest step growth since the last checkpoint,
  so one large paste cannot keep the agent near capacity. At capacity, after
  the compaction model's adaptive handling (chunking, staged sections,
  learned output ceilings, retries) has failed because the connection is
  unavailable (credentials, missing model, outage), the agent's own
  model writes that one checkpoint, including when it shares the endpoint with a different extractor model. Provider only never does this. If no full checkpoint fits, stop recoverably with the original history. Do not commit a partial rewrite and send its cold tool tail.
- **Visibility.** Preparation is reported as `context_precompaction`
  (`started`, `progress`, `ready`, `failed`, `cancelled`, `discarded`). The
  owning slot publishes lifecycle directly; extractor progress uses one adapter,
  never an event filter that drops the slot's own terminal state. Exact job IDs
  fence stale progress and completion after discard, replacement or shutdown.
  Agent details explain **Context summary** preparation/readiness and that agent
  work is not blocked; the telemetry-only header shows no maintenance label.
  Adoption emits the normal compaction
  boundary with trigger `background`. When the run must wait for an
  unfinished job, that wait is announced as the job's boundary; if the job then
  fails or its checkpoint does not fit, the boundary settles as a deferred
  failure before the run compacts synchronously, so no boundary stays active.

## Native compaction on custom connections

The account selects **how** to compact. The agent's fullness setting still decides **when**.
No capability check runs on startup, Settings entry, model selection or passive resume.
Auto waits for an actual compaction boundary before spending on either model when
its wire has a native candidate. Manual, in-run requested, automatic, child and Errand
loop-end checkpoints use the serving connection, not the extractor's credentials, for
native work. Errand only keeps the portable path, including background preparation.
In Auto, Errand prepares in the background only when the route is known to refuse
or is in a cooldown; otherwise the native attempt needs the exact boundary source.
Provider only never charges an Errand fallback.

| Wire | First-use decision |
|---|---|
| OpenAI Responses, including a translated model | Try `responses/compact`. A valid stored-response anchor and a delta of at most 32 KiB can do real work without a probe. Without that proof use an inert challenge. On a missing or incompatible standalone operation, test the final `compaction_trigger` item on the configured Responses endpoint using synthetic input. Learn the working operation; do not send the actual history to both. |
| Anthropic Messages | Read the exact model's `capabilities.compaction.summarize.supported` with `compact-2026-09-04`. Use the on-demand `compaction` parameter. A missing declaration is not a refusal. Check an unadvertised route with synthetic input and a continuation carrying its signed block. |
| Chat Completions, Gemini generateContent, Gemini Interactions, Ollama, ChatGPT Codex HTTP | No bounded native check is implemented for these adapters. Auto uses a full portable checkpoint without probing. Provider only reports the limitation. In particular the streaming Codex subscription adapter strips output caps; public Responses compatibility is not evidence for a cheap subscription probe. |

A successful HTTP status or a text summary does not establish support. Require a
valid native item and a continuation on that account. The synthetic check asks for a
random code that is absent from the follow-up prompt. It has a 20-second deadline,
small input and output allowances, no tools, no redirects and no automatic HTTP
retries. Unsupported account request overrides are not silently discarded. The native
operation, verification and export each account for their own reported usage.

**What is saved.** Keep the complete returned Responses window, or the signed
Messages block. Reject oversized or unusable output rather than trim it into an
invalid artifact. A same-model continuation from an opaque window produces portable
text (also used if a signed summary does not provide the required sections).
The existing four-section validator, bounded human/assignment retention, refreshed
runtime handoff and transactional source archive apply before installation. A
compatible connection can replay the native item. Another account, model, wire or
changed system prefix gets the portable checkpoint; it never receives the opaque
item. Forgetting discovery does not invalidate a working conversation's prefix.

**Failure and deletion.** The application model catalog keeps model/route evidence,
not prompts or keys. A verified result lasts seven days; a definite negative lasts
one day. A transient check failure retains the last verdict and uses an exponential
cooldown (one to five minutes). A later eligible compaction rechecks; this is not a
background polling service. Concurrent checks share a route flight. Forgetting checks,
removing/reconnecting the account or changing its compaction policy fences in-flight
work. Removing the account clears its evidence and attempt records. A clone has a new
identity. Startup prunes orphaned or superseded support observations.

**Recovery.** In Auto a native failure does not stop a run. When the native route
cannot finish a checkpoint (a refusal, timeout, outage, unusable item or incomplete
handoff), Errand writes the full portable checkpoint for that boundary and the run
continues; the portable rules above apply if Errand also fails. The first change of method goes to Notices rather than inserting a new
agent message; later fallbacks during the same cooldown are silent. Provider only
never charges Errand. A definite refusal stops recoverably and names the setting to
change. An outage or uncertain result keeps its provider cause, so run recovery
retries it with the normal countdown.

Only a refused synthetic check, or a second definite failure in a row, is negative
evidence. One failure of a verified route is a cooldown; the next eligible compaction
after it checks the route with synthetic input before real history is sent, even when
the model advertises support. An unverified stored-response delta that finds no
standalone operation is sent once to the create operation instead.

A native POST has no idempotency contract. A write-ahead fingerprint of the owner,
route and source stops automatic resubmission of that source for five minutes (the
shared retry ceiling), including after a crash. Meanwhile Auto uses Errand and
Provider only waits. A confirmed input rejection releases the marker, an explicit
`/compact` can send again, and forgetting a check does not clear it. If the core
cannot open the catalog, Auto uses Errand and Provider only stops: native billing is
not attempted with an in-memory substitute.

A rejected saved artifact before output can use portable text without rerunning any
tool. If doing so would replay more than 8,192 estimated tokens behind a changed
prefix without an accepted portable turn, request a full checkpoint instead. A
timeout, partial output or arbitrary 5xx is not such proof. These limits protect
particular request paths; they are not a total billing guarantee.

## Compaction operations

- **Errand compacts when its agentic loop ends.** The workspace Errand is
  permanent, so when one of its runs completes naturally (the model answers
  without another tool call), the session registers a compaction with trigger
  `loop_end` before the turn releases admission, like a `/compact` the turn
  accepted. It is a session operation, not part of the turn: queued input waits
  for it, Stop cancels it, and the transcript shows an automatic row "after the
  work finished". Stopped, failed or parked runs, other roots and children never
  do this. A loop whose context is under 8,192 estimated tokens is already
  about checkpoint size and is left alone. A human `/compact` already registered
  makes it unnecessary. The replaced context records reason `loop_end_compaction`.

- For the portable method, automatic and manual compaction use the resolved Errand category model for
  a combined no-tools extraction of four sections (intent, work, state,
  continuation) over one immutable durable source, falling back to one request
  per section when needed. Fitting sources preserve message order but are not guaranteed cache hits; oversized
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
  context but stays above, or within one step growth of, an explicit threshold is
  committed and defers the next attempt the same way, so a system prompt and tools
  that nearly fill the percentage never compact after every step; the same holds
  in adaptive mode for a checkpoint that lands within one step of capacity. A
  deferral holds past an explicit percentage and coalesces every attempt it
  postpones into one after the growth; only the hard limit or overflow overrides
  it. It lives in the planner, so it survives the end of a run (a new human turn
  does not compact the same context again), and it belongs to one context
  generation: a manual compaction or any replaced context voids it.
  Only provider context-overflow evidence or reaching the hard
  limit makes a compaction failure stop the run.
- Manual compaction presentation is one retained, operation-ID-keyed
  context-boundary table on the normal chat background with full-bleed
  top/bottom rules using the active-rail accent color. Automatic and background
  compactions keep the same operation-ID-keyed row but paint it as one
  ordinary tool row (see [chat activity](chat-activity.md)). The table shows
  real stage units, never fabricated completion percentages. Active, retrying,
  completed, cancelled, failed, and resumed-interrupted forms share geometry;
  generic turn cleanup must never consume these durable operation rows.
- Manual compaction is a separate exact-ID operation that owns the turn gate
  exclusively, never permission to reactivate old output. A human `/compact` is
  never refused for apparent busyness: during a running turn it is taken at the
  next step (trigger `requested`), and otherwise it waits for a finishing turn
  (see [session commands](session-commands.md#context-compaction-compact)). Connected composers
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
  pending. Human input after the last terminal assistant answer belongs to the
  turn in progress: its prompt and steering stay verbatim, in order, first
  within the retention budget. Every earlier request was already answered:
  never append it as a pending user turn (the next invocation would answer it
  again, or mistake it for the current task); carry those requests verbatim in
  one code-authored, context-only `<answered_human_requests>` history block so
  their requirements stay binding without being re-answered. Durable turn
  state wins when known: a completed turn has nothing pending, and a running
  turn keeps at least its latest human input pending after an interim answer. A child's latest assignment may use
  all available continuation space, not only the recent-input cap; only an
  assignment that cannot fit at all is left to extraction.
- In-run automatic compaction rebuilds the continuation system message from the
  current guidance render. Guidance updates admitted during the run live in
  runtime messages that the checkpoint replaces; never continue on the run's
  initial render. The checkpoint starts a new guidance epoch: later runs of the
  context keep its system prompt and receive guidance changes as appended
  context ([guidance epochs](prompt-architecture.md#guidance-epochs)).
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

When action ranking is enabled, initial human recall reserves about half the
turn's memory count and estimated token budget. At a configured tool-round
boundary, the run uses its own task, tool results and paths to find candidates.
It sends up to eight unseen revisions, with applicable recorded feedback, to
the explicit ranking account in one bounded request. Each candidate gets an
independent score: a yes/no probability from a decision model, or an explicit
0–1 estimate from a chat model. The shared **Model type** setting (Automatic,
Chat or Decision; see [settings](settings.md)) selects the request. The model can reject all candidates.
Only scores above 0.5 qualify. Ties keep the original retrieval order.
Selected revisions are rechecked against the active store and added via the
ordinary hidden context checkpoint before the next request. Recall does not
create positive feedback. Cancellation, new steering, a changed configuration,
stale revisions or a failed decision never require retrying the agent action.
Decision checks have a two-second deadline. Chat allows eight seconds for text
generation, which also bounds an Automatic model-list lookup (cached per
account). Neither mode falls back to another model or endpoint.

- **The curator sees each turn's outcome.** A long turn states its subject
  first and its result last, so no stage of the memory pipeline keeps only the
  beginning. The turn mirror keeps the human text and the final report whole up
  to 8 KiB, and longer texts keep their beginning and end with an
  `[... N bytes omitted ...]` marker (`memory.TruncateMiddle`), as command output
  excerpts do. A turn keeps at most 24 observations; when more exist,
  `memory.SelectObservations` keeps human statements, then validations and
  failed or cancelled attempts, then changes, then other tool results, newest
  first within each group, and stores them in their original order. One curator
  request takes the oldest pending turns whose transcript and evidence fit
  40 KiB, always at least one turn, and the next request continues from its
  watermark until the session is caught up. A full window never cuts its newest
  turn.
- **A session summary is a record, not a memory.** It may report a result that
  only the assistant states, with attribution. It never says that something did
  not happen only because the excerpts omit it, and newer turns replace what the
  previous summary said. Agents receive earlier summaries as lossy history and
  check the workspace (for example Git) before they state what earlier work
  finished.
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

- **Codex memories**, `177c406276b6757ffaf49da9de52cd7a64c97dd5`:
  [stage-one rollout input](https://github.com/openai/codex/blob/177c406276b6757ffaf49da9de52cd7a64c97dd5/codex-rs/memories/write/src/rollout_input.rs)
  selects evidence by provenance tier (human, final answer, other agents,
  commentary, context, tools), newest first within a tier, renders it in source
  order with omission markers, and cuts oversized rows in the middle. Its
  [stage-one prompt](https://github.com/openai/codex/blob/177c406276b6757ffaf49da9de52cd7a64c97dd5/codex-rs/memories/write/templates/memories/stage_one_system.md)
  lets rollout summaries be more permissive than durable memory while keeping
  attribution. Crude adopts the tiers, the newest-first selection and the
  middle cut. It does not drop older turns to fit: its rolling summary takes the
  next window instead, so every turn reaches the curator once.

## Validation and limits

Regressions cover an approximately 540K-token source against a 256K extractor,
real `Session.HandleCompact` histories above 350K with 64K/128K Errand connections,
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
closed with the source retained. General model-visible tool-output offload remains separate. Native support and its limits are described above.

Complete project-guidance intake is a separate pipeline, not conversation
compaction or permission to load every referenced document. Its 64 MiB source
ceiling, target-model token budget, model-backed verification limits and
session-local cache are documented in the
[prompt architecture contract](prompt-architecture.md#complete-guidance-intake).
Neither pipeline's per-request context bound is a total cost guarantee.
