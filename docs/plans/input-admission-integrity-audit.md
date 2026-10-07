# Input Admission And Runtime Integrity Acceptance Ledger

> Status: IN PROGRESS — authoritative active ledger (2026-10-07); implementation and release acceptance remain unverified.

Work is paused at the user's request. Read the
[root immediate-action report](../../IMMEDIATE-ACTION-REQUIRED.md) for the
latest final agent evidence, unresolved startup/integration failures and exact
continuation order. Historical audit baseline results below are not current
release acceptance; no reported issue is cancelled by this pause.

## Authoritative requested behavior

**Stop breaks the agentic execution loop, never the accumulated context.** This
applies to human Stop and main-agent controls over children. A submitted message
shown as sending must survive even if the current HTTP upload has not included it
or completed. Stopping the request must not reject that message, move it back to
the composer, drop it from the transcript, or exclude it from future context.

This document replaces the earlier return-to-composer proposal completely. Remote
provider acceptance is not the local conversation-ownership boundary. Unsubmitted
drafts and synchronous submission rejection remain separate: preserve those
drafts, but do not fabricate a send the application never accepted. Automatic
transport/supervisor recovery attempts retain their current policy; this audit
does not redesign their retry budgets or equate a transient failure with Stop.

Expected example:

1. A loop is uploading its request. A human submits another message, shown as
   sending for the next request.
2. The human stops execution. The upload/request is cancelled and no replacement
   provider/tool attempt begins.
3. The submitted message is committed once to the exact owner's local conversation
   and context. Its sending indicator settles; the composer is not rewritten.
4. A later explicitly requested turn sees both the old context and that message.
   Retaining input does not itself authorize running the stopped task again.

## Release status and proof rules

This document is the single active acceptance ledger for the current reliability
work. It retains the detailed Stop evidence below and expands the release scope to
every currently reported issue. Documentation cannot cancel a defect. A package
test or child report is component evidence, not proof of the integrated user
journey. Release remains blocked while any required row is pending, failed, or
lacks attributed evidence.

Status vocabulary:

- **PASS** — integrated evidence has been reviewed and named here.
- **PARTIAL** — useful evidence exists, but the complete journey or matrix is not
  proven.
- **FAIL** — a current reproduction contradicts the requirement.
- **PENDING** — implementation or acceptable evidence has not been reviewed.

Current baseline:

- **FAIL:** the 13 initial Stop/context reproductions below remain the acceptance
  baseline. One already-admitted upload/Stop/next-turn case passed, but did not
  cover queued local admission.
- **FAIL:** the first integrated affected run was aborted after a decisive real-turn
  context-CAS/owner-lifecycle fault. Runtime subsequently reported a focused fix
  for ID inheritance when a returned message is otherwise byte-identical to the
  canonical append. Four named real-turn tests now pass, but revision/checkpoint
  integration is unfinished.
- **PARTIAL:** independent storage work reported focused/race passes for transaction
  identity, rollback, source revisions and runtime-agent identity. Independent UI
  work reported focused/race passes for draft/envelope behavior. Independent
  command-output work reported `fileio` passes. These do not close integrated
  root/child journeys.
- **PARTIAL:** provider reports `go test ./provider` and
  `go test -race ./provider` passing with typed missing-terminal coverage for all
  registered protocols. Runtime cooldown/continuation remains unverified.
- **BASELINE ONLY:** before implementation, 81 package groups passed and 63 gated
  tests skipped. This stale baseline proves no current change.
- No build-all, full repository suite, lint, complete race matrix, cross-compile,
  schema alignment, native visual acceptance or live-gated provider result is
  currently claimed.

## Complete acceptance ledger

### A. Durable admission, Stop, drafts, media and teardown — **FAIL / BLOCKING**

- [ ] Root/child user input and parent/peer/runtime deliveries commit exactly once
  before provider-dependent preflight. Stop at before-receive, before-loop, every
  gate, upload 0/50/90/95/100%, provider wait and finalization preserves immediate
  chat, transcript, current context and the next explicit request.
- [ ] One typed, owner-scoped, monotonic source revision is authoritative for
  transcript/context publication. Stable message/delivery IDs retain origin,
  actor, owner, root-versus-recipient identity, run/generation, source sequence,
  attachment/capability identities and terminal disposition. Shared-DB changes
  are transactional.
- [ ] The durable source high-water mark covers pending, reserved, admitting and
  received records across queues/mailboxes; a volatile channel or stale in-memory
  mirror is never the only owner.
- [ ] Stop launches no replacement request/tool. Independent text/thinking and
  actual results survive; incomplete tool arguments remain non-executable and
  non-replayable. Newest drafts, cursor, selection, undo/history and attachments
  remain untouched.
- [ ] Close, minimize, terminate, A-B-A session/workspace switch, shutdown, crash
  and passive resume neither lose nor prune accepted work and trigger zero
  implicit provider/tool/child replay.
- [ ] Selected MCP/tool identities, media and references retain immutable
  preimages. Missing/unreadable content produces an actionable error and blocks
  weakened execution; teardown waits for draft/reference persistence.

The detailed failures, architecture requirements and Stop matrix later in this
document remain required; none is superseded by a focused passing test.

### B. Tool-path and guidance preflight — **PENDING / BLOCKING**

- [ ] Invalid concrete tool paths, including `text*` paths and ownership `**`,
  return a normal actionable validation error before guidance scope discovery can
  poison the turn. They cause no filesystem, provider, tool, context replacement
  or guidance-cache effect.
- [ ] A real applicable guidance file that exists but cannot be read still fails
  closed; invalid-operands handling must not weaken complete guidance intake.
- [ ] Root/child blocked messages and controls enter the durable source passively
  with original attribution. Admission neither bypasses validation nor wakes or
  executes the blocked request.

### C. Structural child failure and supervisor settlement — **PENDING / BLOCKING**

- [ ] A structural, unremediable failure in the current child execution ends that
  attempt with an exact durable incident and parent-prompt notification. It does
  not loop forever in Needs attention/recovery or silently become success.
- [ ] Child context, work, reports and incomplete evidence remain owned and
  resumable; missing required final evidence stays unfinished.
- [ ] Transient transport/DNS/provider failures retain automatic bounded recovery.
  Structural/configuration classification must not disable legitimate recovery or
  retry accepted stochastic output.

### D. Exact claim update and explicit continuation — **PENDING / BLOCKING**

- [ ] Claim update checks exact version, runtime epoch and immutable preimage; its
  terminal worker is joined before publication. Failure has no claim, file, run or
  permission effect.
- [ ] The durable repaired claim is the effective scope and local guards derive
  from that one authority. No duplicate/shadow authority or compatibility path
  remains.
- [ ] Continue run and replacement-child launch are explicit. Repair never
  implicitly wakes work, restarts a child, replays effects or widens permission.

### E. Large command output continuity — **PARTIAL / BLOCKING**

- [ ] Large result/status retains exact owner, handle, command/cwd where allowed,
  real backing path, PID/start identity, byte total, bounded ranges, observed
  cursors and terminal state. Only actual facts are recorded.
- [ ] Retrieval is bounded and cursor-based for synchronous, asynchronous, native
  and interactive command paths in roots and children. Continuity/compaction does
  not read or advance bytes.
- [ ] Resume never reruns a command, invents a retained path, or treats running or
  unknown work as exit zero. Missing/expired backing data fails explicitly.

Focused `fileio` evidence does not close session persistence, root/child/native
integration or the remaining Windows interactive coverage.

### F. `agent_wait` and Board lifecycle semantics — **PENDING / BLOCKING**

- [ ] Wait applies the exact conjunction of every supplied agent/run/report
  condition and returns typed actor, run and artifact references. Report readiness
  is not execution success.
- [ ] Normal no-match timeout is graceful and typed, not a leaked raw deadline;
  caller cancellation remains distinct from elapsed wait.
- [ ] Subscription plus a final snapshot closes the registration race. Readiness
  immediately before/during subscribe is not missed; no model/Board polling or
  sleeps are needed.
- [ ] Wait and Board reads do not consume receipts, advance human unread state,
  expose private context or manufacture an ordinary-delivery wake.

### G. Provider-neutral accepted-stream recovery — **PARTIAL / BLOCKING**

- [x] Provider emits exported `provider.StreamInterruptionError` with typed
  missing-terminal/protocol-error kinds, `Unwrap`, preserved
  `io.ErrUnexpectedEOF`, and caller cancellation taking precedence.
- [x] Deterministic local HTTP stream tests dynamically require all registered
  protocols and assert accepted semantic output, missing official terminal, typed
  error and no client retransmission. Provider package and race suites pass in the
  attributed provider milestone.
- [x] Responses retry evidence is narrow: structured gateway termination or the
  exact observed disconnect-before-`response.completed` diagnostic. Generic
  `invalid_request`, schema parameters and similar prose remain nonretryable.
- [ ] The supervisor preserves the accepted prefix, waits on a real capped cooldown
  timer, and autonomously issues a semantic continuation for root and child work;
  it never replays the original stochastic request.
- [ ] Human Stop/cancel suppresses recovery. Permanent typed failures settle into
  the correct incident without retry loops. Partial tool construction executes no
  calls and requests complete calls only through safe continuation.

The actual registered protocol set is:

1. `openai` — OpenAI Chat Completions,
2. `openai-responses` — OpenAI Responses,
3. `anthropic` — Anthropic Messages,
4. `ollama` — native Ollama,
5. `codex` — ChatGPT Codex Responses.

For each protocol retain real local HTTP/SSE or NDJSON fixture coverage for
accepted partial text/reasoning, partial tools, missing official terminal,
autonomous continuation, user cancellation and permanent error. Retain the
all-protocol switch/resume matrix with real tool rounds. Externally gated live
tests are reported separately and never substitute for deterministic fixtures.

Provider evidence: final report
`artifact_029b40cf13faa7b15280fff8252fcffd892d0e771aa05538da444c4b340c5222`
revision 1, authored by the provider owner. It records `go test ./provider`
(23.187s) and `go test -race ./provider` (24.810s), exactly one HTTP request per
accepted-stream fixture, and no live account, endpoint or user-database access.
Its explicit limit is that only Responses has a currently known structured
in-band disconnect envelope; future protocol envelopes require adapter-specific
typed evidence, never a universal English classifier.

### H. Architecture, schema, performance and release evidence — **PENDING / BLOCKING**

- [ ] One authoritative admission/context writer and claim authority exist;
  remove fragile handcrafted synchronization, duplicate mirrors, fallbacks and
  legacy compatibility rather than layering them.
- [ ] UI `Update`/`View` performs no persistence, filesystem, provider, command or
  reference I/O. Realistic dense-session hot-path gates prevent unbounded scans,
  output reads and context exports.
- [ ] Persisted schema is the current `CREATE` contract with no automatic upgrade.
  Before changed schema touches known local data, make a backup and perform
  parent-approved manual alignment outside product code, verifying row counts,
  content hashes, IDs, provenance, revisions, receipts, transcripts, claims,
  reports and passive resume.
- [ ] No half-measure release: canonical references agree with verified behavior,
  and this plan is removed only after the complete proof bundle passes.

## Required final proof bundle

- [ ] Focused regressions and complete affected package suites.
- [ ] `go build ./...` and `go test ./...`, separating documented pre-existing
  failures from new failures.
- [ ] Race coverage for affected admission/session/database/collaboration/provider
  packages, including selected Windows-native paths.
- [ ] Supported cross-compiles for platform-specific lifecycle/command code.
- [ ] `golangci-lint run ./...`.
- [ ] `"C:/Program Files/Git/bin/bash.exe" scripts/docs_check.sh`.
- [ ] Performance gates for changed hot paths.
- [ ] Native Windows visual acceptance through the real product at readable and
  constrained widths; immediate, settled and resumed chat must agree.
- [ ] Deterministic local protocol matrix, all-protocol switch/resume real-tool
  tests, and separately labelled live-test skips/results.
- [ ] Manual data-alignment record and passive-resume verification if schema
  changed.

Every PASS must name the real entry point, asserted durable state, negative cases,
command, platform and artifact/log identity. A command starting, a report being
published, or one package passing is not whole-task proof. Aborted gates and gated
skips remain explicit.

## Scope and verification

The review followed cancellation entry points and context writers across `app`,
`workspace`, `tui/viewmodel`, `agent`, `session`, `sessiondb`, `collaboration`,
`provider`, `prompt`, tool execution and command lifetime. It covered root and
child loops, human steering, parent lifecycle controls, preflight, uploads,
streaming, serial/parallel tools, permissions/questions, recovery settlement,
compaction, Local History boundaries, close/termination, workspace switching,
shutdown, passive resume, protocol changes and provider-side continuation.

Unrelated LSP/search/editor operation cancellation was classified separately:
cancelling an operation-local task is not permission to mutate conversation
context. No blanket claim is made that every filesystem, desktop, provider, crash
or scheduling combination has been exercised.

Evidence used the real Ezra session read-only, product composer/backend entry
points with local provider fixtures, SQLite persistence, and read-only Go overlays
whose test sources/logs live in the current session temporary directory. No actual
sub-agent was launched; child controls were tested with owned fixture handles.
Production Go files and Ezra's local data were not modified.

### Validation results

- **13 top-level new Stop/context reproductions fail**, with three preflight and
  three late-semantic-event subcases. These are defect evidence, not passing tests.
- One new upload/Stop/next-turn journey passes: a message already in canonical
  context survives request cancellation and is included once in the next real
  request with `continue`.
- Full current suites pass for `agent`, `session`, `sessiondb`, `workspace`,
  `collaboration`, `provider`, `prompt`, `tui/input` and `tui/viewmodel` (1,788
  top-level passing test outcomes). Existing coverage misses the clarified rule;
  some existing UI tests intentionally assert that queued semantic data is dropped.
- Selected native Windows race tests pass in `agent` and `session`. The requested
  selector found no matching tests in `workspace` or `collaboration`; that is not
  race coverage of those packages or proof that concurrent context writers are safe.
- Prior audit independently reproduced close-time deletion, selected MCP payload
  omission, draft I/O and attachment-identity problems. Those facts remain relevant;
  earlier expectations that Stop should return submitted messages are superseded.
- Documentation/whitespace checks are required after this revised report. No
  implementation, full-repository build/lint/test run or native desktop capture is
  claimed by this audit.

## Confirmed failures under the revised rule

| Path | Evidence | Violation |
|---|---|---|
| Root preflight Stop | `TestStopContextPreflightKeepsSentInput`, before-turn/history/account subcases; `session/run.go` runs gates before adding the human input. | Stop settles the run while the original sent input occurs zero times in durable transcript and active context. Ezra's exact feature request was retained only in a cancelled recovery record; the first model request contained `continue`, not that request. |
| Published note during upload | `TestStopContextQueuedMessageDuringUploadSurvives`, held at 95% upload; `agent/run.go` releases buffered runtime messages on error/Stop instead of admitting them. | Note is released once, admitted zero times, absent from both archive and active context. Stop does not start another request, but preservation fails. |
| Stop before loop entry | `TestStopContextQueuedMessageBeforeLoopSurvives`; top-of-loop cancellation precedes runtime inbox admission. | Already submitted note is omitted because Stop wins before the next iteration. |
| Durable human-delivery release | `TestStopContextPublishedHumanNoteMustNotBeRejectedOnLoopStop`; `newHumanSteeringRuntimeMessage.Release` uses `Reject:true`. | End of execution changes an already published human message to rejected instead of preserving it in local context. Workspace journal evidence alone is not model-context admission. |
| Actual composer sending/Stop journey | `TestStopContextRealComposerSendingNoteSurvivesStop` uses real Enter, workspace publication, owner Stop, terminal events and shell paint. | The sending note disappears from chat, is inserted back into the composer, and is absent from durable context. No follow-up provider request runs. This directly contradicts the clarified behavior. |
| Removal helper and late disposition | `TestStopContextNeverRemovesSendingMessage`; previous admitted-then-rejected fixture also erased an admitted note. | `RemovePendingSteeringMessages` deletes matching user rows even after their pending flag clears. The reject UI path treats execution lifetime as input lifetime. |
| Parked Stop/passive resume | `TestStopContextParkedPendingInputSurvivesStopAndResume`; `CancelRecoveryFor` builds from checkpoint archive/context, not pending input. | The original sent input remains absent before and after passive resume. A stop boundary is added, but the missing input is not restored to context. |
| Parked Stop versus newer context | `TestStopContextParkedStopCannotOverwriteNewerContext`; previous retry fixture showed the same class. | Stop overwrites a newer Local History restore fact with an older checkpoint. Context can regress even though execution stopped correctly. |
| Main-agent child control | `TestStopContextMainAgentControlKeepsCurrentChildContext` publishes and applies exact `agent_control cancel` against a fixture child. | Control admission replaces the child's durable context using a stale/empty `Agent.ActiveContext` snapshot before signalling Stop. The exact child stops, but its retained context is lost from the durable current row. |
| Supervisor active-context mirror | `TestStopContextSupervisorStopKeepsActiveSnapshot`. | If Stop wins before `Agent.Run`, `RunSupervised` preserves local archive/active variables but leaves `Agent.ActiveContext()` empty/stale. Session/child finalizers read that second owner, risking an older context overriding the correct snapshot. |
| Queued output/real results | `TestStopContextQueuedSemanticEventsMustSurvive`, stream-token/thinking/tool-result subcases; `AgentState.HandleEvent` cancellation shield drops all three. | Preventing visual reactivation also drops received semantic evidence from live chat. Backend durability and resumed chat can disagree with the immediate stopped frame; real results may remain displayed as cancelled. |
| Incomplete tool stream | `TestStopContextPartialToolMustNotEraseReadableReasoning`; `agent/run.go` clears reasoning whenever partial tool calls exist. | Safety removal of invalid tool replay also erases already received plain-text reasoning. Incomplete tool calls must remain non-executable, but their presence must not delete independent readable content. |
| Immediate close before receive | `TestStopContextImmediateShutdownCannotDiscardQueuedInput`; actual service is held before receive, then `CloseOrchestrator` and input-loop teardown run. | A successful queued submission is discarded before any durable context exists. A valid shutdown boundary must stop execution while committing already accepted input. |

### Related durable-data and writer risks

- `Session.Close` preserves transcript/open recoveries but can prune the database
  of a settled early-stopped input. The prior close fixture verified actual file
  deletion. This same close path is used by shutdown and workspace switching.
- A normal send paints a user row and clears the composer after a volatile channel
  acceptance. UI/backend message IDs are unrelated, and the initial checkpoint is
  later. This permits accepted-looking UI without durable local ownership.
- `InputAdmitted` is set after an in-memory append, before its persistence has
  succeeded. Provider/guidance gates must not decide whether a locally submitted
  message exists; the flag currently mixes these boundaries.
- Live control admission, recovery persistence, collaboration, restore and
  compaction all replace the active-context row. Recovery snapshots and the live
  Agent/session mirrors have no common monotonic source revision fence. Exact
  run/epoch fencing protects whom to stop, not which context bytes may be written.
- After Stop, finalization retries use the cancelled execution context. Some paths
  can settle/discard a checkpoint after transcript persistence fails; child unwind
  can report cancelled without successfully mirroring its latest transcript. These
  need explicit preservation acknowledgement and independent persistence lifetime.
- Closure can select `stopCh` instead of receiving `inputCh`; queued accepted work
  must be persisted before the loop's execution service is destroyed.
- Peer evidence released during Stop becomes pending again; idle collaboration
  polling can turn that old obligation into a new execution. Context admission
  and run wake must be independent so preserving old evidence is not a covert
  restart. Genuinely new peer input after the Stop boundary remains a separate,
  explicit policy decision, not an old delivery reclassified as new intent.
- Stop boundaries are currently code-authored as "The user stopped" even when
  a parent control or shutdown caused cancellation. Preserve exact actor/reason/
  run/generation attribution without inventing human intent or success.
- Raw provider auto-truncation can omit old remote context when explicitly enabled.
  This is not caused by Stop, and it does not remove local history. Do not confuse
  transport deltas, outgoing opaque-replay filtering or intentional completed
  compaction with cancellation-based context deletion.

## Audit inventory and existing protections

| Area | Current evidence / required handling |
|---|---|
| Human Stop, direct root cancellation, hard cancel | Exact generation cancellation exists; prompt ownership before preflight and inbox settlement do not. Hard cancel shares the same root/child losses. Preserve submitted content before publishing terminal execution state. |
| Main-agent `agent_control` cancel/pause | Exact run/version/epoch validation prevents stale controls from stopping a replacement. Context admission before signalling uses a competing snapshot; the child fixture fails. Pause must suspend execution/admission policy, not erase published content. Resume/reopen restores availability, never replays an old stack. |
| Root Stop while preflighting | Fails at before-turn, account and history gates. Workspace human control also relies on a collaboration run that may not exist yet; cancellation must reach the exact execution generation without making local input ownership depend on run-start success. |
| Request uploading/provider wait | Already-admitted human input survives in the new full session journey and existing tests. Queued sending notes fail. Transport cancellation must never decide local message lifetime. Internal upload coalescing is not human Stop and already has separate cause handling. |
| Output/thinking/partial tools | Normal partial assistant output is preserved by existing backend tests. New UI-shield and mixed reasoning/tool fixtures fail. Preserve received semantic content; archive non-executable partial-call evidence separately from protocol-valid replay. |
| Tools, parallel/native batches | Existing complete tests preserve real call IDs, one outcome per completed call, finished siblings and unknown crash outcomes; not-yet-started effects do not execute. Retain actual late results without reactivating the loop. No completed effect is rolled back by Stop. |
| Questions/permissions | Existing distinction between Esc Stop, explicit denial and validation failure must remain. The ask/tool envelope and any accepted answer/result remain context; an unsubmitted write-in stays a draft. No cancellation can expand grants or fabricate a denial. |
| Root/child recovery settlement | Open recovery checkpoints retain pending work; resume is passive. Parked Stop, newer snapshots and supervisor mirrors fail the revised tests. Preservation must succeed before cancelled recovery is treated as disposable. Automatic recovery policy itself remains unchanged. |
| Child assignment, reports and outcome | Assignment is checkpointed before the child loop; accepted transcript/results/reports are retained in normal paths. Shared queued-human and UI shields apply to children too; main-agent control can clobber current child context. Missing report or storage failure is not completion. |
| Manual/automatic compaction | Existing cancellation tests retain the exact pre-compaction source and install no partial checkpoint. A Stop racing commit must serialize against input/control admission; cancelled extraction cannot overwrite a newer context, and a completed compaction remains authoritative if its commit won. |
| Close, terminate, workspace switch, shutdown | Tab close minimizes without termination. Explicit termination closes execution and detaches UI; it is not delete-history. Session teardown joins many runtime jobs, but before-receive input and empty-session pruning fail. Draft/preservation jobs must finish before DB close. |
| Resume/process takeover | Existing exact local collaboration receipts are reconciled without provider/tool/child replay. Unadmitted human notes currently are excluded from passive admission and later rejected for a stale run. Old execution IDs may change; durable message identity/owner may not. |
| Model/account/protocol/Responses changes | Complete affected suites pass canonical history re-projection and full local ownership. Interrupted requests may not become server-reference anchors; the next explicit request includes retained local deltas/full context as appropriate. No protocol switch justifies local deletion. |
| Local History and memory | Restore appends a neutral boundary, not reset history. Parked/control snapshot writers can overwrite it; separate transcript/context commits also need atomic publication. Memory mirrors must retain stopped attempts as evidence without promoting them to success/human intent. |
| Shell/floating/skill input and media | Other audit findings remain: rejection before local send must retain the draft; selected MCP payloads are omitted; missing refs/read errors can silently weaken content. Preserve exact submitted attachment/source identities even if preparation stops; repair can block execution without deleting the input. |

Some existing tests enforce the rejected design, including
`TestCancelledPendingSteeringReturnsToComposerAndIsIdempotent`,
`TestRejectedSteeringReturnsToOwningComposerWithoutLosingNewDraft`,
`TestCancellingAgentIgnoresQueuedNonTerminalEvents`, and
`TestCancelSettlesQueuedToolResultsWithoutReplayingAnimation`. Revise only their
incorrect disposition/data-loss expectations; retain exact-owner, idempotence,
anti-reactivation and no-cross-session invariants.

## Small coherent architecture change

### 1. Local context admission is independent of execution

Use the existing session conversation/active-context authority, with durable
submission IDs and source revisions. A successful application send/published
human note means ownership of that exact input, irrespective of upload success.

Commit the original text, origin, attachment IDs/spans/bytes or immutable preimages,
and visibility before clearing/acknowledging a send. Expensive guidance/history/
account/media preparation may remain off-UI and may block execution; it cannot
prevent retaining the already-submitted original. Do not create a second transcript
or a return-draft queue as a substitute for context ownership.

Distinguish these few facts rather than a complex remote-acceptance state machine:

- Draft versus locally submitted/committed input.
- Provider/request execution phase and stop identity.
- Actual accepted output/tool outcome (including unknown effects).

The provider may never have seen the message. Retaining it locally must not claim
that it did; UI may show "saved to context" rather than indefinite "sending".

### 2. Stop has two independent responsibilities

First stop the exact generation's execution promptly: cancel its active transport,
prevent another provider iteration/new tool launch, and apply existing exact
child/command lifetime policy. Do not delay signalling just to paint or retry.

Then finish preserving data, without using the cancelled execution context as the
persistence lifetime:

1. Freeze an exact owner-scoped submission/delivery high-water mark so concurrent
   sends have a deterministic before/after Stop boundary.
2. Commit each already accepted/published input through that mark exactly once,
   including buffered notes, mailbox entries and messages not in the upload.
3. Preserve accepted output, reasoning, real tool results and control evidence;
   append one truthful, actor-attributed stopped boundary.
4. Commit transcript/context and terminal run/receipt facts together where they
   share a DB. Cross-DB receipt publication follows the existing exact local-commit
   protocol. Do not publish a durable-preservation success before it exists.
5. On storage failure, execution stays stopped; retain the latest snapshot/journal
   as actionable preservation-pending state. Retry only persistence, never effects,
   and never prune or accept later work that can overwrite uncommitted context.

Context-only settlement must not wake the loop or consume a new draft. Future
explicit user input/Continue uses retained context without replaying old tools.
New post-boundary peer input remains source-attributed and follows its own wake
policy; pre-boundary buffered evidence does not become a new task after Stop.

### 3. One context writer, not snapshot synchronization

Use one revision-checked writer for submission, runtime admission, recovery,
Stop, control audit, restore, compaction and finalization. A stale context payload
must fail its expected-revision check, not overwrite the row while copying the
new generation number onto old bytes.

Root/child Stop settles against the current durable/live authority. Append exact
uncommitted deltas by their IDs in source order; never select an old checkpoint as
the whole current context. An incompatible prefix fails closed and preserves both
versions for reconciliation. Shared storage transactions must include transcript
and context changes. Supervisor return, `Agent.ActiveContext` and recovery state
must not compete for authority during finalization.

### 4. Data admission is not animation

Replace the blanket cancellation shield with exact owner/generation sequencing:
real tokens/thinking/tool outcomes accepted by the backend still enter stopped
chat, but cannot set Running/Thinking/Streaming back to true or reopen prompts.
Stale events from another/replaced run remain rejected. Reconcile the immediate
terminal projection from the committed semantic boundary so live and resumed
chat agree without waiting for restart.

Incomplete tool arguments are never executed/replayed as a valid call. Retain
their raw evidence separately; preserve independent plain reasoning and text.
Do not parse encrypted reasoning or relax signed/opaque replay restrictions.

### 5. Teardown and drafts remain distinct

Drain/persist successful queued submissions before destroying the execution
service. Close/terminate/switch/restart must retain owned history and input even
when it has zero model replies. Prune only when absence of all context, drafts,
published input, references and unacknowledged work is proven.

Synchronous rejected sends never became context: keep those exact drafts intact.
Explicit Clear input/Cut remain editor operations, not Stop. The current Esc ladder
clears an unsubmitted draft before it stops execution; it must not be described as
context cancellation, and a direct Stop action must leave a newer draft alone.
Improve per-owner draft persistence/epoch/error fencing from the prior audit,
without reintroducing automatic submitted-message returns.

## Required regression and acceptance matrix

- [ ] New send and sending note: Stop before receive, every preflight gate,
  guidance/media preparation, 0/50/90/95/100% upload, provider wait, reasoning,
  output, final completion and request-coalescing race. Assert exact-once input in
  immediate chat, durable transcript/current context and next real provider request.
- [ ] User root/child Stop, parent cancel/pause, hard cancel, same-generation
  duplicate Stop, stale controls, multiple notes/recipients and active siblings.
  Preserve exact actor/provenance and do not affect replacement/unrelated runs.
- [ ] Pending published human/peer/report/control evidence, before/after the Stop
  fence, storage fault and crash between local receipt and workspace publication.
  No old wake/replay and no fake human attribution.
- [ ] Partial thinking plus incomplete tool arguments; serial and native parallel
  tools, real result arriving during Stop, uncertain effects, permission/question
  interruption. Retain evidence, suppress new effects/animation, never fabricate
  cancellation of a completed operation or rollback committed files.
- [ ] Park then restore/append evidence/compact; Retry, Stop, passive resume and
  parent controls cannot restore an older active context. Exact source archive and
  prefix identity remain verifiable.
- [ ] Close/minimize/terminate, A-B-A session/workspace switch, immediate shutdown,
  process crash and passive restart with zero provider/tool/child replay. No
  accepted queued-input loss or empty-session deletion.
- [ ] Full text/whitespace, references/media/capability data, new composer drafts,
  cursor/selection/undo/history survive unchanged by Stop. Rejection before send
  keeps the draft; submission after a Stop fence creates a new explicit intent.
- [ ] Build, full affected/repository suites, race/cross-platform checks,
  documentation/lint/performance gates and readable/constrained native Windows
  rendered acceptance. Immediate, settled and resumed chat must agree.

## Implementation order and boundaries

1. Establish durable local input identity/admission and the single revision-owned
   context writer; do not couple ownership to provider success or cancellable setup.
2. Make all root/child/user/parent Stop paths settle queued context passively;
   remove execution-ended rejection/return/removal paths, not hide them in fallbacks.
3. Unify finalization/context mirrors and late semantic data admission; preserve
   incomplete-call evidence safely and fix actor attribution.
4. Protect teardown/pruning and selected-reference encoding/draft persistence.
5. Promote new repros to permanent correct regression coverage, replace contrary
   tests, update current references/prompt wording only after implementation, and
   remove this active plan when the verified contract lands.

Any persisted-contract change requires backups and explicit evidence-preserving
manual local-data alignment outside product code. No automatic legacy reader,
historical-effect replay or guessed transcript merge. Ezra's stopped prompt is
historical evidence; do not modify or resend it as part of the audit.
