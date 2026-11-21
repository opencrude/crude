package viewmodel

import (
	"crude/tui/ui"
	"crude/types"
)

// Opening Settings asks the existing connected topic to observe the host.
// The subscription owns asynchronous work and installation; opening a form
// does not perform I/O or launch discovery on the shell machine.
func (m *App) requestSettingsShellInventory() {
	if observer, ok := m.ws.(interface{ RequestSettingsShellDiscovery() }); ok {
		observer.RequestSettingsShellDiscovery()
	}
}

func (m *App) applySettingsShellInventory(effective types.AppSettings) {
	if m.overlayHost == nil {
		return
	}
	win := m.overlayHost.Window(ui.OverlayWorkspaceSettings)
	body := m.settingsSurface(win)
	if !body.UpdateShellInventory(effective.TerminalShells, effective.AgentShells) {
		return
	}
	if frame, ok := win.Content.(*ui.WindowFrameWidget); ok {
		frame.Host().MarkLayout()
	}
	win.MarkContentDirty()
	m.forceFrame()
}
