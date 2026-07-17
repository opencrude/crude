package widgets

import (
	"strings"
	"testing"

	"crude/tui/theme"
)

// Visual contract tests migrated to BuildContextMenuFrame + cell-buffer
// assertions in contextmenu_frame_test.go:
//   - TestContextMenu_DisabledAlignment → TestBuildContextMenuFrame_DisabledAlignment
//   - TestContextMenu_DisabledNotItalic → TestBuildContextMenuFrame_DisabledNotItalic
//   - TestContextMenu_NoTitle → TestBuildContextMenuFrame_NoTitleInBorder
//   - TestContextMenu_TightHeight → TestBuildContextMenuFrame_TightHeight
//   - TestContextMenu_AllLinesSameWidth → TestBuildContextMenuFrame_AllLinesSameWidth
//
// The reveal animation tests below remain testing ContextMenuLinesReveal
// directly; the reveal feature is pending WindowFrame animation support
// (Phase 3+). ContextMenuLines/Reveal remain as a shrinking bypass for
// animation only — see docs/tui/bypass_manifest.json.

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

// TestContextMenuLinesReveal_Animation: the reveal function shows
// progressively more rows as elapsed time increases.
func TestContextMenuLinesReveal_Animation(t *testing.T) {
	th := theme.DefaultDark()
	st := &ContextMenuState{
		Sections: []ContextMenuSection{{Items: []ContextMenuItem{
			{Label: "a"},
			{Label: "b"},
			{Label: "c"},
		}}},
	}
	// At t=0, 0 content rows visible.
	lines0 := ContextMenuLinesReveal(th, st, 0, 100)
	// At t=50, ~1-2 content rows visible.
	lines50 := ContextMenuLinesReveal(th, st, 50, 100)
	// At t=100, all rows visible.
	lines100 := ContextMenuLinesReveal(th, st, 100, 100)
	// All should have the same total height (5 = top + 3 + bottom).
	for _, l := range [][]string{lines0, lines50, lines100} {
		if len(l) != 5 {
			t.Errorf("expected 5 lines (3 content + 2 chrome), got %d", len(l))
		}
	}
	// At t=0, no item labels visible in the body (just empty bg).
	body := stripAnsi(lines0[1])
	if strings.Contains(body, "a") || strings.Contains(body, "b") || strings.Contains(body, "c") {
		t.Errorf("t=0 should not show any items: %q", body)
	}
	// At t=100, all items visible.
	body100 := stripAnsi(lines100[1]) + stripAnsi(lines100[2]) + stripAnsi(lines100[3])
	if !strings.Contains(body100, "a") || !strings.Contains(body100, "b") || !strings.Contains(body100, "c") {
		t.Errorf("t=100 should show all items: %q", body100)
	}
}

// TestContextMenuLinesReveal_CompleteImmediately: elapsed >= duration
// should render the full menu.
func TestContextMenuLinesReveal_CompleteImmediately(t *testing.T) {
	th := theme.DefaultDark()
	st := &ContextMenuState{
		Sections: []ContextMenuSection{{Items: []ContextMenuItem{
			{Label: "x"},
		}}},
	}
	// elapsed >> duration — full menu.
	lines := ContextMenuLinesReveal(th, st, 500, 80)
	plain := stripAnsi(lines[1])
	if !strings.Contains(plain, "x") {
		t.Errorf("expected item visible: %q", plain)
	}
}