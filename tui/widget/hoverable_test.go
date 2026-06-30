package widget

import "testing"

func TestHover_ZeroStateIsInactive(t *testing.T) {
	// A freshly constructed Hover (via the constructor) should be in
	// the "no item hovered" state.
	h := NewHover()
	if h.IsActive() {
		t.Fatal("NewHover() returned an active hover")
	}
	if h.Index != -1 {
		t.Fatalf("NewHover().Index = %d, want -1", h.Index)
	}
}

func TestHover_SetReportsChange(t *testing.T) {
	var h Hover
	if h.Set(3) == false {
		t.Fatal("Set(3) on fresh hover should report a change")
	}
	if h.Index != 3 {
		t.Fatalf("Set(3) -> Index = %d, want 3", h.Index)
	}
	if h.Set(3) {
		t.Fatal("Set(3) again should report no change")
	}
	if h.Set(5) == false {
		t.Fatal("Set(5) from 3 should report a change")
	}
	if h.Index != 5 {
		t.Fatalf("Set(5) -> Index = %d, want 5", h.Index)
	}
}

func TestHover_SetNormalisesNegative(t *testing.T) {
	// Negative indices should be normalised to -1 so the renderer
	// can use a single IsActive() check.
	var h Hover
	h.Set(0)
	if !h.IsActive() {
		t.Fatal("Set(0) should be active (row 0 is a valid hover index)")
	}
	h.Set(-5)
	if h.IsActive() {
		t.Fatal("Set(-5) should normalise to inactive")
	}
	if h.Index != -1 {
		t.Fatalf("Set(-5) -> Index = %d, want -1", h.Index)
	}
}

func TestHover_ClearReportsChange(t *testing.T) {
	h := NewHover()
	if h.Clear() {
		t.Fatal("Clear() on fresh hover should report no change")
	}
	h.Set(2)
	if !h.Clear() {
		t.Fatal("Clear() on hovered should report a change")
	}
	if h.IsActive() {
		t.Fatal("hover should be inactive after Clear()")
	}
	if h.Clear() {
		t.Fatal("Clear() on already-cleared should report no change")
	}
}
