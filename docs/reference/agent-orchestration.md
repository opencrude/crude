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

Reporting is the mandatory child return channel, independent of optional task-tool
allowlists. Allowlist expansion must not synthesize a `publish_report` denial.
An explicit denial in a launch, role or profile is rejected before launch, rather
than issuing a contradictory prompt. Offered schemas and execution use the same
selection predicate. If reporting infrastructure is unavailable at a request
boundary, the child parks for configuration repair before another provider call;
model reminders are reserved for an available tool the model omitted.

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

## Managed child execution

`agent_start` accepts `isolation: auto|shared|worktree`; use the managed service,
not shell Git improvisation. Auto isolates supported independent writers in Git
projects and otherwise records shared execution. Dirty files and accepted document
text are preserved; children of an active managed parent use its exact current
`SourceContextID` checkout, not stale logical overlays or private shell drafts.
Relative claims bind the resolved root; absolute logical claims are not silently
rebased. Worktrees are coordination/isolation artifacts, not security sandboxes.
Only the exact reviewed delta integrates through shared documents and mandatory
Local History safety/undo; active-run, Keep and recovery pins prevent unsafe cleanup.
See [managed executions](managed-executions.md) for conservative baseline exclusions,
crash recovery and explicit native Windows/SSH acceptance limits. Research links
above motivate policy, not a guarantee of universal automatic isolation.

## Child reporting, ownership and connections

- `publish_report` is the mandatory child return channel, not an optional task-tool
  allowlist member. Explicit denial is rejected before launch; schema projection,
  dispatch and prompt permissions agree. `artifact_read` and `agent_send` complete
  the coordination channel: narrowing a child's tools with `allowed_tools` keeps
  all three, and only an explicit deny removes the optional two. Children were
  otherwise handed artifact IDs they could not read. Missing reporting infrastructure parks as
  configuration failure before provider execution, never a model reminder loop.
- Native file-ownership rejection is an ordinary, actionable tool error: nothing
  changes, the child's run continues, and its parent receives a deduplicated
  runtime note. A scoped child's write gate reads its *current* committed scope
  (`FileOwnership.WriteScopeAllows`), not the launch snapshot. The child's own
  temporary directory (`CRUDE_TEMP_DIR`) is private scratch: never scope-checked
  or claimed. Public `agent_narrow_claims` narrows the caller's active claims;
  `agent_update_claims` replaces a child's complete scope — wider or narrower —
  during its live run (claims bound to that run, effective on the next write) or
  after an interrupted attempt (declarations used by Continue). The only bound is
  no overlap with another agent's active claims; there is no frozen launch
  ceiling. Neither wakes execution. Run identity, run version and claim
  versions are optional: omitted ones are read from the target's current state
  at call time (an interrupted child needs its `run_id`), and explicit ones are
  exact preimages from `agent_inspect`; the store fences both. Numeric
  preimages are exact positive integers, foreign/stale owners fail closed, and ownership read leases span
  actual native effects so claim replacement cannot overtake an in-flight write.
- **Names.** Agents draw names from a durable, unbounded app-wide sequence
  mapped through a per-installation keyed permutation of each tier (one given
  name, then given name plus one, two, ... surnames), so consecutive agents get
  unrelated names and none repeats. A parent may pass `nickname` to
  `agent_start`; a taken nickname gets the next numeral ("Nova II"). The child's
  full name keeps its lineage ("Nova sub Sigrid").
- **Parents do not poll.** Whenever a child's state changes (working, waiting
  for the human, waiting for a delivery, idle, finished), the root receives one
  `<children_status>` runtime notice before its next request; reports,
  questions and outcomes are delivered automatically as before.
- **Repositories without commits** run every child in the shared checkout
  (auto isolation falls back with that reason); an explicit worktree request
  explains that it needs a first commit.
- **Child connections are complete and parent-controlled.** Every child
  provider is built from `childProviderConfig` (account, model, thinking effort,
  temperature, top-p, context window) at launch, each supervised attempt,
  continuation and request-boundary switch. `Config.ConnectionProvider` is
  connection-only (model listing/probing) and never serves an agent; there is no
  account+model helper that can drop request settings. `categoryFieldRole`
  classifies every `CategoryConfig` field and the wire regression asserts
  `reasoning_effort`/`temperature` on every path, so a dropped setting fails
  tests. A parent picks `model`/`account_id`/`thinking_effort` in `agent_start`
  or changes them with `agent_configure`: a live child switches at its next
  request via `Agent.RequestBoundary` (emitting `connection_changed` so the UI
  and message attribution follow), a stopped child keeps it for `continue`.
  A different model drops the previous model's capability/sampling overrides.
  `types.ThinkingLevels` is the one effort ladder for settings and tools.
