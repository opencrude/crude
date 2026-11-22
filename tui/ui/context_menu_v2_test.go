package ui

import (
	"testing"
)

// Visual contract tests live in contextmenu_frame_test.go (BuildContextMenuFrame
// cell-buffer assertions).

// TestContextMenu_TightHeight: the popup height should be exactly
// content + chrome (2 border rows). No extra bottom padding.
// Kept as a state-level assertion (not rendering).
func TestContextMenu_TightHeight(t *testing.T) {
	st := &ContextMenuState{
		Sections: []ContextMenuSection{{Items: []ContextMenuItem{
			{Label: "a"},
			{Label: "b"},
			{Label: "c"},
		}}},
	}
	// 3 items + 2 chrome = 5 (no extra bottom pad).
	if h := ContextMenuPopupHeight(st); h != 5 {
		t.Errorf("height = %d, want 5", h)
	}
}

// TestContextMenu_FlatItemAt: accessor for the flat-item index.
func TestContextMenu_FlatItemAt(t *testing.T) {
	st := &ContextMenuState{
		Sections: []ContextMenuSection{
			{Items: []ContextMenuItem{
				{Label: "a"},
				{Label: "b", Disabled: true}, // skipped
				{Label: "c"},
			}},
			{Items: []ContextMenuItem{
				{Label: "d"},
			}},
		},
	}
	st.Build()
	// Flat items: a (0,0), c (0,2), d (1,0).
	si, ii, ok := st.FlatItemAt(0)
	if !ok || si != 0 || ii != 0 {
		t.Errorf("FlatItemAt(0) = (%d, %d, %v), want (0, 0, true)", si, ii, ok)
	}
	si, ii, ok = st.FlatItemAt(1)
	if !ok || si != 0 || ii != 2 {
		t.Errorf("FlatItemAt(1) = (%d, %d, %v), want (0, 2, true)", si, ii, ok)
	}
	si, ii, ok = st.FlatItemAt(2)
	if !ok || si != 1 || ii != 0 {
		t.Errorf("FlatItemAt(2) = (%d, %d, %v), want (1, 0, true)", si, ii, ok)
	}
	_, _, ok = st.FlatItemAt(3)
	if ok {
		t.Errorf("FlatItemAt(3) should be out of range")
	}
}
