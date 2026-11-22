package viewmodel

import (
	"reflect"

	settingsui "crude/tui/screens/settingspages"
	"crude/tui/ui"
	"crude/types/statuscenter"
	"crude/workspace"
)

type statusHydrateErrMsg struct{ Err error }

func (m *App) syncAgentStatusEvent(evt AgentEventMsg) {
	if m.ws == nil {
		return
	}
	m.ws.ApplyAgentStatusEvent(workspace.AgentEvent{
		Type: evt.Type, Data: evt.Data, AgentID: evt.AgentID,
	})
}

func (m *App) notifyStatus(severity statuscenter.Severity, source, summary, detail string, owner statuscenter.ResourceRef) string {
	if m.ws == nil || m.ws.Status() == nil {
		return ""
	}
	return m.ws.Status().Notify(statuscenter.NotificationRecord{
		DedupKey: source + ":" + owner.ID + ":" + summary,
		Owner:    owner, Severity: severity, Source: source, Summary: summary, Detail: detail,
		Transient: severity == statuscenter.SeverityInfo,
	})
}

// syncLanguageServerSettings gives an open Settings page the latest
// language-server state. Issues for failed servers belong to the core
// (workspace.SyncLSPIssues); the shell only presents them.
func (m *App) syncLanguageServerSettings() {
	if m.ws == nil || m.ws.LSPMgr() == nil {
		return
	}
	m.syncSettingsLanguageServers(m.ws.LSPMgr().ServerHealthSnapshots())
}

func (m *App) syncAgentStatusInventory() {
	m.ws.SyncAgentStatusInventory()
}

// syncSettingsLanguageServers gives an open Settings window the latest
// language-server runtime snapshot, only when it changed.
func (m *App) syncSettingsLanguageServers(snapshots []statuscenter.LSPServerHealth) {
	if m.overlayHost == nil {
		return
	}
	body := m.settingsSurface(m.overlayHost.Window(ui.OverlayWorkspaceSettings))
	if body == nil || body.Model() == nil {
		m.settingsLSPHealth = nil
		return
	}
	if reflect.DeepEqual(m.settingsLSPHealth, snapshots) {
		return
	}
	m.settingsLSPHealth = snapshots
	m.applyRemoteSettingsMessage(body.Model(), settingsui.LanguageServerHealthMsg{Target: body.Model(), Servers: snapshots})
}

// controlLanguageServers applies a Settings request through the one LSP
// lifecycle owner, then refreshes the projection at once.
func (m *App) controlLanguageServers(msg settingsui.LanguageServerControlMsg) {
	manager := m.ws.LSPMgr()
	if manager == nil {
		return
	}
	switch {
	case msg.All:
		m.refreshLSP()
	case msg.Action == "stop":
		for _, id := range msg.IDs {
			manager.StopServer(id)
		}
	default:
		for _, id := range msg.IDs {
			if !manager.RestartServer(id) {
				m.Feedback.NotifyError("Language server is no longer available")
			}
		}
	}
	m.settingsLSPHealth = nil
	m.syncLanguageServerSettings()
}
