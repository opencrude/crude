# Component Authoring Guide

> Status: Current. Practical recipes for building TUI screens with the native `tui/ui` component system.

Practical recipes for building TUI screens with the native `tui/ui` component
system. See also [`control-usage.md`](../reference/control-usage.md),
[`surface-catalog.md`](../reference/surface-catalog.md), and
[`settings.md`](../reference/settings.md).

## Screen types

| Pattern | When to use | Examples |
|---------|-------------|----------|
| **Host paint screen** | Persistent docked chrome in the main layout | `shellheader`, `shelltabs`, `shellinput`, `shelltree`, `activitybar`, `activitylist`, `shellchat`, `termdock` |
| **OverlayHost + WindowFrame** | Floating dialogs and pickers | `sessionpicker`, `help`, `composer`, `findinfiles`, command palette, application/workspace settings, **floating terminals** |
| **PanelDeck** | Session-owned panels between messages and input; shell `ColorBg`, not popup fill | session settings, questions, tool confirm, YOLO, background jobs |

---

## Recipe 1: Host paint screen (docked shell)

Persistent shell surfaces paint into a dedicated compositor surface each frame
via `ui.Host`.

### Skeleton

```go
const ScreenID ui.ComponentID = "myscreen"

type Screen struct {
    ui.Base
    th     *theme.Theme
    tokens *ui.TokenSet
    // child controls or projection state
}

func New(th *theme.Theme) *Screen {
    if th == nil {
        th = theme.DefaultDark()
    }
    return &Screen{
        Base:   ui.NewBase(ScreenID),
        th:     th,
        tokens: ui.NewTokenSet(th),
    }
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) { s.tokens = tokens }
func (s *Screen) SetTheme(th *theme.Theme) {
    if th == nil { return }
    s.th = th
    s.SetTokens(ui.NewTokenSet(th))
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
    return cs.ClampSize(ui.Size{W: cs.MaxW, H: fixedRows})
}

func (s *Screen) Layout(_ ui.LayoutContext, r ui.Rect) { s.SetBounds(r) }

func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
    // Fill background from tokens, then paint children or projection text
}

func (s *Screen) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
    return ui.EventResult{} // or delegate to child controls
}
```

### Viewmodel wiring

```go
screen := myscreen.New(th)
host := ui.NewHost(th, screen)
host.RegisterTree(screen) // if screen has Children()
host.Resize(w, h)
host.Layout()
host.Paint(buf)
```

### Reference implementations

| Screen | Notes |
|--------|-------|
| `shellheader` | Projection-driven two-row paint; no child components |
| `shelltabs` | `Children()` returns `ui.Tabs`; screen paints separator row |
| `shellinput` | Binds `input.Model`; delegates `HandleShell` to TextArea + slash List |
| `shelltree` | Nested wrappers (`treeFilterField`, `fileTreeView`) + `ui.Tree` + `ScrollbarControl` |
| `shellchat` | `ui.VirtualList` + `ScrollbarControl`; RowPainter from compose path |

### Rules

- Use `ui.TokenSet` for all colors — never hardcode lipgloss hex in screens.
- `SetTheme` / `SetTokens` must propagate to every child control.
- Paint goldens: `host.Paint(buf)` + assert labels and accent FG (see `TestPaintGoldenCellBufferContract` in each screen package).
- Keyboard contracts live on the bound model or child control; table-driven tests in `*_test.go`.

### Geometry and terminal-width contracts

- A `Rect` may use only columns supplied by its parent. Clamp derived widths
  at zero; never turn an undersized constraint into a fabricated minimum
  width. Minimums such as the launcher's 20-column usability policy are
  visibility/adaptation gates, not permission to assign `W: 20`.
- Horizontal theme padding is a request, not guaranteed geometry. Per frame,
  use `effectivePad = min(themePad, ContentW/2)` and derive
  `InnerW = ContentW - 2*effectivePad`; normal widths retain the theme value,
  while every narrow width satisfies `2*PadLR + InnerW <= ContentW`.
- Child bounds are parent-local. `Paint(buf, originX, originY, ...)` receives
  the parent paint origin, so fill the component with
  `ui.FillRect(buf, originX, originY, component.Bounds(), ...)`. Do not add
  `Bounds().X/Y` to the origin and then pass those bounds again.
- Compute shell geometry once per frame and pass that result to projections,
  composition, paint, and hit testing. A chat renderer or tab model must not
  independently restore an old width floor.
- Measure, truncate, and pad text only with `tui/rendering` helpers.
  `rendering.TruncateVisual` preserves CJK, combining, emoji, and ZWJ
  grapheme clusters under the canonical terminal-column method.
- Chat composition uses the actual `InnerW` supplied by shell layout. When a
  markdown renderer's readable-body minimum does not fit, use the plain
  wrapping path and let native paint clip to the real row; do not widen it.

### Async result ownership

- A retained component pointer is not sufficient identity across close/reopen.
  Async intents must include an open epoch and monotonic intent sequence; the
  host adds its own request generation before starting I/O.
- Project-owned commands and callbacks also capture `workspaceSwitchGen`.
  Cancel their contexts at the boundary and reject every result whose
  generation no longer matches, even when the backend ignored cancellation.
- Never perform filesystem, clipboard, SQLite, subprocess, or recursive
  watcher work in `Update`. Return a `tea.Cmd` or use the generation-bound
  detached bridge when a callback signature cannot return one. Closing the
  owning component must cancel its pending request and release retained state.

---

## Recipe 2: OverlayHost + WindowFrame (floating)

Floating surfaces use `wm.FloatingWindow` + `ui.ComponentWidget` (Host
adapter) inside `ui.OverlayHost` policy. WM owns placement only; chrome is
self-drawn.

### Skeleton

```go
// Body screen (Host-ready component tree)
screen := sessionpicker.New(th)
widget := ui.NewComponentWidget(th, screen, preferW, preferH)

frame := ui.NewWindowFrame("myoverlay/frame", ui.FrameWindow, tokens)
frame.SetTitle("Title")
frame.SetContent(screen) // or wrap screen in a Stack

// viewmodel opens via OverlayHost
overlayHost.Open(ui.OverlaySessionPicker, fw, ui.ModalDialogPolicy(), onClose)
```

### Reference: sessionpicker

- `sessionpicker.Screen` is the body: sectioned `ui.List`, `ScrollbarControl`, empty/hints labels.
- `Children()` switches between empty-state and list-state component sets.
- Esc on screen root emits `CancelMsg`; Enter on list emits `sessionpicker.resume` action.
- `ComponentWidget` bridges Host paint into `FloatingWindow.contentSurf`.

### Click-outside policies

| Policy | Behavior |
|--------|----------|
| `CloseRoute` | Close overlay, route click through |
| `KeepRoute` | Stay open, route to editor |
| `Ignore` | True modal — suppress click |

Register per-overlay handlers with `OverlayHost.RegisterContentHandler`.

### Rules

- Product FloatingWindows use `WithDecoration(true)` to enable WM frame hit
  geometry. The flag paints no borders; `ui.WindowFrame` remains the sole
  visual chrome owner.
- Use `ui.WindowFrame` for every floating chrome; never hand-draw `╭─╮`.
- Close button hit region: `WindowFrame.IsOnClose(lx, ly)` / action `window.close`.

---

## Recipe 3: ui.VirtualList + RowPainter

Variable-height lists (chat messages, search results) use `ui.VirtualList` with
a custom `RowPainter`.

### Setup

```go
list := ui.NewVirtualList(AreaID, tokens)
list.SetRowPainter(myRowPainter)
list.Scroll().SetAutoScroll(true) // chat stick-to-bottom
scrollbar := ui.NewScrollbarControl(ScrollbarID, list.Scroll(), tokens)
```

### RowPainter signature

```go
func myRowPainter(buf *surface.CellBuffer, x, y, width int,
    item ui.VirtItem, localRow int, state ui.ControlState,
    ctx ui.PaintContext, tokens *ui.TokenSet)
```

- `item.Height` = rows for this item; `localRow` is 0-based within the item.
- Build `[]ui.VirtItem` with per-row or per-message heights; call `list.SetItems(items)`.
- Scroll offset is in **rows**, not item indices. Use `list.Scroll().StickToBottom()` after append when auto-scroll is armed.

### Reference: shellchat

### Reference: shellchat (agent chat)

- `chatprojection.Renderer` exports `MessageRow` slices and `PaneLineMap`.
- `shellchat.SetMessages` installs variable-height `VirtItem`s; `ComposeRow` reserves gutter **geometry only** (spaces).
- Native nested pane gutters paint via `ui.PaintScrollbarCell` after content in the RowPainter; main gutter uses `ui.ScrollbarControl`.
- **Paint-only scroll contract:** call `list.SetSelectionAnchorsScroll(false)` so user scroll offset is owned by `ScrollModel`, not re-derived from selection anchors on each paint-only sync (see `shellchat.New`).
- Main scrollbar drag, nested pane wheel/gutter drag, and mouse text selection live on `shellchat.Screen` via `ui.Host` pointer capture.

### Reference: findinfiles

- Results map to `VirtItem{ID, Height:1, Label, Payload}`.
- Selection/activate emits search-result actions.
- Query row: SearchField on the left, case/regex ToggleButtons on the right.
- Replace in Files is the same screen (`ModeReplace`) with a replace field and Replace/All on the right.

---

## Recipe 4: ScrollbarControl

Shared thumb geometry over any `ScrollModel` (VirtualList, Tree, List).

```go
sb := ui.NewScrollbarControl(id, scrollModel, tokens)
sb.Layout(ctx, ui.Rect{X: w - 1, Y: listY, W: 1, H: listH})
sb.Paint(buf, ox, oy, ctx)
sb.Handle(ctx, evt) // wheel, drag, page up/down
```

- Pair with list viewport: list gets `W-1` columns; scrollbar owns the final column.
- Nested chat subpane gutters: one column per visible nested pane, painted per-row via `ui.ScrollbarCell` / `ui.PaintScrollbarCell` (not ANSI in `ComposeRow`).
- `ScrollModel.ThumbBounds(viewport)` must stay within track — covered by `FuzzScrollModelOffsetClamp`.

---

## Recipe 5: theme TokenSet

All semantic colors and glyphs resolve through `ui.TokenSet`.

```go
tokens := ui.NewTokenSet(th)
fg := tokens.Color(ui.ColorFg)
accent := tokens.Color(ui.ColorAccent)
glyphs := tokens.Glyphs()
```

### Propagation

```go
func applyTokenSet(c Component, ts *TokenSet) {
    if c == nil { return }
    if setter, ok := c.(interface{ SetTokens(*TokenSet) }); ok {
        setter.SetTokens(ts)
    }
    if p, ok := c.(Parent); ok {
        for _, ch := range p.Children() {
            applyTokenSet(ch, ts)
        }
    }
}
```

`Host.SetTheme` rebuilds tokens and calls `applyTokenSet` on the root tree.
Screens must implement `SetTokens` / `SetTheme` and forward to children.

### Semantic color roles (common)

| Role | Use |
|------|-----|
| `ColorBg`, `ColorFg`, `ColorMuted` | Base text surfaces. Primary labels/values are `ColorFg`; inherited, accelerators, and other meta are `ColorMuted` (`TokenSet.MenuInk`) |
| `ColorAccent`, `ColorAccent2` | Active markers, chips, tab separator when focused, compact-form focused labels |
| `ColorHover`, `ColorSelection` | Mouse/keyboard highlight. Compact form focus uses `ColorHover`; lists keep accent-fill focus via `ListRowColors` |
| `ColorBorder` | Separators, tree gutter |
| `ColorPopupBg`, `ColorPopupBorder` | WindowFrame chrome |
| `ColorError` | Diagnostics, chip close marks |

### Testing theme switches

- Gallery: `TestDemoGalleryRuntimeThemeSwitchPreservesState` in `tui/ui/demo_test.go`.
- Product screens: call `SetTheme` then re-paint; assert accent FG still present.

### Activity-dot animation contract

`ui.ActivityDot(spinFrame)` is the only product activity-dot animation. The
`spinFrame` value is wall-clock Unix milliseconds (`time.Now().UnixMilli()`),
not a rendered-frame counter. Every active dot is synchronized to one cycle:

| Phase | Duration | Glyph |
|-------|----------|-------|
| Filled | 400 ms | `●` |
| Hollow | 400 ms | `○` |
| Full cycle | 800 ms | repeat |

Rules:

- Use `ui.ActivityDot`; never add a local `(frame/N)%2`, braille spinner,
  brightness pulse, color cycle, or terminal `AttrBlink` for product activity.
- Keep the semantic color unchanged between filled and hollow phases. Shape is
  the animation; changing both shape and color reads as flicker.
- Active chat lifecycle, thinking, tool, sub-agent, and live-session rows blink.
  Completed, idle, cancelled, and failed rows are static.
- Failed-attempt recovery is the square-shaped exception: `ui.RecoverySquare`
  shares the same clock and reserves its calibrated width while off. It uses
  `ColorError`; attention/interrupted/settled recovery is static. Expanded
  diagnostic rows stay cached, and only the compact header changes on ticks.
- The bottom status bar never blinks. Healthy aggregate agent/LSP reporting is
  quiet text; `⚠` is reserved for required user action and `✕` for failures.
- Editor, input, and terminal cursors are not activity dots. Their independent
  cursor timing and visibility contracts remain in their owning components.

Colors are semantic and theme-resolved. Default-dark values are references,
not values to hard-code in feature code:

| State | Semantic role | Default dark |
|-------|---------------|--------------|
| Preparing / cancelling | `ColorWarning` | `#d29922` |
| Waiting for provider / thinking | `ColorAccent` | `#f0883e` |
| Tool doing | `ColorAccent2` | `#a5d6ff` |
| Live-session list dot | list row ink (`ColorFg` default) | `#e6edf3` |
| Done | `ColorSuccess` | `#7ee787` |
| Failed | `ColorError` | `#ff7b72` |

Upload lifecycle progress reports bytes consumed from the encoded HTTP request
body (headers excluded): `↑ uploaded / total · percent`. Use
`rendering.FormatBytes` for B/KB/MB/GB labels. Provider wait rows use the
configured provider display name (`Waiting for <provider>`).

---

## Recipe 6: resizable two-pane split

Every two-panel layout uses `ui.SplitModel` + `ui.SplitDivider` (Host pointer
capture). Do not add viewmodel-owned drag flags.

**Two children:** wrap them in `ui.SplitPane` (it owns the model and divider).

```go
split := ui.NewSplitPane("editor/split", ui.Horizontal, left, right)
split.SetMinSizes(12, 20)
split.SetFraction(0.35)
```

**Custom multi-child columns** (settings rail, launcher, Find in Files, file
tree vs the rest of the shell): keep laying out your own children, bind a
shared model to a `SplitDivider`, and read `Resolve` / `HasFirstSize` from
`Layout`.

```go
model := ui.NewSplitModel(ui.Horizontal)
model.SetMinSizes(12, 20)
div := ui.NewSplitDivider("surface/divider", model)
div.SetColorRole(ui.ColorPopupDivider)
div.SetTransparent(true)
// Layout: first, gap, _ := model.Resolve(innerW) after a drag;
// otherwise keep the default first-pane size. Place `div` in the gap.
div.SetCurrentFirst(first)
```

Drag emits `split.resize` with `ui.SplitResize{ID, First}`. Screens that own
their geometry consume it (`InvalidateLayout`). The shell sidebar is the
exception: viewmodel applies the payload to `Tree.Width` so
`computeContentLayout` can grow the slot.

Horizontal splits paint `BorderV`; vertical splits paint `BorderH`. Hover and
drag use `ColorAccent`.

---

## Selection, prompts, and filter fill

Never paint `❯` (or `Glyphs.Prompt`) as a selection marker, list cursor,
tab marker, chat prefix, or input prompt. Selectable rows react with
hover background and selected/focused background only. Chat input has
no leading glyph and no leading space. File-tree expand/collapse still
uses `TreeCollapsed` / `TreeExpanded`. Path breadcrumbs still use
`BreadcrumbSep` (`▸`). Settings and dock titles use `ui.TitleBreadcrumb`
(` · `).

`NewFilterField` and `NewSearchField` always fill with `FilterSurface`
(`ColorPopupSubtleBg`). Do not set a filter to `ColorBg`. Sidebar
filters may override to `ColorSubPaneBg` after `SetTokens` if that
surface is already distinct from the list.

---

## Text editing controls and the clipboard

Text is edited by `ui.TextArea` (multi-line) and `ui.TextField` (single-line);
never hand-roll key handling or a selection model for a new screen. Both own one
shared capability contract (`ui.TextClipboardTarget`, `textClipboardChord` in
`tui/ui/text_clipboard.go`): Ctrl+C / Ctrl+Shift+C copy the selection, Ctrl+X
cuts the selection or the whole value, Esc drops the selection and stays
unhandled when there is none, and a mouse drag copies on release and drops the
highlight.

- A composite that wraps an editor (form field, filter, search, overlay pane)
  implements `TextEditorProvider` (`TextEditor()`) so context menus reach the
  editor through it. A composite that hides its children from host hit-testing
  (a form) also implements `TextControlAtProvider` (`TextControlAt`).
- Hosts add policy on top instead of re-implementing copy: the chat composer
  clears its draft on an unhandled Ctrl+C, and its Esc ladder is selection →
  draft → cancel the turn. `App.textClipboardMenuSection` builds the
  whole-text Copy/Cut context-menu items for any text control under a
  right-click; Cut is dispatched through the owning component so models stay in
  sync.
- Masked controls report `TextSecret()` and refuse every clipboard command.
- Large composer pastes emit a `PasteAttachmentEvent`; the viewmodel captures
  its session/agent/draft and stores it in a command. Do not call reference
  persistence from a text control or silently fall back to raw insertion.
  Pasted text uses the existing chip geometry and native text-overlay shell;
  file-backed storage is not a reason to route its click to an external app.
- Composer growth and paste promotion are independent. `input.Model` requests
  at most `MaxComposerTextRows` text rows; the shell additionally caps its full
  height at one third of available content. Measure/paint/mouse mapping share
  the exact text column and TextArea's scroll offset, not a duplicate wrapper.
  Ordinary short prose and small lists remain inline; promotion uses the
  substantial-overflow policy in the interaction matrix. Unchanged TextArea
  layout preserves explicit scrolling; edits/navigation or viewport changes
  alone bring the caret back into view.
- `TextArea` keeps logical rune offsets separate from wrapped display geometry.
  `WordWrap` omits continuation-leading spaces; a cursor inside that omitted run
  maps to the next visible segment's column zero, not an invalid source slice.
  Cursor visibility, wrapped navigation and paint share `cursorSegmentInfo`.
  Never trim the draft or move the logical cursor to repair this display gap.

---

## Checklist for a new screen

1. Define `ScreenID` and child `ComponentID` constants.
2. Embed `ui.Base`; implement `Component` (Measure, Layout, Paint, Handle, Semantics).
3. Use `TokenSet` exclusively for colors/glyphs.
4. Add `TestPaintGoldenCellBufferContract` via `ui.NewHost`.
5. Add keyboard/mouse contract table tests for primary interactions.
6. Wire in viewmodel with Host (docked) or OverlayHost + ComponentWidget (floating).
7. Delete legacy ANSI/widget path; shrink `bypass_manifest.json` allowlist.
