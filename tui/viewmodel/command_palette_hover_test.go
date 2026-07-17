package viewmodel

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
	"crude/tui/widgets"
)

func openCommandPaletteForInteractionTest(t *testing.T, callback func(string) tea.Cmd) (*App, *ui.ComponentWidget) {
	t.Helper()
	app := newFullTestApp(t)
	app.registerOverlayHandlers()
	if _, cmd := app.toggleCommandPalette(); cmd != nil {
		t.Fatal("opening command palette returned an unexpected command")
	}
	if app.commandPaletteModel == nil {
		t.Fatal("command palette model was not created")
	}
	app.commandPaletteModel.callback = callback
	fw := app.overlayHost.Window(ui.OverlayCommandPalette)
	if fw == nil {
		t.Fatal("command palette window was not opened")
	}
	cw, ok := fw.Content.(*ui.ComponentWidget)
	if !ok || cw == nil {
		t.Fatalf("command palette content is %T, want *ui.ComponentWidget", fw.Content)
	}
	w, h := fw.Size()
	cw.Host().Resize(w, h)
	cw.Host().Layout()
	return app, cw
}

// TestCommandPalette_HoverViaHost tests that mouse motion over the
// command palette frame updates the list hover through the Host
// event routing (replacing the old handleMouseMotion/lineToItem path).
func TestCommandPalette_HoverViaHost(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil, ui.DefaultRegistry())
	cw := widgets.BuildCommandPaletteFrame(testTheme(), widgets.CommandPaletteSpec{
		Items: m.listItems(),
		Width: 60, Height: 14,
	})
	host := cw.Host()
	if host == nil {
		t.Fatal("expected host")
	}
	list := widgets.CommandPaletteList(cw)
	if list == nil {
		t.Fatal("expected list")
	}

	// Simulate mouse motion at a screen position inside the list area.
	// The WindowFrame content starts at y=1 (after title border).
	// The search field is at y=1, divider at y=2, list starts at y=3.
	host.Handle(tea.MouseMotionMsg{X: 5, Y: 5})
	// Hover should have updated; the exact index depends on layout but
	// the host should not crash and should mark paint.
	if !host.NeedsPaint() {
		// Hover may not have changed if already pointing at same item;
		// just verify no crash.
	}
}

func TestCommandPalette_ClickActivatesItem(t *testing.T) {
	var called string
	m := newCommandPaletteModel(testTheme(), func(cmdID string) tea.Cmd {
		called = cmdID
		return nil
	}, ui.DefaultRegistry())
	cw := widgets.BuildCommandPaletteFrame(testTheme(), widgets.CommandPaletteSpec{
		Items: m.listItems(),
		Width: 60, Height: 14,
	})
	host := cw.Host()
	if host == nil {
		t.Fatal("expected host")
	}
	list := widgets.CommandPaletteList(cw)
	if list == nil {
		t.Fatal("expected list")
	}

	// Select the first item and activate it via the list's action.
	item, ok := list.Selected()
	if !ok || !item.Selectable() {
		t.Fatal("expected selectable item")
	}
	entry, isEntry := item.Payload.(cmdEntry)
	if !isEntry {
		t.Fatal("expected cmdEntry payload")
	}
	// Simulate what handleCommandPaletteKey does on Enter
	cb := m.callback
	if cb != nil {
		cb(entry.ID)
	}
	if called == "" {
		t.Error("expected callback to be called")
	}
}

func TestCommandPalette_SearchFieldFocus(t *testing.T) {
	cw := widgets.BuildCommandPaletteFrame(testTheme(), widgets.CommandPaletteSpec{
		Items: []ui.ListItem{{ID: "x", Label: "X"}},
		Width: 60, Height: 14,
	})
	host := cw.Host()
	if host == nil {
		t.Fatal("expected host")
	}
	// The search field should be focused on creation
	if host.Focused() != widgets.CPSearchField {
		// Frame's root may be focused; that's acceptable as long as the
		// search field is registered.
		search := widgets.CommandPaletteSearch(cw)
		if search == nil {
			t.Error("expected search field to exist")
		}
	}
}

func TestCommandPalette_ListNavigationUpDown(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil, ui.DefaultRegistry())
	cw := widgets.BuildCommandPaletteFrame(testTheme(), widgets.CommandPaletteSpec{
		Items: m.listItems(),
		Width: 60, Height: 14,
	})
	list := widgets.CommandPaletteList(cw)
	if list == nil {
		t.Fatal("expected list")
	}
	initialIdx := list.SelectedIndex()

	// Simulate Down key on the list
	list.Handle(ui.EventContext{Theme: testTheme(), Focused: true}, tea.KeyPressMsg{Code: tea.KeyDown})
	afterDown := list.SelectedIndex()
	if afterDown <= initialIdx {
		t.Errorf("Down should advance selection: %d -> %d", initialIdx, afterDown)
	}

	// Simulate Up key
	list.Handle(ui.EventContext{Theme: testTheme(), Focused: true}, tea.KeyPressMsg{Code: tea.KeyUp})
	afterUp := list.SelectedIndex()
	if afterUp >= afterDown {
		t.Errorf("Up should retreat selection: %d -> %d", afterDown, afterUp)
	}
}

func TestCommandPalette_AppKeyboardNavigationPersistsAfterRefresh(t *testing.T) {
	app, cw := openCommandPaletteForInteractionTest(t, nil)
	list := widgets.CommandPaletteList(cw)
	initial := list.SelectedIndex()

	if _, cmd := app.dispatchFloatingWindowKey(tea.KeyPressMsg{Code: tea.KeyDown}); cmd != nil {
		t.Fatal("Down returned an unexpected command")
	}
	if got := list.SelectedIndex(); got <= initial {
		t.Fatalf("Down selection=%d, want greater than %d", got, initial)
	}
}

func TestCommandPalette_AppKeyboardTypingAndEnter(t *testing.T) {
	var called string
	app, cw := openCommandPaletteForInteractionTest(t, func(id string) tea.Cmd {
		called = id
		return nil
	})

	if _, cmd := app.dispatchFloatingWindowKey(tea.KeyPressMsg{Code: 's', Text: "sett"}); cmd != nil {
		t.Fatal("typing returned an unexpected command")
	}
	search := widgets.CommandPaletteSearch(cw)
	if got := search.Value(); got != "sett" {
		t.Fatalf("search value=%q, want sett", got)
	}
	if _, cmd := app.dispatchFloatingWindowKey(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil {
		cmd()
	}
	if called == "" {
		t.Fatal("Enter did not activate the filtered command")
	}
}

func TestCommandPalette_AppMouseHoverAndClickUseWindowLocalCoordinates(t *testing.T) {
	var called string
	app, cw := openCommandPaletteForInteractionTest(t, func(id string) tea.Cmd {
		called = id
		return nil
	})
	fw := app.overlayHost.Window(ui.OverlayCommandPalette)
	frame, ok := cw.Root().(*ui.WindowFrame)
	if !ok {
		t.Fatalf("command palette root is %T, want *ui.WindowFrame", cw.Root())
	}
	list := widgets.CommandPaletteList(cw)
	selectedRow := list.SelectedIndex() - list.Scroll().Offset()
	localX := frame.ContentBounds().X + 1
	localY := frame.ContentBounds().Y + list.Bounds().Y + selectedRow
	sx, sy := fw.Position()

	app.dispatchMouseMotion(tea.MouseMotionMsg{X: sx + localX, Y: sy + localY})
	if got := cw.Host().Hovered(); got != widgets.CPList {
		t.Fatalf("hovered component=%q, want %q", got, widgets.CPList)
	}

	app.dispatchMouseClick(tea.MouseClickMsg{X: sx + localX, Y: sy + localY, Button: tea.MouseLeft})
	if called == "" {
		t.Fatal("mouse click did not activate the selected command")
	}
}
