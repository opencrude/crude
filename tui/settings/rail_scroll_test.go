package settings

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
)

// TestSettingsRailDeepLinkStillRevealsSelection guards the other side of the
// rule: a programmatic jump (deep link, palette, filtered-out row) moves the
// selection, so the rail must still bring the new page into view even after
// the reader has scrolled it away.
func TestSettingsRailDeepLinkStillRevealsSelection(t *testing.T) {
	w, h := 90, 18
	_, root, host := newSettingsFrame(t, catLSP, w, h)
	host.MarkLayout()
	host.Layout()
	list := root.categories.list
	rail, _ := host.ScreenRect(WorkspaceSettingsListID)

	// Scroll hard toward the bottom so a top-of-rail page is out of view.
	for i := 0; i < 4; i++ {
		host.Handle(tea.MouseWheelMsg{X: rail.X + 2, Y: rail.Y + 2, Button: tea.MouseWheelDown})
	}
	railIndex := func(category int) int {
		for index, item := range list.Items() {
			if payload, ok := item.Payload.(int); ok && payload == category {
				return index
			}
		}
		return -1
	}
	target := railIndex(catAppearance)
	if target < 0 {
		t.Fatal("Appearance missing from the rail")
	}
	if list.Scroll().Offset() == 0 {
		t.Fatal("harness: rail did not scroll away from the top")
	}

	// Deep link the way the palette / `/settings` route does.
	root.model.SelectCategory(catAppearance)
	root.syncModel()
	offset, viewport := list.Scroll().Offset(), list.Scroll().Viewport()
	if target < offset || target >= offset+viewport {
		t.Fatalf("deep link left %q off-screen: row %d outside viewport [%d,%d)",
			root.model.Categories()[catAppearance].Name, target, offset, offset+viewport)
	}
}

// TestSettingsRailScrollSurvivesResync pins the reported "settings sidebar
// jumps when I scroll and then move the mouse or click" defect.
//
// The popup widget forwards every event that no child consumed through
// WorkspaceSettings.HandleEvent → SetModel → syncModel, which resyncs the
// category rail several times per pointer move. That resync must not
// re-anchor the viewport to the selected row: the rail belongs to the reader
// once they have wheel-scrolled it.
func TestSettingsRailScrollSurvivesResync(t *testing.T) {
	w, h := 90, 18
	_, root, host := newSettingsFrame(t, catLSP, w, h)
	host.MarkLayout()
	host.Layout()
	list := root.categories.list
	rail, ok := host.ScreenRect(WorkspaceSettingsListID)
	if !ok || rail.Empty() {
		t.Fatal("settings rail not laid out")
	}
	if list.Scroll().MaxOffset() <= 0 {
		t.Fatalf("harness: rail must overflow %dx%d (content=%d viewport=%d)",
			w, h, list.Scroll().Content(), list.Scroll().Viewport())
	}
	before := list.Scroll().Offset()
	host.Handle(tea.MouseWheelMsg{X: rail.X + 2, Y: rail.Y + 2, Button: tea.MouseWheelDown})
	scrolled := list.Scroll().Offset()
	if scrolled == before {
		t.Fatalf("wheel over the rail did not scroll it (offset %d, max %d)", scrolled, list.Scroll().MaxOffset())
	}

	// Same resync the popup performs for every unconsumed pointer event.
	root.syncModel()
	if got := list.Scroll().Offset(); got != scrolled {
		t.Fatalf("rail resync re-anchored the viewport: offset %d → %d (scrolled from %d)", scrolled, got, before)
	}

	// The painted rows must still be the scrolled window, not the top.
	buf := surface.NewCellBuffer(w, h)
	host.Paint(buf)
	top, _ := list.Scroll().ThumbBounds(rail.H)
	bar := root.listScroll
	if bar != nil && bar.Needed() {
		barRect, _ := host.ScreenRect(bar.ID())
		if barRect.Empty() {
			t.Fatal("rail scrollbar not mounted")
		}
		painted := 0
		for y := 0; y < barRect.H; y++ {
			if buf.Get(barRect.X, barRect.Y+y).Char == '┃' || buf.Get(barRect.X, barRect.Y+y).Char == '█' {
				painted = y
				break
			}
		}
		if painted != top {
			t.Fatalf("painted thumb row=%d want %d (viewport did not move)", painted, top)
		}
	}
}
