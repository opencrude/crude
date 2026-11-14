package viewmodel

import (
	"reflect"

	settingsui "crude/tui/settings"
	"crude/tui/ui"
	ttypes "crude/types"
	"crude/workspace"
)

type statusHydrateErrMsg struct{ Err error }

func (m *App) syncAgentStatusEvent(evt ttypes.AgentEventMsg) {
	if m.ws == nil {
		return
	}
	m.ws.ApplyAgentStatusEvent(workspace.AgentEvent{
		Type: evt.Type, Data: evt.Data, AgentID: evt.AgentID,
	})
}

func (m *App) notifyStatus(severity ttypes.StatusSeverity, source, summary, detail string, owner ttypes.ResourceRef) string {
	if m.ws == nil || m.ws.Status() == nil {
		return ""
	}
	return m.ws.Status().Notify(ttypes.NotificationRecord{
		DedupKey: source + ":" + owner.ID + ":" + summary,
		Owner:    owner, Severity: severity, Source: source, Summary: summary, Detail: detail,
		Transient: severity == ttypes.SeverityInfo,
	})
}

func (m *App) syncLSPStatusIssues() {
	if m.ws == nil || m.ws.Status() == nil || m.ws.LSPMgr() == nil {
		return
	}
	center := m.ws.Status()
	failed := make(map[string]bool)
	snapshots := m.ws.LSPMgr().ServerHealthSnapshots()
	m.syncSettingsLanguageServers(snapshots)
	for _, server := range snapshots {
		issueID := "lsp:" + server.ID
		if server.Error == "" {
			center.ResolveIssue(issueID)
			continue
		}
		failed[issueID] = true
		center.SetIssue(ttypes.IssueSnapshot{
			ID: issueID, Owner: ttypes.ResourceRef{Kind: ttypes.ResourceLSP, ID: server.ID},
			Severity: ttypes.SeverityError, Summary: server.Name + " failed", Detail: server.Error,
			Actions: []ttypes.StatusAction{{ID: "lsp:restart", Label: "Retry", Enabled: true}},
		})
	}
	for _, issue := range center.Issues() {
		if issue.Owner.Kind == ttypes.ResourceLSP && !failed[issue.ID] {
			center.ResolveIssue(issue.ID)
		}
	}
}

func (m *App) syncAgentStatusInventory() {
	m.ws.SyncAgentStatusInventory()
}

// syncSettingsLanguageServers gives an open Settings window the latest
// language-server runtime snapshot, only when it changed.
func (m *App) syncSettingsLanguageServers(snapshots []ttypes.LSPServerHealth) {
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
	m.syncLSPStatusIssues()
}
