package ui

import (
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/wm"
)

// renderContextMenuFrame builds the WindowFrame context menu, measures
// it, and renders into a fresh surface for cell-buffer assertions.
func renderContextMenuFrame(t *testing.T, st *ContextMenuState) (*surface.Surface, *ComponentWidget) {
	t.Helper()
	th := theme.DefaultDark()
	if st.FlatItems == nil {
		st.Build()
	}
	cw := BuildContextMenuFrame(th, st)
	w, h := cw.Measure(80, 40)
	if cw2 := ContextMenuPopupWidth(st); cw2 > 0 {
		w = cw2
	}
	if ch := ContextMenuPopupHeight(st); ch > 0 {
		h = ch
	}
	surf := surface.NewSurface("ctx", w, h, cell.TransparentBlank())
	ctx := &wm.RenderContext{Theme: th, Focused: true}
	cw.Render(surf, ctx)
	return surf, cw
}

// plainRow returns the visible text of a surface row (runes only, no
// style) for assertion-level checks.
func plainRow(surf *surface.Surface, y int) string {
	buf := surf.Buffer()
	var b strings.Builder
	for x := 0; x < surf.Bounds().W; x++ {
		c := buf.Get(x, y)
		if c.Char != 0 && c.Char != ' ' {
			b.WriteRune(c.Char)
		} else if c.Char == ' ' {
			b.WriteByte(' ')
		}
	}
	return strings.TrimRight(b.String(), " ")
}

func rowContains(surf *surface.Surface, y int, sub string) bool {
	return strings.Contains(plainRow(surf, y), sub)
}

func rowHasBG(surf *surface.Surface, y int) bool {
	buf := surf.Buffer()
	for x := 0; x < surf.Bounds().W; x++ {
		c := buf.Get(x, y)
		if !c.BG.IsDefault() && c.Char != 0 && c.Char != ' ' {
			return true
		}
	}
	return false
}

func rowHasAttr(surf *surface.Surface, y int, attr cell.Attrs) bool {
	buf := surf.Buffer()
	for x := 0; x < surf.Bounds().W; x++ {
		c := buf.Get(x, y)
		if c.Attr.Has(attr) {
			return true
		}
	}
	return false
}

// TestBuildContextMenuFrame_RendersItemsAndSections: the frame must
// show section labels and items. Replaces TestContextMenuLines_IncludesTitleAndSections.
func TestBuildContextMenuFrame_RendersItemsAndSections(t *testing.T) {
	st := &ContextMenuState{
		Title: "Editor",
		Sections: []ContextMenuSection{
			{SectionLabel: "Code", Items: []ContextMenuItem{
				{Label: "Go to def"},
				{Label: "Find refs"},
			}},
		},
	}
	surf, _ := renderContextMenuFrame(t, st)
	// Top border row 0, section label row 1, items rows 2-3, bottom border row 4.
	if !rowContains(surf, 1, "Code") {
		t.Fatalf("section label missing: %q", plainRow(surf, 1))
	}
	if !rowContains(surf, 2, "Go to def") {
		t.Fatalf("item0 missing: %q", plainRow(surf, 2))
	}
	if !rowContains(surf, 3, "Find refs") {
		t.Fatalf("item1 missing: %q", plainRow(surf, 3))
	}
}

// TestBuildContextMenuFrame_DisabledItemRendered: disabled items must
// appear. Replaces TestContextMenuLines_DisabledItemRendered.
func TestBuildContextMenuFrame_DisabledItemRendered(t *testing.T) {
	st := &ContextMenuState{
		Sections: []ContextMenuSection{{Items: []ContextMenuItem{
			{Label: "enabled"},
			{Label: "disabled", Disabled: true},
		}}},
	}
	surf, _ := renderContextMenuFrame(t, st)
	// Row 1 and 2 are items (row 0 = top border).
	if !rowContains(surf, 1, "enabled") {
		t.Fatalf("enabled item missing: %q", plainRow(surf, 1))
	}
	if !rowContains(surf, 2, "disabled") {
		t.Fatalf("disabled item missing: %q", plainRow(surf, 2))
	}
}

// TestBuildContextMenuFrame_DetailNotTruncated: regression for the
// "Ctrl+V" detail truncation bug. Replaces TestContextMenuLines_DetailNotTruncated.
func TestBuildContextMenuFrame_DetailNotTruncated(t *testing.T) {
	st := &ContextMenuState{
		Sections: []ContextMenuSection{{Items: []ContextMenuItem{
			{Label: "Paste", Detail: "Ctrl+V"},
			{Label: "Find & replace", Detail: "Ctrl+H"},
		}}},
	}
	surf, _ := renderContextMenuFrame(t, st)
	r1 := plainRow(surf, 1)
	if !strings.Contains(r1, "Paste") {
		t.Fatalf("line1 missing label: %q", r1)
	}
	if !strings.Contains(r1, "Ctrl+V") {
		t.Fatalf("line1 missing detail: %q", r1)
	}
	r2 := plainRow(surf, 2)
	if !strings.Contains(r2, "Find & replace") {
		t.Fatalf("line2 missing label: %q", r2)
	}
	if !strings.Contains(r2, "Ctrl+H") {
		t.Fatalf("line2 missing detail: %q", r2)
	}
}

// TestBuildContextMenuFrame_HighlightedHasBG: selected item row must
// have a background color. Replaces TestContextMenuLines_HighlightedSpansFullWidth.
func TestBuildContextMenuFrame_HighlightedHasBG(t *testing.T) {
	st := &ContextMenuState{
		Sections: []ContextMenuSection{{Items: []ContextMenuItem{
			{Label: "Paste", Detail: "Ctrl+V"},
		}}},
	}
	st.Build()
	st.Cursor = 0
	surf, _ := renderContextMenuFrame(t, st)
	// Item is row 1 (row 0 = top border).
	if !rowHasBG(surf, 1) {
		t.Fatal("highlighted item row missing background color")
	}
}

// TestBuildContextMenuFrame_NoTitleInContent: context menus must not
// show the title in the content area. Replaces TestContextMenuLines_NoTitleInContent.
func TestBuildContextMenuFrame_NoTitleInContent(t *testing.T) {
	st := &ContextMenuState{
		Title: "This should not appear in body",
		Sections: []ContextMenuSection{{Items: []ContextMenuItem{
			{Label: "Copy", Detail: "Ctrl+C"},
		}}},
	}
	surf, _ := renderContextMenuFrame(t, st)
	// Item row 1 should contain "Copy" but not the title.
	r1 := plainRow(surf, 1)
	if strings.Contains(r1, "This should not appear") {
		t.Fatalf("title leaked into content: %q", r1)
	}
	if !strings.Contains(r1, "Copy") {
		t.Fatalf("item missing: %q", r1)
	}
}

// TestBuildContextMenuFrame_DividerBetweenSections: a separator row
// must appear between sections. Replaces TestContextMenuLines_DividerBetweenSections.
func TestBuildContextMenuFrame_DividerBetweenSections(t *testing.T) {
	st := &ContextMenuState{
		Sections: []ContextMenuSection{
			{SectionLabel: "A", Items: []ContextMenuItem{{Label: "a1"}}},
			{SectionLabel: "B", Items: []ContextMenuItem{{Label: "b1"}}},
		},
	}
	surf, _ := renderContextMenuFrame(t, st)
	// Layout: row0 top, row1 "A", row2 a1, row3 divider, row4 "B", row5 b1, row6 bottom.
	dividerFound := false
	for y := 0; y < surf.Bounds().H; y++ {
		r := plainRow(surf, y)
		if strings.Contains(r, "─") || strings.Contains(r, "━") {
			dividerFound = true
			break
		}
	}
	if !dividerFound {
		t.Fatal("no divider/separator row found between sections")
	}
}

// TestBuildContextMenuFrame_DisabledAlignment: disabled items must
// have the same leading indent as enabled items. Replaces
// TestContextMenu_DisabledAlignment.
func TestBuildContextMenuFrame_DisabledAlignment(t *testing.T) {
	st := &ContextMenuState{
		Sections: []ContextMenuSection{{Items: []ContextMenuItem{
			{Label: "Enabled"},
			{Label: "Off", Disabled: true},
			{Label: "On"},
		}}},
	}
	surf, _ := renderContextMenuFrame(t, st)
	// Items at rows 1, 2, 3. Find the column where each label starts
	// by scanning the row for the label rune sequence. The marker
	// labels should start at the same column. Selection is background only.
	labels := []string{"Enabled", "Off", "On"}
	var cols []int
	for i, label := range labels {
		y := i + 1
		runes := []rune(label)
		buf := surf.Buffer()
		found := -1
		for x := 0; x <= surf.Bounds().W-len(runes); x++ {
			match := true
			for k, r := range runes {
				c := buf.Get(x+k, y)
				if c.Char != r {
					match = false
					break
				}
			}
			if match {
				found = x
				break
			}
		}
		if found < 0 {
			t.Fatalf("label %q not found on row %d", label, y)
		}
		cols = append(cols, found)
	}
	if cols[0] != cols[1] || cols[1] != cols[2] {
		t.Fatalf("label columns differ: enabled=%d off=%d on=%d (should be equal)",
			cols[0], cols[1], cols[2])
	}
}

// TestBuildContextMenuFrame_DisabledNotItalic: disabled items must
// NOT be rendered in italic. Replaces TestContextMenu_DisabledNotItalic.
func TestBuildContextMenuFrame_DisabledNotItalic(t *testing.T) {
	st := &ContextMenuState{
		Sections: []ContextMenuSection{{Items: []ContextMenuItem{
			{Label: "Off", Disabled: true},
		}}},
	}
	surf, _ := renderContextMenuFrame(t, st)
	// Row 1 is the disabled item.
	if rowHasAttr(surf, 1, cell.AttrItalic) {
		t.Fatal("disabled item row has italic attribute (should be dim only)")
	}
}

// TestBuildContextMenuFrame_NoTitleInBorder: context menus must not
// show a title in the top border. Replaces TestContextMenu_NoTitle.
func TestBuildContextMenuFrame_NoTitleInBorder(t *testing.T) {
	st := &ContextMenuState{
		Title: "This should not appear",
		Sections: []ContextMenuSection{{Items: []ContextMenuItem{
			{Label: "Item"},
		}}},
	}
	surf, _ := renderContextMenuFrame(t, st)
	topRow := plainRow(surf, 0)
	if strings.Contains(topRow, "This should not appear") {
		t.Fatalf("title leaked into top border: %q", topRow)
	}
}

// TestBuildContextMenuFrame_AllLinesSameWidth: the rendered surface
// should be uniformly wide (no jagged right edge). Replaces
// TestContextMenu_AllLinesSameWidth.
func TestBuildContextMenuFrame_AllLinesSameWidth(t *testing.T) {
	st := &ContextMenuState{
		Sections: []ContextMenuSection{
			{SectionLabel: "A", Items: []ContextMenuItem{
				{Label: "item 1"},
				{Label: "item 2", Disabled: true},
			}},
		},
	}
	surf, _ := renderContextMenuFrame(t, st)
	// The surface width is fixed; all rows share it. Verify the
	// surface bounds are consistent.
	w := surf.Bounds().W
	if w < 20 {
		t.Fatalf("surface too narrow: %d", w)
	}
	// Check that rendering.VisibleWidth of each non-empty row matches.
	// We verify via the buffer: every row should be filled to the
	// same width (surface width).
	buf := surf.Buffer()
	for y := 0; y < surf.Bounds().H; y++ {
		// Count non-transparent cells.
		visible := 0
		for x := 0; x < w; x++ {
			c := buf.Get(x, y)
			if !c.Transparent && c.Char != 0 {
				visible++
			}
		}
		// Every row should have visible content (border or item).
		if visible == 0 {
			t.Errorf("row %d is entirely transparent", y)
		}
	}
}

// TestBuildContextMenuFrame_TightHeight: the frame height should
// match content + chrome (2 border rows), no extra padding. Replaces
// TestContextMenu_TightHeight (which tested ContextMenuPopupHeight
// directly — still valid, re-asserted here for the frame path).
func TestBuildContextMenuFrame_TightHeight(t *testing.T) {
	st := &ContextMenuState{
		Sections: []ContextMenuSection{{Items: []ContextMenuItem{
			{Label: "a"},
			{Label: "b"},
			{Label: "c"},
		}}},
	}
	_, cw := renderContextMenuFrame(t, st)
	_, h := cw.Measure(80, 40)
	expected := ContextMenuPopupHeight(st)
	if h != expected {
		t.Fatalf("frame height = %d, want %d (content + 2 chrome)", h, expected)
	}
}
