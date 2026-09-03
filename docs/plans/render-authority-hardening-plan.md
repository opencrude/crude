# Render Authority and Editor Scrolling Hardening

> Status: LANDED — single-authority frame coalescing, warm projection switches,
> shared cursor clock/color, document-wide width index, horizontal wheel input,
> and interaction/performance gates completed 2026-09-02.

## Problem

Rendering policy is split between Bubble Tea message cadence, `App.View`, WM
damage flags, projection invalidation, component-local blink clocks, and editor
viewport geometry. The pieces are locally reasonable but can fight:

- every mouse-motion mutation triggers a synchronous full frame, so queued drag
  coordinates are replayed at render speed;
- tab focus invalidates valid retained message caches, making a real transition
  intentionally cold;
- independent animation clocks drift or stop when another subsystem suppresses
  frames;
- editor horizontal extent changes with the visible row set;
- horizontal SGR wheel events are decoded but not consumed by the editor.

Performance tests allowed these behaviours because they budgeted work after it
happened rather than pinning interaction latency and cache authority.

## Research

- Scintilla separates horizontal scroll width from viewport rendering and
  exposes width tracking because measuring the whole document on every paint is
  expensive. Its tracked width grows from observed lines rather than changing
  with each viewport.
  <https://www.scintilla.org/ScintillaDoc.html>
- CodeMirror documents the opposite failure mode: viewport-only rendering can
  make the horizontal scrollbar resize as wide lines enter/leave view.
  <https://discuss.codemirror.net/t/horizontal-scrollbar-resize-issue/4592>
- Monaco exposes independent horizontal scrollbar/slider policy and consumes
  horizontal wheel input.
  <https://microsoft.github.io/monaco-editor/api/interfaces/monaco.editor.IEditorScrollbarOptions.html>
- SGR mouse buttons 66/67 are horizontal wheel events. The vendored Bubble Tea
  decoder already publishes them as `MouseWheelLeft`/`MouseWheelRight`.
  <https://terminfo.dev/modes/decset-1006-sgr-mouse>

## Single-Authority Architecture

### 1. Mutation is immediate; presentation is coalesced

Input handlers always apply the latest pointer coordinate immediately. One App
frame scheduler decides whether the following `View()` presents a frame:

- interactive/streaming: at most one frame per 16 ms;
- idle: one 400 ms tick (2.5 FPS, never zero-FPS locking);
- release, key, paste, resize completion, and explicit damage: immediate frame;
- one outstanding deadline command wakes the renderer for deferred final state.

Returning cached ANSI is a presentation decision only. It never discards,
queues, or replays model mutations. WM `NeedsRender` reports damage; it does not
own cadence.

### 2. Projection caches are source-version owned

Message caches are invalidated only by source/version, width profile, theme, or
explicit disclosure changes. Focus and tab selection never invalidate content.
Inactive projections remain warm. Animation invalidates only active animated
rows, never every projection.

`computeContentLayout` is pure geometry and never calls renderer setters.
`configureChatScreenWithLayout` is the single chat-geometry writer. The
renderer owns a generation-keyed `MessageRowsSnapshot`; shellchat replaces its
VirtualList only for structural/height/committed-width changes and applies
stable-height row updates by index.

Frame dependency is output metadata, not a role/type switch. A render that uses
the shared frame marks its item frame-sensitive; the renderer retains those IDs
and invalidates only them when the 400 ms phase changes. In-place lifecycle,
context-upload, tool-progress, and provider-wait metrics flow through canonical
Conversation source versions and update the exact projected row.

### 3. One frame clock

`App.SpinFrame` is Unix milliseconds from the scheduler tick and is passed
through `PaintContext`. Native TextField, TextArea, editor, terminal, streaming
cursor, and activity indicators derive phase from that clock. Components may
record an activity epoch but may not start independent timers.

Native text cursors are reverse-video block overlays using semantic
`ColorCursor` (mapped to the softer muted palette), preserving the underlying
grapheme and width. Terminal applications retain blink/steady timing but not a
different product cursor geometry.

### 4. Document-wide horizontal extent

The editor owns a revision-keyed line-width index. Buffer mutations update or
invalidate affected line widths; viewport paint only reads the cached maximum.
The horizontal scrollbar therefore represents the whole document, never the
current row or viewport. Width-profile changes rebuild the index lazily once.
Horizontal wheel buttons scroll X directly; Shift+vertical-wheel is a fallback
for terminals that encode two-finger horizontal gestures that way.

## Gates

1. A burst of 1,000 resize motions applies the final coordinate immediately and
   presents at most bounded interactive frames; release presents final state.
2. Cached chat rows remain cached across agent/editor/agent and agent/agent tab
   switches; transition render work is zero unless source/version changed.
3. Idle scheduler interval is at most 500 ms and cursor phase changes under
   idle cadence.
4. Every native cursor preserves grapheme/width, shares phase, and uses
   `ColorCursor`, not full foreground white.
5. Horizontal extent includes an off-screen widest line, stays stable while
   vertically scrolling, shrinks after that line is edited shorter, and rebuilds
   after width-profile calibration.
6. MouseWheelLeft/Right and Shift+WheelUp/Down change only horizontal offset.
7. Existing frame-work and allocation ceilings remain green; interactive frame
   coalescing gets a separate presentation-count budget rather than weakening
   idle render-work budgets.

## Acceptance

- no buffered drag playback;
- no cold chat tab switch without source damage;
- minimum 2 FPS idle cadence with low CPU;
- synchronized, softer block cursors;
- stable document-wide horizontal scrollbar and two-finger horizontal scroll;
- focused race, full tests, bench gate, lint, docs, and live drag/tab/editor
  captures pass.

## Landed Evidence

- `TestPointerBurstCoalescesPresentationWithoutBufferingGeometry`: 1,000 model
  mutations, latest clamped divider geometry applied, bounded presentations,
  forced final release frame.
- `TestSidebarResizeDefersChatReflowUntilRelease`: zero message-row renders
  during the captured drag and at most one session pass on release.
- `TestAgentToAgentSwitchKeepsBothProjectionsWarm`: zero message rows rendered
  on a source-unchanged switch.
- `TestDocumentWidthIndexIsIncrementalForOrdinaryEdit`: 10,000-line initial
  index, at most one line measurement after an ordinary edit, zero after a
  vertical scroll.
- `TestRenderDeclaredFrameDependenciesAnimateWithoutStaticInvalidation` and
  `TestAgentWindowDynamicRowsFollowFrameAndSourceDependencies`: rendered output
  declares animation dependency, all active circles advance in agent windows,
  and static rows remain retained.
- `TestLifecycleMetricSourceVersionUpdatesExactRow`: in-place context upload
  and progress metrics advance the exact row through Conversation versioning.
- `BenchmarkPointerMotionCoalesced`: about 12 µs, 9,835 B, 42 allocations.
- `BenchmarkAgentSwitchWarm_80Msgs`: about 234 µs, 70,612 B, 362 allocations.
- Dense 500-message resize frame: reduced from about 7.7 ms / 2.22 MB /
  20,500 allocations to about 0.34 ms / 21 KB / 262 allocations.
- One changed row stays near-constant from 50 to 500 retained messages
  (~0.49 ms to ~0.51 ms, with the same 2,277 allocation count).
- A committed width change with 500 rich twelve-line Markdown messages reflows
  viewport plus overscan in 0.95-1.32 ms instead of synchronously traversing
  all historical content.
- `make bench-gate` passes all leaf and scenario ceilings.
- `scripts/tuishot.sh dragburst:…` sent 500 divider motions; immediate and
  two-second-settled captures were identical at the final width.
- Live SGR wheel buttons 66/67 moved the editor horizontally in both
  directions without changing vertical position.
- A resumed 94-round real session received 500 divider motion events; the
  first post-release ordered frame and the two-second-settled frame had
  identical chat wrapping and viewport position (only the elapsed timer
  changed).
