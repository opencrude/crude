package viewmodel

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
)

// Moving the pointer over a real context menu updates its highlighted row
// through the same overlay path the product uses. This is the menu behaviour
// every other hover surface has.
func TestContextMenuFollowsPointerMotion(t *testing.T) {
	app := newTestAppForPopupMouse(t)
	app.registerOverlayHandlers()
	t.Cleanup(app.closeContextMenu)
	st := &ui.ContextMenuState{Sections: []ui.ContextMenuSection{{
		SectionLabel: "Panes",
		Items: []ui.ContextMenuItem{
			{Label: "Split right"},
			{Label: "Split down"},
			{Label: "Split left"},
		},
	}}}
	app.openContextMenuState(tea.Mouse{X: 10, Y: 5}, st)
	fw := app.windowManager.Window("context-menu")
	if fw == nil || app.LastContextMenu != st {
		t.Fatal("context menu did not open")
	}
	x, y := fw.Position()
	// Row 0 is the section label, row 1 is the first item.
	_, _ = app.Update(tea.MouseMotionMsg{X: x + 2, Y: y + 2})
	if st.Cursor != 0 {
		t.Fatalf("pointer over the first item set cursor=%d", st.Cursor)
	}
	_, _ = app.Update(tea.MouseMotionMsg{X: x + 2, Y: y + 3})
	if st.Cursor != 1 {
		t.Fatalf("pointer over the second item set cursor=%d", st.Cursor)
	}
	_, _ = app.Update(tea.MouseMotionMsg{X: x + 2, Y: y + 4})
	if st.Cursor != 2 {
		t.Fatalf("pointer over the third item set cursor=%d", st.Cursor)
	}
}
