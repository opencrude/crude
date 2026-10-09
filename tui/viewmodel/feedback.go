package viewmodel

import (
	"strings"

	types "crude/types"
	"crude/workspace"
)

// feedbackSink maps short presentation feedback into the typed workspace
// notification history. It owns no lifecycle, accounting, or paint state.
type feedbackSink struct{ app *App }

func newFeedbackSink(app *App) *feedbackSink { return &feedbackSink{app: app} }

func (s *feedbackSink) center() *workspace.StatusCenter {
	if s == nil || s.app == nil || s.app.Workspace == nil {
		return nil
	}
	return s.app.Status
}

func (s *feedbackSink) owner() types.ResourceRef {
	if s == nil || s.app == nil {
		return types.ResourceRef{}
	}
	if path := s.app.EditorState.ActiveView; path != "" {
		return types.ResourceRef{Kind: types.ResourceEditor, ID: path}
	}
	if id := s.app.AgentView.ActiveID(); id != "" {
		ref := types.ResourceRef{Kind: types.ResourceAgent, ID: id}
		if session := s.app.SessionsByAgent[id]; session != nil {
			ref.SessionID = session.SessionID
		}
		return ref
	}
	return types.ResourceRef{Kind: types.ResourceWorkspace, ID: s.app.RootPath}
}

func (s *feedbackSink) NotifyInfo(message string) {
	center, owner := s.center(), s.owner()
	if center == nil || strings.TrimSpace(message) == "" {
		return
	}
	center.Notify(types.NotificationRecord{
		DedupKey: "feedback:" + string(owner.Kind) + ":" + owner.ID + ":" + message,
		Owner:    owner, Severity: types.SeverityInfo, Source: "feedback",
		Summary: message, Read: true, Transient: true,
	})
}

func (s *feedbackSink) NotifyAgentTransient(agentID, message string, severity types.StatusSeverity) {
	center := s.center()
	if center == nil || strings.TrimSpace(agentID) == "" || strings.TrimSpace(message) == "" {
		return
	}
	owner := types.ResourceRef{Kind: types.ResourceAgent, ID: agentID}
	if s.app != nil && s.app.AgentMgr != nil {
		if root := s.app.AgentMgr.OrchestratorFor(agentID); root != nil {
			if root.ID != agentID {
				owner.ParentID = root.ID
			}
			if session := s.app.SessionsByAgent[root.ID]; session != nil {
				owner.SessionID = session.SessionID
			}
		}
	}
	center.Notify(types.NotificationRecord{
		DedupKey: "agent-terminal:" + agentID + ":" + message,
		Owner:    owner, Severity: severity, Source: "agent", Summary: message,
		Read: true, Transient: true,
	})
}

func (s *feedbackSink) NotifyError(message string) {
	center, owner := s.center(), s.owner()
	if center == nil || strings.TrimSpace(message) == "" {
		return
	}
	center.Notify(types.NotificationRecord{
		DedupKey: "error:" + string(owner.Kind) + ":" + owner.ID + ":" + message,
		Owner:    owner, Severity: types.SeverityError, Source: "operation",
		Summary: message, Detail: message,
	})
}
