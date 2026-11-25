package workspace

import (
	"crude/types/statuscenter"
)

// QueueStartupIssue retains recovery context until the frontend has a usable
// terminal size. It performs no I/O and never prevents the shell from opening.
func (w *Workspace) QueueStartupIssue(issue statuscenter.OperationIssue) {
	w.startupIssues = append(w.startupIssues, issue)
}

// TakeStartupIssues transfers pending presentation-neutral diagnostics once.
func (w *Workspace) TakeStartupIssues() []statuscenter.OperationIssue {
	issues := w.startupIssues
	w.startupIssues = nil
	return issues
}

func (w *Workspace) queuePersistenceStartupIssues() {
	if w.workspaceDBErr != nil {
		w.QueueStartupIssue(statuscenter.OperationIssue{Title: "Workspace storage needs attention", Summary: "Crude opened, but session and workspace persistence are unavailable.", Detail: w.workspaceDBErr.Error(), Guidance: "Check storage permissions and free disk space, then try creating or resuming the session again. Existing saved data has not been reset.", Target: statuscenter.RepairSettings, Path: w.WorkspaceDir})
	}
	if w.collaborationErr != nil {
		w.QueueStartupIssue(statuscenter.OperationIssue{Title: "Workspace runtime needs attention", Summary: "Crude opened, but agent registration is unavailable.", Detail: w.collaborationErr.Error(), Guidance: "Check access to workspace storage. Repair the reported problem before starting an agent; no saved sessions have been reset.", Target: statuscenter.RepairSettings, Path: w.WorkspaceDir})
	}
	if w.localHistoryErr != nil {
		if w.Logger != nil {
			w.Logger.Warn("WORKSPACE", "local_history_open_failed", "error", w.localHistoryErr.Error())
		}
		w.QueueStartupIssue(statuscenter.OperationIssue{Title: "Local History needs attention", Summary: "Crude opened, but workspace history could not initialize.", Detail: w.localHistoryErr.Error(), Guidance: "Check the reported path and storage permissions. Required history safeguards remain enforced; Crude will not bypass them to execute work.", Target: statuscenter.RepairSettings, Path: w.WorkspaceDir})
	}
}
