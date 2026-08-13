// Package ui is the Crude TUI component host and design-system foundation.
//
// Phase 2 of TUI_COMPONENT_SYSTEM_PLAN.md. Product screens migrate onto this
// package in later phases. Until then, legacy widgets remain the runtime path
// and may only be bridged through explicit adapters.
//
// Invariants:
//   - Paint is side-effect free with respect to domain state.
//   - Handle does not paint.
//   - Component IDs are stable and unique among mounted components.
//   - Geometry for paint and hit-testing comes from the same layout pass.
package ui

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
)

// ComponentID is a hierarchical path identifying a mounted component.
// Format: "root/dialog/confirm/button-ok" (slash-separated segments).
type ComponentID string

// RootID is the empty root sentinel.
const RootID ComponentID = ""

// NewID joins parent and segment.
func NewID(parent ComponentID, segment string) ComponentID {
	if parent == RootID {
		return ComponentID(segment)
	}
	return ComponentID(string(parent) + "/" + segment)
}

// Parent returns the parent path, or RootID.
func (id ComponentID) Parent() ComponentID {
	s := string(id)
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return ComponentID(s[:i])
		}
	}
	return RootID
}

// Segment returns the last path segment.
func (id ComponentID) Segment() string {
	s := string(id)
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return s[i+1:]
		}
	}
	return s
}

// Size is a width/height in terminal cells.
type Size struct {
	W, H int
}

// Constraints bound a Measure call (Flutter-style box constraints).
type Constraints struct {
	MinW, MaxW int
	MinH, MaxH int
}

// Tight returns fixed-size constraints.
func Tight(w, h int) Constraints {
	return Constraints{MinW: w, MaxW: w, MinH: h, MaxH: h}
}

// Loose returns 0..max constraints.
func Loose(maxW, maxH int) Constraints {
	return Constraints{MinW: 0, MaxW: maxW, MinH: 0, MaxH: maxH}
}

// ClampSize clamps a preferred size into constraints.
func (c Constraints) ClampSize(s Size) Size {
	if s.W < c.MinW {
		s.W = c.MinW
	}
	if s.W > c.MaxW {
		s.W = c.MaxW
	}
	if s.H < c.MinH {
		s.H = c.MinH
	}
	if s.H > c.MaxH {
		s.H = c.MaxH
	}
	return s
}

// Rect is a cell-space rectangle (half-open).
type Rect struct {
	X, Y, W, H int
}

// Contains reports whether (x,y) is inside the half-open rect.
func (r Rect) Contains(x, y int) bool {
	return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

// Empty reports zero/negative area.
func (r Rect) Empty() bool {
	return r.W <= 0 || r.H <= 0
}

// ToSurfaceRect converts to surface.Rect.
func (r Rect) ToSurfaceRect() surface.Rect {
	return surface.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H}
}

// FromSurfaceRect converts from surface.Rect.
func FromSurfaceRect(r surface.Rect) Rect {
	return Rect{X: r.X, Y: r.Y, W: r.W, H: r.H}
}

// Invalidation describes what must be recomputed after an event.
type Invalidation uint8

const (
	InvalidateNone  Invalidation = 0
	InvalidatePaint Invalidation = 1 << iota
	InvalidateLayout
	InvalidateFocus
)

// Action is a typed user-intent emitted by components.
type Action struct {
	ID      string
	Payload any
}

// EventResult is returned from Component.Handle.
type EventResult struct {
	Handled    bool
	Action     *Action
	Cmd        tea.Cmd
	Invalidate Invalidation
}

// Merge combines two results (second wins on Action if both set).
func (r EventResult) Merge(other EventResult) EventResult {
	out := EventResult{
		Handled:    r.Handled || other.Handled,
		Invalidate: r.Invalidate | other.Invalidate,
	}
	if other.Action != nil {
		out.Action = other.Action
	} else {
		out.Action = r.Action
	}
	if other.Cmd != nil && r.Cmd != nil {
		out.Cmd = tea.Batch(r.Cmd, other.Cmd)
	} else if other.Cmd != nil {
		out.Cmd = other.Cmd
	} else {
		out.Cmd = r.Cmd
	}
	return out
}

// SemanticRole is an accessibility / test role.
type SemanticRole uint8

const (
	RoleNone SemanticRole = iota
	RoleButton
	RoleTextInput
	RoleList
	RoleListItem
	RoleTree
	RoleDialog
	RoleMenuItem
	RoleLabel
	RoleGroup
	RoleScrollBar
	RoleSeparator
)

// Semantics describes a component for testing and future a11y.
type Semantics struct {
	Role        SemanticRole
	Label       string
	Description string
	Disabled    bool
	Selected    bool
	Value       string
}

// LayoutContext is passed to Measure/Layout.
type LayoutContext struct {
	Theme *theme.Theme
}

// PaintContext is passed to Paint.
type PaintContext struct {
	Theme   *theme.Theme
	Focused bool
	Hovered bool
	Frame   int
}

// EventContext is passed to Handle.
type EventContext struct {
	Theme   *theme.Theme
	Focused bool
	LocalX  int
	LocalY  int
	ScreenX int
	ScreenY int
}

// Component is the atomic unit of the retained UI tree.
type Component interface {
	ID() ComponentID
	Measure(ctx LayoutContext, cs Constraints) Size
	Layout(ctx LayoutContext, r Rect)
	Paint(buf *surface.CellBuffer, originX, originY int, ctx PaintContext)
	Handle(ctx EventContext, evt Event) EventResult
	Semantics() Semantics
	Bounds() Rect
}

// ActionHandler is implemented by composite screens that translate actions
// emitted by descendants. Host bubbles actions through mounted parents until
// one consumes or replaces the action.
type ActionHandler interface {
	HandleAction(ctx EventContext, action Action) EventResult
}

// PointerPolicy controls how ui.Host routes pointer messages to a component.
type PointerPolicy uint8

const (
	// PointerPolicyDefault: the hit component handles pointer messages.
	PointerPolicyDefault PointerPolicy = iota
	// PointerPolicyPassthrough: pointer messages route to the nearest ancestor
	// that does not passthrough (paint/hit bounds unchanged). Keyboard focus and
	// non-pointer messages are unaffected.
	PointerPolicyPassthrough
)

// PointerPolicyProvider is implemented by components that override Host pointer
// routing (e.g. paint-only VirtualList children owned by a composite screen).
type PointerPolicyProvider interface {
	PointerPolicy() PointerPolicy
}

// PointerCaptureCanceller is implemented by components that hold drag or
// selection state across an active left-button capture. Host invokes
// CancelPointerCapture when capture is cleared without a matching release.
type PointerCaptureCanceller interface {
	CancelPointerCapture()
}

// Base is an embeddable partial implementation.
type Base struct {
	id     ComponentID
	bounds Rect
}

// NewBase constructs a Base with the given ID.
func NewBase(id ComponentID) Base {
	return Base{id: id}
}

func (b Base) ID() ComponentID { return b.id }

func (b *Base) SetBounds(r Rect) { b.bounds = r }

func (b Base) Bounds() Rect { return b.bounds }

func (b Base) Semantics() Semantics { return Semantics{} }

// Measure default fills available space.
func (b Base) Measure(_ LayoutContext, cs Constraints) Size {
	return Size{W: cs.MaxW, H: cs.MaxH}
}

// Layout default stores bounds.
func (b *Base) Layout(_ LayoutContext, r Rect) {
	b.bounds = r
}

// Handle default ignores.
func (b Base) Handle(_ EventContext, _ Event) EventResult {
	return EventResult{}
}

// Paint default is a no-op.
func (b Base) Paint(_ *surface.CellBuffer, _, _ int, _ PaintContext) {}

// PaintTree paints c and recursively paints Parent children.
// Used by WindowFrame and ComponentWidget when Host is not driving paint.
func PaintTree(c Component, buf *surface.CellBuffer, originX, originY int, ctx PaintContext) {
	if c == nil || buf == nil {
		return
	}
	c.Paint(buf, originX, originY, ctx)
	p, ok := c.(interface{ Children() []Component })
	if !ok {
		return
	}
	b := c.Bounds()
	for _, ch := range p.Children() {
		if ch == nil {
			continue
		}
		PaintTree(ch, buf, originX+b.X, originY+b.Y, ctx)
	}
}
