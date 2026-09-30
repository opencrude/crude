# End-to-End Reliability and Interaction Hardening Plan

> Status: ACTIVE - umbrella execution plan for the user-reported reliability,
> safety, projection, interaction, performance, transport, and local-history
> program. Update the progress table and acceptance evidence in the same change
> that lands each item.

## 1. Purpose

This plan turns the reported defects into one ordered engineering program. The
work must not become a collection of local visual fixes: shared controls,
domain ownership, persistence, live/hydrated parity, and fault handling are the
acceptance boundaries.

Canonical supporting documents remain authoritative for their areas:

- [Chat projection and planning overhaul](chat-projection-handoff.md)
- [Message-scoped undo and local history](undo-local-history-plan.md)
- [Component authoring](../guides/component-authoring.md)
- [Control usage](../reference/control-usage.md)
- [Interaction matrix](../reference/interaction-matrix.md)
- [Performance baselines](../reference/performance-baselines.md)
- [Terminal width](../reference/terminal-width.md)
- [Settings inheritance](../reference/settings.md)
- [Context compaction and runtime continuity](context-compaction-runtime-plan.md)

## 2. Progress

| Workstream | Status | Current evidence / next boundary |
|---|---|---|
| Context preservation and configurable compaction | LANDED, hardened | Provider-measured occupancy plus semantic deltas; connection-scoped capacities and output reserves; complete oversized-source chunk-and-reduce; progress-gated overflow recovery; four-stage atomic Aeron checkpoints; durable lifecycle and archive; passive resume; explicit 0 disables |
| Cumulative token header and BTW marker | LANDED | Session totals plus per-agent context; resume restores root and child totals; plain `total` labels; `✎` note marker |
| Chat color persistence | LANDED | RebuildItems now preserves parallel native rows; cache mismatch re-renders styled content; real 126-round session replay and work budgets pass |
| Progress-aware HTTP stall recovery | LANDED | Adaptive byte-progress monitor; upload/header/download blackhole retries; partial replay suppression/divergence safety; provider race suite passes |
| Configurable command severity and global danger notification | LANDED | Structural parser plus configurable threshold/script/unknown/rules; hard blocks immutable; background danger escapes editor/minimized state into persistent global attention |
| Shared scrollbar modernization | LANDED, hardened | One shared interaction/paint contract; page-on-track, configurable 2-3 cell pointer targets, Host capture outside bounds, layout+paint invalidation, authoritative-model synchronization; nested panes, terminal scrollback, and editor migrated; live Settings/sidebar/editor drags verified |
| Shared selectable/blinking inline inputs | LANDED, hardened | Shared undo/redo, selection replacement, Ctrl/Super shortcuts, word/document/page movement, copy/cut commands, double/triple/shift click, captured outside drag with autoscroll, wheel navigation, Unicode-width field geometry, and composite Form capture routing |
| Plan projection | LANDED | Short-description prompt/schema, completed strikethrough, active-step previous/current/next window |
| Edit/write projection | LANDED, hardened | Exact spaces and tab-stop indentation, syntax-colored changed rows, hunk context/ellipsis, multi-file transitions |
| Grep/read/search/command compact rows | LANDED | Compact title/count/duration rows; full operands and evidence only on disclosure |
| Editor latency/backpressure | LANDED | Single presentation authority coalesces pointer/stream/PTY bursts at 16 ms without delaying mutations; sidebar drag defers chat reflow to release; warm tab switches retain caches; interaction benchmarks/gates pass |
| Sidebar width and tab/prompt routing | LANDED, monitor | Shared configured width and window-authoritative active tab/prompt target |
| Horizontal editor scrollbar | LANDED, hardened | Whole-document retained width index, O(1) ordinary line updates, stable viewport-independent extent, native SGR horizontal wheel plus Shift-wheel fallback |
| Message-scoped local-history undo | LANDED | Full system in `undo-local-history-plan.md` |

## 3. Program Invariants

1. One domain authority per state: workspace owns business state, TUI owns
   retained presentation, and provider transport owns no UI semantics.
2. Live and hydrated views consume the same canonical message/state contract.
3. Background work cannot steal focus, tab selection, or prompt ownership.
4. No fallback may silently discard semantic styling, safety information,
   selection, or history.
5. Every asynchronous operation has owner, generation, cancellation, timeout,
   stale-result rejection, and user-visible failure semantics.
6. Every shared interaction uses the component system: no new feature-specific
   input engine, scrollbar, hit testing, or cursor timer.
7. Performance fixes require measurement and must not bypass correctness.
8. Dangerous actions require visible ownership and remain discoverable when the
   originating agent is minimized or the user is inside another IDE surface.

## 4. Execution Order

### Phase 1: Close current correctness regressions

- Make shell usage cumulative and explicit while context percentage remains the
  visible agent's current request occupancy.
- Remove the BTW emoji and use one calibrated text glyph.
- Repair chat native-cache reconstruction and delete every colorless fallback.
- Add real-session replay using canonical hydrated messages through:
  `syncAgentMessages -> configureChatScreenWithLayout -> ExportMessageRows`.
- Re-run idle work budgets to ensure correctness does not reintroduce churn.

Exit:

- Styled spans survive append, hydration, resize, theme, status churn,
  streaming transitions, cache invalidation, and item-list rebuild.
- Text/native row counts are equal for every rendered item.
- Header totals match durable cost snapshots and aggregate child usage.

### Phase 2: Progress-aware HTTP stall recovery

#### Contract

The transport must distinguish slow progress from zero progress. A wall-clock
request timeout is not sufficient.

Track independently:

- request-body bytes read by the HTTP transport
- response-header arrival
- response-body bytes delivered to the decoder
- monotonic timestamp of the last positive byte delta
- smoothed interval and variation between positive progress samples

Use a Jacobson/Karels-style adaptive deadline over inter-progress samples:

```text
error     = sample_gap - smoothed_gap
S         = S + alpha * error          (alpha = 1/8)
V         = V + beta * (abs(error)-V)  (beta = 1/4)
deadline  = clamp(S + 4*V + margin, floor, ceiling)
```

Initial startup/header deadline is separate and conservative. Recommended
defaults must be proven by live/fault tests, not guessed into permanence:

- startup/header floor: 60 seconds
- active transfer floor: 45 seconds
- ceiling: 5 minutes
- reset only when byte count increases
- very low but positive throughput never times out solely for being slow

These initial/minimum/maximum limits are now configurable in provider-template
and account Connection tuning. Settings and client construction reject
minimum > initial or initial > maximum instead of silently clamping configured
values. The overall request timeout is enforced across streaming and retry
waits as well as non-streaming calls, using the earlier of it and the caller's
deadline. Regression coverage includes real HTTP header/body pauses, account
inheritance, saved configuration, and resume.

#### Retry safety

- Reuse one logical request/idempotency key where a provider supports it.
- Before response bytes: retry from zero using normal retry policy.
- After partial response bytes: discard partial decode state before retry;
  never execute a partial tool call.
- Bound attempts and report the stall phase, bytes, silence duration, and retry.
- Cancellation always wins over retry.
- Upload and download monitors are independent.

Tests:

- slow byte every N seconds survives
- upload accepts bytes then blackholes without RST
- headers arrive then body blackholes
- partial NDJSON/SSE frame blackholes
- resumed transfer succeeds
- cancellation during stall exits immediately
- no duplicate tool execution after retry

### Phase 3: Safety policy and notification center

#### Command risk engine

Replace string-only shell severity with structured analysis:

- parse shell syntax into commands, pipelines, redirections, substitutions,
  executable paths, and argument vectors
- classify behavior, not suffix: executing `script.sh` is not inherently
  destructive; deletion, privilege, persistence, network exfiltration, device
  writes, package/system mutation, and broad workspace writes are
- support explicit allow/deny/severity rules by executable, subcommand, path,
  operation, and normalized command pattern
- show matched rule and reason in confirmation UI
- preserve unknown/parse-failed commands as configurable cautious severity

Settings require a dedicated Safety category:

- severity thresholds per operation family
- executable/subcommand overrides
- workspace-scoped trusted commands
- protected paths
- notification policy
- import/export/reset policy
- rule test field: paste command, inspect parse and effective decision

#### Global dangerous-action notifications

Implement workspace-owned attention events and a TUI notification center:

- modal for blocking critical danger
- persistent header/activity badge for pending confirmations
- originating session/agent name and command/path
- activate action jumps to owning agent and confirmation panel
- minimized/background agents remain visible
- editor/terminal focus cannot hide required attention
- duplicate events coalesce; resolution clears all projections

### Phase 4: Shared scrollbar and input foundations

#### Scrollbar

Extend `ui.ScrollbarControl` only.

- vertical and horizontal orientation
- track click pages toward pointer
- thumb drag with Host pointer capture outside bounds
- hover state widens visual affordance from padded `|` to active form without
  changing layout geometry
- calibrated one-cell glyphs and padding through terminal-width APIs
- on-demand visibility and configurable gutters
- shared empty-column contract for settings, Agents, and subagents
- editor horizontal scrollbar binds to horizontal viewport and appears only
  when content exceeds width

Hardening invariants learned from direct-manipulation failures:

- The painted stroke remains one cell, while Host hit bounds expand inward to a
  configurable minimum target. Explicit hit bounds outrank deeper content
  descendants, otherwise a nested list silently steals the larger target.
- Adjacent scrollbars must not overlap ownership. Main chat claims only its
  separator; nested-pane gutters expand toward pane content.
- Offset changes invalidate layout and paint. Some collections read offsets in
  paint, while retained forms apply offsets to child bounds during layout.
- A control that mirrors another authoritative offset must synchronize through
  `OnScroll`; exact child hits never depend on parent pointer bubbling.
- Mounted scrollbars receive input through Host. Composite/manual forwarding
  uses `ui.ChildEventContext`; editor pointer input now enters through its
  mounted shell screen so release and cancellation work outside the pane.
- Overflow decisions use the candidate viewport for the current layout, never
  the previous frame's `ScrollModel.Viewport`.

#### Inputs

Extend the shared TextEdit engine:

- mouse selection for every inline text input
- drag selection with pointer capture and horizontal autoscroll
- Shift/word/select-all parity
- focused active cursor uses one activity-gated blink service
- every text cursor is an overlay-only reverse-video block: preserve the
  underlying grapheme, never render a bar/underline, shift placeholder text,
  append a cursor glyph, or consume layout width
- first typed character replaces active placeholder/armed content where the
  product control requests fill-on-type
- clipboard and grapheme contracts remain shared

No feature-level input state machine is permitted.

### Phase 5: Canonical evidence projections

#### Plans

- Plan prompt/schema teach one-line step descriptions; the renderer enforces it:
  every step is exactly one row (whitespace collapsed, overlong text elided).
- Long notes belong in explanation/result, never description.
- The objective is never truncated: it wraps with a hanging indent and the
  status/count suffix trails the final row.
- Completed steps render check plus strikethrough description.
- Collapsed view is windowed around active work with a floor of five rows: the
  active step plus two neighbours on each side, widened at the plan's ends, plus
  concise counts for hidden earlier/later work.
- If no active step, anchor around the first pending, else latest touched, step.
- Structured planning failures render the red contract dot plus a one-line
  reason; the canonical JSON payload stays model-facing and never enters a row.
- Expansion exposes complete plan and history.
- Typed PlanView/store remains the target from
  `chat-projection-handoff.md`; do not add more formatted-string parsing.

#### Edit and write

- Parse canonical diff structure, not rendered strings.
- Preserve indentation exactly.
- Expand source tabs through the shared grapheme-width/tab-stop path before
  diff width calculation, wrapping, highlighting, and cell painting.
- Syntax-highlight code with file language.
- Show changed hunks plus configurable context rows.
- Merge nearby hunks; insert semantic ellipsis rows between distant blocks.
- Multi-file operations show one compact file transition row.
- Background tint spans exactly to the scrollbar gutter.
- Expanded copy remains byte-equivalent to source diff/content.

#### Grep/read/search/command

Create one evidence-row grammar modeled on compact developer-tool timelines:

- action verb, target/query/command, concise count/status, duration
- exact operands preserved in disclosure/copy, not stuffed into title
- success/error/cancel semantics and permission tags consistent
- bounded collapsed preview, complete expansion
- wrapping respects content and scrollbar gutters
- live/hydrated output is row-for-row identical

### Phase 6: Editor latency and backpressure

Measure first:

- keyboard event enqueue-to-apply latency
- Bubble Tea queue depth by message class
- agent/token/status event throughput
- editor update time, paint time, and per-frame work counters
- profiler under synthetic key burst plus streaming/background notifications

Implementation direction:

- coalesce high-rate non-input notifications by owner/generation
- never coalesce key presses, paste, edits, permission, or lifecycle boundaries
- process a bounded notification batch per update and prioritize interactive
  input without reordering within each owner stream
- keep provider/session FIFO before TUI bridge
- move any remaining editor I/O off Update
- preserve unconditional shell repaint; optimize editor-internal retained data

Acceptance:

- burst test sends more key events than a human can produce and reaches the
  final buffer without a growing lag tail
- p95 enqueue-to-apply latency is recorded and reviewed
- existing editor paint/allocation and scenario work budgets pass
- agent streaming and safety notifications remain responsive concurrently

### Phase 7: Message-scoped local-history undo

Execute [undo-local-history-plan.md](undo-local-history-plan.md) in its phases:

- content-addressed immutable local history
- generated-content exclusions
- root user-message attribution propagated to children
- preview-first atomic restore and safety snapshot
- one `/undo` command
- per-user-message context menu
- restore appends context/history events without rewriting conversation history

## 5. Cross-Cutting Acceptance Matrix

Every phase must supply relevant evidence from:

- deterministic unit/property/fuzz tests
- race tests on changed ownership/concurrency packages
- fault-injection tests for I/O and partial failure
- live `glm-5.3-flash:cloud` tests only where provider behavior matters
- repository-native `scripts/tuishot.sh` captures at narrow/standard/wide sizes
- dark/light/high-contrast where semantic colors change
- live/hydrated row equality fixtures
- `go build ./...`
- `go test ./...`
- `golangci-lint run ./...`
- `make docs-check`
- `make bench-gate` and scenario work-budget tests for hot paths

## 6. Definition of Ready

An item may enter implementation only after:

1. Its current code path and owner are mapped.
2. Existing shared component/service capabilities are reviewed.
3. Persistence and resume behavior are specified.
4. Failure, cancellation, timeout, and stale-result behavior are specified.
5. At least one regression test fails for the reported defect.
6. Visual changes have an approved capture target.

## 7. Definition of Done

The umbrella program is complete only when every progress-table item is marked
LANDED with file/test/capture evidence, all cross-cutting gates pass, and no
temporary compatibility path silently discards state, styling, safety,
interaction, or history.
