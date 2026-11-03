package viewmodel

import (
	"crude/shellport"
	settingsui "crude/tui/settings"
	"crude/tui/ui"
)

// Inventory requests use the same drained off-Update command lane as native
// remote administration. The exact retained Settings model owns its generations.
func (m *App) bindCodeToolsSettings(body *settingsui.WorkspaceSettingsSurface) {
	service, _ := m.ws.(shellport.CodeTools)
	if cmd := body.SetCodeTools(service); cmd != nil {
		m.remoteUICommands = append(m.remoteUICommands, cmd)
	}
}

func (m *App) cancelCodeToolsSettings() {
	if m.overlayHost != nil {
		if body := m.settingsSurface(m.overlayHost.Window(ui.OverlayWorkspaceSettings)); body != nil {
			body.CancelCodeTools()
		}
	}
}
