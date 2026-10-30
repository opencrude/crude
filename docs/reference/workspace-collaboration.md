# Workspace Collaboration Runtime

> Status: Current reference (2026-10-05) — durable workspace-wide agent
> communication, delivery, reports, controls, claims, and recovery.

## Scope and ownership

`collaboration/` is the presentation-neutral workspace authority for:

- Stable agent identity and runtime epochs.
- Run lifecycle and versioned terminal outcomes.
- Typed peer/human/runtime events.
- Per-recipient delivery state and durable context admission.
- Work-request revisions and dispositions.
- Immutable report/artifact revisions.
- Workspace resource claims.
- Board history/search and human unread state.
- Same-host, same-user, same-data-directory process ownership.

It has no TUI imports. `workspace/` attaches the service and exposes UI-facing
methods. `session/` owns provider/tool execution and commits admitted events to
the exact agent context. The TUI renders neutral events and snapshots.

Permissions, model/account selection, plans, filesystem commits, Local History,
accounting, memory truth, and UI focus remain with their existing authorities.
Peer communication never widens them.

Collaboration borrows the current workspace's SQL handle; its lifetime must end
before that handle closes. A project switch validates and attaches the target
collaboration store before tearing down the current workspace. Old sessions detach
while their store is still usable, then its runtime locks are released and the old
database closes. The replacement store is published with the target database, so
new and resumed launcher sessions never register through the previous project.
A target attachment failure leaves the current workspace and sessions intact.

## Identity and lifecycle

Names are labels, never routing identities. All operations use exact stable IDs:

- `AgentID` identifies a workspace participant.
- `RuntimeEpoch` fences one attached execution owner.
- `RunID` identifies one immutable execution attempt.
- `RequestID` plus revision identifies collaboration intent.
- `EventID`, `DeliveryID`, `ThreadID`, and `ArtifactID` identify durable facts.

Participant state (`available`, `suspended`, `closed`), runtime presence
(`attached`, `offline`, `unresponsive`), run state, delivery state, request
disposition, and report revision are independent. A completed run is never
retroactively cancelled; a follow-up creates another run.

Each attached runtime holds an OS advisory lock. SQLite epochs reject a stale
process after takeover. Heartbeats indicate liveness but never authorize stealing
an agent while the process lock remains held.

A newly acquired owner lock advances the epoch even when the owner label is
reused. Attachment preserves suspended/closed choices; it does not silently make
an unavailable agent available. Child execution is bounded and leaf-only: roots
may launch children, while children may communicate with existing peers or ask
their parent for independent work. A finished child is a retained review surface,
not a runnable process waiting to be reopened.

## Model operation surface

| Tool | Contract |
|---|---|
| `agent_list` | Bounded name lookup with exact IDs, ownership, availability, presence and current run facts |
| `agent_inspect` | Read-only exact identity, current/latest run, trigger, bounded public recent work, report references, claims and recovery |
| `agent_start` | Start a bounded leaf; optional `profile` selects an exact enabled reusable profile whose category matches |
| `agent_send` | Directed note/reply/request/revision or rare running-agent broadcast |
| `agent_respond` | Accept, defer, or decline an exact request revision |
| `agent_control` | Request pause/resume/cancel/reopen for an exact agent/run version |
| `agent_narrow_claims` | Narrow or explicitly relinquish the caller's own active run claims, without ending the run; only `paths` is required, identities default to the current run and claims |
| `agent_configure` | Change a child's model, account or thinking effort; a running child switches at its next request, a stopped child keeps it for continuation; finished children need a new assignment |
| `agent_update_claims` | Replace a child's complete scope (wider or narrower) during its live run or after an interrupted attempt, under the exact parent; run version and claim versions default to the child's current state unless given as preimages; never starts execution |
| `publish_report` | Publish immutable authored evidence linked to the actual run/request |
| `artifact_read` | Read an exact artifact revision; revision zero selects latest |
| `agent_wait` | Wait for an exact run/report/agent condition without model polling |
| `board_read` | Targeted event/thread history with search, sender/recipient filters and stable snapshot pagination; not delivery transport |

Directed delivery is automatic. Models do not call `board_read`, `agent_wait`, or
sleep merely to notice an ordinary message, report, or terminal outcome.

### Passive inspection

Resolve an agent's exact name with `agent_list`, then call
`agent_inspect(agent_id)` with the returned stable ID. Presence describes runtime
attachment, not task progress. `run` is current only and is null when none exists;
`latest_run` is the most recent attempt, including finished attempts. Claims and
recovery remain separate evidence, not inferred from presence or report prose.

`trigger` exposes the recorded input/event identity, origin, sender and bounded
preview when known. `recent_work` exposes bounded latest persisted public assistant
text and tool names/statuses with recorded turn identity and explicit availability
and completeness. Missing evidence is explicit; neither surface reveals private
reasoning or raw provider context. Reports are bounded summaries and exact artifact
IDs/revisions for `latest_run`, not large bodies or stale reports from older runs.

Public work is a persisted tail, not live provider buffers. Root evidence reads
both runtime-owned input rows and session-owned output rows. It includes its
recorded turn/generation and a run link when recorded. A child's assignment
transcript can span continuation attempts, so it is labeled
`child_assignment_transcript` without a guessed attempt generation or run link.
Trigger attribution requires an exact input/admission link. Active human inputs
and initial child assignments can be visible before that link is recorded;
`trigger.available:false` reports this limitation, not an idle or failed run.
Ordinary active checkpoints are not presented as stopped recoveries.

Read the exact report revision with `artifact_read`; use `board_read` for a
targeted event/thread, `search_history` for older session work, and
`execution.review` for changes. Inspection never requires `session.open`,
`agent_send`, controls, waits or polling just to observe. Do not inspect SQLite or
logs when supported evidence suffices, and say what evidence is missing rather
than inventing progress, a trigger or a successful outcome.

Choose direct work when simpler. Inspect an existing peer's exact identity,
availability and ownership before reuse; otherwise delegate one independent,
explicitly owned assignment. Answer substantive requests, not every note or
outcome. Gratuitous acknowledgements create message loops rather than progress.

`agent_wait` checks **all** supplied run/report conditions and rejects mismatched
agent, run and artifact identities. A requested run returns its typed terminal
outcome; report-only waits return the artifact. A report becoming available is
never interpreted as execution success.

Committed run transitions and agent availability/presence changes independently
wake store subscribers after their transaction commits. A report observed before
terminal settlement cannot be the only wake for a combined wait. These signals
are coalesced observations, not fabricated Board events or delivery admissions;
ordinary reads, rejected writes and timestamp-only heartbeats do not signal.

Any communication addressed to the waiting agent ends an unmet wait as
`directed_event`, including notes, replies, work requests/dispositions, reports,
terminal outcomes and runtime notices. The sender need not be the waited agent.
Human/lifecycle cancellation and actionable recovery retain their existing
handling. The result preserves unmet conditions: waking is not completion.

Waiting observes the existing journal, never reserves or admits deliveries,
marks human history read, or writes context/checkpoints. An atomic initial page
captures pending/admitting obligations and the publication sequence; a journal
read after subscription observes later addressed publications even if normal
admission already committed them. This closes both entry/subscription gaps
without replaying previously admitted history on the next wait. Normal safe-boundary
context admission remains the only delivery authority.

## Work requests

The immutable journal is the single request authority, not another scheduler.
An initial request starts at revision 1 and gets a stable tool-call-derived ID
when the caller omits it. Only its author may issue the exact next revision;
original recipients cannot be replaced or widened. Optional `supersedes` must
identify the latest request event, and revisions remain in the same thread.

Only an addressed participant may accept/defer/decline the latest revision,
addressing its owner. Disposition is a typed reference, never inferred from
free-form prose. Identical originating-call retries are idempotent; duplicate
acknowledgements are rejected. A declined request needs an author-issued revision
before reconsideration. Older accepted bytes remain historical evidence, not a
current agreement. A disposition never grants tools, files or human approval.

## Delivery and context admission

Publication allocates the event sequence and exact recipient obligations in one
workspace transaction. The event body, sender, origin, recipients, thread,
request/run identity, supersession, references, and idempotency fingerprint are
immutable.

Ordinary serialized events remain limited to 128 KiB. Runtime command outcomes
with a bounded captured excerpt may use up to 255 KiB so JSON control-character
escaping cannot discard otherwise valid text. This is a storage allowance, not
an output transformation or a larger process-capture budget; events still fit
the unchanged 256 KiB Board page, and oversized evidence fails explicitly.

Peer notes, reports, outcomes and lifecycle controls retain their own event
identity. A `history_message` reference names evidence, never a replacement
identity for that event. Only an explicitly human-authored steering admission
may bind its submitted input identity through that reference. Receipt
publication must not reclassify an existing human or assistant transcript row
as hidden runtime context.

For each recipient:

1. The attached runtime reserves one pending delivery using its epoch.
2. It renders an attributed peer/human/runtime context input.
3. It commits the context plus `(AgentID, DeliveryID)` admission evidence in the
   agent database.
4. It publishes the workspace admission receipt idempotently.

A failed context commit admits nothing. A crash after the local commit replays
only the receipt. Edit/withdraw can win only while every obligation is still
pending; an admitting or admitted delivery cannot be falsely retracted.

Attachment loads local admission evidence **before** taking over. In one
workspace transaction it validates exact delivery/event/recipient/epoch/version/
commit identities, admits committed receipts, requeues only uncommitted
reservations, interrupts orphaned runs, releases their claims and frees the
active run slot. Old-epoch mutation and admission attempts fail closed.
Recovery inspects reservations and explicit receipts, not all admitted history.
Root collaboration commits use the same session-owned active-context row as
normal turns and compaction; receipts retain the exact runtime agent identity.

Human steering targets an exact active run and retains genuine human origin.
Peer/runtime content is never wrapped as a human interruption.
Publication transactionally rejects a stopped, pausing, stale or terminal run;
the execution owner additionally fences the interval between cancellation or
terminal sealing and durable settlement. Already accepted duplicate receipts
remain observable after that fence. Natural completion seals admission and drains
previously accepted notes before it can finish, so a last-moment note cannot be
acknowledged into an inbox whose consumer has already ended.

## Rare running-agent broadcast

`agent_send` with `broadcast=true` is permitted only for a peer `note`. It cannot
carry explicit recipients or masquerade as a request, revision, reply, approval,
permission, or lifecycle control.

The recipient set is snapshotted transactionally to every *other* agent that is:

- `available`,
- `attached`, and
- the owner of a current nonterminal run at the same runtime epoch.

Idle, suspended, offline, unresponsive, closed, and finished history is excluded.
Later agents do not inherit the old broadcast. A broadcast with no eligible
recipient fails explicitly.

In chat, a broadcast paints as `N running agents · Broadcast`; it does not dump a
large recipient roster into the visible header. The exact recipient snapshot
remains available in durable evidence.

Broadcast is for rare workspace-wide hazards or invariant changes. Routine
progress, task fan-out, acknowledgement loops, and messages for one known agent
must use directed delivery.

## Reports and outcomes

Reports are immutable, versioned artifacts with author, run/request identity,
summary, complete body, digest, references, metadata, and supersession. Any
eligible root or child may publish one. Publication is evidence delivery, not
proof of task success.

Revisions cannot change author, producing run or request identity, or downgrade
a final report. New publications require the attached author and any supplied
run to be current. A child final must address its exact parent and its active
owned run; a child run has one final artifact, with explicit same-artifact
corrections allowed while active. A nonempty request identity must be an actual
work request in which the author participates or the child's exact launch
assignment. Accepted originating-call retries still return the original evidence
after completion or detach.

Automatic notifications carry the bounded summary and exact artifact ID/revision,
not the complete potentially large body. Use `artifact_read` on that revision
before relying on the evidence. Interim publication never satisfies the final
deliverable flag, and a missing-final terminal reason cannot be marked completed.

Run terminal state comes from typed execution state, not words such as “quota,”
“cancelled,” or “success” in prose. Reading an interim report never consumes or
suppresses the later terminal outcome. Corrections append a new report revision;
already accepted bytes remain readable.

Children must publish the required final report exactly once before ending.
Missing that deliverable is not success; the [run supervisor](run-recovery.md)
performs bounded corrective continuation or retains an actionable unfinished
task. Crude does not fabricate a success report from the final chat text.

## Claims and authority boundaries

Workspace claims describe intended file/resource ownership for exact agents and
runs. Overlapping active paths conflict deterministically. Claims do not replace
the low-level `fileio` mutation lease, Local History, dirty-buffer checks,
preview preconditions, or undo receipts.

Opaque shell commands and external MCP writers are not made safe merely by a
claim. Unknown or unfenced effects remain explicitly limited.

Declared child ownership is reserved before model execution, across sessions.
Native writes conflict with another active owner; repeated writes reuse the
run's claim. Delegating a path the launching parent itself holds transfers the
parent's claim to the child in the same transaction (`DelegatorID`); a parent
claim broader than the delegated path, or another agent's claim, still conflicts
and names the holder and the remedy. Failed launches release their exact runtime and claims rather than
leaving invisible running agents.

Native ownership rejection is an ordinary, actionable tool error. Nothing
changes, the child's run continues, and its parent receives one runtime note per
distinct rejection naming the child, path and reason. A scoped child's write gate
reads its current committed scope, so the parent's `agent_update_claims` on the
live run takes effect on the child's next write. The child's private temporary
directory is never scope-checked or claimed. A parent may widen or narrow its
child's scope; the only bound is that it must not overlap another agent's active
claims. Managed-checkout children also remain bounded to their execution checkout:
relative claim repairs bind that checkout and absolute external claims are rejected,
not rebased. A scope update never makes a provider call, file effect or wake.
Session binding supplies the ownership service to restored runners as well as
new ones. Runners resolve it from the Session rather than retaining an earlier
provisional owner; repair, native-write guards and in-flight effect leases must
observe the same committed projection after resume.

A child that stops for attention (a run-level fault such as rejected
credentials, storage, compaction or exhausted bounded recovery) is never a dead
end. Its parent is notified once the incident is actionable, sees it through
`agent_inspect`, `agent_list` and `agent_wait` (which ends with
`needs_attention`), and continues or cancels it with `agent_control`
`continue`/`cancel` and the exact `recovery_id` and `recovery_generation`, even
while the child has no attached runtime. Refusals return their reason.

Run and claim preimages are positive exact integers; fractional, negative,
unsafe or missing values fail closed. Narrowing cannot widen scope, steal a
foreign run or claim, or repair an active child. An explicitly empty path list
relinquishes ownership. Native file effects hold a read lease on their checked
ownership through completion; claim replacement waits for in-flight effects.
Multi-target preflight is atomic, so a rejected operand cannot leak neighboring
claims. These leases complement, not replace, `fileio` mutation and history
safeguards. Opaque shell and external MCP effects remain outside this guarantee.

## Lifecycle controls

Pause/cancel require exact run ID and version. Application checks that identity
again transactionally; another run starting at version 1 cannot inherit the
earlier control. Finalization tolerates concurrent version changes only for
that same owned run. Pause cancels current execution and suspends admission.
Resume/reopen require an exact runtime epoch and no active run, restoring
availability—not a frozen execution stack or an automatic child relaunch.
Controls for unattached history fail explicitly.

Control submission and its authoritative applied/rejected outcome are separate
durable events. Human-origin outcomes are audit-only Board facts, not fake
deliveries to a “human” agent and not fresh model turns.

Peer claims and reports are attributed evidence. They do not become verified
memory merely through delivery, repetition, or exposure.

## Presentation and recovery

The Board footer panel is retained workspace history/search. Human unread is
independent of agent admission. Live events and SQLite hydration enter the same
conversation projection, preserving sender/recipient identities, origin, body,
receipt state, report references, copy text, disclosure, and hit testing.

Incoming collaboration must not steal focus, select a tab, modify a draft,
consume attachment state, reset undo, or break pointer capture.

Command-outcome events carry immutable typed command facts into live and persisted
conversation attribution. They paint compact ordinary-tool rows, not shaded peer
cards; disclosure retains exact command, PID, exit availability, time and output
size. Model-facing event bodies and durable delivery remain independent of this
presentation. No renderer parses event prose to reconstruct process state.

Resume is passive: it restores participants, contexts, pending deliveries,
artifacts, and run facts without provider calls, command replay, or implicit
child launch. Already-published evidence is admitted passively through a stable
startup sequence boundary; genuinely new peer input can subsequently wake an idle
root. Recovered children have their own receipts/runs settled without restarting.
Interrupted attempt facts do not settle their durable unfinished task. Explicit
**Continue run** uses the owning session's safe recovery checkpoint and starts a
checked replacement attempt; it is separate from availability-only resume/reopen
controls and never relaunches old tool effects.
Workspace switch/shutdown joins local workers, releases exact
runtime locks, and does not stop another process's agents.

There is one current collaboration schema and no compatibility board, legacy
tool facade, session membership gate, or report-path delivery fallback.

## Acceptance evidence and limits

Current acceptance includes:

- Cross-process runtime-lock and epoch takeover tests on Windows.
- Idempotent publication, reservation/admission, withdrawal races, pagination,
  report provenance, typed request/disposition fencing, claim conflict, and
  before/after-local-commit recovery coverage.
- Regression coverage for old controls against replacement runs, interim reports
  masquerading as finals, exact pause/resume, cancellation during finalization,
  profile selection, combined waits, and passive restart with zero provider replay.
- `TestWorkspaceSwitchLauncherStartsAndResumesSessions`: repeated project switches
  start actual launcher agents, retain explicitly submitted conversations and
  resume their exact sessions without provider replay. Old SQL handles close and
  replacement sessions use the target memory/collaboration services. Target
  collaboration preparation failure is covered separately and preserves the
  current workspace.
- Live/SQLite/hydration projection parity at narrow and wide widths.
- Broadcast tests with large closed history proving only active attached runs
  receive the event.
- `TestWorkspaceAgentsCollaborateToRepairAndVerifyProject`: two independent root
  sessions perform a real tool loop where one requests a code repair, the peer
  accepts the exact request, edits and checks the Go project, and publishes a
  report. The coordinator receives its exact reference automatically, reads the
  complete artifact, and runs the authoritative test.
- Live workspace coordination in which directed notes and report publication
  arrived automatically between independently running roots.

These establish engineering operation, not a universal productivity claim.
The deterministic integration provider is scripted; it does not establish that
every real model will independently choose this workflow. Local ownership and
durable event/receipt principles are informed by
[Temporal's execution/run distinction](https://docs.temporal.io/workflow-execution)
and [Codex subagent guidance](https://developers.openai.com/codex/subagents), not a
claim of deterministic replay or a multi-machine Temporal-style service.
Held-out real-model with/without comparisons are still required before claiming
general task-quality, token, latency, or cost improvement.

## Delivery and control invariants

- Directed collaboration events and report/outcome publications are delivered
  automatically at safe boundaries; Board reads are history/search, not polling
  transport. Peer data is never approval or permission. `agent_send` broadcast
  is peer-note-only and snapshots only other available, attached agents with a
  current nonterminal run; it excludes idle/suspended/offline/finished history
  and is never routine progress or task fan-out. Human unread and durable agent
  context admission remain independent. Memory revisions use expected-version
  correction; exposure never reinforces usefulness.
- Collaboration work requests are journal-owned: initial revision 1, exact next
  author-issued revision with unchanged recipients, and typed latest-revision
  accept/defer/decline. Never infer agreement from prose or allow duplicate
  acknowledgement loops. Roots select reusable children with `agent_start.profile`;
  bounded leaves coordinate with peers but cannot recursively delegate.
- Controls fence exact run ID/version or availability epoch; a replacement run
  never inherits an earlier cancel. Pause cancels execution and suspends admission;
  resume restores availability, not a stack. Human outcomes are durable audit
  facts, not fake model deliveries. Publication is never proof of application.
- Report author/run/request provenance is immutable across revisions. Only an
  actual child-owned final addressed to its parent satisfies the deliverable;
  interim reports and missing-final terminal reasons cannot become completed
  work. Notifications expose exact artifact references; read the referenced
  revision before relying on its summary.
- Runtime takeover first loads local context receipts, then atomically reconciles
  admissions, requeues only uncommitted reservations, interrupts orphaned runs and
  releases their claims under the exact process lock/epoch. Root admission,
  compaction and normal turns share one authoritative active context. Resume
  admits old evidence passively with zero provider/tool/child replay; genuinely
  new peer input may wake the attached idle root.
