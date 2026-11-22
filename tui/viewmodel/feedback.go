package viewmodel

import (
	"strings"

	"crude/core/shellport"
	"crude/types/statuscenter"
)

// feedbackSink maps short presentation feedback into the typed workspace
// notification history. It owns no lifecycle, accounting, or paint state.
type feedbackSink struct{ app *App }

func newFeedbackSink(app *App) *feedbackSink { return &feedbackSink{app: app} }

func (s *feedbackSink) center() shellport.StatusService {
	if s == nil || s.app == nil || s.app.ws == nil {
		return nil
	}
	return s.app.ws.Status()
}

func (s *feedbackSink) owner() statuscenter.ResourceRef {
	if s == nil || s.app == nil {
		return statuscenter.ResourceRef{}
	}
	if path := s.app.EditorState.ActiveView; path != "" {
		return statuscenter.ResourceRef{Kind: statuscenter.ResourceEditor, ID: path}
	}
	if id := s.app.AgentView.ActiveID(); id != "" {
		ref := statuscenter.ResourceRef{Kind: statuscenter.ResourceAgent, ID: id}
		if session := s.app.ws.SessionsByAgent()[id]; session != nil {
			ref.SessionID = session.SessionID
		}
		return ref
	}
	return statuscenter.ResourceRef{Kind: statuscenter.ResourceWorkspace, ID: s.app.ws.RootPath()}
}

func (s *feedbackSink) NotifyInfo(message string) {
	center, owner := s.center(), s.owner()
	if center == nil || strings.TrimSpace(message) == "" {
		return
	}
	center.Notify(statuscenter.NotificationRecord{
		DedupKey: "feedback:" + string(owner.Kind) + ":" + owner.ID + ":" + message,
		Owner:    owner, Severity: statuscenter.SeverityInfo, Source: "feedback",
		Summary: message, Read: true, Transient: true,
	})
}

func (s *feedbackSink) NotifyAgentTransient(agentID, message string, severity statuscenter.Severity) {
	center := s.center()
	if center == nil || strings.TrimSpace(agentID) == "" || strings.TrimSpace(message) == "" {
		return
	}
	owner := statuscenter.ResourceRef{Kind: statuscenter.ResourceAgent, ID: agentID}
	if s.app != nil && s.app.ws.AgentMgr() != nil {
		if root := s.app.ws.AgentMgr().OrchestratorFor(agentID); root != nil {
			if root.ID != agentID {
				owner.ParentID = root.ID
			}
			if session := s.app.ws.SessionsByAgent()[root.ID]; session != nil {
				owner.SessionID = session.SessionID
			}
		}
	}
	center.Notify(statuscenter.NotificationRecord{
		DedupKey: "agent-terminal:" + agentID + ":" + message,
		Owner:    owner, Severity: severity, Source: "agent", Summary: message,
		Read: true, Transient: true,
	})
}

func (s *feedbackSink) NotifyError(message string) {
	s.NotifyErrorDetail(message, message)
}

// NotifyErrorDetail reports a failure with a one-line summary and the full
// technical text as its detail.
func (s *feedbackSink) NotifyErrorDetail(summary, detail string) {
	center, owner := s.center(), s.owner()
	if center == nil || strings.TrimSpace(summary) == "" {
		return
	}
	center.Notify(statuscenter.NotificationRecord{
		DedupKey: "error:" + string(owner.Kind) + ":" + owner.ID + ":" + summary,
		Owner:    owner, Severity: statuscenter.SeverityError, Source: "operation",
		Summary: summary, Detail: detail,
	})
}
