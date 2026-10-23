# Session Commands: Plan, Goal and Fork

> Status: Current reference — Plan mode, session goals with independent verification, session fork, and where commands live.

The composer's slash commands act on the selected session (`/plan`, `/goal`,
`/fork`, `/compact`, `/retry`, `/undo`, `/history`, `/sessions`, `/memory`,
`/skills`, `/mcps`, `/settings`, `/fast`, `/yolo`). The command palette
(Ctrl+Alt+P) lists only global commands with their live shortcuts; there is no
Help window and no `/model` (model choice lives in `/settings`). Plan mode,
goals and fork follow the established Codex contracts (`/plan`, `/goal`,
`/fork`); Crude adds runtime enforcement and an independent goal verifier.

## Plan mode (`/plan`)

- `/plan` toggles Plan mode for the session; `/plan <prompt>` turns it on and
  sends the prompt in it. The mode is persisted with the session
  (`session_runtime_state` key `plan_mode`), so resume and fork keep it, and it
  applies from the next turn: a running turn keeps the mode it started with.
- A Plan mode turn carries `prompt.PlanModeContract` in its system prompt
  (explore first, settle intent and implementation with `question`, then one
  decision-complete `<proposed_plan>` block). The runtime enforces the
  boundary independently of that text: the offered tools are read-only
  (`planModeAllows`), every call is checked again at execution
  (`planModeRefusal`), commands must classify as safe, and `update_plan`,
  `create_goal`, `update_goal`, `agent_start` and `crude_control` are not
  offered. The agent's policy is `ReadOnlyPolicy` for the turn.
- The first turn after Plan mode is turned off receives a context-only notice
  that the mode ended.
- `<proposed_plan>` renders as a titled **Proposed plan** section; live and
  hydrated rows share `renderMarkdown`. After a completed Plan mode turn whose
  final reply contains a proposed plan (tags alone on their lines), the
  selected session asks **Implement this plan?** with Codex's three answers:
  **Implement** turns Plan mode off and sends
  `prompt.PlanImplementationRequest`; **New session** opens a new
  session (outside Plan mode, in its own tab) whose first message is
  `prompt.PlanImplementationFreshContext` with the plan's Markdown and nothing
  of the planning conversation, leaving the planning session as it was;
  **Keep planning** leaves the mode on. A connected shell holds that first
  message until the new session reaches it (`pendingFirstInputs`).
- The header shows **Plan mode** for the selected session.

## Goals (`/goal`)

A goal is one objective the session's main agent pursues across turns until
an independent verifier confirms it, or the user or a stop rule ends it.

- Commands: `/goal` shows a one-line summary; `/goal <objective>` sets it and
  asks **Replace the current goal?** while an unfinished one exists; `/goal
  pause`, `resume`, `clear`; `/goal edit` puts the current objective in the
  composer and `/goal edit <objective>` changes it without restarting. The
  header shows **Goal** with its state (checking, waiting, paused, blocked,
  limited by budget, complete); clicking it shows the summary.
- State: one row per session in `session_goal` (objective, status
  `active|paused|blocked|budget_limited|complete`, reason, tokens, token
  budget, active time, waiting, running verifier). Shells read it through
  `AgentSnapshot.Goal`; changes are core owner jobs (`agent.goal`) and appear
  as status rows in every shell (see Status rows).
- Continuation: the orchestrator's own run loop starts the next goal turn when
  the agent is idle, after collaboration deliveries and queued human input,
  with no pending recovery or compaction, and never in Plan mode. The turn's
  input is context-only runtime input (`prompt.GoalContinuation`), never a
  human message in the transcript. Plan mode turns do not count.
- Stop rules: Stop on a goal turn, or stopping its unfinished run, pauses the
  goal; a terminal turn failure blocks it; three consecutive automatic turns
  that are empty, or whose tool calls all failed, block it. A turn parked for
  recovery changes nothing: recovery owns it and no goal turn can start until
  it settles. A reopened session never restarts work by itself: an active
  goal waits until the user resumes it or sends a message, and the session
  offers **Resume the goal?** for waiting, paused and blocked goals.
- Token budget (Codex semantics): `/goal` never sets one; `create_goal` takes
  an optional `token_budget` only when the user explicitly asks for one. The
  continuation states the budget and remaining tokens. When the goal's tokens
  reach the budget during a turn, the runtime marks it `budget_limited`, stops
  a running verifier, and tells the running turn once, at its next step, to
  wrap up (`prompt.GoalBudgetLimit`); no further goal turn starts. A
  budget-limited goal cannot be resumed or paused; set a new goal or clear it.
- Completion: the main agent's `update_goal(status: "complete", evidence)`
  does not complete the goal. It launches an independent verifier, an
  ordinary visible, recoverable child under the root (policy `verify`: it may
  read and run checks under the session's command approvals, but has no
  editing, delegation or plan tools). The user's multi-agent setting does not
  apply because the runtime, not the model, starts it. The verifier's
  `publish_report` has a required `verdict` (`achieved|not_achieved`) that the
  runtime stores as `goal_verdict` metadata. Achieved completes the goal;
  not achieved returns its findings to the next goal turn; a verifier that
  ends without a verdict three times in a row blocks the goal. A verifier from
  a previous runtime never reports into a reopened session; the claim is
  checked again when the work resumes.
- The main agent's tools are `get_goal`, `create_goal` (only when the user
  asks in conversation) and `update_goal` (`complete`, `blocked` after the same
  blocker repeated three goal turns, `paused` only on the user's request).
  Children have none.

## Status rows

Plan mode, YOLO and goal changes are transcript rows owned by the session,
never by a shell: the session stores each in `session_status_rows` and sends
it as a `status_row` event (`recordStatusRow`), so every shell shows it and a
reopened session hydrates the same rows in time order (`statusRowView` serves
both paths). A row is recorded only when the value changes; goal rows derive
from the last goal published (`goalTransitionRow`), so accounting updates and
a reopen add none. Session facts (`status_row`, `goal`) apply whatever state
the agent's turn is in.

## Fork (`/fork`)

- `/fork` continues the selected conversation in a new saved session, opened
  in its own tab; the source is unchanged. It is refused while a turn,
  compaction, unfinished-run recovery or child agent is in flight, because a
  copy would duplicate that ownership.
- `sessiondb.Fork` copies the live database with `VACUUM INTO` (a consistent
  snapshot) under the source's turn locks, rewrites the session identity and
  the root context key, gives the fork its own agent name, and drops
  runtime-owned rows: recoveries, collaboration admissions, file claims, child
  results, billed usage, queued input and the composer draft. Plans, settings,
  references, Plan mode and the selected execution are kept; execution hygiene
  scans every session database, so the shared checkout stays protected.
- An active goal is kept but waits for the user in the fork.
- The core method is `session.fork`; it runs through the same lifecycle job
  as `session.open`.
