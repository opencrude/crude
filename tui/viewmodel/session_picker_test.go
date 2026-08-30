package viewmodel

import (
	"testing"

	"crude/tui/ui"
	"crude/workspace"
)

func TestOpenSessionPickerAllowedWhileAgentActive(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 100, 30
	streaming := &workspace.AgentState{Streaming: true}
	app.AgentMgr.AddOrchestrator("busy", streaming)
	if !app.anyActive() {
		t.Fatal("precondition: anyActive should be true while streaming")
	}

	app.openSessionPicker()

	if app.overlayHost.Window(ui.OverlaySessionPicker) == nil {
		t.Fatal("session picker must open even while an agent is streaming")
	}
	if app.sessionPicker == nil || !app.sessionPicker.IsOpen() {
		t.Fatal("sessionPicker screen was not opened")
	}
	app.closeSessionPicker()
}
