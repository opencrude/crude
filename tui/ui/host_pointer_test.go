package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

type passthroughCapture struct {
	Base
	child   Component
	pointer int
	key     int
}

func newPassthroughCapture(id ComponentID, child Component) *passthroughCapture {
	return &passthroughCapture{Base: NewBase(id), child: child}
}

func (p *passthroughCapture) Children() []Component { return []Component{p.child} }

func (p *passthroughCapture) Layout(_ LayoutContext, r Rect) {
	p.SetBounds(r)
	p.child.Layout(LayoutContext{}, r)
}

func (p *passthroughCapture) Handle(_ EventContext, msg tea.Msg) EventResult {
	switch msg.(type) {
	case tea.MouseClickMsg, tea.MouseMotionMsg, tea.MouseReleaseMsg, tea.MouseWheelMsg:
		p.pointer++
		return EventResult{Handled: true}
	default:
		p.key++
		return EventResult{Handled: true}
	}
}

func TestHostPointerPassthroughRoutesToParent(t *testing.T) {
	list := NewVirtualList("parent/list", nil)
	list.SetPointerPolicy(PointerPolicyPassthrough)
	list.SetItems([]VirtItem{{ID: "a", Label: "one"}, {ID: "b", Label: "two"}})
	root := newPassthroughCapture("parent", list)

	host := NewHost(nil, root)
	host.Resize(20, 5)
	host.Layout()

	if id := host.HitTest(3, 1); id != "parent/list" {
		t.Fatalf("hit=%q want parent/list", id)
	}

	res := host.Handle(tea.MouseClickMsg{X: 3, Y: 1, Button: tea.MouseLeft})
	if !res.Handled {
		t.Fatal("click should be handled by parent")
	}
	if root.pointer != 1 {
		t.Fatalf("parent pointer=%d want 1", root.pointer)
	}
	if host.Focused() != "parent" {
		t.Fatalf("focus=%q want parent", host.Focused())
	}

	res = host.Handle(tea.MouseWheelMsg{X: 3, Y: 1, Button: tea.MouseWheelDown})
	if !res.Handled || root.pointer != 2 {
		t.Fatalf("wheel parent pointer=%d want 2 res=%+v", root.pointer, res)
	}

	host.Focus("parent/list")
	before := list.SelectedIndex()
	res = host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	if list.SelectedIndex() == before {
		t.Fatal("keyboard should reach focused passthrough list child")
	}
	if root.key != 0 {
		t.Fatalf("keyboard should not route to parent key=%d", root.key)
	}
}

func TestHostDefaultVirtualListHandlesPointer(t *testing.T) {
	list := NewVirtualList("list", nil)
	list.SetItems([]VirtItem{{ID: "a", Label: "one"}, {ID: "b", Label: "two"}})
	host := NewHost(nil, list)
	host.Resize(20, 3)
	host.Layout()

	before := list.SelectedIndex()
	host.Handle(tea.MouseClickMsg{X: 1, Y: 0, Button: tea.MouseLeft})
	if list.SelectedIndex() == before && list.Scroll().MaxOffset() == 0 {
		// When not scrollable, click should still select first item.
		if list.SelectedIndex() != 0 {
			t.Fatalf("expected item 0 selected, got %d", list.SelectedIndex())
		}
	}

	offsetBefore := list.Scroll().Offset()
	host.Handle(tea.MouseWheelMsg{X: 1, Y: 0, Button: tea.MouseWheelDown})
	if list.Scroll().Offset() == offsetBefore && list.Scroll().MaxOffset() > 0 {
		t.Fatal("default list should handle wheel scroll")
	}
}

type clickCounter struct {
	Base
	clicks   int
	motions  int
	releases int
}

func newClickCounter(id ComponentID) *clickCounter {
	return &clickCounter{Base: NewBase(id)}
}

func (c *clickCounter) Layout(_ LayoutContext, r Rect) { c.SetBounds(r) }

func (c *clickCounter) Handle(_ EventContext, msg tea.Msg) EventResult {
	switch msg.(type) {
	case tea.MouseClickMsg:
		c.clicks++
		return EventResult{Handled: true}
	case tea.MouseMotionMsg:
		c.motions++
		return EventResult{Handled: true}
	case tea.MouseReleaseMsg:
		c.releases++
		return EventResult{Handled: true}
	}
	return EventResult{}
}

type splitRow struct {
	Base
	left  *clickCounter
	right *clickCounter
}

func newSplitRow(id ComponentID) *splitRow {
	return &splitRow{
		Base:  NewBase(id),
		left:  newClickCounter(id + "/left"),
		right: newClickCounter(id + "/right"),
	}
}

func (s *splitRow) Children() []Component { return []Component{s.left, s.right} }

func (s *splitRow) Measure(_ LayoutContext, cs Constraints) Size {
	return cs.ClampSize(Size{W: cs.MaxW, H: cs.MaxH})
}

func (s *splitRow) Layout(_ LayoutContext, r Rect) {
	s.SetBounds(r)
	s.left.Layout(LayoutContext{}, Rect{X: 0, Y: 0, W: r.W / 2, H: r.H})
	s.right.Layout(LayoutContext{}, Rect{X: r.W / 2, Y: 0, W: r.W - r.W/2, H: r.H})
}

func TestHostPointerCaptureLeftClick(t *testing.T) {
	root := newSplitRow("row")
	host := NewHost(nil, root)
	host.Resize(20, 1)
	host.Layout()

	host.Handle(tea.MouseClickMsg{X: 2, Y: 0, Button: tea.MouseLeft})
	if !host.HasPointerCapture() {
		t.Fatal("handled left click should capture")
	}
	if root.left.clicks != 1 {
		t.Fatalf("left clicks=%d want 1", root.left.clicks)
	}
}

func TestHostPointerCaptureCrossSiblingMotionRelease(t *testing.T) {
	root := newSplitRow("row")
	host := NewHost(nil, root)
	host.Resize(20, 1)
	host.Layout()

	host.Handle(tea.MouseClickMsg{X: 2, Y: 0, Button: tea.MouseLeft})
	host.Handle(tea.MouseMotionMsg{X: 15, Y: 0, Button: tea.MouseLeft})
	if root.left.motions != 1 {
		t.Fatalf("captured left motions=%d want 1", root.left.motions)
	}
	if root.right.motions != 0 {
		t.Fatalf("sibling should not receive motion while captured")
	}

	host.Handle(tea.MouseReleaseMsg{X: 15, Y: 0, Button: tea.MouseLeft})
	if host.HasPointerCapture() {
		t.Fatal("release should clear capture")
	}
	if root.left.releases != 1 {
		t.Fatalf("captured left releases=%d want 1", root.left.releases)
	}
	if root.right.releases != 0 {
		t.Fatal("sibling should not receive release while captured")
	}
}

func TestHostPointerCaptureWrongButtonReleasePreservesCapture(t *testing.T) {
	root := newSplitRow("row")
	host := NewHost(nil, root)
	host.Resize(20, 1)
	host.Layout()

	host.Handle(tea.MouseClickMsg{X: 2, Y: 0, Button: tea.MouseLeft})
	if !host.HasPointerCapture() {
		t.Fatal("expected left capture")
	}

	host.Handle(tea.MouseReleaseMsg{X: 15, Y: 0, Button: tea.MouseRight})
	if !host.HasPointerCapture() {
		t.Fatal("non-matching release must not clear capture")
	}
	if root.left.releases != 0 {
		t.Fatalf("capture owner should not receive wrong-button release, releases=%d", root.left.releases)
	}
	if root.right.releases != 1 {
		t.Fatalf("wrong-button release should hit-test to current target, releases=%d", root.right.releases)
	}

	host.Handle(tea.MouseMotionMsg{X: 15, Y: 0, Button: tea.MouseLeft})
	if root.left.motions != 1 {
		t.Fatalf("capture should survive wrong-button release, left motions=%d", root.left.motions)
	}

	host.Handle(tea.MouseReleaseMsg{X: 15, Y: 0, Button: tea.MouseLeft})
	if host.HasPointerCapture() {
		t.Fatal("matching release should clear capture")
	}
	if root.left.releases != 1 {
		t.Fatalf("matching release should reach capture owner, releases=%d", root.left.releases)
	}
}

func TestHostPointerCaptureClearsOnSetRoot(t *testing.T) {
	root := newSplitRow("row")
	host := NewHost(nil, root)
	host.Resize(20, 1)
	host.Handle(tea.MouseClickMsg{X: 2, Y: 0, Button: tea.MouseLeft})
	if !host.HasPointerCapture() {
		t.Fatal("expected capture")
	}

	other := newClickCounter("solo")
	host.SetRoot(other)
	host.Resize(10, 1)
	if host.HasPointerCapture() {
		t.Fatal("SetRoot should clear stale capture")
	}
}

func TestHostCancelPointerCaptureInvokesHook(t *testing.T) {
	root := newCancellableSplitRow("row")
	host := NewHost(nil, root)
	host.Resize(20, 1)
	host.Layout()
	host.Handle(tea.MouseClickMsg{X: 2, Y: 0, Button: tea.MouseLeft})
	if !host.HasPointerCapture() {
		t.Fatal("expected capture")
	}

	host.CancelPointerCapture()
	if host.HasPointerCapture() {
		t.Fatal("CancelPointerCapture should clear host capture")
	}
	if root.left.cancelled != 1 {
		t.Fatalf("cancel hook calls=%d want 1", root.left.cancelled)
	}
}

func TestHostLayoutRemovalInvokesCancelHook(t *testing.T) {
	scroll := NewScrollModel()
	scroll.SetViewport(5)
	scroll.SetContent(20)
	sb := NewScrollbarControl("sb", scroll, nil)
	root := newConditionalRoot("root", sb)
	host := NewHost(nil, root)
	host.Resize(10, 10)
	host.Layout()

	host.Handle(tea.MouseClickMsg{X: 0, Y: 2, Button: tea.MouseLeft})
	if !host.HasPointerCapture() || !sb.IsDragging() {
		t.Fatal("scrollbar drag should capture")
	}

	root.includeScrollbar = false
	host.MarkLayout()
	host.Layout()

	if host.HasPointerCapture() {
		t.Fatal("layout removal should clear capture")
	}
	if sb.IsDragging() {
		t.Fatal("layout removal should cancel scrollbar drag state")
	}
}

func TestScrollbarControlCancelPointerCapture(t *testing.T) {
	scroll := NewScrollModel()
	scroll.SetViewport(5)
	scroll.SetContent(20)
	sb := NewScrollbarControl("sb", scroll, nil)
	sb.Layout(LayoutContext{}, Rect{W: 1, H: 5})
	sb.Handle(EventContext{LocalY: 2}, tea.MouseClickMsg{Button: tea.MouseLeft})
	if !sb.IsDragging() {
		t.Fatal("expected dragging")
	}
	sb.CancelPointerCapture()
	if sb.IsDragging() {
		t.Fatal("CancelPointerCapture should end drag")
	}
}

type cancellableCounter struct {
	clickCounter
	cancelled int
}

func newCancellableCounter(id ComponentID) *cancellableCounter {
	cc := &cancellableCounter{}
	cc.clickCounter = *newClickCounter(id)
	return cc
}

func (c *cancellableCounter) CancelPointerCapture() { c.cancelled++ }

type cancellableSplitRow struct {
	Base
	left  *cancellableCounter
	right *clickCounter
}

func newCancellableSplitRow(id ComponentID) *cancellableSplitRow {
	return &cancellableSplitRow{
		Base:  NewBase(id),
		left:  newCancellableCounter(id + "/left"),
		right: newClickCounter(id + "/right"),
	}
}

func (s *cancellableSplitRow) Children() []Component { return []Component{s.left, s.right} }

func (s *cancellableSplitRow) Measure(_ LayoutContext, cs Constraints) Size {
	return cs.ClampSize(Size{W: cs.MaxW, H: cs.MaxH})
}

func (s *cancellableSplitRow) Layout(_ LayoutContext, r Rect) {
	s.SetBounds(r)
	s.left.Layout(LayoutContext{}, Rect{X: 0, Y: 0, W: r.W / 2, H: r.H})
	s.right.Layout(LayoutContext{}, Rect{X: r.W / 2, Y: 0, W: r.W - r.W/2, H: r.H})
}

type conditionalRoot struct {
	Base
	child            Component
	includeScrollbar bool
}

func newConditionalRoot(id ComponentID, child Component) *conditionalRoot {
	return &conditionalRoot{Base: NewBase(id), child: child, includeScrollbar: true}
}

func (r *conditionalRoot) Children() []Component {
	if r.includeScrollbar {
		return []Component{r.child}
	}
	return nil
}

func (r *conditionalRoot) Measure(_ LayoutContext, cs Constraints) Size {
	return cs.ClampSize(Size{W: cs.MaxW, H: cs.MaxH})
}

func (r *conditionalRoot) Layout(_ LayoutContext, rect Rect) {
	r.SetBounds(rect)
	if r.includeScrollbar {
		r.child.Layout(LayoutContext{}, rect)
	}
}

func TestHostReleaseWithoutCaptureUsesHitTestNotHover(t *testing.T) {
	root := newSplitRow("row")
	host := NewHost(nil, root)
	host.Resize(20, 1)
	host.Layout()

	host.Handle(tea.MouseMotionMsg{X: 2, Y: 0, Button: tea.MouseLeft})
	if host.Hovered() != "row/left" {
		t.Fatalf("hover=%q want row/left", host.Hovered())
	}

	host.Handle(tea.MouseReleaseMsg{X: 15, Y: 0, Button: tea.MouseLeft})
	if root.right.releases != 1 {
		t.Fatalf("release should hit-test right, releases=%d", root.right.releases)
	}
	if root.left.releases != 0 {
		t.Fatal("stale hover must not receive release")
	}
	if host.HasPointerCapture() {
		t.Fatal("non-captured release should not leave capture")
	}
}

func TestHostUnhandledClickDoesNotCapture(t *testing.T) {
	passive := &unhandledClick{Base: NewBase("passive")}
	host := NewHost(nil, passive)
	host.Resize(10, 1)
	host.Handle(tea.MouseClickMsg{X: 1, Y: 0, Button: tea.MouseLeft})
	if host.HasPointerCapture() {
		t.Fatal("unhandled click must not capture")
	}
}

type unhandledClick struct {
	Base
}

func (u *unhandledClick) Handle(_ EventContext, msg tea.Msg) EventResult {
	if _, ok := msg.(tea.MouseClickMsg); ok {
		return EventResult{}
	}
	return EventResult{}
}
