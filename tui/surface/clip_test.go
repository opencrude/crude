package surface

import "testing"

func TestClipStack_EmptyIsInfinite(t *testing.T) {
	cs := NewClipStack()
	if cs.Depth() != 0 {
		t.Fatal("new stack depth 0")
	}
	// Infinite active should contain arbitrary points
	if !cs.Contains(0, 0) || !cs.Contains(1_000_000, -50) {
		t.Fatal("empty stack should not clip")
	}
	active := cs.Active()
	if active.W < 1e9 {
		t.Fatalf("expected infinite-ish rect, got %+v", active)
	}
}

func TestClipStack_PushPopIntersection(t *testing.T) {
	cs := NewClipStack()
	cs.Push(Rect{X: 0, Y: 0, W: 10, H: 10})
	if cs.Depth() != 1 {
		t.Fatal("depth 1")
	}
	if !cs.Contains(5, 5) || cs.Contains(10, 5) {
		t.Fatal("first clip bounds")
	}
	cs.Push(Rect{X: 5, Y: 5, W: 10, H: 10})
	// Intersection should be 5,5,5,5
	if !cs.Contains(5, 5) || !cs.Contains(9, 9) {
		t.Fatal("intersection should include interior")
	}
	if cs.Contains(4, 5) || cs.Contains(5, 4) || cs.Contains(10, 5) {
		t.Fatal("outside intersection")
	}
	active := cs.Active()
	if active != (Rect{X: 5, Y: 5, W: 5, H: 5}) {
		t.Fatalf("active=%+v", active)
	}
	cs.Pop()
	if cs.Depth() != 1 {
		t.Fatal("after pop depth 1")
	}
	if !cs.Contains(0, 0) {
		t.Fatal("outer clip restored")
	}
	cs.Pop()
	if cs.Depth() != 0 {
		t.Fatal("empty after second pop")
	}
	// Safe pop on empty
	cs.Pop()
	if cs.Depth() != 0 {
		t.Fatal("pop empty no-op")
	}
}

func TestClipStack_EmptyIntersection(t *testing.T) {
	cs := NewClipStack()
	cs.Push(Rect{X: 0, Y: 0, W: 5, H: 5})
	cs.Push(Rect{X: 100, Y: 100, W: 5, H: 5})
	if cs.Contains(0, 0) || cs.Contains(100, 100) {
		t.Fatal("empty intersection should contain nothing useful")
	}
	if !cs.Active().Empty() {
		t.Fatalf("active should be empty: %+v", cs.Active())
	}
}

func TestClipStack_Reset(t *testing.T) {
	cs := NewClipStack()
	cs.Push(Rect{W: 1, H: 1})
	cs.Push(Rect{W: 1, H: 1})
	cs.Reset()
	if cs.Depth() != 0 {
		t.Fatal("reset depth")
	}
	if !cs.Contains(999, 999) {
		t.Fatal("after reset no clipping")
	}
}
