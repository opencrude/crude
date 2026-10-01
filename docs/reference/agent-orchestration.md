# Agent orchestration

> Status: Current — bounded delegation, explicit handoff, and event-driven completion.

## Ownership and stopping

Main agents and children are prompted to find and read applicable project guidance
with tools before changing code or answering project-specific questions. They
check `AGENTS.md`, `CLAUDE.md`, and relevant project documentation at the workspace
root, in ancestor directories, and under the paths they work in. Crude does not
inject repository files into their prompts or claim those files were read. The
GPT fallback prompt makes the same distinction. Project guidance remains scoped
to its files and subordinate to the current user and system instructions.

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
