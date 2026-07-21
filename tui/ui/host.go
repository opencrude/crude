package ui

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
)

// Host owns the root component tree, focus, hover, and layout geometry.
// It is the Phase 2 integration point for viewmodel.App (not yet wired to
// product screens).
type Host struct {
	theme   *theme.Theme
	tokens  *TokenSet
	root    Component
	width   int
	height  int
	focused ComponentID
	hovered ComponentID
	// registry maps ID -> component for hit testing and focus.
	registry map[ComponentID]Component
	// screen bounds per component after last layout
	screen map[ComponentID]Rect
	// depth per component (root = 0) for hit-test preference
	depth    map[ComponentID]int
	parent   map[ComponentID]ComponentID
	needsLay bool
	needsPnt bool
}

// NewHost creates a host with the given theme and optional root.
func NewHost(th *theme.Theme, root Component) *Host {
	h := &Host{
		theme:    th,
		tokens:   NewTokenSet(th),
		root:     root,
		registry: make(map[ComponentID]Component),
		screen:   make(map[ComponentID]Rect),
		depth:    make(map[ComponentID]int),
		parent:   make(map[ComponentID]ComponentID),
		needsLay: true,
		needsPnt: true,
	}
	if root != nil {
		h.registry[root.ID()] = root
	}
	return h
}

// Tokens returns the resolved token set.
func (h *Host) Tokens() *TokenSet { return h.tokens }

// SetTheme rebuilds tokens and invalidates layout+paint.
func (h *Host) SetTheme(th *theme.Theme) {
	h.theme = th
	if h.tokens == nil {
		h.tokens = NewTokenSet(th)
	} else {
		h.tokens.Rebuild(th)
	}
	applyTokenSet(h.root, h.tokens)
	h.needsLay = true
	h.needsPnt = true
}

// SetRoot replaces the root component.
func (h *Host) SetRoot(root Component) {
	h.root = root
	h.registry = make(map[ComponentID]Component)
	h.screen = make(map[ComponentID]Rect)
	h.depth = make(map[ComponentID]int)
	h.parent = make(map[ComponentID]ComponentID)
	if root != nil {
		h.registry[root.ID()] = root
	}
	h.needsLay = true
	h.needsPnt = true
	h.focused = RootID
	h.hovered = RootID
}

// Root returns the root component.
func (h *Host) Root() Component { return h.root }

// Resize updates host viewport size.
func (h *Host) Resize(w, hgt int) {
	if w == h.width && hgt == h.height {
		return
	}
	h.width = w
	h.height = hgt
	h.needsLay = true
	h.needsPnt = true
}

// Focus sets keyboard focus to id if registered.
func (h *Host) Focus(id ComponentID) bool {
	if id != RootID {
		if _, ok := h.registry[id]; !ok {
			return false
		}
	}
	if h.focused == id {
		return true
	}
	h.focused = id
	h.needsPnt = true
	return true
}

// Focused returns the focused component ID.
func (h *Host) Focused() ComponentID { return h.focused }

// Hovered returns the hovered component ID.
func (h *Host) Hovered() ComponentID { return h.hovered }

// Register adds a component to the hit/focus registry (for composites).
func (h *Host) Register(c Component) {
	if c == nil {
		return
	}
	h.registry[c.ID()] = c
}

// Layout runs measure+layout if dirty.
func (h *Host) Layout() {
	if h.root == nil || h.width <= 0 || h.height <= 0 {
		return
	}
	if !h.needsLay {
		return
	}
	lctx := LayoutContext{Theme: h.theme}
	cs := Tight(h.width, h.height)
	_ = h.root.Measure(lctx, cs)
	h.root.Layout(lctx, Rect{X: 0, Y: 0, W: h.width, H: h.height})
	h.screen = make(map[ComponentID]Rect)
	h.depth = make(map[ComponentID]int)
	h.parent = make(map[ComponentID]ComponentID)
	h.registry = map[ComponentID]Component{h.root.ID(): h.root}
	h.collectScreen(h.root, RootID, 0, 0, 0)
	if h.focused != RootID {
		if _, ok := h.screen[h.focused]; !ok {
			h.focused = h.root.ID()
		}
	}
	h.needsLay = false
}

func (h *Host) collectScreen(c Component, parent ComponentID, ox, oy, depth int) {
	b := c.Bounds()
	h.screen[c.ID()] = Rect{X: ox + b.X, Y: oy + b.Y, W: b.W, H: b.H}
	h.depth[c.ID()] = depth
	if parent != RootID {
		h.parent[c.ID()] = parent
	}
	if p, ok := c.(Parent); ok {
		for _, ch := range p.Children() {
			h.Register(ch)
			// Child bounds are parent-local; screen = parent origin + child bounds
			h.collectScreen(ch, c.ID(), ox+b.X, oy+b.Y, depth+1)
		}
	}
}

// Parent is implemented by components that own children.
type Parent interface {
	Children() []Component
}

// Paint renders the tree into buf at (0,0).
func (h *Host) Paint(buf *surface.CellBuffer) {
	if h.root == nil {
		return
	}
	h.Layout()
	pctx := PaintContext{
		Theme:   h.theme,
		Focused: h.focused == h.root.ID(),
		Hovered: h.hovered == h.root.ID(),
	}
	h.paintNode(h.root, buf, 0, 0, pctx)
	h.needsPnt = false
}

func (h *Host) paintNode(c Component, buf *surface.CellBuffer, ox, oy int, base PaintContext) {
	pctx := base
	pctx.Focused = h.focused == c.ID()
	pctx.Hovered = h.hovered == c.ID()
	c.Paint(buf, ox, oy, pctx)
	if p, ok := c.(Parent); ok {
		b := c.Bounds()
		for _, ch := range p.Children() {
			h.paintNode(ch, buf, ox+b.X, oy+b.Y, base)
		}
	}
}

// HitTest returns the topmost registered component containing screen (x,y).
// Preference order: greater tree depth, then smaller area (more specific).
func (h *Host) HitTest(x, y int) ComponentID {
	h.Layout()
	var best ComponentID
	bestDepth := -1
	bestArea := int(^uint(0) >> 1)
	for id, r := range h.screen {
		if !r.Contains(x, y) {
			continue
		}
		d := h.depth[id]
		area := r.W * r.H
		if d > bestDepth || (d == bestDepth && area < bestArea) {
			bestDepth = d
			bestArea = area
			best = id
		}
	}
	return best
}

// Handle dispatches a message to the focused component, with pointer
// messages first updating hover and targeting the hit component.
func (h *Host) Handle(msg tea.Msg) EventResult {
	if h.root == nil {
		return EventResult{}
	}
	h.Layout()

	switch m := msg.(type) {
	case tea.MouseMotionMsg:
		id := h.HitTest(m.X, m.Y)
		if id != h.hovered {
			h.hovered = id
			h.needsPnt = true
		}
		return h.dispatchTo(id, m.X, m.Y, msg)
	case tea.MouseClickMsg:
		id := h.HitTest(m.X, m.Y)
		if id != RootID {
			h.Focus(id)
		}
		return h.dispatchTo(id, m.X, m.Y, msg)
	case tea.MouseReleaseMsg:
		id := h.hovered
		if id == RootID {
			id = h.HitTest(m.X, m.Y)
		}
		return h.dispatchTo(id, m.X, m.Y, msg)
	case tea.MouseWheelMsg:
		id := h.HitTest(m.X, m.Y)
		return h.dispatchTo(id, m.X, m.Y, msg)
	default:
		id := h.focused
		if id == RootID {
			id = h.root.ID()
		}
		return h.dispatchTo(id, 0, 0, msg)
	}
}

func (h *Host) dispatchTo(id ComponentID, sx, sy int, msg tea.Msg) EventResult {
	c, ok := h.registry[id]
	if !ok || c == nil {
		if h.root != nil {
			c = h.root
			id = h.root.ID()
		} else {
			return EventResult{}
		}
	}
	r := h.screen[id]
	ectx := EventContext{
		Theme:   h.theme,
		Focused: h.focused == id,
		LocalX:  sx - r.X,
		LocalY:  sy - r.Y,
		ScreenX: sx,
		ScreenY: sy,
	}
	res := c.Handle(ectx, msg)
	res = h.bubbleAction(id, sx, sy, res)
	if res.Invalidate&InvalidateLayout != 0 {
		h.needsLay = true
	}
	if res.Invalidate&InvalidatePaint != 0 || res.Invalidate&InvalidateLayout != 0 {
		h.needsPnt = true
	}
	if res.Invalidate&InvalidateFocus != 0 {
		h.needsPnt = true
	}
	if res.Action != nil && res.Action.ID == "ui.focus" {
		if h.needsLay {
			h.Layout()
		}
		if focusID, ok := res.Action.Payload.(ComponentID); ok && h.Focus(focusID) {
			res.Action = nil
			res.Handled = true
			res.Invalidate |= InvalidateFocus | InvalidatePaint
		}
	}
	return res
}

func (h *Host) bubbleAction(source ComponentID, sx, sy int, result EventResult) EventResult {
	action := result.Action
	current := source
	for action != nil {
		parentID, ok := h.parent[current]
		if !ok {
			break
		}
		parent := h.registry[parentID]
		handler, ok := parent.(ActionHandler)
		if !ok {
			current = parentID
			continue
		}
		r := h.screen[parentID]
		response := handler.HandleAction(EventContext{
			Theme: h.theme, Focused: h.focused == parentID,
			LocalX: sx - r.X, LocalY: sy - r.Y, ScreenX: sx, ScreenY: sy,
		}, *action)
		if !response.Handled {
			current = parentID
			continue
		}
		result = result.Merge(response)
		result.Action = response.Action
		action = response.Action
		current = parentID
	}
	return result
}

// NeedsPaint reports whether a repaint is pending.
func (h *Host) NeedsPaint() bool { return h.needsPnt || h.needsLay }

// MarkPaint requests a repaint.
func (h *Host) MarkPaint() { h.needsPnt = true }

// MarkLayout requests relayout.
func (h *Host) MarkLayout() {
	h.needsLay = true
	h.needsPnt = true
}

// FillRect is a paint helper: fills a rect in buf with a cell.
func FillRect(buf *surface.CellBuffer, originX, originY int, r Rect, c cell.Cell) {
	for y := 0; y < r.H; y++ {
		for x := 0; x < r.W; x++ {
			buf.Set(originX+r.X+x, originY+r.Y+y, c)
		}
	}
}
