# Component Authoring Guide

Practical recipes for building TUI screens with the native `tui/ui` component
system. See also [`CONTROL_USAGE.md`](./CONTROL_USAGE.md) and
[`SURFACE_CATALOG.md`](./SURFACE_CATALOG.md).

## Screen types

| Pattern | When to use | Examples |
|---------|-------------|----------|
| **Host paint screen** | Persistent docked chrome in the main layout | `shellheader`, `shelltabs`, `shellinput`, `shelltree`, `shellchat` |
| **OverlayHost + WindowFrame** | Floating dialogs and pickers | `sessionpicker`, `composer`, `findinfiles`, command palette |
| **PanelDeck** | Session-owned panels between messages and input | settings, questions, tool confirm, YOLO |

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

func (s *Screen) Handle(ctx ui.EventContext, msg tea.Msg) ui.EventResult {
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

- FloatingWindow is always `WithDecoration(false)` — no WM borders.
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

---

## Recipe 4: ScrollbarControl

Shared thumb geometry over any `ScrollModel` (VirtualList, Tree, List).

```go
sb := ui.NewScrollbarControl(id, scrollModel, tokens)
sb.Layout(ctx, ui.Rect{X: w - 1, Y: listY, W: 1, H: listH})
sb.Paint(buf, ox, oy, ctx)
sb.Handle(ctx, msg) // wheel, drag, page up/down
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
| `ColorBg`, `ColorFg`, `ColorMuted` | Base text surfaces |
| `ColorAccent`, `ColorAccent2` | Active markers, chips, tab separator when focused |
| `ColorHover`, `ColorSelection` | Mouse/keyboard highlight |
| `ColorBorder` | Separators, tree gutter |
| `ColorPopupBg`, `ColorPopupBorder` | WindowFrame chrome |
| `ColorError` | Diagnostics, chip close marks |

### Testing theme switches

- Gallery: `TestDemoGalleryRuntimeThemeSwitchPreservesState` in `tui/ui/demo_test.go`.
- Product screens: call `SetTheme` then re-paint; assert accent FG still present.

---

## Checklist for a new screen

1. Define `ScreenID` and child `ComponentID` constants.
2. Embed `ui.Base`; implement `Component` (Measure, Layout, Paint, Handle, Semantics).
3. Use `TokenSet` exclusively for colors/glyphs.
4. Add `TestPaintGoldenCellBufferContract` via `ui.NewHost`.
5. Add keyboard/mouse contract table tests for primary interactions.
6. Wire in viewmodel with Host (docked) or OverlayHost + ComponentWidget (floating).
7. Delete legacy ANSI/widget path; shrink `bypass_manifest.json` allowlist.
