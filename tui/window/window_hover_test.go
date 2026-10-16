package window

import (
	"testing"
)

func TestWindow_HoverSetAndClear(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)
	w.OpenTab("A", "", true)
	w.OpenTab("B", "", true)

	if w.Hover.IsActive() {
		t.Error("expected no hover initially")
	}

	changed := w.Hover.Set(0)
	if !changed {
		t.Error("expected Set(0) to return true")
	}
	if !w.Hover.IsActive() {
		t.Error("expected hover to be active after Set(0)")
	}
	if w.Hover.Index != 0 {
		t.Errorf("expected hover index 0, got %d", w.Hover.Index)
	}

	changed = w.Hover.Clear()
	if !changed {
		t.Error("expected Clear() to return true")
	}
	if w.Hover.IsActive() {
		t.Error("expected hover to be inactive after Clear()")
	}
	if w.Hover.Index != -1 {
		t.Errorf("expected hover index -1, got %d", w.Hover.Index)
	}
}

func TestWindow_HoverReportsChangeCorrectly(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)
	w.OpenTab("A", "", true)
	w.OpenTab("B", "", true)

	// First change should report true.
	if !w.Hover.Set(1) {
		t.Error("expected Set(1) to return true on first call")
	}

	// Setting the same index again should report false.
	if w.Hover.Set(1) {
		t.Error("expected Set(1) to return false when already set")
	}

	// Setting a different index should report true.
	if !w.Hover.Set(0) {
		t.Error("expected Set(0) to return true after change")
	}

	// Clear and confirm.
	if !w.Hover.Clear() {
		t.Error("expected Clear() to return true after active hover")
	}

	// Clear again: no-op, should report false.
	if w.Hover.Clear() {
		t.Error("expected Clear() to return false when already clear")
	}

	// Set with negative index should behave like Clear (no-op when already clear).
	if w.Hover.Set(-5) {
		t.Error("expected Set(-5) to return false when already clear")
	}
	if w.Hover.IsActive() {
		t.Error("expected hover to be inactive after Set(-5)")
	}
}

func TestWindow_HitTestReturnsIndex(t *testing.T) {
	w := newTestWindow()
	w.SetWidth(80)

	// Empty window → no hit.
	hit := w.HitTest(5, 0)
	if hit.TabIndex != -1 {
		t.Errorf("expected -1 for empty window, got %d", hit.TabIndex)
	}

	w.OpenTab("A", "", true)
	w.OpenTab("B", "", true)

	// Hit first tab.
	hit = w.HitTest(5, 0)
	if hit.TabIndex != 0 {
		t.Errorf("expected tab 0, got %d", hit.TabIndex)
	}

	// Hit second tab (first tab width + gap).
	tabWidths := w.computeTabWidths()
	secondTabStart := tabWidths[0] + tabGap
	hit = w.HitTest(secondTabStart+1, 0)
	if hit.TabIndex != 1 {
		t.Errorf("expected tab 1 at X=%d, got %d", secondTabStart+1, hit.TabIndex)
	}

	// Wrong Y → no hit.
	hit = w.HitTest(5, 1)
	if hit.TabIndex != -1 {
		t.Errorf("expected -1 for wrong Y, got %d", hit.TabIndex)
	}

	// Beyond the tabs → no hit.
	hit = w.HitTest(200, 0)
	if hit.TabIndex != -1 {
		t.Errorf("expected -1 beyond tabs, got %d", hit.TabIndex)
	}
}
