package viewmodel

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/ui"
)

func TestContextMenuNativeNavigationReachesBoundedLastItem(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	app.width, app.height = 80, 24
	chosen := -1
	items := make([]ui.ContextMenuItem, 40)
	for i := range items {
		index := i
		items[i] = ui.ContextMenuItem{Label: fmt.Sprintf("Scope %02d", i), Action: func() { chosen = index }}
	}
	items[39].Disabled = true
	menu := &ui.ContextMenuState{Sections: []ui.ContextMenuSection{{SectionLabel: "Scopes", Items: items}}}
	app.openContextMenuState(tea.Mouse{X: 5, Y: 3}, menu)
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	if menu.Cursor != 38 {
		t.Fatalf("End cursor=%d want last enabled38", menu.Cursor)
	}
	window := app.overlayHost.Window(ui.OverlayContextMenu)
	frame := window.Content.(*ui.ComponentWidget)
	list := ui.ContextMenuList(frame)
	if list.Scroll().Offset() == 0 {
		t.Fatal("End did not scroll last item into view")
	}
	x, y := window.Position()
	r, _ := frame.Host().ScreenRect(ui.ComponentID(ui.ContextMenuListID))
	offset := list.Scroll().Offset()
	app.Update(tea.MouseWheelMsg{X: x + r.X + 2, Y: y + r.Y + 2, Button: tea.MouseWheelUp})
	if list.Scroll().Offset() >= offset {
		t.Fatal("native context menu swallowed wheel over visible last scopes")
	}
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	_ = app.View()
	if !strings.Contains(app.View().Content, "Scope 38") {
		t.Fatal("last scope was not painted")
	}
	app.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	if menu.Cursor != 0 {
		t.Fatal("Home did not select first scope")
	}
	app.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if menu.Cursor <= 0 {
		t.Fatal("PageDown did not advance")
	}
	app.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if menu.Cursor != 0 {
		t.Fatal("PageUp did not return")
	}
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if chosen != 38 || app.overlayHost.Window(ui.OverlayContextMenu) != nil {
		t.Fatalf("wrong exact activation: %d", chosen)
	}
}
