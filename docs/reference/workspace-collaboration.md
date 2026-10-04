# Workspace Collaboration Runtime

> Status: Current reference (2026-10-04) — durable workspace-wide agent
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

## Model operation surface

| Tool | Contract |
|---|---|
| `agent_list` | Bounded workspace directory query with exact IDs and live facts |
| `agent_inspect` | Exact agent/run/reports/claims without secrets or raw private context |
| `agent_start` | Start a bounded child under effective policy and resource limits |
| `agent_send` | Directed note/reply/request/revision or rare running-agent broadcast |
| `agent_respond` | Accept, defer, or decline an exact request revision |
| `agent_control` | Request pause/resume/cancel/reopen for an exact agent/run version |
| `publish_report` | Publish immutable authored evidence linked to the actual run/request |
| `artifact_read` | Read an exact artifact revision; revision zero selects latest |
| `agent_wait` | Wait for an exact run/report/agent condition without model polling |
| `board_read` | Browse workspace communication history; not ordinary delivery transport |

Directed delivery is automatic. Models do not call `board_read`, `agent_wait`, or
sleep merely to notice an ordinary message, report, or terminal outcome.

## Delivery and context admission

Publication allocates the event sequence and exact recipient obligations in one
workspace transaction. The event body, sender, origin, recipients, thread,
request/run identity, supersession, references, and idempotency fingerprint are
immutable.

For each recipient:

1. The attached runtime reserves one pending delivery using its epoch.
2. It renders an attributed peer/human/runtime context input.
3. It commits the context plus `(AgentID, DeliveryID)` admission evidence in the
   agent database.
4. It publishes the workspace admission receipt idempotently.

A failed context commit admits nothing. A crash after the local commit replays
only the receipt. Edit/withdraw can win only while every obligation is still
pending; an admitting or admitted delivery cannot be falsely retracted.

Human steering targets an exact active run and retains genuine human origin.
Peer/runtime content is never wrapped as a human interruption.

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

Run terminal state comes from typed execution state, not words such as “quota,”
“cancelled,” or “success” in prose. Reading an interim report never consumes or
suppresses the later terminal outcome. Corrections append a new report revision;
already accepted bytes remain readable.

Children must publish the required final report exactly once before ending.
Missing that deliverable is a policy failure; Crude does not fabricate a success
report from the final chat text.

## Claims and authority boundaries

Workspace claims describe intended file/resource ownership for exact agents and
runs. Overlapping active paths conflict deterministically. Claims do not replace
the low-level `fileio` mutation lease, Local History, dirty-buffer checks,
preview preconditions, or undo receipts.

Opaque shell commands and external MCP writers are not made safe merely by a
claim. Unknown or unfenced effects remain explicitly limited.

Peer claims and reports are attributed evidence. They do not become verified
memory merely through delivery, repetition, or exposure.

## Presentation and recovery

The Board footer panel is retained workspace history/search. Human unread is
independent of agent admission. Live events and SQLite hydration enter the same
conversation projection, preserving sender/recipient identities, origin, body,
receipt state, report references, copy text, disclosure, and hit testing.

Incoming collaboration must not steal focus, select a tab, modify a draft,
consume attachment state, reset undo, or break pointer capture.

Resume is passive: it restores participants, contexts, pending deliveries,
artifacts, and run facts without provider calls, command replay, or implicit
child launch. Workspace switch/shutdown joins local workers, releases exact
runtime locks, and does not stop another process's agents.

There is one current collaboration schema and no compatibility board, legacy
tool facade, session membership gate, or report-path delivery fallback.

## Acceptance evidence and limits

Current acceptance includes:

- Cross-process runtime-lock and epoch takeover tests on Windows.
- Idempotent publication, reservation/admission, withdrawal races, pagination,
  report revision, claim conflict, and recovery coverage.
- Live/SQLite/hydration projection parity at narrow and wide widths.
- Broadcast tests with large closed history proving only active attached runs
  receive the event.
- `TestWorkspaceAgentsCollaborateToRepairAndVerifyProject`: two independent root
  sessions perform a real tool loop where one requests a code repair, the peer
  edits the Go project and publishes a report, and the coordinator receives it
  automatically before running the authoritative test.
- Live workspace coordination in which directed notes and report publication
  arrived automatically between independently running roots.

These establish engineering operation, not a universal productivity claim.
Held-out real-model with/without comparisons are still required before claiming
general task-quality, token, latency, or cost improvement.
