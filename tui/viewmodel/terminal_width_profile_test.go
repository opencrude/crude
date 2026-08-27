package viewmodel

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
)

func TestTerminalWidthProfileInvalidatesOpenFloatingWindow(t *testing.T) {
	app := newFullTestApp(t)
	app.openWorkspaceSettings()
	window := app.windowManager.Window(string(ui.OverlayWorkspaceSettings))
	if window == nil {
		t.Fatal("settings window not open")
	}
	window.Render(app.renderCtx)
	if window.IsContentDirty() {
		t.Fatal("settings window should be clean after initial render")
	}

	app.applyTerminalWidthProfile(tea.TerminalWidthProfileMsg{Measured: 12, Corrections: 2})
	if !window.IsContentDirty() {
		t.Fatal("width calibration did not invalidate retained settings content")
	}
}

func TestTerminalWidthProfileWithoutMeasurementsDoesNotInvalidateWindow(t *testing.T) {
	app := newFullTestApp(t)
	app.openWorkspaceSettings()
	window := app.windowManager.Window(string(ui.OverlayWorkspaceSettings))
	window.Render(app.renderCtx)

	app.applyTerminalWidthProfile(tea.TerminalWidthProfileMsg{})
	if window.IsContentDirty() {
		t.Fatal("empty calibration unexpectedly invalidated retained content")
	}
}
