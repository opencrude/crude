package ui

import (
	"testing"
	"time"
)

func TestWheelGestureStationaryContentCannotRetarget(t *testing.T) {
	var g WheelGesture
	now := time.Unix(1, 0)
	if owner, fresh := g.Resolve(now, 10, 10, "outer", false); owner != "outer" || !fresh {
		t.Fatal(owner, fresh)
	}
	if owner, fresh := g.Resolve(now.Add(50*time.Millisecond), 10, 10, "inner", false); owner != "outer" || fresh {
		t.Fatal("content stole gesture", owner, fresh)
	}
	if owner, fresh := g.Resolve(now.Add(60*time.Millisecond), 12, 10, "inner", false); owner != "inner" || !fresh {
		t.Fatal("deliberate pointer move ignored")
	}
	g.Transfer("outer")
	if owner, _ := g.Resolve(now.Add(70*time.Millisecond), 12, 10, "inner", false); owner != "outer" {
		t.Fatal("boundary transfer bounced back")
	}
	if owner, fresh := g.Resolve(now.Add(time.Second), 12, 10, "inner", false); owner != "inner" || !fresh {
		t.Fatal("idle did not allow inner")
	}
}
func TestScrollWheelDeltaConservesRemainderAndDoesNotSnap(t *testing.T) {
	s := NewScrollModel()
	s.SetContent(20)
	s.SetViewport(5)
	s.ScrollTo(1)
	if left := ScrollWheelDelta(s, -3); left != -2 || s.Offset() != 0 || s.AutoScroll() {
		t.Fatal(left, s.Offset(), s.AutoScroll())
	}
	s.ScrollTo(12)
	if left := ScrollWheelDelta(s, 1); left != 0 || s.Offset() != 13 || s.AutoScroll() {
		t.Fatal("near-bottom swallowed delta", left, s.Offset())
	}
	if left := ScrollWheelDelta(s, 4); left != 2 || s.Offset() != 15 || !s.AutoScroll() {
		t.Fatal(left, s.Offset(), s.AutoScroll())
	}
}
