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

`write_report` delivers a concise artifact: outcome, findings or changed paths,
focused checks, and unresolved blockers. It is not a request to copy source files
or continue investigating. A generated diagnostic report, file list, or prior
tool activity does not establish successful completion. Unrecovered stream
failures without a completed deliverable are failures; cancellation and timeout
remain distinct from success. The parent still reviews the evidence.

Existing runtime timeouts and repeated-identical-tool detection complement these
prompt rules. There is no arbitrary global step ceiling: a legitimate long task
is not the same as a loop. Prompt guidance is not a guarantee against every
unproductive behavior; report actual limitations rather than claiming otherwise.

## Completion delivery

Sub-agent terminal events update the UI and queue a structured parent wake.
Delivery must work both while the parent is idle and at a safe model-request
boundary during an active turn. Results already returned synchronously or by an
explicit terminal-result lookup are consumed once. Reading an intermediate
report must not suppress a later failure/completion notification.

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
