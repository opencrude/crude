package ui

import (
	tea "charm.land/bubbletea/v2"
	"testing"
	"time"
)

type wheelTestControl struct {
	Base
	wheels, clicks int
	reset          int
	target         ComponentID
}

func (c *wheelTestControl) Handle(_ EventContext, e Event) EventResult {
	if e.Kind == EventMouseWheel {
		c.wheels++
	}
	if e.Kind == EventMouseClick {
		c.clicks++
	}
	return EventResult{Handled: true}
}
func (c *wheelTestControl) ResetWheelGesture()       { c.reset++ }
func (c *wheelTestControl) WheelTarget() ComponentID { return c.target }

type wheelTestRoot struct {
	Base
	children []Component
}

func (r *wheelTestRoot) Children() []Component { return r.children }
func (r *wheelTestRoot) Layout(_ LayoutContext, b Rect) {
	r.SetBounds(b)
	for i, c := range r.children {
		c.Layout(LayoutContext{}, Rect{X: i * 10, W: 10, H: b.H})
	}
}

func TestHostWheelMountIdentityAndSiblingOwnership(t *testing.T) {
	a := &wheelTestControl{Base: NewBase("a")}
	b := &wheelTestControl{Base: NewBase("b")}
	r := &wheelTestRoot{Base: NewBase("root"), children: []Component{a, b}}
	h := NewHost(nil, r)
	h.Resize(20, 5)
	h.Layout()
	h.Focus("b")
	h.Handle(tea.MouseWheelMsg{X: 9, Y: 1, Button: tea.MouseWheelDown})
	h.Handle(tea.MouseWheelMsg{X: 10, Y: 1, Button: tea.MouseWheelDown})
	if a.wheels != 1 || b.wheels != 1 || h.Focused() != "b" {
		t.Fatal("wheel crossed sibling or changed focus")
	}
	replacement := &wheelTestControl{Base: NewBase("b")}
	r.children[1] = replacement
	h.MarkLayout()
	h.Layout()
	h.Handle(tea.MouseWheelMsg{X: 10, Y: 1, Button: tea.MouseWheelDown})
	if b.wheels != 1 || replacement.wheels != 1 {
		t.Fatal("same-ID replacement inherited stale wheel owner")
	}
	h.Handle(tea.MouseClickMsg{X: 10, Y: 1, Button: tea.MouseLeft})
	if !h.HasPointerCapture() {
		t.Fatal("wheel altered click capture contract")
	}
	h.ResetWheelGesture()
	if !h.HasPointerCapture() {
		t.Fatal("reset wheel cancelled pointer capture")
	}
	h.Handle(tea.MouseReleaseMsg{X: 19, Y: 4, Button: tea.MouseLeft})
	if h.HasPointerCapture() {
		t.Fatal("release did not finish capture")
	}
}

func TestWheelGestureGutterCannotAcquireStationaryTransaction(t *testing.T) {
	var g WheelGesture
	now := time.Unix(1, 0)
	g.Resolve(now, 10, 10, "outer", false)
	owner, fresh := g.Resolve(now.Add(100*time.Millisecond), 10, 10, "nested-gutter", true)
	if owner != "outer" || fresh {
		t.Fatal("content motion was mistaken for explicit scrollbar intent")
	}
	owner, fresh = g.Resolve(now.Add(200*time.Millisecond), 11, 10, "nested-gutter", true)
	if owner != "nested-gutter" || !fresh {
		t.Fatal("deliberate scrollbar move not immediate")
	}
}

func TestHostWheelModalScopeDoesNotRouteToUnderlyingSibling(t *testing.T) {
	a := &wheelTestControl{Base: NewBase("a")}
	b := &wheelTestControl{Base: NewBase("b")}
	r := &wheelTestRoot{Base: NewBase("root"), children: []Component{a, b}}
	h := NewHost(nil, r)
	h.Resize(20, 5)
	h.Layout()
	h.Handle(tea.MouseWheelMsg{X: 3, Y: 1, Button: tea.MouseWheelDown})
	h.PushFocusScope("b", true)
	h.Handle(tea.MouseWheelMsg{X: 3, Y: 1, Button: tea.MouseWheelDown})
	if a.wheels != 1 {
		t.Fatal("modal scope leaked wheel to underlying sibling")
	}
	h.Handle(tea.MouseWheelMsg{X: 13, Y: 1, Button: tea.MouseWheelDown})
	if b.wheels != 1 {
		t.Fatal("modal content lost own scrolling")
	}
	h.PopFocusScope()
	h.Handle(tea.MouseWheelMsg{X: 3, Y: 1, Button: tea.MouseWheelDown})
	if a.wheels != 2 {
		t.Fatal("modal close failed to start fresh owner")
	}
}
