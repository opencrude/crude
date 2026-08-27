# Chat Projection and Planning Overhaul Handoff

## Purpose

This document is the implementation handoff for two related workstreams:

1. Finish the semantic chat-projection overhaul currently partially implemented
   in the working tree.
2. Replace the current fragile planning micro-tools with a typed, declarative,
   atomic plan service that models can reliably edit, reorder, skip, reopen,
   pause, resume, and cancel.

The user will return to the original review agent after another implementation
agent finishes this work. Preserve the decisions and acceptance criteria below;
do not substitute a generic card UI or a new rendering architecture.

## Repository Snapshot

- Branch at handoff: `master`
- Base commit before this work: `755dac0 fix: lots of small ones`
- Architecture instructions: root `AGENTS.md`
- Rendering architecture: one root `ui.Host`, one shell surface, retained
  per-session chatprojection renderers.
- Validation baseline:
  - `go build ./...`
  - `go test ./...`
  - `golangci-lint run ./...`
  - `scripts/tui_design_audit.sh`
  - intentional TUI captures through `scripts/tuishot.sh` only

At the point this handoff was written, full tests, build, lint, and the TUI
design audit all passed.

## Non-Negotiable User Requirements

### Information hierarchy

- Live chat and hydrated/resumed chat must render the same semantic shape.
- Show information needed for immediate judgment.
- Hide repetitive, protocol-level, and low-value output behind disclosure.
- Never discard raw output. Expansion must reveal retained content.
- Preserve text selection and copy behavior.
- Preserve current preparing / doing / done tool phases for every tool family.

### Visual language

- Keep Crude's existing visual identity:
  - `│` block gutter
  - `●` state dot
  - muted/result semantic colors
  - no popup-card chrome for ordinary transcript items
- Do not add a redundant result-prefix row or extra indentation.
- Use the approved native diff-body treatment:
  - full-row addition/deletion background tints
  - compact line-number gutter
  - clear `+` / `-` indicators
  - minimal context and elision
- Thinking must be visually non-nested:

```text
│ ● Thought for 9s ▾
│   Retained reasoning body...
```

Never render:

```text
│ ● Thought for 9s ▾
│   │ ● Thought
│   │   Retained reasoning body...
```

### Thinking lifecycle

- Active reasoning starts with `Thinking`.
- Completed reasoning collapses to `Thought for Ns ▸`.
- It is clickable and keyboard-expandable.
- Expanded state uses `Thought for Ns ▾` and body-only reasoning rows.
- Never display `Thought for 0s`; use `<1s` or omit duration when unavailable.
- A promoted thinking pane has one lifecycle owner: the parent pane.
- The retained nested renderer is allowed internally for wrapping/cache/scroll,
  but must be body-only and must not apply another Thinking/Thought lifecycle.

### Turn lifecycle and control

- Preparation and transport progress live in the chat timeline, never in the
  bottom-left system status: `│ ● Getting next turn ready · ↑ 24 KB / 96 KB ·
  25%` transitions in place to `│ ● Waiting for <provider> · Ns`.
- The transient lifecycle row is workspace-owned live state with one stable ID.
  A direct tool response promotes that same row into the streamed tool row;
  thinking/text consumes it. It is never persisted into completed transcripts.
- The bottom-left status is reserved for compact aggregate agent counts and IDE
  health. It is static, with attention signs reserved for required user action
  and failures; it must not show provider waits, file paths, commands, or witty
  per-agent activity phrases.
- Escape on a tool permission prompt cancels the turn. It must not send or
  record `deny`; denial is only the explicit Deny button / `n` action.
- Permission decisions are backend lifecycle events (`allowed`, `always`,
  `denied`) attached to the canonical tool row and persisted with its display
  metadata, keeping live and hydrated permission tags identical.
- YOLO is session-scoped and read dynamically at every permission boundary, so
  `/yolo` affects the already-running turn. Tab switches reactivate the owning
  backend session before any slash command is routed.
- `/undo` and `/compact` capture their agent/session owner, execute off the UI
  thread, and reject overlap with an active turn.

### Commands

- Commands and similarly important evidence use the available width up to the
  scrollbar footprint.
- Wrap instead of clipping meaningful text.
- Use a generous default vertical budget (approximately two-thirds of the
  visible chat viewport).
- Long command previews retain both setup and outcome: head + explicit elision
  + tail.
- Errors remain visible without requiring expansion.
- Expansion reveals every retained line.

### Edit/write

- Edit title retains current Crude phase/title/count format.
- Edit body is a compact native diff with full-row green/red bands.
- No markdown fences, `---`/`+++`, or `@@` headers in projection.
- No inner ` │ ` separator between line number and sign/content.
- Context rows are untinted.
- Wrapped continuation rows retain the same band and blank number/sign gutter.
- Write defaults to title-only; expansion reveals the additions diff.

## Layer Boundaries

Do not violate these while finishing the work:

- `workspace/` owns presentation-neutral state and events. It must never import
  `crude/tui`.
- `tui/` owns wording, colors, width policy, previews, disclosure, hit-testing,
  and painting.
- TUI calls workspace methods directly; workspace notifies TUI through events.
- Do not add app-layer callbacks.
- Do not reintroduce frame-level main-content dirty caching.
- Width calculations must use the existing grapheme-width path.
- Hard-coded colors belong only in `tui/theme`.

## Current Partial Implementation

The working tree already contains the following implementation. Review it
before replacing anything.

### Disclosure state

File: `tui/chatprojection/disclosure.go`

- `Renderer.expanded` stores TUI-only per-message expansion state.
- Deterministic defaults:
  - active/interrupted thinking: expanded
  - completed thinking: collapsed
  - subpane placeholder: collapsed
  - completed tools: preview/collapsed mode
- `ToggleDisclosure` invalidates one retained item.
- `ToggleLastDisclosure` supports keyboard activation.

Disclosure state is intentionally not persisted. Live and hydration parity
comes from deterministic defaults plus identical canonical message data.

### Disclosure input routing

Files:

- `tui/chatprojection/message_row.go`
- `tui/screens/shellchat/selection.go`
- `tui/screens/shellchat/selection_test.go`
- `tui/viewmodel/activity.go`
- `tui/viewmodel/keyboard.go`
- `tui/ui/action_registry.go`

Implemented behavior:

- `MessageRow.Toggle` and `ToggleLine` mark a real disclosure row without
  abusing the integer zero value.
- A click without drag on a disclosure row emits `chat.toggle-details`.
- Dragging still performs text selection/copy.
- Viewmodel routes the action to the active renderer.
- `Ctrl+Alt+E` toggles the newest expandable block.

Remaining verification:

- Confirm pointer capture does not remain after a toggle.
- Confirm click and drag behavior at non-zero scroll offsets.
- Confirm expanding/collapsing a row keeps its header visually anchored.
- Add a direct viewmodel keyboard test for `Ctrl+Alt+E`.

### Non-nested thinking presentation

Files:

- `tui/chatprojection/message_render.go`
- `tui/chatprojection/renderer.go`
- `tui/viewmodel/subpane_render.go`
- `tui/chatprojection/subpane_build.go`
- `tui/rendering/format.go`
- `workspace/subpane_view.go`

Implemented behavior:

- Completed inline thinking renders one `Thought ... ▸` row by default.
- Expanded inline thought renders the same row with `▾` plus retained body.
- Active inline thinking renders one explicit `Thinking` header plus body.
- PaneThinking projections set their retained renderer to body-only mode.
- Parent pane owns collapse/expand and Thinking/Thought chrome.
- Sub-second durations use `<1s`.
- Completed subpane summaries include the disclosure glyph.

Known follow-ups:

- Visually verify active inline `Thinking` and promoted-pane `Thinking` use the
  same spacing, role colors, and elapsed-duration wording.
- Verify interrupted thinking copy/strike behavior after expansion.
- Ensure hydrated historical reasoning with unavailable duration shows
  `Thought ▸`, never a fabricated duration.
- Confirm no nested `│ │` gutters in all widths.

### Hydration parity

Files:

- `workspace/bootstrap.go`
- `workspace/message_view.go`
- `workspace/message_view_test.go`
- `tui/viewmodel/message_view.go`

Implemented behavior:

- Hydrated tool messages retain the actual provider tool-result body instead of
  replacing it with `toolName + args`.
- Tool metadata still carries name, args, status, duration, counts, phase, and
  subpane IDs.
- Thinking duration uses persisted duration when available, otherwise a bounded
  next-message timestamp heuristic.
- User reference captions continue using display-only metadata.

Remaining verification:

- Compare live and resumed fixtures for command, grep, edit, write, plan,
  thinking, error, cancellation, and subpane messages row-for-row.
- Ensure the next-message duration heuristic never crosses turn/session
  boundaries or includes a long tool wait. If unreliable, persist explicit
  thinking duration instead and remove the heuristic.
- Add one hydration fixture containing every major message family.

### Command/search/memory preview policy

File: `tui/chatprojection/tool_rows.go`

Implemented behavior:

- Plain tool output wraps using the chat's inner width rather than full terminal
  width.
- Repeated blank lines collapse in preview mode and remain intact when expanded.
- Command-family long previews use head + elision + tail.
- Command-family budget remains approximately two-thirds of chat height.
- Search/web/list previews use a smaller maximum.
- Memory/history previews use a smaller maximum.
- Expansion renders all retained rows.
- Common absolute directory prefixes are still stripped with a visible header.

Current budgets:

- command/async/javascript evidence: `TerminalResultHeight`
- grep/glob/list/web/arxiv: maximum 10 rows
- memory/history: maximum 8 rows
- generic: maximum 12 rows

Remaining work:

- Derive the exact wrapping width from the same geometry consumed by
  `shellchat.LayoutRow.ContentMax`; current implementation uses `innerW - 6`
  and needs a row-for-row width test with all three scrollbar/gutter columns.
- Improve command failure extraction: always retain exit summary, failure lines,
  and two context rows before/after each failure marker.
- Ensure ANSI/control sanitization does not remove meaningful indentation.
- Add fixtures for Go, pytest, npm, compiler errors, generic logs, and commands
  with long unbroken tokens.

### Edit/write diff presentation

Files:

- `tui/chatprojection/diff_rows.go`
- `tui/chatprojection/tool_rows.go`
- `tui/theme/theme.go`
- `tui/theme/theme_light.go`
- `tui/theme/theme_midnight.go`
- `tui/theme/theme_warm.go`
- `tui/theme/theme_high_contrast.go`
- `tui/ui/theme_tokens.go`

Implemented behavior:

- New `BuildBandedDiffRows` parses unified diff data and omits diff chrome.
- Tight gutter format: line number + space + sign + space + content.
- Additions/deletions use semantic foreground roles and full-row background
  bands after the outer Crude gutter.
- Context and ellipsis rows remain untinted.
- Continuation rows retain the band with blank number/sign columns.
- Preview is budgeted; expansion renders full diff.
- Write defaults to title-only; edit defaults to compact diff preview.
- All themes define addition/deletion background tokens.

Known limitations to address:

- Deletion rows currently rely on the parser's available line number; the
  structural parser may need both old and new line numbers for ideal gutters.
- Hidden-row counting currently uses structural row counts and may be inaccurate
  when hard wrapping creates several visual rows.
- Band padding uses projection width; verify it stops exactly before pane/main
  scrollbar gutters at 80/120/160 columns.
- Context trimming should be exactly two lines around hunks.
- Multi-file diffs need a compact, non-redundant file transition row.
- Write success with no diff payload needs a safe title-only fallback.
- Add visual and cell-level tests for every theme.

### Plan projection coalescing (temporary)

Files:

- `tui/chatprojection/renderer.go`
- `tui/chatprojection/message_render.go`
- `tui/chatprojection/tool_rows.go`

Implemented behavior:

- Historical plan-tool messages with a newer plan mutation are title-only by
  default.
- The latest plan message retains a compact body.
- Expansion reveals historical snapshots.
- Plan and memory bodies have preview budgets.
- Preparing/doing/done titles are preserved.

This is a temporary presentation patch. Replace it with the first-class
`PlanView` event model below when the planning service is implemented. Do not
add more string parsing to this coalescer.

## Remaining Chat Projection Work

### Phase 1: Stabilize disclosure behavior

Tasks:

1. Add keyboard-contract test for `Ctrl+Alt+E`.
2. Add mouse tests at top gravity, non-zero scroll, and nested-pane widths.
3. Add collapse-after-expand tests for thinking, write, read, command, diff,
   memory, and plan messages.
4. Ensure expanded state survives content streaming updates with stable IDs.
5. Purge expansion entries when messages are removed to avoid map growth.
6. Preserve viewport/header anchoring when item height changes.
7. Ensure hidden/removed session renderers cannot receive stale toggle actions.

Acceptance criteria:

- Click-without-drag toggles exactly one block.
- Drag on the same row selects text and never toggles.
- Keyboard and mouse produce identical state.
- Toggle re-renders only the target item.
- No stale pointer capture remains.
- The clicked header moves by at most one row after reflow.

### Phase 2: Introduce a universal block presentation contract

Create a projection-level type such as:

```go
type BlockPlan struct {
    ID           string
    Header       PaintLine
    Preview      []PaintLine
    Full         func() []PaintLine
    Expandable   bool
    DefaultState DisclosureState
}
```

Do not add another widget tree. Adapt the current renderer/tool families to
produce `BlockPlan`, then keep `Renderer -> MessageRow -> shellchat VirtualList`.

Tasks:

1. Make exactly one function responsible for external block spacing.
2. Make header/preview/full/disclosure behavior consistent across families.
3. Replace family-specific truncation markers with one semantic disclosure row.
4. Keep raw body construction lazy for collapsed blocks.
5. Keep selection/copy plain-text mirrors parallel to native rows.

Acceptance criteria:

- Exactly one blank row separates adjacent blocks.
- No header repeats information already shown in its preview.
- Every omission states its count.
- No raw JSON appears in preview mode.
- Full expansion reveals retained content without data loss.

### Phase 3: Finish Thinking/Thought lifecycle

Tasks:

1. Use one parent lifecycle header for inline and promoted thinking.
2. Active: `Thinking [elapsed]` plus body-only rows.
3. Completed default: `Thought for Ns ▸`.
4. Expanded: `Thought for Ns ▾` plus body-only rows.
5. Interrupted: `Thought interrupted ▸`, warning role, partial body struck on
   expansion.
6. Empty: `Thought`, no disclosure.
7. Persist explicit thinking duration in neutral message metadata; remove the
   timestamp heuristic when available.
8. Confirm long expanded thoughts use one pane scrollbar and no nested chrome.

Acceptance criteria:

- No `0s` labels.
- No double Thinking/Thought headers.
- No `│ │` nested gutters.
- Live/hydrated collapsed rows are identical.
- Expanded body text and copy output are identical live/hydrated.
- Parent pane alone owns disclosure and scrollbar.

### Phase 4: Finish command/evidence presentation

Tasks:

1. Share exact `ContentMax` geometry between projection wrapping and shellchat
   painting rather than approximating it.
2. Classify important lines: exit, fail/error/panic, test summary, compiler
   location, warning.
3. For failures, compose preview from:
   - command/context line
   - important lines with +/-2 context
   - final process summary/tail
4. For successful long output, keep setup + summary + tail.
5. Preserve full raw output for expansion/copy.
6. Add explicit exit code/cwd/total-lines neutral metadata; stop inferring these
   facts from body strings.

Acceptance criteria:

- Short command output is shown completely.
- Long preview never exceeds family budget.
- Failure cause and exit state are visible without expansion.
- Wrapped rows never enter scrollbar/gutter columns.
- Expansion is complete and byte-equivalent for copy.

### Phase 5: Finish search/web/memory projection

Tasks:

1. Grep/search: group matches by file; first useful rows only; match count in
   title; shared directory prefix once.
2. Glob/list: compact one-row paths, directories visually distinct.
3. Web search: title/domain rows; omit repeated snippets by default.
4. Web fetch: first useful extracted paragraphs; raw extraction on expansion.
5. Memory/history: one row per result in preview; metadata/body detail on
   expansion; no three-row-per-hit explosion.
6. Use typed neutral result metadata where available instead of parsing text.

Acceptance criteria:

- Search preview fits its declared budget.
- At least the first useful result is visible.
- Result counts are correct.
- Repeated absolute prefixes and raw JSON are absent.
- Hydrated and live grouping/order match exactly.

### Phase 6: Finish edit/write diff implementation

Tasks:

1. Extend diff structure with old/new line numbers if required for deletion
   gutters.
2. Count hidden visual rows after wrapping, not structural input rows.
3. Verify band fill at all scrollbar/nesting combinations.
4. Keep addition/deletion indicators ASCII `+` / `-` for copy/color-blind use.
5. Keep context rows untinted and line-number role muted.
6. Use two context lines and `···` between separated hunks.
7. Write stays title-only by default; expansion reveals additions diff.
8. Failed edit/write shows error and hint, never a partial success diff.

Acceptance criteria:

- No fences/file headers/hunk headers in projection.
- Every changed row has correct foreground and background semantic roles.
- Background spans the row to the scrollbar gutter, no shorter and no overlap.
- Long changed lines wrap with continuous band and blank continuation gutter.
- Multi-file diff remains understandable and compact.
- Live/hydrated rows are equal.

### Phase 7: Remove temporary plan string coalescing

Complete the planning-domain work below first. Then:

1. Treat canonical `update_plan` / `get_plan` tool-result JSON as the single
   live/hydration source; do not emit a second synthetic plan event/message.
2. Render one plan card keyed by `plan_id`.
3. Do not append a full snapshot for each plan mutation.
4. Keep compact update events (preparing/doing/done) in transcript.
5. Hydrate canonical plan card from plan persistence.
6. Delete formatted-plan parsing from chatprojection.

## Planning Tool Overhaul

### Current defects

Current implementation uses:

- context keys `plan_steps`, `plan_step_statuses`, `plan_step_labels`, and
  `plan_goal_id`
- seven advertised planning tools
- duplicated execution code in `session/tools.go` and `session/subagent.go`

Observed defects:

- dependencies are stored but not validated/enforced
- result/reason fields are not durably recorded
- no edit/reorder/add/remove/skip/reopen/pause/resume/cancel operations
- failed steps count as terminal and may auto-complete the plan
- completion clears plan state and loses history
- blockers mutate every active step and resolving loses prior state
- updates may partially mutate before a later validation error
- schema says string while implementation accepts comma strings/arrays
- tool results are formatted UI text instead of structured protocol
- planning behavior has almost no direct tests

### Recommended tool API

Advertise only:

1. `update_plan`
2. `get_plan`

`update_plan` is declarative without forcing unchanged data to be repeated.
Creation requires objective and a complete ordered step snapshot. Existing
updates may omit unchanged objective/status/steps; whenever steps are supplied,
that array is the complete desired ordered step snapshot. This is easier and
safer than either seven micro-tools or a generic operation DSL.

Example:

```json
{
  "plan_id": "plan-123",
  "revision": 4,
  "objective": "Fix prompt-history persistence",
  "status": "active",
  "explanation": "Migration is unnecessary; verification moved earlier.",
  "steps": [
    {
      "id": "audit",
      "description": "Audit live and hydrated history flow",
      "status": "completed",
      "depends_on": [],
      "result": "Found persistence bypass in submit path."
    },
    {
      "id": "verify",
      "description": "Add regression tests before implementation",
      "status": "in_progress",
      "depends_on": ["audit"]
    },
    {
      "id": "migration",
      "description": "Migrate obsolete history files",
      "status": "skipped",
      "depends_on": [],
      "reason": "No obsolete format exists."
    }
  ]
}
```

Plan statuses:

- `active`
- `paused`
- `completed`
- `cancelled`

Step statuses:

- `pending`
- `in_progress`
- `completed`
- `skipped`
- `blocked`
- `failed`
- `cancelled`

Conditional top-level fields make metadata/status-only updates reliable across
models. A supplied complete step snapshot supports reorder, edit, add, skip,
reopen, retry, remove from scope, pause, resume, and cancel atomically.

### Typed domain model

Create a presentation-neutral domain package/service:

```go
type Plan struct {
    ID          string
    Revision    uint64
    Objective   string
    Status      PlanStatus
    Explanation string
    Steps       []PlanStep
    CreatedAt   time.Time
    UpdatedAt   time.Time
    ClosedAt    *time.Time
}

type PlanStep struct {
    ID          string
    Description string
    Status      StepStatus
    DependsOn   []string
    Result      string
    Reason      string
    Attempts    int
    CreatedAt   time.Time
    StartedAt   *time.Time
    FinishedAt  *time.Time
}
```

Service contract:

```go
type PlanService interface {
    Update(context.Context, UpdateRequest) (Plan, ChangeSet, error)
    Get(context.Context, string) (Plan, error)
    Active(context.Context, string) (*Plan, error)
    List(context.Context, string) ([]PlanSummary, error)
}
```

Both orchestrators and sub-agents must call the same implementation. Delete the
duplicated planning switch logic after canonical cutover.

### Atomic validation rules

Validate everything before mutation:

1. existing updates require matching `revision`
2. objective is non-empty and bounded
3. step IDs are stable and unique
4. dependency IDs exist
5. no self-dependency or cycle
6. `in_progress` dependencies are satisfied
7. completed may include result
8. skipped/blocked/failed/cancelled require reason
9. cancelling a depended-on step requires dependents to change in same update
10. terminal step reopening requires explanation
11. plan completion requires every step completed/skipped/validly cancelled
12. failed/blocked steps prevent completion
13. no automatic plan completion
14. entire request commits or rolls back

Revision conflict returns the canonical current plan.

### Blockers

Remove advertised `report_blocker` and `resolve_blocker`:

- step-specific blocker: step status `blocked` + reason
- global blocker: plan status `paused` + explanation
- human input: existing `question` tool
- resolution: normal `update_plan` status change

This preserves which step was blocked and avoids changing every blocked step to
`doing` during resolution.

### Persistence

Recommended first-class tables in the per-session database:

```text
plans
  id, session_id, objective, status, revision,
  explanation, created_at, updated_at, closed_at

plan_steps
  plan_id, step_id, position, description, status,
  depends_on_json, result, reason, attempts,
  created_at, started_at, finished_at

plan_events
  plan_id, revision, event_type, payload_json, created_at
```

One transaction performs revision check, validation, mutation, event insertion,
revision increment, and commit.

### Canonical cutover (no backward compatibility)

1. Add typed plan domain and validation tests.
2. Add transactional store.
3. Route main agent and sub-agent through the same PlanService.
4. Advertise only `update_plan` and `get_plan`.
5. Remove old planning tool definitions, handlers, context-key parsing, status
   formatting, resume migration, and migration tests.
6. Do not import pre-service plan state on resume; old plan state is unsupported.
7. Stop returning formatted plan boxes as protocol; return structured JSON.
8. Render canonical update-plan results as one coalesced TUI plan card.

### Planning tests

Required tests:

- create valid plan
- reject duplicate IDs
- reject missing dependencies
- reject cycles
- reorder without changing IDs
- edit description/dependencies
- add/cancel steps
- skip/reopen
- complete/reopen
- fail/retry
- block/unblock one step
- pause/resume/cancel plan
- explicit completion validation
- stale revision conflict
- rollback invalid update
- persistence round trip
- rejection of removed legacy tool names and absence of legacy state import
- main/sub-agent parity
- concurrent stale update rejection
- live/hydrated PlanView equality
- preparing/doing/done projection parity
- one-card coalescing

## Suggested Implementation Sequence for Next Agent

1. Run all gates and capture current baseline.
2. Add missing disclosure and hydration tests before further visual changes.
3. Finish exact width sharing and evidence previews.
4. Finish diff gutter/band correctness and captures.
5. Finish Thinking/Thought captures and parity fixture.
6. Introduce typed plan model and tests.
7. Introduce PlanService/store and canonical-only cutover.
8. Replace advertised tools and unify main/sub-agent paths.
9. Replace temporary plan transcript coalescing with canonical PlanView
   coalescing keyed by `plan_id` across both planning tools.
10. Run all gates and visual captures.

Do not combine the PlanService rewrite with unrelated editor/window changes.

## Validation Commands

```bash
go build ./...
go test ./tui/chatprojection ./tui/screens/shellchat ./tui/viewmodel ./workspace
go test ./...
golangci-lint run ./...
scripts/tui_design_audit.sh
```

Visual captures:

```bash
COLS=160 ROWS=40 FONT_SIZE=11 scripts/tuishot.sh /tmp/crude-chat-handoff ...
```

Always close/verify `CrudeShot` windows after capture.

## Definition of Done

Chat projection is done only when:

- every family has one header, one preview policy, and one expansion path
- commands use true available width and preserve important evidence
- edit/write bands and gutters match the approved design
- active Thinking and completed Thought are non-nested and clickable
- no `0s`, duplicate lifecycle label, nested gutter, or competing scrollbar
- preparation/upload/provider-wait uses one transient in-chat row that either
  transforms into a tool row or disappears before normal provider output
- the bottom status remains aggregate-only; per-agent lifecycle stays in chat
- Escape cancellation and explicit permission denial remain distinct
- plan transcript uses one canonical card
- live/hydrated fixtures are row-for-row equivalent
- text selection/copy remains correct
- full raw content remains available
- build/tests/lint/audit pass
- intentional captures have been reviewed

Planning is done only when:

- two advertised tools replace seven micro-tools
- updates are declarative, revisioned, validated, and atomic
- skip/reopen/edit/reorder/add/cancel/pause/resume all work
- dependencies and cycles are enforced
- results/reasons/history survive completion and hydration
- failed steps never auto-complete the plan
- main and sub-agent execution share one service
- one persisted PlanView drives live and hydrated TUI
- removed legacy plan tools/state are not imported or advertised

## Review Notes for the Returning Agent

The final review should focus on:

1. behavioral parity live vs hydration
2. raw-content retention under preview/collapse
3. scroll/selection regressions after item reflow
4. exact terminal-width handling with nested scrollbars
5. diff band width and color semantics across themes
6. plan atomicity, transition invariants, and canonical-only cutover
7. duplicated plan logic fully removed
8. no presentation parsing left in workspace/domain layers
