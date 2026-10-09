package viewmodel

import (
	"crude/tui/types"
	ttypes "crude/types"
	"crude/workspace"
)

type statusHydrateErrMsg struct{ Err error }

func (m *App) syncAgentStatusEvent(evt types.AgentEventMsg) {
	if m.Workspace == nil {
		return
	}
	m.ApplyAgentStatusEvent(workspace.AgentEvent{
		Type: evt.Type, Data: evt.Data, AgentID: evt.AgentID,
	})
}

func (m *App) notifyStatus(severity ttypes.StatusSeverity, source, summary, detail string, owner ttypes.ResourceRef) string {
	if m.Workspace == nil || m.Status == nil {
		return ""
	}
	return m.Status.Notify(ttypes.NotificationRecord{
		DedupKey: source + ":" + owner.ID + ":" + summary,
		Owner:    owner, Severity: severity, Source: source, Summary: summary, Detail: detail,
		Transient: severity == ttypes.SeverityInfo,
	})
}

func (m *App) syncLSPStatusIssues() {
	if m.Workspace == nil || m.Status == nil || m.LSPMgr == nil {
		return
	}
	center := m.Status
	failed := make(map[string]bool)
	for _, server := range m.LSPMgr.ServerHealthSnapshots() {
		issueID := "lsp:" + server.ID
		if server.Error == "" {
			center.ResolveIssue(issueID)
			continue
		}
		failed[issueID] = true
		center.SetIssue(ttypes.IssueSnapshot{
			ID: issueID, Owner: ttypes.ResourceRef{Kind: ttypes.ResourceLSP, ID: server.ID},
			Severity: ttypes.SeverityError, Summary: server.Name + " failed", Detail: server.Error,
			Actions: []ttypes.StatusAction{{ID: "lsp:restart", Label: "Restart Server", Enabled: true}},
		})
	}
	for _, issue := range center.Issues() {
		if issue.Owner.Kind == ttypes.ResourceLSP && !failed[issue.ID] {
			center.ResolveIssue(issue.ID)
		}
	}
}

func (m *App) syncAgentStatusInventory() {
	m.SyncAgentStatusInventory()
}
