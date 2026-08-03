package viewmodel

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
)

// shellSlotID identifies one mounted shell surface slot in the root tree.
// Slots are the persistent shell surfaces; each maps to exactly one screen
// component. Active slots are mounted as children of shellRoot (the others
// are not in the tree at all), so Host hit-testing, focus, and paint all
// agree on what is visible — the geometry that paints is the geometry that
// receives input.
type shellSlotID uint8

const (
	shellHeaderSlot shellSlotID = iota
	shellStatusSlot
	shellTabsSlot
	shellSidebarSlot
	shellChatSlot
	shellInputSlot
	shellEditorSlot
	shellLauncherSlot
	shellDeckSlot
	shellSlotCount
)

func (id shellSlotID) String() string {
	switch id {
	case shellHeaderSlot:
		return "shell/header"
	case shellStatusSlot:
		return "shell/status"
	case shellTabsSlot:
		return "shell/tabs"
	case shellSidebarSlot:
		return "shell/sidebar"
	case shellChatSlot:
		return "shell/chat"
	case shellInputSlot:
		return "shell/input"
	case shellEditorSlot:
		return "shell/editor"
	case shellLauncherSlot:
		return "shell/launcher"
	case shellDeckSlot:
		return "shell/deck"
	}
	return "shell/none"
}

type shellSlot struct {
	active bool
	rect   ui.Rect
}

// shellRoot is the viewmodel-owned root composite (ID "shell"). It mounts
// every persistent shell surface under ONE ui.Host (App.rootHost) and is
// painted into ONE full-screen compositor surface (App.mainSurf).
//
// Layout driver: the viewmodel keeps its geometry computation
// (computeContentLayout) and pushes per-frame slot rects via setSlot;
// shellRoot.Layout assigns each active slot's rect to its screen, and Host
// builds the single hit/focus/paint geometry from those bounds. Slot rects
// are screen-absolute because the parent origin is (0,0).
type shellRoot struct {
	ui.Base
	header   ui.Component
	status   ui.Component
	tabs     ui.Component
	sidebar  ui.Component
	chat     ui.Component
	input    ui.Component
	editor   ui.Component
	launcher ui.Component
	deck     *deckMount

	slots [shellSlotCount]shellSlot
	dirty bool
}

func newShellRoot(
	header, status, tabs, sidebar, chat, input, editor, launcher ui.Component,
	deck *deckMount,
) *shellRoot {
	return &shellRoot{
		Base:     ui.NewBase("shell"),
		header:   header,
		status:   status,
		tabs:     tabs,
		sidebar:  sidebar,
		chat:     chat,
		input:    input,
		editor:   editor,
		launcher: launcher,
		deck:     deck,
		dirty:    true,
	}
}

// setSlot updates a slot's active flag and rect. It marks the tree dirty
// when the composition or geometry changed so the host relayouts (and
// recollects the hit registry) on the next frame.
func (s *shellRoot) setSlot(id shellSlotID, active bool, rect ui.Rect) {
	if s.slots[id].active == active && s.slots[id].rect == rect {
		return
	}
	s.slots[id] = shellSlot{active: active, rect: rect}
	s.dirty = true
}

// ClearDirty reports whether the tree changed since the last layout.
func (s *shellRoot) Marked() bool { return s.dirty }

// ClearDirty acknowledges the last layout pass.
func (s *shellRoot) ClearDirty() { s.dirty = false }

// SetScreen replaces the component mounted for a slot. Used when a screen
// is re-homed (e.g. openLauncher recreates the launcher screen) or in tests
// that substitute a custom screen. Marks the tree dirty so the host
// recollects the new component on the next layout.
func (s *shellRoot) SetScreen(id shellSlotID, c ui.Component) {
	switch id {
	case shellHeaderSlot:
		s.header = c
	case shellStatusSlot:
		s.status = c
	case shellTabsSlot:
		s.tabs = c
	case shellSidebarSlot:
		s.sidebar = c
	case shellChatSlot:
		s.chat = c
	case shellInputSlot:
		s.input = c
	case shellEditorSlot:
		s.editor = c
	case shellLauncherSlot:
		s.launcher = c
	case shellDeckSlot:
		// deck mount is fixed; ignored
		return
	default:
		return
	}
	s.dirty = true
}

func (s *shellRoot) Children() []ui.Component {
	out := make([]ui.Component, 0, 8)
	appendSlot := func(id shellSlotID, c ui.Component) {
		if s.slots[id].active && c != nil && !s.slots[id].rect.Empty() {
			out = append(out, c)
		}
	}
	appendSlot(shellHeaderSlot, s.header)
	appendSlot(shellStatusSlot, s.status)
	appendSlot(shellTabsSlot, s.tabs)
	appendSlot(shellSidebarSlot, s.sidebar)
	appendSlot(shellChatSlot, s.chat)
	appendSlot(shellInputSlot, s.input)
	appendSlot(shellEditorSlot, s.editor)
	appendSlot(shellLauncherSlot, s.launcher)
	appendSlot(shellDeckSlot, s.deck)
	return out
}

func (s *shellRoot) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (s *shellRoot) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	layoutSlot := func(id shellSlotID, c ui.Component) {
		if c == nil {
			return
		}
		slot := s.slots[id]
		if slot.active && !slot.rect.Empty() {
			c.Layout(ctx, slot.rect)
		}
	}
	layoutSlot(shellHeaderSlot, s.header)
	layoutSlot(shellStatusSlot, s.status)
	layoutSlot(shellTabsSlot, s.tabs)
	layoutSlot(shellSidebarSlot, s.sidebar)
	layoutSlot(shellChatSlot, s.chat)
	layoutSlot(shellInputSlot, s.input)
	layoutSlot(shellEditorSlot, s.editor)
	layoutSlot(shellLauncherSlot, s.launcher)
	layoutSlot(shellDeckSlot, s.deck)
}

// Paint is a no-op: children are painted by Host.paintNode at their slot
// rects. The root fills the full-screen background in View().
func (s *shellRoot) Paint(_ *surface.CellBuffer, _, _ int, _ ui.PaintContext) {}

// Handle is a no-op: the Host dispatches to the hit/focused component. Keys
// that bubble here are deliberately unhandled so the viewmodel keeps its
// focusTarget-based routing.
func (s *shellRoot) Handle(_ ui.EventContext, _ ui.Event) ui.EventResult {
	return ui.EventResult{}
}

func (s *shellRoot) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Shell"}
}

var _ ui.Component = (*shellRoot)(nil)
var _ ui.Parent = (*shellRoot)(nil)

// deckMount mounts the ACTIVE session's retained PanelDeck Host into the
// root tree. Per-session deckHosts stay as they are (Phase 12 design):
// deckMount only adapts paint (deck surface blit at the slot rect) and
// input (EventContext locals → deck-local tea.Msg) so the deck participates
// in the one hit tree without rehoming its Host.
type deckMount struct {
	ui.Base
	app  *App
	surf *surface.Surface
}

func newDeckMount(app *App) *deckMount {
	return &deckMount{
		Base: ui.NewBase(ui.NewID("shell", "deck")),
		app:  app,
	}
}

func (d *deckMount) Children() []ui.Component { return nil }

func (d *deckMount) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (d *deckMount) Layout(_ ui.LayoutContext, r ui.Rect) { d.SetBounds(r) }

func (d *deckMount) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	b := d.Bounds()
	proj := d.app.activeSessionDeck()
	if proj == nil || !proj.deck.IsOpen() || b.Empty() || buf == nil {
		return
	}
	if d.surf == nil || d.surf.Width() != b.W || d.surf.Height() != b.H {
		d.surf = surface.NewSurface("shell-deck", b.W, b.H, cell.TransparentBlank())
	}
	bg := cell.BlankCell(rendering.ThemeColorToCell(d.app.Theme.Colors.Bg))
	d.surf.Buffer().Fill(bg)
	proj.deckHost.Resize(b.W, b.H)
	proj.deckHost.Layout()
	d.app.focusSessionDeckAfterLayout()
	proj.deckHost.Paint(d.surf.Buffer())
	surface.BlitOpaque(buf, ox+b.X, oy+b.Y, d.surf.Buffer())
}

func (d *deckMount) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	proj := d.app.activeSessionDeck()
	if proj == nil || !proj.deck.IsOpen() {
		return ui.EventResult{}
	}
	result := proj.deckHost.Handle(deckLocalMsg(evt.ToMsg(), ctx.LocalX, ctx.LocalY))
	if result.Action != nil && result.Action.ID == "paneldeck.empty" {
		proj.deckFocused = false
		proj.deckFocus = ui.RootID
	}
	d.app.normalizeSessionDeckFocus(proj)
	return result
}

func (d *deckMount) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Session panel deck"}
}

var _ ui.Component = (*deckMount)(nil)

// deckLocalMsg converts a screen-absolute tea mouse message into deck-local
// coordinates. EventContext locals are already deck-local (the deck slot
// rect origin); the deckHost expects local tea.Msg coordinates.
func deckLocalMsg(msg tea.Msg, localX, localY int) tea.Msg {
	switch m := msg.(type) {
	case tea.MouseClickMsg:
		m.X, m.Y = localX, localY
		return m
	case tea.MouseMotionMsg:
		m.X, m.Y = localX, localY
		return m
	case tea.MouseReleaseMsg:
		m.X, m.Y = localX, localY
		return m
	case tea.MouseWheelMsg:
		m.X, m.Y = localX, localY
		return m
	default:
		return msg
	}
}
