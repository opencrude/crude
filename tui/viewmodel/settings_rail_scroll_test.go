package viewmodel

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/tui/wm"
)

// TestSettingsRailScrollSurvivesPointerEvents reproduces the reported defect
// end-to-end through the real popup path: wheel over the settings rail, then
// move the mouse across the window and click an inert area. The popup forwards
// every pointer event its children do not consume through
// HandleEvent → SetModel → syncModel; that resync must not re-anchor the
// category rail onto the selected row.
func TestSettingsRailScrollSurvivesPointerEvents(t *testing.T) {
	app, _, _ := newSettingsPromptApp(t)
	app.width, app.height = 90, 18
	app.openApplicationSettings()
	parent := app.overlayHost.Window(ui.OverlayWorkspaceSettings)
	body := app.settingsSurface(parent)
	if parent == nil || body == nil {
		t.Fatal("settings window did not mount")
	}
	frame, ok := parent.Content.(*ui.WindowFrameWidget)
	if !ok {
		t.Fatal("settings window has no frame")
	}
	host := frame.Host()
	host.MarkLayout()
	host.Layout()

	rail, ok := host.ScreenRect(settingsui.WorkspaceSettingsListID)
	if !ok || rail.Empty() {
		t.Fatal("settings rail not mounted")
	}
	scroll := body.RailScrollForTest()
	if scroll.MaxOffset() <= 0 {
		t.Fatalf("harness: rail must overflow (content=%d viewport=%d)", scroll.Content(), scroll.Viewport())
	}

	px, py := parent.Position()
	width, _ := parent.Size()

	_, _ = app.Update(tea.MouseWheelMsg{X: px + rail.X + 2, Y: py + rail.Y + 2, Button: tea.MouseWheelDown})
	scrolled := scroll.Offset()
	if scrolled == 0 {
		t.Fatal("wheel over the settings rail did not scroll it")
	}

	// The interactions the user described: leave the rail, hover the window
	// chrome, then click an inert area.
	steps := []struct {
		name string
		msg  tea.Msg
	}{
		{"pointer leaves the rail", tea.MouseMotionMsg{X: px + rail.X + 2, Y: py - 1, Button: tea.MouseNone}},
		{"pointer crosses the title row", tea.MouseMotionMsg{X: px + width - 3, Y: py + 2, Button: tea.MouseNone}},
		{"click on inert chrome", tea.MouseClickMsg{X: px + width - 3, Y: py + 2, Button: tea.MouseLeft}},
		{"release on inert chrome", tea.MouseReleaseMsg{X: px + width - 3, Y: py + 2, Button: tea.MouseLeft}},
		{"pointer returns to the rail", tea.MouseMotionMsg{X: px + rail.X + 2, Y: py + rail.Y + 1, Button: tea.MouseNone}},
	}
	for _, step := range steps {
		_, _ = app.Update(step.msg)
		if got := scroll.Offset(); got != scrolled {
			t.Fatalf("%s jumped the rail: offset %d → %d", step.name, scrolled, got)
		}
	}
	if !app.overlayHost.IsOpen(ui.OverlayWorkspaceSettings) {
		t.Fatal("settings window closed during rail interaction")
	}

	// The painted rail must still show the scrolled window, not the top rows.
	host.MarkLayout()
	host.Layout()
	parent.Render(&wm.RenderContext{Theme: app.Theme})
	plain := bufferPlain(parent.Surface().Children()[0].Buffer())
	if !strings.Contains(plain, "Certificates") && !strings.Contains(plain, "Connection") {
		t.Fatalf("scrolled rail did not paint its lower rows:\n%s", plain)
	}
}

// TestSettingsRailKeyboardNavigationStillRevealsSelection is the negative
// guard for the fix above: suppressing the implicit re-anchor must not stop
// arrow navigation from bringing the newly selected category into view.
func TestSettingsRailKeyboardNavigationStillRevealsSelection(t *testing.T) {
	app, _, _ := newSettingsPromptApp(t)
	app.width, app.height = 90, 18
	app.openApplicationSettings()
	parent := app.overlayHost.Window(ui.OverlayWorkspaceSettings)
	body := app.settingsSurface(parent)
	scroll := body.RailScrollForTest()
	if scroll.MaxOffset() <= 0 {
		t.Fatalf("harness: rail must overflow (content=%d viewport=%d)", scroll.Content(), scroll.Viewport())
	}

	// Walk the selection to the last category with the keyboard. It must end
	// up visible even though the rail started at the top.
	for i := 0; i < scroll.Content(); i++ {
		_, _ = app.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if got := scroll.Offset(); got == 0 {
		t.Fatal("keyboard navigation did not reveal the last category")
	}
	host := parent.Content.(*ui.WindowFrameWidget).Host()
	host.MarkLayout()
	host.Layout()
	host.Focus(settingsui.WorkspaceSettingsListID)
	parent.Render(&wm.RenderContext{Theme: app.Theme})
	if plain := bufferPlain(parent.Surface().Children()[0].Buffer()); !strings.Contains(plain, body.Model().Categories()[body.Model().SelectedCategory()].Name) {
		t.Fatalf("selected category %q is not painted after keyboard navigation:\n%s",
			body.Model().Categories()[body.Model().SelectedCategory()].Name, plain)
	}
}
