# Session Commands: Plan, Goal, Loop and Fork

> Status: Current reference — Plan mode, verified goals, scheduled loops, session fork, and where commands live.

The composer's slash commands act on the selected session (`/plan`, `/goal`, `/loop`,
`/fork`, `/compact`, `/retry`, `/undo`, `/history`, `/sessions`, `/memory`,
`/skills`, `/mcps`, `/settings`, `/fast`, `/yolo`). A command can be typed
anywhere in a draft: `/` at the start of a word opens the list for the name at
the caret, and choosing a command (click, Enter or Tab) removes only its name
and runs it. The rest of the prompt, its attachments, undo history and caret
stay. A name that matches no command (a path such as `/usr/bin`) stays prose. A
draft that starts with a command that takes no arguments runs the command and
keeps the text after it as the draft; `/plan`, `/goal`, `/loop`, `/skills` and
`/mcps` read the rest of the line. A draft that is only an unknown `/name` is
explained and kept for correction, never sent or cleared. The command palette
(Ctrl+Alt+P) lists only global commands with their live shortcuts; there is no
Help window and no `/model` (model choice lives in `/settings`). Plan mode,
goals and fork follow the established Codex contracts (`/plan`, `/goal`,
`/fork`); Crude adds runtime enforcement and an independent goal verifier.

## Context compaction (`/compact`)

A human `/compact` is authoritative. The owning session is the only admission
authority; neither the core nor a shell pre-judges whether the agent looks
busy, so a running background command, a child agent or a turn that is still
saving its Local History snapshot never refuses it. `session.PrepareCompaction`
decides one of these outcomes, and the session writes the words the shell
shows:

- **Idle, or a turn is finishing or stopping.** The session registers one
  exclusive operation at once. New turns and queued input wait behind it, and
  its boundary row appears in every shell. It then waits on the context-aware
  turn gate (`agent/session/turn_gate.go`, a `semaphore.Weighted` like workspace
  storage) for the finishing turn to release the context, discards any
  background preparation, and compacts the complete settled context. Stop cancels the wait or the extraction; queued input stays
  queued.
- **A turn is running.** The turn accepts the request and the footer says
  "Compaction will start after the current step." The run compacts before its
  next model request (trigger `requested`, shown as an ordinary compaction row
  marked "requested with /compact"), also when automatic compaction is off or
  deferred, and then continues the same work. A `wait` or `agent_wait` in
  progress ends at once, and a slow command continues as a background handle,
  so the next step comes now rather than after their timeouts. The checkpoint
  always covers the complete current context: a checkpoint prepared earlier in
  the background is discarded, never adopted with raw recent history behind it. A repeated `/compact` reports the request
  already accepted. If the turn ends before another step, the request becomes
  the exclusive operation above and runs before any other turn. If the turn is
  stopped or fails first, the request settles as a cancelled compaction row; it
  never disappears.
- **Nothing to compact, or already compacting.** A short footer notice says so.
- **Real refusals** are only a closing session, a session reserved for core
  shutdown, an agent with no open session, and child agents (they compact
  automatically; run `/compact` in the main agent tab). Each states why and
  that the context was not compacted.

## Fast mode (`/fast`)

`/fast` toggles a session override for the main agent's next request. A short
footer notice confirms the save; while fast is configured and supported, the
composer's guidance row starts with **⚡ Fast** and the focused main-agent
header appends **fast** to its model. Other sessions and child-agent headers do
not borrow that indicator. Clearing the override returns to the inherited speed,
which can itself still be fast; the notice says so rather than claiming it is
off. **Speed** in the session's agent settings and in the Application/Workspace
Agents editor provides the same choice for individual roles and named agents.
Changing a named agent affects later root requests and newly launched delegated
agents; already launched children retain their launch configuration. A separate
root-only session override still takes precedence. The root choices use the
same next-request projection as `/fast`.

These are requested settings, not proof of the tier the provider actually
served; fast processing can bill at a premium rate. If the selected connection
cannot send the fast tier, the footer says
**⚡ Fast unsupported**, and the model is not labelled fast. The retained
choice is ignored by that connection until a supporting one is selected. If the
connection is missing or invalid, the footer says **⚡ Fast unavailable** instead
of guessing its capabilities.

## Plan mode (`/plan`)

- `/plan` toggles Plan mode for the session; `/plan <prompt>` turns it on and
  sends the prompt in it. The mode is persisted with the session
  (`session_runtime_state` key `plan_mode`), so resume and fork keep it, and it
  applies from the next turn: a running turn keeps the mode it started with.
- A root Plan mode turn carries `prompt.PlanModeContract`. The ordinary source
  stays read-only. The agent can ask questions and propose a decision-complete
  `<proposed_plan>`; it cannot switch modes, delegate or run implementation
  controls. `open_plan_investigation` enables the scoped experiment described
  below. Both modes declare the same tools, including `open_plan_investigation`.
  The runtime refuses disallowed calls without executing them. A mode change
  replaces the planning contract in a context-only message at the next turn.
- The first turn after Plan mode is turned off receives a context-only notice
  that the mode ended.
- `<proposed_plan>` renders as a titled **Proposed plan** section; live and
  hydrated rows share `renderMarkdown`.
- **Plan review.** Each proposal is a revision of the session's plan review
  (see [Plan review](#plan-review)). After a completed Plan mode turn whose
  final reply proposed a plan, the selected session asks **Implement this
  plan?** with **Implement**, **New session**, **Review** and **Keep
  planning**. The offer is bound to the revision that turn recorded (its turn
  generation); a stale proposal or delayed completion cannot be offered as a
  later turn's result.
- The header shows **Plan mode** for the selected session. The footer confirms
  both toggles and retains **Plan · next turn** while enabled; an already
  running turn keeps the policy it started with.

### Investigation worktrees

1. Entering Plan mode allocates no checkout. To test a hypothesis, the main
   agent opens an investigation. The workspace captures the current working
   files and accepted document edits, or the current bytes of the source
   execution already bound to the input. The investigation needs a supported
   Git repository with a commit. If setup fails, the agent can still plan;
   it never writes the test into the source as a fallback.
2. Reads remain source-relative. In the investigation, native writes and patches
   target the separate checkout; use its returned path to read the experiment.
   A bounded set of direct inspection commands can run on the source. Before a
   project command runs, **Allow for this plan** grants host execution for this
   investigation across review turns. **Do not run** still lets the agent write
   a test and report that it did not run. YOLO and Auto Review cannot answer
   this question. Normal command policy still applies after consent. A worktree
   is **not a sandbox**: test programs have the core host user's permissions.
3. A command records its input files before launch and its exit and output after
   settlement. A command that starts is not a passing test. Missing exit status,
   changed measured inputs or evidence too large to verify cannot establish a
   pass. Snapshots cover project files, not the entire host or ignored build
   dependencies. Output is bounded. Larger source changes include readable
   excerpts and a lossless archive within the evidence limits.
4. A completed proposal binds immutable results to its exact revision. Select
   **Evidence** in the plan review or **Review Investigation Evidence** from
   the palette to read them. The latest 24 command references are retained in
   the working evidence window; the review reports omissions. Earlier
   revisions keep their own evidence. Revising a plan keeps the investigation;
   a fork gets historical evidence, not the worktree or the host-run consent.
5. **Implement** and **New session** pass the approved revision's verified
   evidence to the normal implementation turn. Neither selects, copies nor
   merges the investigation. Turning Plan mode off closes that planning cycle
   for future turns. A live run, command or parked recovery still retains its
   checkout. Disposal uses reviewed discard and an archive, not age or force.
   An uncertain disposition retains the files. See [managed executions](managed-executions.md).

## Plan review

Every plan a root session proposes in Plan mode is kept, edited and commented
on as a real file, like a pull request for the plan. This goes beyond the
current harnesses: Claude Code and Cursor let the user edit a plan but not
comment on it; Copilot (VS Code) and Kiro add comments but keep one draft;
Antigravity loses queued comments. Crude keeps every revision, anchors each
comment to its text so it follows the user's edits, keeps unsent comments
across restarts and shells, and sends the agent the exact quotes, line ranges,
the user's edits as a diff, and the plan as the user sees it.

- **Record.** The session owns it (`agent/session/plan_review.go`,
  `session_runtime_state` key `plan_review`, `conversation.PlanReview`). When a root
  turn that *started* in Plan mode finishes and its final reply contains a
  `<proposed_plan>` block (`conversation.ProposedPlan`), the session records revision
  N+1 before the turn's completion reaches any shell: title
  (`conversation.PlanTitle`), turn generation, the original proposal, and the file
  `<session dir>/plans/<slug>-v<N>.md`. Earlier open revisions become
  *replaced*. At most `conversation.PlanRevisionMaxCount` revisions are kept. The
  path is stored relative to the session directory; opening a revision
  recreates a missing file from the proposal, so a fork (which copies only the
  database) can open its plans. Children never record plans.
- **Editing.** **Review** (in the plan decision, the palette's **Review
  Plan**, or the bar's **Open revision N**) opens the file through the normal
  shared-document path, beside the session's chat: the pane to its right, a
  new split when there is room, else the chat's pane. Every editor feature
  works on it. The review bar above the editor states the revision and its
  state and offers **Implement**, **New session** (in a pane of 72 columns or
  more), **Revise** (send the edits; the agent revises the plan) and
  **Comment**, or **Send comments** and **Comment** while comments are
  waiting. A replaced or implemented revision says so and stays readable.
- **Comments.** **Ctrl+Alt+C** (editor menu **Add comment**) comments on the
  selection, or on the caret's line, in the shared editor text prompt; the
  same key on a commented range edits that comment, and an empty text deletes
  it. **Alt+F9** / **Shift+Alt+F9** go to the next and previous comment
  (VS Code's keys); clicking a card edits it. Anchors follow the W3C Web
  Annotation model (`conversation.TextAnchor`: quote with prefix and suffix context,
  byte position as a hint) and resolve with `crude/text/anchor`: exact quote
  occurrences scored by context and distance (Hypothesis), then a bounded
  diff-match-patch Bitap match near the hint, rejected beyond
  `PatchDeleteThreshold`. The editor shows each comment as a display-only card
  below the commented text and tints the range (see the editor reference). A
  comment whose text changed or was removed keeps its card near its old place
  and says **The commented text changed.** Comments are stored in the core at
  once (`agent.plan_comment`, idempotent by a shell-made ID), so they survive
  Esc, restarts and other shells. Sent comments are read-only and muted.
- **Settling** (`agent.plan_review_settle`) acts only on the latest open
  revision, with the plan text the user sees (the open editor's text, else the
  core's live document, else the file); a changed plan is saved first.
  **Revise** / **Send comments** sends `prompt.PlanReviewFeedback`
  (every unsent comment with its quote, current line range and a
  `text_changed` flag, the user's edits as a unified diff, and the plan); Plan
  mode stays on and the reply is the next revision. It needs a comment or an
  edit. **Implement** needs no unsent comments; it turns Plan mode off and
  sends `prompt.PlanImplementationRequest`, or `prompt.PlanImplementationEdited`
  with the approved text and diff when the user edited the plan. Approval
  binds that exact revision; a later proposal or comment changes the decision
  first. **New session** first creates a normal session. Settlement targets
  that exact destination. Its first durable input contains the plan as
  reviewed and references to verified copies of the revision’s evidence in the
  destination; that input and the destination's receipt commit in one
  transaction. Only accepted input closes the originating review and opens
  the new session's tab; the source remains in Plan mode. If the source cannot
  record the result, the review stays bound to that destination: the bar says
  **Check that the new session received the plan.** and **Check** compares the
  destination's receipt with the bound input. It never sends the plan again or
  to another session. A matching receipt closes the review; no receipt means
  the plan did not arrive, so the choice opens again. While bound, the review
  takes no comments and no other action. The transcript shows a short summary
  of feedback, not the full prompt.
- **Delivery.** Feedback and approval enter the session's durable input
  queue in the same SQLite transaction as the settled record (and, for
  approval, Plan mode off), and Plan mode is published before the queue
  dispatches; the shell sends nothing itself. They are admitted only while
  the main agent is ready: no active turn or compaction, no older queued
  message, and no stopped run waiting for recovery (**The agent has work in
  progress. Wait for it to finish, then try again.**). A refused or failed
  admission leaves the record, its comments and Plan mode unchanged.
- **Decision.** While comments wait, the chat's decision becomes **Send your
  comments?** with **Send comments**, **Review** and **Keep planning**. Review
  is an answer that stays: it opens the plan and keeps the question open
  (`decision.Answer.Stay`).

## Goals (`/goal`)

A goal is one objective the session's main agent pursues across turns until
an independent verifier confirms it, or the user or a stop rule ends it.

- Commands: `/goal` opens a scrollable, selectable status snapshot with the full
  objective, state, reason, usage and management commands; `/goal <objective>` sets it and
  asks **Replace the current goal?** while an unfinished one exists; `/goal
  pause`, `resume`, `clear`; `/goal edit` puts the current objective in the
  composer and `/goal edit <objective>` changes it without restarting. The
  header shows **Goal** with its state (checking, waiting, paused, blocked,
  limited by budget, complete); clicking it opens the same reader. Reopen the
  snapshot to refresh it. The reader closes on session switch and never consumes
  a composer draft. Goals take text, not composer attachments; an attempted
  attached goal is refused with its draft intact.
- State: one row per session in `session_goal` (objective, status
  `active|paused|blocked|budget_limited|complete`, reason, tokens, token
  budget, active time, waiting, running verifier). Shells read it through
  `AgentSnapshot.Goal`; changes are core owner jobs (`agent.goal`) and appear
  as status rows in every shell (see Status rows).
- Continuation: the orchestrator's own run loop starts the next goal turn when
  the agent is idle, after collaboration deliveries and queued human input,
  with no pending recovery or compaction, and never in Plan mode. The turn's
  input is context-only runtime input (`prompt.GoalContinuation`), never a
  human message in the transcript. Plan mode turns do not count toward goal
  time or token budget; a mid-turn mode toggle takes effect at the turn boundary.
- Stop rules: Stop on a goal turn, or stopping its unfinished run, pauses the
  goal; a terminal turn failure blocks it with the failure's exact reason (a
  permanent provider verdict such as a policy refusal ends the turn rather than
  waiting for Retry; see [run recovery](run-recovery.md)); three consecutive automatic turns
  that are empty, or whose tool calls all failed, block it. A turn parked for
  recovery changes nothing: recovery owns it and no goal turn can start until
  it settles (Retry, Stop, or a new human message, which continues it). An ordinary reopened session never restarts work by itself: an active
  goal waits until the user resumes it or sends a message, and the session
  offers **Resume the goal?** for waiting, paused and blocked goals.
- A separately authorized [checkpointed core restart](core-restart.md) can consume
  one private, exact-goal continuation permit after a successful natural boundary.
  It releases only its own handover wait through the normal runtime scheduler,
  never broad user resume. New input, goal/settings/mode/selection changes, expiry,
  recovery or failed settlement revoke that authorization. Cold open without the
  permit stays passive, forks drop it, and consumption cannot override a later
  user pause or start twice.
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

## Scheduled loops (`/loop`)

`/loop 5m <prompt>` schedules an explicitly supplied task in the selected
session's retained core. It starts once as soon as that agent is eligible, then
no more often than the fixed interval after each admitted start. Intervals use
Go duration notation (`1m`, `90m`, `2h`) or whole days (`1d`–`7d`), with a
one-minute minimum and seven-day maximum. Up to ten schedules may coexist.
This is fixed-interval scheduling, not Claude Code's dynamic `/loop` or a
general cron service; a bare `/loop` inspects rather than invents a task.

- `/loop` and the header's **Loop** label open a scrollable snapshot with full
  prompts, exact IDs, next times, expiry, state and reasons. The header reports
  when active schedules are waiting for Plan mode or an active goal. Reopen to
  refresh.
  `/loop pause <id>`, `/loop resume <id>` and `/loop clear <id>` address exactly
  one schedule; use `/loop` to find its ID. Pause and clear prevent future
  iterations but do not cancel a turn already admitted. Esc/Stop of a running
  loop iteration pauses that loop. Resuming schedules the next run one interval
  later rather than replaying old occurrences. If persisting a stop fails, the
  loop stays visibly paused in this runtime and the error asks for a retry;
  a cold reopen also restores it paused.
- The session owns the schedule, persisted in its own database and replicated
  through `AgentSnapshot.Loops`; shells only issue `agent.loop` actions and
  render snapshots. Scheduled input is attributed as context-only runtime input,
  not a new human message. The transcript records the task when scheduled and
  identifies each iteration by ID, plus pause, expiry and clear. Usual session
  permissions, approvals and recovery apply; scheduling does not grant additional
  authority. Attachments are not supported and an attempted attached loop retains
  the original draft.
- Human and collaboration input, compaction and recovery retain their existing
  admission priority. Due iterations wait while the agent is busy, while Plan
  mode is on, or while an active goal owns automatic continuation. Missed
  intervals coalesce into one iteration; there is no backlog of repeated calls.
  When several schedules are due, the oldest deadline is admitted first.
  A stopped or failed iteration pauses the schedule. A parked recovery retains
  ownership until it is continued, stopped or answered by a new human message;
  a parked loop iteration then pauses the schedule like a stopped one.
- Active loops keep the core from being considered idle even with no shells
  attached. They run only while the owning session and core are running; they
  are not machine-wide jobs. Schedules expire after seven days and require a new
  `/loop` to continue. On a cold session reopen they are restored paused, with
  no catch-up; a fork never copies schedules. The scheduler durably advances an
  occurrence before admitting its turn: a crash at that boundary can miss that
  occurrence, but it is not blindly replayed. As with any model work, scheduled
  turns can incur token charges.

## Status rows

Plan mode, YOLO, goal and loop changes are transcript rows owned by the session,
never by a shell: the session stores each in `session_status_rows` and sends
it as a `status_row` event (`recordStatusRow`), so every shell shows it and a
reopened session hydrates the same rows in time order (`statusRowView` serves
both paths). A row is recorded only when the value changes; goal rows derive
from the last goal published (`goalTransitionRow`), so accounting updates and
a reopen add none. Session facts (`status_row`, `goal`, `loops`) apply whatever
state the agent's turn is in.

## Fork (`/fork`)

- `/fork` continues the selected conversation in a new saved session, opened
  in its own tab; the source is unchanged. It is refused while a turn,
  compaction, unfinished-run recovery or child agent is in flight, because a
  copy would duplicate that ownership.
- `sessiondb.Fork` copies the live database with `VACUUM INTO` (a consistent
  snapshot) under the source's turn locks, rewrites the session identity and
  the root context key, gives the fork its own agent name, and drops
  runtime-owned rows: recoveries, collaboration admissions, file claims, child
  results, billed usage, queued input, schedules and the composer draft. Plans, settings,
  references, Plan mode and the selected execution are kept; execution hygiene
  scans every session database, so the shared checkout stays protected.
- An active goal is kept but waits for the user in the fork.
- The core method is `session.fork`; it runs through the same lifecycle job
  as `session.open`.
