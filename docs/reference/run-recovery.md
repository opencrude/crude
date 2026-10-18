# Unfinished run recovery

> Status: Current reference — supervised attempts, durable unfinished work, and bounded cooldowns.

## Contract

An unsuccessful attempt is not a completed task. Normal completion, an explicit
user stop/termination, or an owning parent lifecycle decision can settle work.
Transport exhaustion, request deadlines, unexpected returns, truncation, loop
guards, missing child reports, configuration faults and internal failures must
not silently erase ownership or manufacture a successful finish.

Crude is resilient, not literally indestructible. It cannot repair missing
credentials, obtain permission, invent a certificate trust decision, make broken
storage writable, or guarantee progress from an unreachable provider. It retains
unfinished work and makes those conditions actionable instead of hammering them.
Uncertain side effects require inspection rather than replay. Process death,
hardware failure, externally deleted/corrupted data and uncooperative remote
writers remain real limits.

## One supervisor, separate budgets

`agent.Supervisor` owns the root/child recovery lifecycle. `session/` supplies
typed failure facts, exact pending input, generation-fenced wake signals and
transactional `sessiondb` checkpoints. `types/recovery.Status` is the neutral
projection shared by runtime, chat and hydration. `prompt/recovery.go` owns the
code-authored continuation instructions; these are runtime data/control, not a
new human message or a replacement for permission policy.

| Failure | Response |
|---|---|
| Any provider failure without positive evidence of permanence (network/provider outage, lost HTTP/2 connection, peer reset, unfamiliar disconnect shape) or an exhausted request-attempt budget | Automatic continuation with a capped cooldown and Retry now while unfinished work remains owned |
| Provider error object with no recognizable type, code or status | No fast retry inside the request; five supervised automatic attempts with the countdown, then Needs attention stating that automatic recovery stopped |
| DNS lookup failure, including “not found” | The task supervisor rechecks automatically with capped pacing; the bounded request-attempt policy remains separate |
| Request deadline, with the owning turn still live | Recover the attempt; never label it “stopped by you” |
| Accepted partial response | Preserve it and append a semantic continuation, never replay the stochastic request |
| Truncated/unfinished tool arguments | Execute none of that response's calls; retain visible text and ask for complete calls |
| Repeated tool loop, output truncation, missing final report | Bounded corrective continuation, then an actionable pause if correction does not work |
| Authentication, billing/quota, invalid configuration/trust/guidance | Needs attention; no unbounded invalid-request loop or silent account/model failover |
| Persistence failure | Keep the owned snapshot/draft, block unsafe advancement, and allow a checked retry |
| Native file-ownership rejection (outside scope, or another agent's claim) | Ordinary tool error: nothing changes, the run continues, the parent is notified and can replace the live child's scope with `agent_update_claims` |
| Automatic compaction failure while the next request still fits | The run continues on the retained full context and retries compaction after the context grows by a bounded step; the boundary row records a deferred failure, nothing needs attention. Background preparation failures back off silently |
| Compaction model unavailable at capacity | After the extractor's own adaptation (chunking, staged sections, learned output ceilings, retries), the agent's own connection writes that checkpoint; if no model can, deterministic decay of re-runnable tool results frees room; only then does the run stop recoverably |
| Compaction failure with no room left (at the hard limit, or after a provider context-overflow rejection) | Provider-originated failures recover like any provider request; a local fault (for example a checkpoint that still does not fit) needs attention with a compaction-specific action |
| Faulty clipboard-image capture | Exclude only the missing/empty/invalid image, preserve the original text and reference evidence, report the exact exclusion and continue with usable attachments |
| Tool panic (uncertain effects) | Tool error result telling the model to inspect affected state; later serial siblings of that batch are skipped; the run continues |
| Agent or child runtime panic | Preserve safe boundaries and pause for reconciliation; keep the root input service usable |
| Child stopped for any attention reason | Parent is notified with the exact incident, sees it via `agent_inspect`/`agent_list`/`agent_wait`, and can `agent_control` `continue` or `cancel` it without a human |
| Explicit user or parent cancellation | Stop promptly, preserve completed effects/text and record the explicit stop boundary |

Provider request attempts, compaction extraction attempts, permission reviews and
background memory jobs retain their own bounded budgets. Spending one budget
does not settle the unfinished coding task. Valid permission denials are never
retried as technical errors, and recovery never expands tool/file/network grants.
Children have no arbitrary task deadline; parents and users retain lifecycle control.

`provider.Classify` is the single failure-classification authority and requires
positive evidence of **permanence**: an explicit retry veto, a permanent HTTP
status or provider code (authentication, permission, quota/billing, validation,
unknown model/endpoint, policy), an identified invalid parameter, a certificate
failure, an unparseable endpoint URL or an oversized response. Everything else is
transient. An allowlist of transient errors cannot keep up with HTTP/2 stacks,
proxies and gateways; each miss used to become a "configuration" incident that
waited for a human (a lost HTTP/2 connection parked a root session this way).
A failed read of an established response body while the caller still waits is
typed at the stream boundary as a `transport_read_failure` interruption, so the
agent continues accepted partial output and the supervisor recovers.
`provider.Retryable` is the request owner's immediate-retry decision: transient
only, excluding a spent attempt budget and a resolver "not found".

HTTP failure classification is shared by every wire protocol and by streaming and
non-streaming requests. A generic `invalid_request_error` / `invalid_request`
envelope does not override a transient HTTP 408/409/425/429/5xx status. Specific
quota, billing, authentication, model and validation evidence, an identified
invalid parameter, and an explicit `x-should-retry: false` still prevent retries.
Numeric status evidence inside a structured stream error follows the same rule.
Every registered streaming decoder also shares one wire-boundary classifier for
the exact gateway diagnostic `stream error: stream disconnected before completion:
stream closed before response.completed` or explicit `stream_error` /
`gateway_stream_terminated` evidence. Gateways can retain that upstream Responses
diagnostic in Chat Completions or Anthropic error events, or translate it into an
Ollama NDJSON string/structured error. The classifier retains the original API
cause and downstream protocol identity; it does not make arbitrary disconnect
prose or non-streaming invalid requests retryable. Invalid parameters, specific
permanent codes and the HTTP retry veto still take precedence.

Vendor-native coverage is separate from gateway translations. Anthropic SSE
`overloaded_error`, `api_error`, `timeout_error` and rate-limit errors remain
transient; authentication/billing/permission/not-found/oversized/validation
evidence remains permanent even when wrapped in a transient status. Ollama's
documented exact string-only model-execution failures are recoverable at the
stream boundary; missing models, invalid input, insufficient-memory requirements
and unknown text are not guessed transient: they are unrecognized verdicts that are
never fast-retried and receive only the bounded supervised attempts. The successful-stream retry veto
also survives a missing terminal event. References: [Anthropic errors](https://platform.claude.com/docs/en/api/errors)
and [Ollama errors](https://docs.ollama.com/api/errors). These tests simulate the
documented wires locally, not live vendor availability.

This distinction matters for gateways: an upstream stream disconnect can arrive
as an HTTP 408 JSON response even when Crude is using Chat Completions. Typing
only a Responses SSE disconnect event does not cover that path. Such a
misclassification parks without a deadline; the five-minute ceiling cannot wake
an attention state that never scheduled an automatic wait.

## Runtime invariants

- `agent.Supervisor` is the one root/child unfinished-run lifecycle. Request
  exhaustion, deadlines, truncation, loop guards, missing reports and panics are
  not natural completion or permission to kill a task. Recover transient outages;
  park configuration, guidance, storage and unavoidable compaction failures with
  an actionable Needs attention state. A compaction failure that leaves room for
  the next request is deferred, never a stop: cheap or unstable compaction
  models must not halt a run that can still make requests. Only natural finish or explicit user/parent
  lifecycle may settle work. Preserve safe canonical boundaries; never replay
  accepted stochastic output, historical tool effects or existing commands.
- **The agentic loop is sacred.** Tool-level failures never end a run: ownership
  scope/conflict rejections, write-lease races, tool panics (uncertain effects,
  later serial siblings skipped) and guidance read failures for a batch are tool
  error results the model handles. Only run-level faults (provider permanence,
  configuration, storage, compaction, guidance compile, agent panic, exhausted
  corrective/bounded recovery) stop a run. Never reintroduce a typed tool error
  that parks or waits.
- **A stopped child is never a dead end.** Every child `NeedsAttention` — parked
  or waiting in place — notifies its parent at the point the incident is
  actionable (`Supervisor.Attention` for waits, `parkRecovery` for parks) with
  the exact `recovery_id`/`recovery_generation` and controls. `agent_inspect`,
  `agent_list` and `agent_wait` expose the open incident (`agent_wait` ends with
  `needs_attention`). The parent continues or cancels it with `agent_control`
  `continue`/`cancel` + recovery identity, offline or not; human Retry/Stop use
  the same `RetryRecoveryFor`/`CancelRecoveryFor`, which return reasons and are
  shown in the TUI — never a silent no-op. A cancelled parked child notifies its
  parent. `ResumeRecovery` reports every refusal; run-bound claims of the
  interrupted attempt are released by runtime takeover and never block it.
  Parked recovery is published only after old-attempt cleanup releases its run,
  manager, claims, connection, DB and runtime attachment. Fence its in-place wake
  slot as teardown-only before parking; an immediate Continue must not race
  deferred cleanup or inherit a run still owned by the interrupted attempt.
- `sessiondb` retains exact pending input, owner/generation, archive and active
  context through one durable recovery checkpoint. Transcript, turn ledger and
  collaboration settlement must succeed before acknowledging natural completion.
  Finished parallel leaves checkpoint while siblings run, including native batches;
  completed results receive their stable message IDs before their first checkpoint.
  Evolving native-batch evidence lives in `RecoveryCheckpoint.InFlightTools`, not
  the immutable active-message prefix. Completion clears only its exact evidence;
  passive interruption preserves finished leaves and marks the remainder unknown.
  missing crash outcomes are unknown, never fabricated user cancellations.
  Preflight-blocked inputs keep their DB even without transcript rows. Pending
  children restore as actionable review surfaces, not completed or invisible work.
  Resume remains passive; explicit
  Continue run or `/retry` fences the exact incident/generation and must not
  consume drafts, switch another tab or restart old effects. Stop must also work
  for parked/interrupted work without an active collaboration run.
- `sessiondb.PublishContext` is the revision-fenced runtime publication owner.
  Root writers serialize durable publication and live installation under
  `msgsMu`; checkpoints retain every committed source message, finalization
  cannot roll back a newer admission, and complete tool rounds never regress
  partial checkpoints. No root write buffer or child whole-transcript replacement
  writer remains. Original input, successful immutable preparation and the
  initial supervisor checkpoint commit before a receipt. Closing rejects new
  reservations and preserves prepared input without enqueueing execution.
  Starting an already-admitted turn reuses that durable envelope rather than
  republishing the unchanged source. Related-only publications still advance the
  revision under CAS and commit their recovery/receipts transactionally, but do
  not re-encode or update unchanged `messages_json` payloads.
  Stopped children use current authoritative context and retain newer facts;
  parent mirrors/results are separately checked, never silently acknowledged.
  Root and child checkpoints reconcile exact late lifecycle controls and
  journal-admitted human steering with its immutable preparation suffix.
  Runtime-note receipts commit before one complete batch checkpoint, never after
  checkpointing reserved notes that a failed admission would try to remove.
- `retry/` owns every Crude-managed automatic cooldown. Exponential math
  saturates safely and cancellation is prompt; **five minutes is the absolute
  per-wait maximum**, including Retry-After and millisecond/date hints. A server's
  24-hour hint must never cause an hours-long local retry sleep. Request/job
  attempt budgets and successful maintenance cadence remain separate.

## Shared cooldown contract

`retry/` is the one authority for automatic cooldown arithmetic and waits:

- **Absolute maximum: five minutes per cooldown**, including server hints.
- Exponential growth saturates before duration/integer overflow.
- Provider retries use bounded jitter; zero/past hints do not remove local pacing.
- `Retry-After`, HTTP-date values and `retry-after-ms` are clamped to the ceiling.
  Thus an advertised 24-hour wait becomes a five-minute cooldown; this explicitly
  takes precedence over a longer server-advised minimum.
- Cancellation is checked before waiting, including a zero-duration wait.
- Configurable provider/account backoff values cannot exceed 300 seconds.

Five minutes is not a total outage deadline, an attempt count, a progress timeout,
or a normal maintenance cadence. A transient outage may require many separate
cooldowns; no individual automatic retry wait grows into hours. The public model
catalog has the same failure ceiling; its successful six-hour refresh cadence is
not shortened. Reviewer/model-job safety budgets remain independent.

The provider's per-attempt progress watchdog captures the actual transport
connection. A typed upload/header/download progress stall retires that exact
connection before the next attempt can reuse it. Cancelling an HTTP/2 stream
alone leaves a silently blackholed connection in the multiplexed pool; a finished
cooldown would otherwise select the same unusable connection instead of redialing.
Ordinary user cancellation and overall request deadlines do not trigger this
retirement, and no pool-wide flush, policy change or unconditional redial is used.
Retiring a failed multiplexed connection can interrupt sibling streams on it;
accepted output still uses semantic continuation, never stochastic request replay.

## Durability and replay safety

Pending work is checkpointed before admission/effects, with the original input,
exact owner/generation, input-admission flag, archive and active context. Recovery
uses that same current context owner as normal turns and compaction. Accepted
assistant/tool boundaries are retained; transcript/context persistence must succeed
before a natural completion is acknowledged.

Finished parallel siblings are checkpointed while other calls are still running,
including children of the native parallel wrapper. Interrupted wrapper snapshots
retain real leaf results and mark their overall outcome unknown. A tool panic,
including inside a parallel batch, becomes a tool error stating that its effects
are uncertain; not-yet-started serial siblings do not execute after it, and the
run continues so the model inspects state. Per-path locks release even when the
tool panics.

An unfinished wrapper's evolving leaf evidence is recovery state, not an immutable
accepted tool message. `InFlightTools` preserves that exact evidence (including
attachments) separately until completion. A completed tool result keeps the same
message identity across partial-round and final checkpoints. Passive interruption
uses captured evidence once and explicitly marks unknown outcomes without replay.
Root and child tests exercise real SQLite publication during out-of-order tool
completion, rather than accepting every checkpoint in a mock callback.

Normal input acceptance commits the original, successful immutable preparation,
and initial supervisor checkpoint together before returning its owner-fenced
receipt. A failed selection or preparation keeps its exact pending input and
requires attention instead of issuing a weakened request. Stop/close preserve
accepted input even when execution has not been queued. Semantic late tool and
stream events cannot reactivate a stopping or terminal generation or consume a
later draft; a genuinely new generation remains runnable.

Root checkpoint/finalization and delivery writers share the serialized
revision-owned publication described in [adaptive context](adaptive-context.md).
Root and child checkpoint reconciliation retains late journal-admitted human
steering and its immutable preparation suffix, just as it retains lifecycle
controls. A failed attachment must not turn an independently committed note into
an unrecoverable stale-checkpoint error. Changed/reordered original source still
fails closed; this never replaces a newer context with an old one.
Child terminal context, transcript deltas and recovery settle in one transaction.
Parent mirrors/results follow through separately checked publication; errors
remain retryable and never manufacture successful cancellation or completion.

Terminal settlement retains a typed, immutable publication intent in the durable
checkpoint. Stop cancels execution, not that publisher's ownership. Required
ledger, collaboration, result and mirror failures keep the exact incident
actionable and withhold completion events. After shutdown or restart, passive
resume performs no execution; exact Continue repairs only the captured settlement,
without another provider request, tool effect or child run. Captured successful
outcomes and report provenance are not rewritten into fabricated cancellations.

Natural completion also waits for the durable turn and collaboration ledgers.
Retrying finalization acknowledges only an already-committed identical terminal
outcome for the exact run/owner/epoch/context; a different stop/outcome fails closed.
Persistence retries do not request another stochastic answer.
Session editor initialization always uses the owning session's artifact root and
identity. A storage error is actionable startup failure, never permission to
select a process-global journal directory, invent another session identity or
continue with a missing editor. Failed resume preserves pre-existing session
artifacts while cleaning only temporary data created by that failed attempt.
An old recovery cancellation fences the owning turn context itself, not merely
the earlier incident lookup. New human/peer turn admission cannot overwrite a
parked task. The composer refuses an ordinary send before consuming that draft;
Retry now/Stop and slash remediation remain available.

Human steering admission shares the execution owner's Stop and terminal-seal
fence, not just the collaboration run's still-nonterminal record. At natural
completion the owner seals new notes, flushes all previously accepted notes,
and takes another model step if any remain. Only then may it publish completion.
A note refused after sealing or Stop leaves the exact composer draft intact.
Ordinary connected Enter always goes to the same core admission owner, which
chooses live steering, a fresh turn or a durable queue behind terminal sealing.
It never treats a stale shell's routing hint as a second lifecycle authority.
Continue reuses an already-recorded Local History message scope and its original
safety snapshots through a continuation lease, rather than inserting a duplicate
scope under the same accepted human-input identity.

No supervisor automatically reruns a historical tool call, relaunches an existing
command, advances an async output cursor, or treats a running process as exit zero.
Already accepted results remain evidence. Opaque commands/MCP effects that may
have happened are not made idempotent by a retry timer or a file-ownership claim.
Permissions, mutation leases and mandatory Local History safeguards still apply.

Resume is passive. Startup marks unfinished execution as interrupted and restores
its safe checkpoint without provider calls, command replay or implicit child
launch. **Continue run** explicitly requests a checked replacement attempt.
Collaboration `RunID`/version/epoch facts remain immutable attempt identities;
an interrupted attempt does not prove its unfinished task was completed.

A child's parked incident becomes actionable only after the old attempt has
released its run, claims, manager entry, connection state, execution pin, database
handle and runtime attachment. Needs-attention persistence is not teardown
completion. Parking fences the old in-place wake slot as teardown-only before
emitting its status; waits remain pending until the parent notification and
actionable recovery entry publish last. An
immediate Continue therefore cannot collide with an old run or have its new
attachment/connection removed by the previous attempt's deferred cleanup.

The accepted checkpoint tail is restored into the ordinary root transcript before
resume is exposed. Missing tool outcomes are explicitly unknown, never reported as
“cancelled by user.” A newer finalized transcript is not replaced with an older
checkpoint, and a mismatching accepted prefix fails closed. Pending inputs retain
their session DB even before input admission. Pending child work returns to Agents
with its accepted transcript and own incident; child mirror incidents do not
appear in the root chat. A prior attempt's final report remains evidence and cannot
satisfy a replacement collaboration run's deliverable.

## Provider failure classification

- **Provider failure classification requires positive evidence of permanence,
  never of transience.** `provider.Classify` is the one authority:
  `FailurePermanent` (auth/permission/quota/validation/unknown-model status or
  code, identified param, retry veto, certificate, unparseable endpoint,
  oversized response) waits for a human; `FailureUnrecognizedVerdict` (a provider
  error object with no recognizable type/code/status) is never fast-retried and
  gets five supervised automatic attempts, then attention saying recovery
  stopped; everything else — including disconnect shapes nobody has seen — is
  `FailureTransient` and recovers automatically with countdown and Retry now.
  Never reintroduce a transient allowlist whose miss defaults to "configuration":
  it parked Hannah on `http2: client connection lost`. A failed read of an
  established body while the caller still waits is typed at the stream boundary
  as `StreamInterruptionTransport`. Local agent faults carry explicit failure
  kinds so this provider policy never applies to them; compaction recovers
  automatically only for provider-originated failures.
- Shared HTTP recovery uses status evidence, not generic error-envelope labels:
  `invalid_request_error` must not turn 408/429/5xx into a permanent settings
  fault. Specific auth/quota/validation evidence and explicit retry vetoes remain
  non-retryable. Test every registered protocol plus real root/child timer expiry;
  an SSE-only adapter test does not cover a gateway's HTTP JSON rejection.
- Established-stream errors use the shared `provider.streamErrorDecoder` for
  Chat Completions, Responses/Codex, Anthropic SSE and Ollama NDJSON. A gateway
  may translate an upstream disconnect into any of these wires, including a
  string error. Recognize narrow typed/observed disconnect evidence at the stream
  boundary, not arbitrary diagnostic prose in global retry policy. Preserve
  auth/quota/validation and retry vetoes; accepted output requires semantic
  continuation, never stochastic replay or execution of partial tool arguments.

## Presentation and interaction

One ID-keyed incident row is updated through Recovering, Needs attention,
Recovered, Interrupted and explicitly Cancelled. It defaults to one compact
content row, without the old full-width notice rule or exposed error paragraphs.
Recovering shows a shared-clock blinking red square, a real countdown, a
clickable **(Retry now)** and ordinary disclosure chevron. Retry now shortens the
exact owner's current cooldown; timers continue automatically without `/retry`.
The owner's acknowledged replacement attempt clears the old deadline and changes
the retained row to a quiet, static **Run resumed** boundary. It keeps the complete
diagnostic behind disclosure without a red square, blink or retry target.
That deadline-free owner acknowledgement releases the live activity phase:
subsequent upload, provider wait, reasoning and response events remain authoritative
through ticks and tab changes. The retained incident must not overwrite them with
“Recovering”. The previous live attention issue resolves on acknowledgement;
its notification remains in Notices. Only a cooldown/attention/interruption exposes
Continue/Retry now, never an already-running replacement attempt. Explicit Stop
outranks a pending incident while cancellation is acknowledged.
Resumed is not a successful task finish: its durable checkpoint stays open until
the supervisor commits completion/cancellation, so passive crash recovery still
keeps the unfinished work. A subsequent failure updates the same incident.
Needs attention and interrupted incidents remain static and expose Retry now.
Recovered/cancelled incidents remove the retry target.

Explicitly stopped turns retain their ordinary stop boundary and cancelled
recovery incident as canonical history, but both contribute zero chat geometry.
The fixed activity strip alone shows one quiet **Stopped** label. Other recovery
states remain visible; stopping is not a request to erase accepted output.

Clicking the header/chevron or the existing disclosure shortcut expands the
actual full reason and remediation; dragging uses ordinary selection/copy and
never activates Retry now. Expanded diagnostics use normal foreground ink and
guidance uses muted ink. Header clock changes reuse cached evidence and do not
change row height or rewrap the reason. Credential-shaped diagnostics are
redacted rather than disclosed. The entire remaining diagnostic is available for
selection/copy; it is not arbitrarily cut at 140/240 characters.

The fixed activity strip carries the concise state/countdown, not raw diagnostics.
Needs-attention and interrupted states are static: no fake working animation or
rotating plan pages. Accepted messages, tools and the composer draft remain intact.
Live and hydrated rows use the same typed projection and timestamps.

Explicit entry points are **Continue run** in chat/agent/tab context menus,
**Continue Agent Run** in the palette, and `/retry`. An asynchronous request is
fenced to exact backend owner, incident ID, turn generation and workspace boundary;
it cannot wake another incident or consume a new draft. Stop remains available
while waiting/recovering. Configuration changes can wake an active waiting owner;
they do not silently restart passively recovered history.

A root panic/unwind first installs the exact passive checkpoint and releases its
old turn reservation, then emits the actionable attention state. Retry cannot
race a published incident against missing pending ownership or an old still-live
reservation. Ordinary active cooldown/attention waits keep their live wake path;
this publication ordering does not wait or replay accepted work.

Exact per-message timestamps/producing connection facts are retained when already
present in display snapshots. A crash checkpoint that contains only a canonical
tail can supply its boundary timestamp, not reconstruct unrecorded per-token times
or invent missing producing-account metadata. No terminal blink attribute, new
private timer, or optimistic lifecycle state is used for the row.

See [network transport](network.md) for secure remediation of routing/TLS issues,
[chat activity](chat-activity.md) for transcript contracts, and
[workspace collaboration](workspace-collaboration.md) for exact lifecycle controls.

## Incident presentation

- Recovery uses one compact, expandable incident row. Automatic transport/DNS
  recovery needs no `/retry`; a real countdown and exact-owner `(Retry now)`
  action can shorten its cooldown. A red square blinks on the shared activity
  clock only during recovery, with stable measured width. Expansion shows the
  full neutral diagnostic and muted guidance without rewrapping on clock ticks.
  Waiting for attention never animates fake work. Live and hydrated projection,
  copy/selection, timestamps and explicit cancellation must agree.
- A deadline-free recovering acknowledgement is an active replacement attempt,
  not another cooldown. Its retained incident must never overwrite subsequent
  upload/wait/thinking/response phases during inventory or activity projection.
  Paint it as a static, muted Run resumed boundary, not a blinking red Recovering
  row above tools already executing; keep its unfinished checkpoint and evidence.
  Resolve the live attention issue on acknowledgement, retain Notices history,
  remove duplicate Continue actions, and let explicit Stop outrank the incident.

## Regression acceptance

- `provider/upload_retry_network_test.go`: actual HTTP/2 TLS through a local TCP
  blackhole; upload and header stalls automatically redial after cooldown and
  send the complete byte-identical original payload without a reset/wake. Accepted
  partial output is not replayed; cancellation/deadline during cooldown prevents
  a second attempt. Custom transports without `httptrace.GotConn` cannot provide
  this exact-connection retirement; the shared product transport is exercised.
- `provider/http_recovery_regression_test.go`: transient HTTP classes, generic
  envelopes, streaming/non-streaming retry, explicit permanent evidence/vetoes,
  and preserved invalid-parameter evidence.
- `provider/stream_recovery_regression_test.go`: exact established Chat gateway
  failure before and after text/reasoning/tool fragments; actual HTTP SSE/NDJSON
  paths for every registered protocol, including Anthropic events and Ollama
  string/structured errors, permanent negatives and explicit retry vetoes.
- `agent/stream_recovery_regression_test.go`: root/child semantic continuations
  across every registered protocol, retained accepted prefix, discarded incomplete
  calls, real countdown expiry and no manual wake or original-request replay.
- `session/stream_recovery_regression_test.go`: actual Chat root/child runners,
  durable SQLite countdown/continuation, retained exactly-once earlier file effect,
  required child report provenance and exact Stop preventing a delayed restart.
- `agent/http_recovery_regression_test.go`: every registered protocol, root and
  child, repeated exhausted requests beyond the five-minute saturation point.
  The clock wait is simulated; real timer expiry is tested below.
- `session/http_recovery_regression_test.go`: real local HTTP, SQLite, root and
  actual child runner; an earlier completed file effect remains exactly once,
  persisted owner-specific countdown expires without a wake, child final report
  retains provenance, and exact Stop prevents a delayed restart.
- `session/upload_retry_journey_test.go`: actual root and `StartAgent` child,
  HTTP/2 upload flow-control stall through a TCP blackhole, durable automatic
  supervisor cooldown and redial without manual wake. The complete original
  input and earlier exactly-once file effect survive; child reports retain their
  true run/author, and exact Stop prevents a later dial after the abandoned wait.
- Existing supervised protocol journeys inject partial tool streams, missing
  terminals and cancellation across all registered protocols. Persistence,
  panic, permission and passive-resume suites remain required negative gates.
- `tui/viewmodel/run_recovery_lifecycle_test.go` submits through the composer and
  checks painted deadlines, manual repair for authentication, automatic HTTP 408
  continuation, retained draft, and removal of stale recovery actions.
- `tui/viewmodel/remote_stop_lifecycle_test.go` completes automatic compaction,
  repeats Stop/follow-up across two connected shells, proves steering reaches
  the next provider request, stops an authentication failure and passively
  reopens before a fresh turn. Existing connected manual-compaction journeys
  cover queued prompts and cancelled extraction. `stop_lifecycle_visual_test.go`
  runs Stop, steering and manual-compaction cancellation through the repository
  capture workflow at desktop and constrained sizes.
- `session/retained_session_lifecycle_test.go` optionally accepts SQLite backup
  images through `CRUDE_LIFECYCLE_SESSION_FIXTURES`; it touches only temporary
  copies and a local provider, checks exact Stop, three fresh turns, passive
  reopen and unchanged pre-existing transcript metadata/provenance.

These are deterministic local failure simulations, not a guarantee that every
remote gateway reports trustworthy status codes or that an unavailable service
will become reachable. Permanent/unknown faults and uncertain tool effects stay
actionable rather than becoming blind infinite retries.
