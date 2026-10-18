# Shell Host and Frame Pipeline

> Status: Current architecture — the root Host, shell surface, floating/docked overlay ownership and per-frame repaint contract.

This document is the contract for how the persistent shell becomes a frame.
The underlying cell, surface, compositor and window-manager design lives in
[the compositing engine](compositing-engine.md); component authoring rules live
in [component authoring](../guides/component-authoring.md).

## Surface tree

The compositor uses a tree of `Surface` nodes. Each `Surface` has a
`CellBuffer` (flat `[]cell.Cell` array) and can have child surfaces
with z-ordering.

**Composition pipeline:**
1. `BeginFrame()` — clears root children + back buffer
2. Background renders into root surface
3. Window manager renders floating windows (each window has its own
   surface with a child content surface)
4. `Composite()` — flattens surface tree via `FlattenZOrder()`, blits
   all surfaces bottom-to-top into the back buffer using `surface.Blit()`
5. Blit compares old vs new back buffer, sends ANSI diff to terminal

## Cell types

- `Cell.Char` — the rune
- `Cell.Width` — measured display width (1+ for a primary, 0 for continuation)
- `Cell.FG` / `Cell.BG` — colors (`ColorDefault` = inherit during Blit)
- `Cell.Transparent` — if true, Blit skips this cell

## Blit semantics

`surface.Blit()` composites a source buffer onto a destination:
- Transparent source cells are **skipped** — destination shows through
- Cells with `BG.IsDefault()` inherit the destination cell's BG color
- Multi-column cells write all `Width-1` continuation cells automatically
- Continuation cells (Width=0) are skipped
- Occlusion is grapheme-atomic: writing over a primary OR continuation blanks
  the old span before the new cell is written. Never allow half an emoji to
  survive under a floating border.

## Floating window rendering

Each `FloatingWindow` has:
- A main surface (`fw.surf`) sized `w x h`; WM owns placement and z-order only
- A full-size child content surface (`fw.contentSurf`)
- Frame interaction geometry for focus, close, title drag, and resize hit testing
- `ui.WindowFrame` content for visual background, border, title, and close chrome

**Render flow:**
1. `fw.surf.Buffer().Clear()` — transparent blanks
2. `fw.contentSurf.Buffer().Clear()` — transparent blanks
3. `fw.Content.Render(fw.contentSurf, ctx)` — normally ComponentWidget paints WindowFrame + body
4. Content surface added as child of window surface
5. Compositor blits window surface then content surface onto back buffer

WM must never paint borders, titles, backgrounds, or close glyphs. All product
floating windows use `ui.WindowFrame`; `wm/decoration.go` no longer exists.

## Root Host and shell surface

The whole persistent shell is ONE component tree mounted on a single
`ui.Host` (`App.rootHost`) painted into ONE full-screen compositor surface
(`App.mainSurf`, z=0, re-mounted after every `BeginFrame`). The composite
is `shellRoot` (`tui/viewmodel/shell_root.go`, ID `"shell"`) whose slots
map to the shell screens:

```text
rootHost
└── shellRoot ("shell")
    ├── header     shellheader.Screen   — always; rect (0, 1, W, 2)
    ├── footer     shellfooter.Screen   — always; rect (0, H-2, W, 2)
    ├── sidebar    shelltree.Screen     — active when Tree.Visible
    ├── termDock   retained workspace bottom panel
    └── mainPanes  recursive Window-owned tab groups + SplitDividers
        └── group  own shelltabs strip + selected retained view
            ├── editor / chat / git review / empty pane / workspace home
            ├── input  exact session-family composer (one mounted owner)
            └── deck   exact session-family deckMount → retained deckHost
```

Rules:

- `View()` = bg fill → re-mount `mainSurf` → `configureShell()` (geometry
  via `computeContentLayout` + slot rects + screen projections) →
  `renderShellToSurface()` (unconditional `MarkLayout` + `Layout` → focus sync
  → `Paint`) → WM render → composite. `syncRootFocus()` maps `focusTarget()`
  to the root host's single focused component after the current content is
  registered, before its first frame paints.
- `renderShellToSurface` fills `mainSurf` with the theme background BEFORE
  the tree paints: unmounted slots (launcher replacing the editor, chat
  replacing the launcher, ...) must not leave their previous opaque cells
  behind — every cell not covered by an active slot shows the clean
  background (this fixed the editor→launcher gutter artifacts).
- Mouse targeting is `rootHost.Handle` with raw screen coordinates; the
  Host computes `LocalX/LocalY` from layout geometry. `classifyShellRegion`
  remains only for viewmodel POLICY (focus-stack pushes, status row,
  context menus). Host pointer capture is the ONE capture service — hidden
  slots cancel capture via `Host.Layout` `validatePointerCapture`.
- **Main-tab actions return keyboard focus to content.** Pointer selection,
  close and overflow browsing never retain `FocusTabBar`; only deliberate
  keyboard strip navigation keeps its heavy baseline until committed/exited.
  Release a close action before a dirty-close modal captures return focus,
  never at a late save acknowledgement. Background tab sync/completion must
  preserve explicit focus. Guarded by `tab_action_focus_test.go`.
- Editor keys route through viewmodel; pointers enter retained
  `shelleditor.Screen` with Host-local coordinates and capture. Deck panels keep their per-session
  `deckHost`; `deckMount` adapts paint (blit) and input (local coords).
- **Main panes have one selection/topology authority:** `window.Window` owns
  the recursive split tree, tab membership and selected tab per group. Moving
  tabs never clones resources. `mainPaneLayout` retains geometry/screens, not
  another active-view model. Every view has unique mounted component identity;
  focused-screen aliases are only consumers. Per-main-agent inline embedding
  registries isolate nested geometry/scroll. A family shares exactly one draft
  and deck; rebind all visible composers before pointer dispatch, never activate
  another session from Paint. Main tabs opt into shared drag/drop + reserved +;
  previews are theme-colored, non-mutating until release. Small grids retain the
  tree and show the focused group, with keyboard/header-menu navigation. Layout
  is process-local and resets on workspace switch, not persisted on resume.
  See `docs/reference/main-view-panes.md`; floating main-view pop-outs remain
  separate future work, not an excuse to bypass modal settings/decision policy.
- **Parent-local child rects:** `Host.paintNode` paints a child at (parent
  origin + child bounds) — child bounds are PARENT-LOCAL by contract. A
  shell screen that passes its absolute slot rect to a child's `Layout`
  double-counts the origin (the shellchat double-origin bug: chat content
  shifted right/down, and long conversations painted over the input prompt
  and footer). Children must be laid out with `{X:0, Y:0, ...}` rects.
  Guarded by `TestAgentView_ChatPaintsInsideSlot` /
  `TestAgentView_LongChatDoesNotOverflowInputOrStatus`.
- **Chat gravity:** short chat content anchors to the BOTTOM of the viewport
  (`ui.VirtualList.SetBottomGravity(true)`, set by `shellchat.New`) so the
  transcript remains attached to the input prompt. Guarded by
  `TestAgentView_ShortChatAnchorsToBottom`.
- **Input shell is clamped to the footer:** `computeContentLayout`
  caps the full composer at one third of its available column, with the text
  viewport growing from one to at most six rows. The remaining footer/deck/tab
  clamp still applies on tiny grids (one chat row minimum). The old layer
  surfaces masked oversized inputs with the status surface's higher z-order; the root host
  paints status FIRST in tree order, so an unclamped shell painted its rows
  over the status separator/content. The textarea scrolls internally within
  the clamped height. Guarded by
  `TestAgentView_LongInputDoesNotOverlapStatus`.
- **TextArea.Paint FillRect uses the PAINT origin, not the translated
  origin:** `TextArea.Paint` computes `ox = originX + r.X` /
  `oy = originY + r.Y` for its content, but `FillRect(buf, ox, oy, r, …)`
  re-adds `r.X`/`r.Y` internally, so passing `ox, oy` double-counts the
  origin and shifts the bg fill down by `r.Y` and right by `r.X`. For the
  chat input (bounds `r={X:pad, Y:1}` under a `shellinput` slot at row 56),
  the white `PopupBg` fill landed at row `56 + 2*1 = 58` (the status
  separator) and col `SidebarW + 2*pad + 2`, erasing the right portion of
  the divider and leaving a white band over the status content ("input
  pushes the right half of the footer and its separator out of
  screen"). `TextField.Paint` already passed `originX, originY` correctly;
  `TextArea` was the lone offender. Guarded by
  `TestAgentView_TextAreaBgDoesNotOverwriteStatus`.
- **Launcher workspace load is async** (2026-08-01): `openLauncher` paints
  immediately (last-known data) and loads `ListWorkspaceInfos` in a
  goroutine via `refreshLauncherAsync(cwd)` → `launcherWorkspacesMsg`
  (event-bus `Send`, generation-deduped by `App.launcherLoadGen`). The
  ~200ms DB scan must never block the update goroutine (back-to-launcher
  hotpath). Guarded by `launcher_async_test.go`.
- **Workspace home preserves its original action controls.** On wide grids New
  Session keeps the full-width four-row outline with the original cycling icon,
  project path and Enter hint. Beneath it, the folder-icon New Workspace control
  returns to the left pane; after one blank row, Skills and MCP split all remaining
  left-pane height equally while Recent workspaces stays in the right pane. On
  cramped grids the two action controls stack with one blank row, the capability
  pane hides, and Recent workspaces becomes full width. Do not replace this with
  a full-width resource grid or add a repeated workspace heading or duplicate
  Settings/Help/palette/session controls. Search always owns a separate row.
  Workspace names and paths share a two-row hit target with a non-interactive
  blank separator between entries. The Recent-workspaces guide is pinned to the
  pane bottom regardless of resource count. Retry lives beside the heading,
  never inside the search row.
  Named Skills distinguish enabled modes from live Loaded bindings; MCP shows
  actual connected or attention states, never cached discovery as connected.
  Runtime observation is cancellable and off-UI, with no transport startup or
  skill-file scan. Loading uses `ui.ProgressRule` ink motion and keeps the last
  usable listing; settled home stops fast ticks. Delete stays in the workspace
  context menu, previews saved-data consequences, and moves data to recoverable
  storage with Undo; project files are untouched. See the workspace-home section
  of `docs/reference/interaction-matrix.md`.
- `themeConsumers()` walks `rootHost` (SetTheme propagates tokens to all
  mounted screens via `applyTokenSet`).

## Floating versus docked overlay ownership

- `FloatingWindow` is the actual positioned compositor window. It owns its
  window/content surfaces and supports focus, movement, resize, and close hit regions.
- `OverlayHost` is the typed lifecycle and policy controller around
  `WindowManager`/`FloatingWindow`. It is not a competing visual window type and
  must not be removed when a floating window body migrates to native components.
- Workspace settings, file/workspace picker, Find in Files, composer,
  text/reference editor, palettes, menus, and LSP popups are essential floating IDE
  surfaces. Keep their `OverlayHost` + `FloatingWindow` shell while migrating only
  their legacy body content.
- `PanelDeck` is exclusively for session-owned docked content between messages and
  chat input: session settings, questions, tool confirmation, YOLO, and background
  job details. Those docks share the chat `ColorBg` — they are not popup cards.
- **Deck and tab-bar slots span the FULL content column** (`SidebarW..width`, no
  `PadLR` inset in their slot rects) so separator rules run edge to edge, same as
  the chat and terminal dock. The `PadLR` model lives inside the components:
  `PanelDeck.contentPad` and `shelltabs.Screen.Layout` self-derive it from
  `TokenSet.PadLR()` (clamped at half the width on narrow terminals) and inset
  only their tabs and content.
- **Full-bleed separator rules are configurable, parent-published:** each
  `PanelDeck.Layout` calls `applyRuleBleed` with the deck row geometry
  (`offsetX`, `width`) through the content tree (`ruleBleeder` interface on Form,
  ActionBar, screens with their own divider). Components expand rules ONLY when
  they opted in with `SetRuleFullBleed(true)` — `NewDockedForm`/`NewDockedActionBar`
  do it by default; floating popups keep rules bounded. The Form paints into a
  buffer that reserves the left spill (`off = -min(0, ruleX)`) and blits shifted,
  so rules can start in the left padding without moving field geometry.
- **Readable-width panels:** `PanelEntry.Width = PanelReadableWidth` (session
  settings) clamps content via `ui.ReadableWidth` (≥200 → 1/3, ≥110 → 1/2, else
  full), left-aligned in the padded column; the parent enforces it.
- **Compact form tables:** `planCompactTable` caps the label→value bonus
  (`TableMaxLabelGapBonus`) and spends the rest before the trailing meta column;
  a `TableMinGap` gutter always separates the last column from the nav chevron.
  `FieldState.Columns` are also accepted by `FieldCycle` as spacer slots so a
  cycle control can align with a specific shared value column (Multi Agent sits
  under · provider). Zero-width leading columns must never abort row painting —
  `paintTableCells` skips blank cells before the width guard. Single-line
  editor rows (`FieldText`/`FieldPassword`) participate in the same table:
  `compactColumnsRaw` exposes their value as one cell, and `compactEditorX`
  starts the retained `TextField` at the plan's first value cell (both in
  `Layout` and `Paint`), so every second-column value on a docked form — nav
  columns, cycle controls, and editable text — shares one column. Never
  reintroduce label-hugging control geometry for compact editor rows; narrow
  docks fall back to `controlX` only when the row cannot hold the table.
- **Form hover-focusing:** `Form.Handle` moves the field highlight to the row
  under the pointer on `EventMouseMotion` (decorative rows never take it), like
  menus and lists; an open dropdown owns hover first.
- The old `App.Overlay` inline tab system is deleted. Never reintroduce it or route
  floating windows through `PanelDeck` as a shortcut.

## Unconditional main-content repaint

The `m.contentDirty` flag and the main-content ANSI cache were deleted.
`View()` rebuilds the main content surface every frame. This is safe because
the editor's expensive syntax work is **prepared once per source revision off
the UI thread**, with compact document spans and lazy per-line cell maps.
Geometry has its own cache; scroll/resize never restarts the lexer. A warm editor
repaint is a cell fill plus a cached-color walk (see `BenchmarkEditorPaint_*`
in `tui/screens/shelleditor/` and [performance baselines](../reference/performance-baselines.md)).

**RULES:**
- Never reintroduce a frame-level dirty flag for the main content. If a
  future perf problem appears, add a benchmark first (compare against
  `docs/reference/performance-baselines.md`) and cache at the editor level.
- `computeContentLayout()` may be called every frame; it only computes
  shell geometry (slot rects, chat/editor/input/deck rects) — no ANSI and NO
  chat renderer setters or conversation export. `configurePaneChat` is the sole writer of
  chat renderer geometry; duplicate width writers caused dense-chat resize to
  invalidate every message twice per frame.
- `chatprojection.Renderer` owns the generation-keyed `MessageRowsSnapshot`.
  `shellchat.Screen` rebuilds its VirtualList only for structure/height/width
  changes and applies stable-height row deltas by index. Never export and
  republish the full conversation merely because a frame was presented.
- Committed chat width changes use dynamic-height virtualization: source and
  hydration invalidation remain eager, but width-only invalidation reflows
  viewport plus overscan and retains offscreen estimated heights until scroll
  exposes those rows. Never restore frame-time `EnsureHeights` for width-only
  changes; it caused multi-second release stalls on dense resumed sessions.
- Dynamic rows declare frame sensitivity in their rendered projection metadata;
  the renderer tracks those item IDs and invalidates only that set when the
  shared phase changes. Never restore role/tool-specific tick classifiers or
  broad tick invalidation. In-place metrics update through Conversation version
  changes and invalidate their exact row independently of animation.
- `m.cachedOutput` is presentation-only: `frameGate` reuses it while draining
  high-rate pointer/stream/PTY events, at most until one 16 ms deadline. Model
  mutations are never queued or dropped. Every presented frame still performs
  the unconditional root-shell repaint described above; idle ticks remain
  400 ms (2.5 FPS), never zero-FPS locking.
- `wm.FloatingWindow.contentDirty` is a separate, unrelated mechanism
  (window-surface invalidation) — do not confuse it with the removed flag.

## Popup frames

All product floating windows use `ui.WindowFrame`, built by the frame builders
in `tui/ui/*_frame.go` (confirm, context menu, list popup, command palette,
find/replace, text prompt) or by native screen packages:

```
╭─ Title ──────────────────────────╮
│  body content line 1             │
│  body content line 2             │
╰──────────────────────────────────╯
```

- Product `FloatingWindow`s use `WithDecoration(true)` to enable WM frame
  interaction geometry (close, title drag, border hover, resize). The flag
  paints nothing: the WM owns placement and interaction, and `WindowFrame` owns
  every visual chrome cell.
- Chrome is self-drawn by `WindowFrame` / `ComponentWidget` bodies; the WM never
  paints borders, titles, backgrounds or close glyphs.

## Popup click-outside behaviors

| Behavior | Meaning |
|----------|---------|
| `CloseRoute` | Close + route click through |
| `KeepRoute` | Keep open, route click to editor |
| `Ignore` | True modal — suppress click entirely |

---

## Popup positioning

- **`clampPopupPosition` exists** in `tui/viewmodel/mouse.go`. Use it
  for new popup placement.
- **WM uses screen-absolute (X, Y) coordinates.** Translate local-popup
  (X, Y) to screen (X, Y) using the floating window's `Position()`.

## Paint and pointer ownership pitfalls

- **Text painted with a default background inherits the fill already in its
  buffer** (`CellBuffer.WriteString`); only transparent cells defer to lower
  layers. Writing a literal default once punched through popup fills, leaving
  dark bands behind labels in floating windows.
- **A mouse release belongs to the window whose Host captured the pointer**,
  focused or not (passive popovers never take focus), and stops there; every
  release branch keeps the commands it accumulated.
