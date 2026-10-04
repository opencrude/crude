# Workspace Collaboration Runtime — Coherent Replacement Plan

> Status: IN PROGRESS (2026-10-03) — end-state implementation specification; one integrated cutover, not a phased rollout. Product implementation has not started.

## 1. Decision And Completion Boundary

Replace session-local board routing, root-only BTW delivery, child-only report
handoffs, and separate completion-wake bookkeeping with one workspace collaboration
runtime. Every workspace agent is discoverable and addressable, independent of
session, parentage, selected tab, or whether its tab is minimized.

This is **one implementation with one acceptance boundary**. The sections below
divide responsibilities, not releases. Internal commits can follow dependency
order, but no partial board, temporary adapter, dual delivery path, or disabled
"new runtime" switch is an acceptable shipped result. Deletion, data preservation,
UX, recovery, performance, and proof are part of the implementation.

The governing distinction is:

**Universal reach; explicit authority; selective attention.**

Generalize communication, requests, reports, outcomes, receipts, waits, controls,
and their identity/attribution. Do not turn the board into the authority for
permissions, model configuration, filesystem transactions, accounting, or plans.
One coherent system is not one giant object that owns every domain.

The user approved the architectural direction and requested this plan, not product
implementation in this planning task. Proposed guarantees below are requirements
to prove, not claims that the current product already satisfies them.

## 2. Verified Starting Point

Inspected source on 2026-10-03; these are architectural observations, not newly
reproduced production bugs.

| Current owner/path | Observed behavior | Replacement obligation |
|---|---|---|
| `workspace/capabilities.go:bindSessionCapabilities` | Creates `session:<id>` boards; collaboration is attached alongside capabilities | Workspace identity and collaboration lifetime independent of capability setup |
| `workspace/capabilities.go:JoinCollaboration` | Rebinds selected available roots to one board | Automatic workspace directory; threads/groups are organization, not membership gates |
| `session/capabilities.go:bindAgentRuntime` | Captures board identity/store, adds polling notices and board tool handlers | Stable runtime service reference and typed directed delivery |
| `session/capabilities.go:bindBoardAdmission` | Saves context, then separately accepts a board cursor; pending state is keyed by tool call | Durable per-event admission and recoverable acknowledgement, independent of how content arrived |
| `board/model.go`, `board/read.go` | Board-global sequence/cursor, active/finished/unknown roster, fixed unread notice | Separate participant/run identity, liveness, availability, inbox receipts and browse cursors |
| `workspace/bootstrap.go:SubmitAgentInput/SubmitAgentBtw` | Reject child targets; inputs go through the owning root | Same user input/control API for every agent |
| `session/orchestrator.go`, `session/run.go` | BTW queue/ownership, runtime wake queue/consumed markers, and main input have different paths | One ingress/admission contract with distinct typed origins |
| `prompt/runtime.go:BTWInterruptionPrefix` | Labels BTW as an urgent user interruption | Human-only authority; peer events can never use that wrapper |
| `session/subagent.go`, `agent/subagent_types.go` | Child lifecycle/report/result delivery separate from root execution | One actor/run/finalization contract; parent is a relationship, not a second runtime species |
| `session/subagent.go:WriteReport` | Writes the same `<agent>-report.md` pathname; handle retains the pathname | Immutable revisions, durable publication and optional derived exports |
| `session/subagent.go:finalizeSubAgent` | Persists, emits a UI completion, sends ResultCh, queues a wake, then marks the manager finished | One durable outcome with multiple idempotent observers |
| `session/subagent.go:buildResultFromMessages` | Includes quota/auth classification from tool/assistant text | Typed execution outcomes; narrative words cannot determine terminal status |
| `workspace/agent_manager.go` | UI-goroutine forest describes itself as agent existence authority | Presentation index over durable runtime identity; selection/visibility remain local UI state |
| `agent/file_ownership.go` | Process-local claim map, separate from `fileio` mutation serialization | Workspace-durable claim authority; execution-time checks and cross-process native mutation exclusion |
| `fileio/mutation_lease.go` | Mutation lease is process-local | Extend this same low-level owner for supported local multi-process use; do not add a competing lock |
| `sessiondb/turn_state.go` | Local turn lifecycle tied to session/agent generations | Stable run IDs; one terminal-outcome authority and retained human-turn attribution |
| `tui/viewmodel/structured_projection_test.go` | Existing semantic tool live/SQLite parity coverage | Extend through actual communication admission, controls, reports, cancellation and recovery |

The [live board assessment](shared-board-live-assessment.md) established useful
messaging but exposed polling overhead, ambiguous receipt claims, parallel-post
ordering, incomplete thread linkage, and inaccurate self-reported counts. Its
cooperative two-child experiment is not sufficient acceptance for this design.

## 3. Authoritative Architecture

```text
human UI / agent tools / runtime events
                   |
           typed authenticated ingress
                   |
     workspace-owned CollaborationService
       /        |          |           \
 directory   journal    requests     artifacts
             inboxes    + controls   + reports
                   |
       per-agent serialized runtime owner
       /               |                 \
 input admission   provider/tool loop   finalization
       \               |                 /
          durable context + outbox
                   |
        canonical conversation projections
                   |
     chat / Board / agent details / Notices
```

### Ownership table

| Fact or responsibility | Sole authority |
|---|---|
| Workspace attachment, service start/stop, UI-facing domain methods | `workspace/` composition root |
| Agent identity, run lifecycle, request revision, control operation, delivery journal | New neutral `collaboration/` domain service/store |
| Provider/tool execution and safe points for one agent | One `session.AgentRuntime`, using the existing `agent.Agent` loop |
| Immutable accepted transcript, active context, compaction, admission records | That agent's `sessiondb` context store |
| Report content, immutable artifact revisions, publication relationships | `collaboration` artifact/report service |
| Permission decisions, exact approval scope, YOLO and role ceilings | Existing safety/reviewer/configuration owners |
| File/resource claims between agents | Workspace collaboration claim store |
| Actual native filesystem commit exclusion, Local History and undo receipts | Existing `fileio` and Local History owners |
| Execution-derived usage/cost | Existing accounting owner; never report prose |
| Visible plan banner | Existing `plan.Store`; not a scheduler or task ownership database |
| Durable knowledge/feedback | Existing memory store; peer claims do not automatically become verified memory |
| Conversation semantic state | Existing canonical conversation model, consuming versioned collaboration events |
| Cell rendering/hit testing/copy/disclosure | Existing shared TUI projection/components |
| UI selection, tabs, minimized state, draft cursor | TUI presentation state, never agent lifetime |

`collaboration/` has no TUI imports. Sessions receive neutral typed service
contracts, not a workspace callback bundle. TUI calls workspace methods and
consumes domain events. The existing UI event bus remains a presentation
notification mechanism, not durable agent delivery.

There is no external message broker, generic workflow engine, or second database
framework dependency in this design.

## 4. Identity And State Model

### Persistent identities

- `WorkspaceID`: exact registered project identity, resolved before routing.
- `AgentID`: stable participant identity across turns, restart and tab changes.
- `RuntimeEpoch`: new execution incarnation; never inferred from PID or nickname.
- `RunID`: immutable execution identity, separate from participant lifetime.
- `RequestID` + revision: accepted collaboration/assignment intent.
- `EventID`, `DeliveryID`, `ThreadID`, `ArtifactID` + revision: exact references.
- `ContextCommitID` + context generation: what was durably admitted or finalized.

Names are labels. Parent/root/session IDs are relationships. Never parse `_orch_`
or use the currently selected tab to decide runtime ownership. Reuse established
stable identities during explicit local data alignment rather than changing
identities merely because a schema changes.

### Orthogonal states

Do not overload one `completed`/`active` boolean:

- Participant: available, suspended, closed.
- Runtime presence: attached, offline, unresponsive/unknown.
- Run: queued, running, waiting-for-human, waiting-for-event, pausing, paused,
  stopping, succeeded, failed, cancelled, interrupted.
- Deliverable: not-required, missing, published, invalid/incomplete.
- Delivery: pending, admitting, admitted, superseded, withdrawn-before-admission,
  rejected, expired.
- Request disposition: pending, accepted, deferred, declined, superseded.

Terminal run outcomes are immutable. A later follow-up creates a new run for the
same agent. A report can exist for a failed or cancelled run. Process exit,
delivery admission and task-quality verification are not interchangeable.

A normal completed child can remain available for follow-up without retaining
a provider request or spinning model loop. Its original run stays completed.
Explicit stop/suspension prevents unsolicited wakes; reopening history is passive.

## 5. Workspace Scope And Process Ownership

All workspace roots and children register automatically. Any participant may
query and communicate with any other, subject to its communication tool policy.
No session membership switch or "main" singleton is needed. `parent` may be
resolved explicitly from the sender's relationship, never from the UI.

The directory supports bounded search by identity, role, task, session, availability
and owned resource. Default results show relevant/live participants; closed
history remains queryable. Do not inject the whole directory into every request.

Support multiple Crude processes on the **same host, same user data directory,
local filesystem**. SQLite WAL is not a multi-host coordination system.

- One runtime owner holds a process-lifetime OS advisory lock for each attached
  agent/context, with platform-specific Unix/Windows implementations.
- A monotonic epoch fences runtime publications and controls in the database.
- Heartbeats report liveness, but missed heartbeats never authorize a second
  writer to steal an agent from a paused-but-living process.
- Stale publication/control attempts fail closed. Recovery can attach only after
  exclusive ownership is established; it reconciles durable terminal intents
  before marking remaining unfinished runs interrupted.
- Per-process background inbox followers read indexed pending deliveries for
  locally attached actors. An in-process signal reduces latency; a bounded
  periodic database check covers other processes and lost signals. The durable
  queue is authoritative. Models do not poll, sleep, or pay provider calls for it.
- A different process can message or issue an authorized control request to the
  owner; it cannot independently operate the owner's provider, PTY or process.

No remote agents, network filesystem coordination, or distributed exactly-once
execution is implied. A workspace switch detaches only that process's actors;
it must not cancel actors owned by another process.

## 6. Typed Communication And Authority

All ingress creates a typed envelope with runtime-derived sender/origin,
workspace, target, event ID, causation/correlation, thread, request/run scope,
revision preconditions, body/parts or immutable artifact references, and delivery
policy. Model arguments cannot forge the human/runtime origin or raise priority.

The closed event kinds cover:

| Kind | Intended consumer | Authority |
|---|---|---|
| Human message/steering | Target agent loop | Genuine human input; precise run targeting |
| Peer note/reply | Target agent loop | Attributed information, not user intent |
| Work request/revision/disposition | Target agent plus request service | Bounded cooperation; acceptance does not grant tools/files |
| Report publication/revision | Subscribed recipients and Board | Authored evidence with immutable provenance |
| Run outcome | Runtime and subscribed recipients | Code-authored lifecycle fact |
| Permission/question request or response | Existing exact decision owner | Normal safety/human decision contract |
| Authorized lifecycle control | Runtime control lane | Validated operation, not natural-language instruction |
| Relevant command/job completion | Interested agent/runtime | References the existing command owner and exact handle |

The protocol is shared; semantic handling is not erased. Permission replies and
cancel commands go directly to their typed owner, not through an LLM interpretation
of prose. Credentials, raw reviewer-private input and masked fields are not
published into a workspace-readable discussion.

Workspace board discussions are shared, not confidential DMs. Every normal
participant can inspect discussion/report content in the workspace. Actual
artifact retrieval still applies the recipient's normal read/tool/media policy;
a reference is not an access grant. Provider opaque replay, secrets, unsubmitted
drafts and private reasoning are not exported by "inspect agent."

Human instructions outrank peer requests. A peer can ask for changed work;
conflicting work is deferred/declined unless an authorized owner revises it.
This does not require a human confirmation for every harmless peer exchange.
Permissions continue to use their existing authority and review policy.

## 7. Durable Publication And Admission

### Workspace database

The collaboration store owns current-schema tables for participants/incarnations,
runs/outcomes, requests/revisions, journal events, delivery obligations,
controls, report/artifact metadata, resource claims, human read positions and
query indexes. Indexes/materialized views are rebuildable projections, not second
writers of domain truth.

Posting an event, allocating sequence identity and creating its recipient
obligations happen in one transaction. Recipient sets are snapshotted for
broadcasts; later agents do not inherit old delivery obligations automatically.
An idempotency key plus payload digest makes unchanged retries return the same
result and changed retries fail.

### Per-agent context database

Agent context stores retain exact transcript/context and:

- Admission records keyed by `(AgentID, DeliveryID)`, surviving compaction.
- Durable context-commit markers, with run/epoch identity.
- A transactional outbox for publication/receipt/finalization commands.
- Persisted semantic conversation events needed for local chat reconstruction.

No cross-database atomic commit is assumed. SQLite WAL does not make an
`ATTACH` transaction atomically commit multiple databases as a set.

### Incoming delivery

1. The runtime selects eligible addressed deliveries under priority, causality
   and context budget. It validates the target run, epoch and request revision,
   then atomically reserves the exact delivery version as `admitting`.
2. It builds an attributed context input. Small directed messages include their
   useful body; large artifacts carry a useful summary and exact retrieval ID.
3. It commits context, admission identity and receipt-outbox entry atomically in
   the agent database. Failure admits nothing and triggers no new task effects.
4. It publishes the receipt idempotently to the workspace store. A crash between
   these steps replays the receipt, not the message or tool action.
5. A subsequent provider request records which admitted inputs it includes.
   This is evidence of request inclusion, not comprehension or agreement.

The context store is the admission authority; the workspace receipt is its
recoverable published projection. Browsing the Board or reading another agent's
state never claims delivery to that agent. Human unread stays independent.

Reservation is necessary because a workspace receipt can lag the actual context
commit. Edit/withdraw wins only while the delivery is `pending`; `admitting`
returns "admission in progress," not a false successful retraction. Editing
creates a new immutable event superseding the unreserved version, never mutates
published bytes. Broadcast recipients have independent obligations, so a revision
cannot pretend to retract bytes already admitted by one recipient.

After owner loss, recovery inspects the exact local admission record: if present,
publish its receipt; if absent, release/reject the reservation according to
current target policy. Never expire a live owner's reservation and admit a second
copy based only on elapsed time. A provider request is dispatched only after
local context commit; no claim of exactly-once external effects is made.

Admission is by event ID, not a single global "everything below N was read" cursor.
Browse pagination uses a query-bound snapshot/high-water token and keyset order.
Filtered reads cannot acknowledge unrelated inbox items or skip concurrent posts.

## 8. Execution, Steering, Waiting And Backpressure

Root and child paths use one `AgentRuntime` input loop and finalizer. Parentage
affects default routing, policy inheritance and cancellation ownership, not
message transport or the ability to address an agent.

| Recipient condition | Required behavior |
|---|---|
| Running provider/tools | Queue and admit at a safe boundary; no stochastic request replay after accepted output |
| Upload eligible for human steering | Preserve the existing at-most-90% restart rule before acceptance/output; coalesce ready human inputs |
| Routine peer message during upload | Do not restart upload; next safe admission boundary |
| Available idle actor | Directed actionable request or awaited result may start a peer/runtime-origin run under wake policy |
| Waiting for permission/question | Keep decision ownership intact; control remains responsive, peer input cannot answer it |
| Waiting for an event | Runtime suspension, not repeated model calls; matching delivery/human steering/control resumes the correct path |
| Paused/suspended | Keep durable inbox; no peer-triggered execution |
| Closed/offline | Retain/query message with explicit pending-unavailable status; no implicit process restart |
| Targeted run already terminal | Reject stale steering or explicitly offer new follow-up work; never silently retarget |

User input offers explicit **Send**, **Steer current run**, and **Queue follow-up**
semantics. A rejected or cancelled pre-admission input remains recoverable as an
unsent item bound to its owner. It never overwrites a newer draft or jumps sessions.
Admitted text is not erased from accepted context; corrections append superseding
facts.

Pause means stop admitting new model/tool work at a safe boundary. Already-started
tools are observed to settlement unless explicitly cancelled through their owner;
show `pausing` until that boundary is reached. Cancel commits the stop decision,
cancels owned in-flight work, and records uncertainty/partial results honestly.
It never means arbitrary process suspension or speculative rollback.

There is one serialized actor lane, so control and human input cannot race a
second provider turn. Control has a reserved execution lane independent of a
full model-context queue.

Control submission returns an operation identity and `requested`, not "stopped."
The owner serializes its effective transition with admission/finalization and
publishes `applied`, `already-settled`, or `rejected` against an expected run
version. Effective stop versus terminal publication is a compare-and-swap
decision: a completed run is never retroactively cancelled; a committed stop
cannot later publish success. The runtime does not ask an LLM to settle that race.

### Attention and budget

- Human steering and exact control take priority, with fairness for required
  outcomes/decisions; unrelated routine progress cannot starve either.
- Targeted content and subscribed request outcomes are pushed automatically.
  Ambient board discussion is queryable, not broadcast into every context.
- Only replaceable progress notifications may coalesce. Exact messages, human
  input, outcomes, approvals and report revisions are never silently dropped.
- Receipt updates do not wake models. No ACK-of-ACK loop, automatic response to
  every INFO, or broadcast wake cascade.
- Work requests have explicit recipients, purpose/scope and a wake budget.
  Policies distinguish active-only delivery from idle actionable wakes. Budget
  exhaustion produces an explicit deferred state; it never kills legitimate
  unrelated long-running work.
- Replies inherit causal/thread identity. Repeated unproductive request cycles
  become a visible deferred conflict rather than an unbounded ping-pong.
- Context admission uses the existing pressure/compaction authority. Pending
  input is durable and visible when it does not fit; it is not silently clipped.
- Settings expose admission/wake budgets and queue health. Hard origin,
  permission and exact-target invariants are not disableable checkboxes.

## 9. Reports Are First-Class Deliverables

This is not `write_report` with an extra recipient field.

A report is an immutable, versioned artifact with:

- Report/artifact identity and exact revision/hash.
- Author AgentID, RuntimeEpoch, RunID and originating request revision.
- Authored summary, findings, changed paths, verification evidence and remaining
  blockers/limitations.
- References to real tool/test/artifact evidence, with provenance and availability.
- Explicit final/interim designation and supersession relationship.

Runtime facts such as elapsed time, status, tool counts and costs are attached
from their authoritative owners, never extracted from the author's narrative.
Unsupported claims remain authored claims; cited successful checks are validated
against their exact evidence where available. Publication is not proof of task
quality.

Provider/tool outcomes must be typed all the way to finalization. Words such as
"quota," "cancelled" or "authentication" inside a quoted result or assistant report
cannot classify the run. A recovered tool error also does not automatically
invalidate a subsequently successful run; terminal cause comes from execution.

### Publication and delivery

- Any eligible agent can publish a report, not only a child.
- One publication stores the immutable body and journal event. Recipients receive
  a useful summary plus the report ID; they need not reread a generated local
  pathname merely to learn the result.
- Reports can answer a request, update an earlier finding, or provide interim
  evidence. Thread/request routing is attached automatically and validated.
- Publishing, subscribing, waiting and direct inspection all reference the same
  report/outcome identity. None creates a second authored summary or delivery
  contract.
- Optional Markdown export is a derived artifact, not the only source or delivery
  authority. A missing export file cannot erase a published report.
- Reports/media use the shared supported content/admission path; unsupported
  modality is reported explicitly, not downgraded into raw binary text.

### Finalization protocol

1. Establish the run's actual terminal cause, including cancellation, provider
   failure and outstanding effects. A report saying "success" cannot override it.
2. Commit final transcript/context plus an idempotent terminal publication intent
   in the agent database. This produces the exact `ContextCommitID`.
3. Make immutable report blobs durable before referencing them. Publish required
   report metadata, run outcome and recipient obligations in one workspace
   transaction, keyed by RunID/epoch/commit identity.
4. Only then expose durable completion to waiters/observers and release run
   ownership. A crash after step 2 finishes publication through the outbox without
   repeating provider/tool work. Failure to persist never becomes success.

Recovery of a committed outbox intent is not a fresh publication from a stale
worker. The new exclusive owner validates its original producing epoch and
ContextCommitID, preserves that attribution and replays it idempotently through a
dedicated recovery operation. Arbitrary callers cannot use this exception to
publish under an old epoch. Reconcile these intents before synthesizing an
interrupted outcome, or recovery itself would overwrite completed work.

For a normal human conversation, the final assistant answer can be stored as an
answer artifact without an extra tool call or duplicate narration. A delegated
request can require a structured final report. Missing that required deliverable
is a contract failure, not a fabricated diagnostic report marked successful.
An intermediate report never consumes or suppresses the later terminal outcome.

A crashed/cancelled run preserves already-published partial reports. Later report
corrections are new revisions, never silent edits to what another agent accepted.
Final run outcomes do not change when a report is corrected.

Awaiting completion observes `(RunID, outcome version)`; report browsing observes
`(ArtifactID, revision)`. Reading an early report cannot swallow a terminal event.
This replaces `reportsConsumed`-style actor-level suppression.

## 10. Model And Human Operation Surface

Use a small explicit family. The names below are the target interface, not
compatibility aliases for old handlers:

| Operation | Contract |
|---|---|
| `agent_list` | Bounded workspace directory queries; exact IDs and availability |
| `agent_inspect` | Exact agent/run state, requests, published artifacts and permitted capabilities; no secret/raw-context dump |
| `agent_start` | Create participant/assignment under effective role, account, capability and resource limits; returns exact AgentID/RunID |
| `agent_send` | Typed note, work request/revision or reply; target exact identities, inherit thread, validate revision preconditions |
| `agent_respond` | Accept, defer or decline an exact work-request revision; validates disposition and resource scope atomically |
| `agent_control` | Pause/resume/cancel/reopen exact target with validated authority and expected generation |
| `publish_report` | Publish an immutable authored report/revision linked to the actual run/request |
| `artifact_read` | Retrieve exact report/evidence revision with bounded sections/media admission and full-content access |
| `agent_wait` | Park on explicit run/request/event predicates; human/control input can interrupt the wait, no model polling loop |
| `board_read` | Workspace history/thread/search query with stable pagination; not required for ordinary delivery |

Ten focused operations replace the fragmented family; do not grow a universal
`agent_action` JSON blob with arbitrary mode flags. Each union has a closed,
validated schema. Runtime delivery/receipts require no model tool calls.

`run_sub_agent`, `poll_sub_agent`, `list_sub_agents`, `cancel_sub_agent`,
`write_report`, and `board_post` are removed from offered schemas, prompt text,
dispatch, summary/title logic and tests. The new `board_read` name retains the
user's Board concept, but its implementation/schema is the new workspace query
contract, not an old path wrapped by a new facade.

Tool selection/effect metadata and provider-native schemas must cover the complete
family. Publishing one's report is constrained communication, not arbitrary file
write access. Starting/controlling agents remains an execution effect. Model
selection does not widen policy, depth, spending, capability or human-only grants.

The human UI can target every actor and exercise the corresponding supported
controls. Peers may request collaboration everywhere; cross-agent destructive
control requires ownership or an explicit grant. Human grants are scope-bound
records from the real approval path, never board text.

## 11. Resource Ownership And Adjacent Domain Integration

Resource claims are durable workspace records bound to request/run identity and
revision, with atomic conflict checks for normalized overlapping paths.
Accepting work or handing it to a child does not duplicate a claim in another map.
Transfer/release names the exact current claim and happens only after in-flight
native mutations settle. Conflicts return owner/request details suitable for
direct collaboration, not a generic failure loop.

Keep assignment claims separate from short-lived filesystem commit exclusion:
they solve different problems. Extend `fileio`'s one mutation service to
cross-process native commit exclusion using the same protected data root/OS lock
contract. Preserve canonical external-path mutation receipts and Local History
recovery. A claim does not bypass dirty buffers, preview preconditions or undo.

Opaque shell commands/external MCP writers are not magically sandboxed by a
path-claim table. Unknown mutating effects remain conservatively reviewed and
serialized where owned; effects that cannot be fenced are declared unconfined.
Do not promise safe concurrent overlapping edits through such a tool.

Other domains integrate by exact typed facts, not by giving up their authority:

- Permission/question lifecycle: shared routing, existing validation/decisions;
  exact owner/run/request IDs on response, stale response rejected.
- Background commands: subscribe to exact handle/start identity, never process
  names; no output cursor advances merely because a notification is delivered.
- Plans: display current owner plan and request links; no dependency scheduler
  hidden in plan statuses.
- Accounting: recipient execution charges its actual producing connection; a
  peer message cannot silently replace a selected account/model.
- Memory: retain message/report origin and evidence lineage; never treat peer
  repetition, delivery or context exposure as verified usefulness.
- Capability settings: collaboration has its own lifetime; capability grants and
  policy ceilings remain enforced at definition and execution time.
- Notices: unread user attention is a projection of typed events, not another
  copy of agent delivery state; a resolved permission is not still "Needs input."

## 12. Conversation, Board And UI Contract

The existing Board footer control opens the retained workspace bottom panel.
No second floating inbox, duplicate agent rail, or replacement settings design.
The panel supports workspace threads, requests, reports and participants using
shared tabs/search/list/detail primitives. Agent selection and filtering never
change delivery ownership.

Required interactions:

- Find any agent, inspect actual state, send directly, and jump to its tab.
- Queue/steer/follow up explicitly; show exact destination and expected run.
- Inspect delivery state separately from disposition and run outcome.
- Edit/withdraw only an unreserved pending item with version checks; once
  admission begins, show that fact and offer a superseding correction.
- Pause/cancel/resume the selected exact target, with accurate pending states.
- Read/copy/export reports, inspect revisions and evidence, reply in-thread.
- Recover a rejected/unsent message without overwriting a newer draft.
- Manage wake policy and inspect deferred/backpressured work.
- Work at narrow widths through shared overflow, not hidden-only mouse actions.

Async loads/actions carry workspace, actor, open-epoch and request generation.
No SQLite/filesystem/provider work in Paint or Update. Incoming communication
does not steal focus, selected tabs, capture, attachment state or composer undo.
Closing an agent tab still minimizes; closing the Board still hides its panel.

### Exact live/hydration projection

One versioned semantic conversation event describes each message/report/control
and its immutable identity. Persistence records the same data consumed by the
live reducer. Live and SQLite replay enter the same projection builders.

- Render meaningful sender → recipient, kind, readable content, receipt and
  evidence; no raw JSON or arbitrary ellipsis budgets.
- A tool publication and its published message share a causal identity: the
  sender's transcript has one communication item with tool evidence attached,
  not independent duplicate content cards.
- Recipient transcript shows its admitted peer content once. Queued-but-unadmitted
  content lives in the pending UI, not falsely in accepted conversation history.
- Receipt/outcome revisions update that item using explicit events, not snapshots
  guessed from whichever runtime is currently selected.
- Board history, chat, tool leaves and compact agent views reuse the same semantic
  body/title/attribution contract and expose full evidence.
- Same event state + width + theme + presentation state/clock means identical
  cell rows, copy text, disclosure, links and pointer hit targets.
- Restart reconciliation appends explicit interrupted/offline facts; hydration
  never silently recolors a historical successful run as cancelled.
- Shared terminal width, animation, scrolling and cursor contracts remain intact.

## 13. Recovery, Compaction, Retention And Shutdown

- Resume restores participants, contexts, inbox state and artifacts without
  provider calls, command replay or implicit child launches.
- Inbox admission IDs survive context compaction. The checkpoint carries pending
  requests, report references and receipt lineage through the typed continuity
  snapshot; it does not replay completed human input as a new request.
- Compaction failure leaves the prior context and unacknowledged deliveries
  authoritative. Successful compaction and its local admission lineage commit
  together; shared receipts recover through the same outbox.
- Account/model/protocol switches preserve origin, identities and request scope;
  adapters encode them under existing opaque replay/prefix rules.
- Workspace switch stops local admission, settles/cancels owned work explicitly,
  commits context/publication intents, joins delivery workers, releases actor
  locks, then closes stores. Stale callbacks cannot target the replacement project.
- Human cancel disables unsolicited continuation of that run. Unadmitted
  run-scoped items become rejected/superseded with reason; participant-scoped
  follow-up remains pending according to explicit policy.
- Report blobs are staged and atomically committed before metadata references
  them. An unreferenced staged blob may be reclaimed; a referenced blob cannot
  be pruned while any retained report/transcript/delivery still needs it.
- Deleting a session cannot orphan shared report evidence. Retain referenced
  artifacts or refuse deletion with an exact recoverable-data preview.
- Backups must include consistent SQLite/WAL state and referenced blobs. Do not
  copy a live `.db` alone and call it a complete backup.

## 14. Mandatory Replacement And Deletion Inventory

The implementation is incomplete while any old authority below remains active:

| Remove/replace | New authoritative path | Must preserve |
|---|---|---|
| `board/` membership-bound runtime model, `BoardID` session binding, `JoinCollaboration` | `collaboration` workspace directory/journal/query service | Existing historical bodies, identities, provenance and threads in manual alignment |
| `SetCapabilities` board/ownership bundling and per-turn board closure wiring | Separate typed runtime dependencies owned by workspace lifetime | Capability policy enforcement |
| `PendingNoticeText`, board polling notices, `boardPending`, board-only cursor admission callbacks | General durable event admission/outbox receipts | Failed admission consumes nothing |
| `btwCh`, `pendingBtw`, BTW forwarding goroutine and special consumed/cleared transport | General incoming-event owner and unsent-item state | Genuine human origin, media/draft recovery, cancellation generation |
| `BtwMessage`/BTW-only upload controller interfaces | Typed human-steering admission using shared request-local upload policy | The tested ≤90%/no-accepted-output invariants |
| `WakeSubAgentDone`, `pendingWakes`, runtime wake reinsertion, `reportsConsumed`/`IsAndClearReportConsumed` | Durable outcome subscriptions and delivery receipts | No lost completion, no double wake after synchronous observation |
| Child report pathname delivery, report-result fallback synthesis and child-only `write_report` | Versioned reports + terminal outcomes + optional exports | Partial evidence, explicit missing-report failure |
| `buildResultFromMessages` narrative-based terminal classification | Typed provider/tool/runtime cause | Recovered errors, quoted error text and real terminal failures remain distinguishable |
| Parallel root/child input/finalization semantics | One `AgentRuntime` parameterized by assignment/policy, not UI role | Parent relationships, ceilings, cancellation scope, current settings |
| Actor-level completion dedupe and ambiguous handle/time-derived status | Immutable RunID outcomes and runtime epochs | Exact attribution across follow-ups/restart |
| Root-only user submission guards and `_orch_` identity routing | Directory-backed exact target methods | Owner-correlated permission routing |
| Process-local `agent.FileOwnership` as domain authority | Durable workspace claim service | Path overlap checks, readable owners, native mutation/undo guarantees |
| UI agent forest claiming existence/lifecycle authority | Presentation index fed by runtime snapshots/events | Selection/minimize/tab ordering without backend effects |
| Old sub-agent tool schemas/dispatch/title/classifier/permission branches | Entire new operation family end to end | Full evidence, policy metadata, native provider encoding |
| Special BTW/wake/report hydration guesses and duplicate chat summaries | Canonical typed event reducers and retained snapshots | Exact live/hydration parity and human/runtime separation |
| Lifecycle state duplicated in local turn records and workspace runtime | Workspace run authority; local context commit/admission evidence | Latest pending/completed human request and monotonic generations |
| Automatic legacy readers/upgraders for the replaced board/actor/report contracts | Current-schema checks and explicit off-product data alignment | Fail closed without deleting existing work |

Search all consumers before removal, including tests, prompts, tool metadata,
provider adapters, title summaries, status views, memory extraction and docs.
Move shared test helpers instead of retaining old production code to make tests
compile. Legitimate domain boundaries are not "legacy"; delete obsolete owners,
not unrelated editor/LSP/terminal services.

## 15. Proof Obligations — One Release Gate

Tests must exercise real ingress, persistence, runtime and rendered output. A
unit test against a proposed helper or a model saying "done" is insufficient.

| ID | Required acceptance |
|---|---|
| A1 | Two independent root sessions and nested children discover/message each other without joining a board |
| A2 | Same-workspace separate processes deliver correctly; same-agent double ownership and stale epochs fail closed |
| A3 | No nickname, active-tab, session switch or parent-path confusion in message/control/permission routing |
| D1 | Directed short message arrives automatically; no `board_read`, `wait`, polling or manual ACK tool needed for transport |
| D2 | Duplicate publication/receipt retries are idempotent; changed retry payloads fail |
| D3 | Crash at every publication/admission/outbox/finalization boundary loses no accepted message and repeats no admitted input/tool effect |
| D3a | Withdraw/edit versus admission reservation has one winner; receipt lag cannot produce a falsely successful retraction |
| D4 | Filtered pagination with concurrent posts never skips query results or acknowledges unrelated inbox items |
| D5 | Human steering wins over peer requests; forged human origin/approval/priority is rejected by execution, not only model refusal |
| D6 | Upload restart preserves ≤90% boundary, coalescing and accounting; peer storms cause zero routine upload restarts |
| R1 | Any root/child publishes report; exact revision reaches all intended recipients and is readable without a report pathname |
| R2 | Interim report inspection cannot consume terminal outcome; sync wait/direct inspection/async arrival agree on one result |
| R3 | Report exists + later crash/cancel still shows crash/cancel; missing required final report cannot be success |
| R4 | Report correction preserves old accepted bytes; deleted session/export file does not lose retained evidence |
| R5 | Recovery publishes a durably committed final outcome before interrupted reconciliation; quoted quota/auth text cannot invent failure |
| C1 | Running, idle, paused, blocked, offline and closed recipients each follow the documented delivery/wake policy |
| C2 | Human controls remain responsive under full queues; stale control never reaches replacement run; STOP never auto-restarts |
| C3 | A pending decision can be answered only by its valid authority; peer cooperation never expands capability/file/model permissions |
| C4 | Request revisions conflict deterministically; parallel responses cannot both acquire one exclusive resource |
| C5 | Native overlapping mutations across supported processes preserve claims/commit/Local History semantics; opaque effects are not overstated |
| H1 | Passive resume does zero provider/tool work; compaction/model switches retain origin, delivery lineage and pending intent |
| H2 | Workspace switch/shutdown join the right workers, preserve pending data and do not stop another process's agents |
| U1 | Every new operation has live → SQLite → hydration cell/text/disclosure/hit-test parity, including failure, retry and interruption |
| U2 | Messages/reports/controls appear once with correct origin; sender and recipient identities/evidence are never lost |
| U3 | Keyboard/mouse/paste/copy/links work at 48/80/160-column fixtures; real 80×24 and 160×40 dark/light captures pass sequentially |
| U4 | Background delivery preserves exact drafts, references, selection, undo, focused tab and pointer capture |
| P1 | No per-model polling/wake-on-receipt loop; only targeted/required data enters context; human input is not starved under load |
| P2 | Real multi-agent coding tasks improve collaboration without unacceptable token/cost/latency overhead against a no-board control |
| X1 | Old symbols/schemas/handlers and compatibility branches absent; architecture boundaries and shared primitives respected |

### Performance evidence

Record baseline and repeated post-change measurements on the same hardware with:
2, 16 and 64 live participants; dense retained history (100,000 events), large
reports, concurrent senders, offline recipients and resumed contexts. The history
size is a benchmark workload, not a product membership cap.

Measure separately: durable publish, owner notification, next-safe-boundary
admission, provider request inclusion, human-visible frame update, CPU/allocation,
idle I/O and model input/output/cached tokens. Do not call provider generation
delay "board latency." Indexing must make an idle actor's work independent of
unrelated total history; large evidence remains lazy and fully accessible.

Provisional engineering targets to validate on the benchmark host: p95 durable
small-message publication under 50 ms; eligible idle-owner notification under
250 ms in-process and under one second cross-process; no lost messages at the
declared load; unchanged 16 ms active presentation gate and at least 2 FPS idle
UI cadence. These are proposed acceptance targets, not measured achievements.
If a target proves irreducible, document repeated measurements and architecture
cost rather than quietly weakening a gate.

Repeat the original live experiment with automatic delivery, then run realistic
cross-session tasks with human corrections, report revision, cancellation and
restart. Use multiple repetitions and record failures; do not infer reliability
or SOTA quality from one cooperative run.

### Required repository checks

Focused suites, full affected packages, race/fault-injection/multi-process tests,
`go build ./...`, `go test ./...`, `golangci-lint run ./...` (zero issues),
`make docs-check`, existing design/benchmark gates, supported platform builds,
and native platform lifecycle/interaction acceptance. Visual captures use the
repository workflow and all capture processes are closed.

Acceptance failures are reported and resolved, not reclassified as unrelated
when they concern this replacement. No claim of "proven" precedes this evidence.

## 16. Single-Cutover Execution Contract

Before editing implementation, freeze the exact typed contracts and ownership
table above into compile-checked interfaces and independent acceptance fixtures.
Implement the full replacement across store, runtime, tools, prompts, persistence
and TUI; remove superseded paths in the same integrated change. Internal work
ordering is dependency management, not a series of incomplete product releases.

For local data, stop all affected owners, take verified consistent backups, and
manually align it outside product code. Preserve messages, agent/run identities,
reports, original terminal causes, provenance, histories, model/account pins and
resume behavior. Never invent missing receipts or report revisions. Unknown
historical facts stay unknown. No converter, fallback reader or dual-format
switch remains in the repository.

Before acceptance:

- Reconcile every row in the replacement inventory with source searches and tests.
- Reopen aligned local data passively and verify content/identity hashes, evidence
  links and zero provider/tool execution.
- Update `AGENTS.md`, prompt architecture, orchestration, adaptive context,
  chat activity, interaction/control/settings/surface references, capability
  workflows and performance baselines to the one implemented contract.
- Mark relevant sections of prior board/capability plans superseded without
  deleting historical records. This plan becomes LANDED only after full proof.
- Review the integrated system for competing lifecycle/receipt/report authorities.
  Wrapping an old path does not count as removal.

## 17. Design References And Limits

- [Codex App Server](https://developers.openai.com/codex/app-server): explicit
  thread/turn/item identities, steering versus interrupt, typed lifecycle events.
  Borrow the separation, not its protocol implementation or experimental remote
  transport guarantees.
- [Transactional outbox](https://microservices.io/patterns/data/transactional-outbox.html):
  commit publication intent with source state and use idempotent consumers.
  This informs local SQLite boundaries; it does not require microservices.
- [NATS pull consumers](https://docs.nats.io/learn/jetstream/pull-consumers):
  consumer-owned flow control and acknowledgements rather than application-level
  busy loops. NATS is a reference, not a new dependency.
- [SQLite WAL](https://www.sqlite.org/wal.html): local-host concurrency, one-writer
  transactions and lack of cross-database atomicity constrain the design.
- Current Crude contracts:
  [prompt architecture](../reference/prompt-architecture.md),
  [orchestration](../reference/agent-orchestration.md),
  [adaptive context](../reference/adaptive-context.md),
  [chat activity](../reference/chat-activity.md),
  [terminal width](../reference/terminal-width.md).

These are established design patterns, not evidence that the proposed Crude
implementation already works. This plan intentionally excludes unrelated product
redesigns, a remote multi-host agent network, a universal job scheduler, and
security claims for unfenced external side effects. It includes every local
communication/report/control path necessary for the requested coherent outcome.
