package widgets

import (
	"strings"
	"testing"

	"crude/tui/rendering"
	"crude/tui/theme"
)

// TestContextMenu_DisabledAlignment: disabled items must have the
// same leading indent as enabled items. Previously they were
// rendered with an extra space ("  Copy" vs " Copy"), which made
// the menu visually shift between enabled and disabled rows.
//
// We strip ANSI from the body lines and look for each label. The
// label must appear at the same character index in every line,
// proving the indent is consistent across enabled and disabled items.
func TestContextMenu_DisabledAlignment(t *testing.T) {
	th := theme.DefaultDark()
	st := &ContextMenuState{
		Sections: []ContextMenuSection{{Items: []ContextMenuItem{
			{Label: "Enabled"},
			{Label: "Off", Disabled: true},
			{Label: "On"},
		}}},
	}
	lines := ContextMenuLines(th, st, 0)
	// Body lines are at indices 1, 2, 3. Strip ANSI and find the
	// character index of each label. They must all have the same
	// leading columns (i.e. same position).
	// Print stripped lines to understand the structure.
	for i := 0; i < 3; i++ {
		plain := stripAnsi(lines[i+1])
		t.Logf("line %d stripped: %q (len=%d)", i, plain, len(plain))
	}
	var cols []int
	for i, label := range []string{"Enabled", "Off", "On"} {
		plain := stripAnsi(lines[i+1])
		idx := strings.Index(plain, label)
		if idx < 0 {
			t.Fatalf("line %d missing label %q in plain text: %q",
				i, label, plain)
		}
		cols = append(cols, idx)
	}
	if cols[0] != cols[1] || cols[1] != cols[2] {
		t.Fatalf("label positions differ: enabled=%d off=%d on=%d (all should be same)",
			cols[0], cols[1], cols[2])
	}
	// Each body line has the structure: │ (3 UTF-8 bytes) + 2 pad +
	// 1 indent + label. So the label starts at byte index 6.
	if cols[0] != 6 {
		t.Fatalf("label position = %d, want 6 (│ = 3 byte-positions)", cols[0])
	}
}

// TestContextMenu_DisabledNotItalic: disabled items must NOT be
// rendered in italic. The user feedback was: "why they even are
// italic". Just dim them.
func TestContextMenu_DisabledNotItalic(t *testing.T) {
	th := theme.DefaultDark()
	st := &ContextMenuState{
		Sections: []ContextMenuSection{{Items: []ContextMenuItem{
			{Label: "Off", Disabled: true},
		}}},
	}
	lines := ContextMenuLines(th, st, 0)
	// Italic in lipgloss is \x1b[3m. The disabled line should
	// not contain it.
	for _, l := range lines {
		if strings.Contains(l, "\x1b[3m") {
			t.Errorf("italic ANSI found in line: %q", l)
		}
	}
}

// TestContextMenu_NoTitle: right-click context menus should have
// no title in the top border. The top border becomes pure dashes
// with no embedded text.
func TestContextMenu_NoTitle(t *testing.T) {
	th := theme.DefaultDark()
	st := &ContextMenuState{
		Title: "This should not appear",
		Sections: []ContextMenuSection{{Items: []ContextMenuItem{
			{Label: "Item"},
		}}},
	}
	lines := ContextMenuLines(th, st, 0)
	plain := stripAnsi(lines[0])
	if strings.Contains(plain, "This should not appear") {
		t.Errorf("title leaked into top border: %q", plain)
	}
	// Top border is just dashes between corners.
	if !strings.HasPrefix(plain, "╭") || !strings.HasSuffix(plain, "╮") {
		t.Errorf("bad top border shape: %q", plain)
	}
}

// TestContextMenu_TightHeight: the popup height should be exactly
// content + chrome (2 border rows). No extra bottom padding.
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

// TestContextMenu_AllLinesSameWidth: chrome wrapping must produce
// a uniformly-wide popup (no jagged right edge).
func TestContextMenu_AllLinesSameWidth(t *testing.T) {
	th := theme.DefaultDark()
	st := &ContextMenuState{
		Sections: []ContextMenuSection{
			{SectionLabel: "A", Items: []ContextMenuItem{
				{Label: "item 1"},
				{Label: "item 2", Disabled: true},
			}},
		},
	}
	lines := ContextMenuLines(th, st, 0)
	if len(lines) == 0 {
		t.Fatal("no lines")
	}
	w0 := rendering.VisibleWidth(lines[0])
	for i, l := range lines {
		if w := rendering.VisibleWidth(l); w != w0 {
			t.Errorf("line %d width = %d, want %d", i, w, w0)
		}
	}
}
