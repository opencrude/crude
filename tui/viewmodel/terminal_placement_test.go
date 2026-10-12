package viewmodel

import (
	"testing"

	"crude/tui/ui"
	roottypes "crude/types"
	"crude/workspace"
)

func TestNewTerminalOpensWhereSettingsPlaceIt(t *testing.T) {
	app, ws := newRealShellApp(t)
	defaults := ws.AppDefaults()
	defaults.Terminal.Placement = roottypes.TerminalPlacementFloating
	ws.SetAppDefaults(defaults)
	created, ok := app.openNewTerminal()().(terminalCreatedMsg)
	if !ok || created.err != nil {
		t.Fatalf("terminal creation: %+v", created)
	}
	t.Cleanup(func() { app.closeTerminal(created.info.ID) })
	_, next := app.Update(created)
	runTerminalCommand(t, app, next)
	if !app.overlayHost.IsOpen(ui.TerminalOverlayID(created.info.ID)) {
		t.Fatal("Floating placement did not open the new terminal in a window")
	}
	if app.termDock.ActiveID() == created.info.ID {
		t.Fatal("a floating terminal was also mounted in the dock")
	}
	if app.terminalPlacement(created.info.ID) != workspace.TerminalFloating {
		t.Fatalf("placement = %v; want floating", app.terminalPlacement(created.info.ID))
	}
}
