package editor

import (
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestInvalidateWidthProfileDropsGeometryOnly(t *testing.T) {
	ed := &Editor{
		lineRunesCacheValid: true,
		lineRunesCacheLine:  4,
		lineRunesCache:      []rune("☰ text"),
		lineGeomCache:       map[int]*lineGeomCache{4: {raw: "☰ text", totalW: 6}},
	}
	ed.InvalidateWidthProfile()
	if ed.lineRunesCacheValid || ed.lineRunesCache != nil || ed.lineGeomCache != nil {
		t.Fatalf("width geometry survived invalidation: %+v", ed.lineGeomCache)
	}
}

func TestTypingRetainsUnchangedLineGeometryAndPaint(t *testing.T) {
	const source = "x\nunchanged 界\ttext\nanother line\n"
	ed := NewPreview(theme.DefaultDark(), "geometry.txt", source, 1)
	defer ed.Close()
	ed.readOnly = false
	ed.SetCursorPosition(0, 1)
	surf := surface.NewSurface("geometry", 50, 6, cell.TransparentBlank())
	paint := func() { ed.Render(surf, ui.PaintContext{Theme: ed.th, Focused: true}) }
	paint()
	cached := ed.lineGeomCache[1]
	before := make([]cell.Cell, surf.Width())
	for x := range before {
		before[x] = surf.Buffer().Get(x, 1)
	}
	ed.HandleEvent(tea.KeyPressMsg{Code: 'x', Text: "x"})
	paint()
	if ed.Value() != "x"+source || ed.lineGeomCache[0].raw != "xx" {
		t.Fatal("typing failed to update source and its painted geometry")
	}
	if ed.lineGeomCache[1] != cached {
		t.Fatal("typing rebuilt geometry for an unchanged line")
	}
	after := make([]cell.Cell, surf.Width())
	for x := range after {
		after[x] = surf.Buffer().Get(x, 1)
	}
	if !slices.Equal(before, after) {
		t.Fatal("typing changed the painted cells of an unrelated line")
	}
	// The cache is keyed by exact line text, not merely the line number.
	ed.HandleEvent(tea.KeyPressMsg{Code: tea.KeyEnter})
	paint()
	if ed.lineGeomCache[1].raw != "" || ed.lineGeomCache[2].raw != "unchanged 界\ttext" {
		t.Fatal("line insertion reused geometry from a different source line")
	}
}
