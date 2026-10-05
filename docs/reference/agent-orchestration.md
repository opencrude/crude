# Agent orchestration

> Status: Current — bounded delegation, enforced guidance intake, explicit handoff, and event-driven completion.

## Ownership and stopping

The canonical ownership, intake and development contract is
[model prompts and project guidance](prompt-architecture.md). `prompt/` owns
root/child and specialized job instructions; execution packages supply typed
facts and own lifecycle/persistence. Callers use direct prompt APIs rather than
forwarding wrappers or provider-specific behavioral forks. Editable profile
instructions remain lower-priority overlays. Real tool/wire differences stay in
their interface adapters.

Before normal project work, Crude consumes complete applicable `SOUL.md`,
`CLAUDE.md`, and `AGENTS.md` sources accepted by the 64 MiB aggregate ceiling,
or fails closed. Fitting sets are verbatim; oversized sets are compiled and
model-verified into bounded ledgers. The task model need not receive every raw
byte, and verification is not proof of perfect semantic preservation. Other
documentation stays selective. Newly discovered applicable guidance defers the
entire planned tool batch before effects. See the canonical reference for exact
scope discovery, budget/cache identity, resource limits and guard coverage.

Supplied guidance ends with an intake-complete notice; consuming it satisfies
its own startup read requirements. Unchanged rereads remain legal and do not
republish guidance or rewrite the accepted cache prefix. Known source changes
publish one complete superseding snapshot, including removal of deleted rules.

The parent owns requirements, shared interfaces, integration, and final repository
checks. Delegate independent tasks with owned paths, acceptance criteria, and a
clear stopping condition. Children inspect and verify their assigned flow, not the
whole changing repository. A dependency or unrelated build failure is a reason
to return a blocked/partial handoff, not repeatedly read files or rerun checks.
Repeat verification only after a relevant change or new evidence.

Roots may reuse suitable attached workspace peers after checking exact identity,
availability and ownership, or launch bounded leaf children with an optional exact
configured `profile`. Children may coordinate with peers but cannot recursively
launch more children. Accept/defer/decline substantive exact requests; do not
acknowledge every note/outcome. Request revisions are journal-fenced and cannot
widen permission. Read the exact referenced report revision before integration.
See [workspace collaboration](workspace-collaboration.md) for enforced request,
control, report, claim and crash-recovery contracts.

`publish_report` delivers an immutable, versioned artifact: outcome, findings or
changed paths, focused checks, and unresolved blockers. It is not a request to
copy source files or continue investigating. Publication is evidence delivery,
not proof that the run succeeded or the task quality is sufficient. An unsuccessful
attempt without a required final deliverable is not success; unfinished root/child
work remains supervised or waits for attention. Cancellation remains distinct
from success. The parent still reviews the evidence.

Request/job timeouts and repeated-identical-tool detection complement these prompt
rules without permanently settling unfinished work. Children have no arbitrary
task deadline. There is no arbitrary global step ceiling: a legitimate long task
is not the same as a loop. Prompt guidance is not a guarantee against every
unproductive behavior; report actual limitations rather than claiming otherwise.

The authoritative [run recovery contract](run-recovery.md) separates bounded
request attempts from owned unfinished tasks, defines actionable attention states,
safe continuation/checkpoint/resume and the shared five-minute cooldown ceiling.

## Completion delivery

Workspace collaboration events update the UI and enter the exact recipient at a
safe request boundary. Directed notes, requests, report publications and terminal
outcomes need no Board polling or acknowledgement tool for transport. Delivery
works while an agent is idle and during an active turn without replaying accepted
model output. Reading an interim report never suppresses a later terminal outcome.

An interim report also never satisfies the mandatory final deliverable. The final
artifact must belong to the child and its actual run and address the exact parent;
one child run cannot publish multiple distinct final artifacts. Submission of a
control is not application: old run IDs are rejected against replacement runs
even when their versions match. Pause cancels execution and suspends admission;
resume restores availability, not an execution stack. Startup settles orphaned
runs and admission receipts passively before processing genuinely new peer input.

`agent_send(broadcast=true)` is the narrow exception for a genuinely urgent note
needed by every other currently running agent. It snapshots only available,
attached agents with active nonterminal runs; it excludes idle, suspended,
offline and finished history. It is never task fan-out, permission, approval or a
routine progress channel.

Notifications are runtime context, not human instructions. They do not cancel an
active request, invoke an extra human turn, or change the selected tab. Background
permission/question requests use their owner-correlated prompt surface; safety
alerts remain globally visible without navigating away from the user's work.

Each root and child has an independent Auto Review request lifecycle and
per-turn budget. They share only the persisted reviewer policy and connection.
The owning session's live YOLO state bypasses reviewer and confirmation for the
root plus its complete descendant tree; another root/session remains isolated.
When confirmation is still required, the currently painted agent chat uses its
session dock, while background sessions/agents and editor views use the global
owner-correlated modal without switching the user's active view.

## Design references

Primary documentation reviewed for this change:

- [OpenAI Codex subagents](https://developers.openai.com/codex/subagents): bounded
  independent work, distilled summaries rather than noisy intermediate output,
  caution with concurrent code edits, and approval overlays identifying inactive
  source threads without requiring unsolicited navigation.
- [Anthropic multi-agent research system](https://www.anthropic.com/engineering/multi-agent-research-system):
  explicit objectives/output formats/tool guidance/task boundaries; task-scaled
  effort; artifact handoffs; evaluation of both outcomes and tool efficiency.
- [Claude Code subagents](https://code.claude.com/docs/en/sub-agents): focused
  contexts, limited tools, concise returned summaries, partial/failure reporting,
  and explicit lifecycle controls.
- [Google ADK loop workflows](https://google.github.io/adk-docs/agents/workflow-agents/loop-agents/):
  termination conditions or iteration limits are required for intentional loops;
  repeated refinement must have an exit condition.

These sources motivate Crude's ownership and handoff rules, not a claim that its
runtime is identical to those products. In particular, this change does not add
Claude Code's turn-budget configuration or ADK's deterministic workflow scheduler.
