package viewmodel

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"crude/fileio"
	"crude/tui/screens/backgroundjobs"
	"crude/types"
)

func (m *App) terminateBackgroundJob(request backgroundjobs.TerminateRequest) tea.Cmd {
	job := request.Job
	owner, projection := m.sessionProjectionForAgent(job.OwnerID)
	if projection == nil || projection.backgroundJobs != request.Target || job.Status != "running" {
		return nil
	}
	ref := fileio.AsyncCmdRef{Handle: job.Handle, PID: job.PID, StartedAt: job.StartedAt}
	terminate, err := m.PrepareAsyncCommandTermination(job.OwnerID, ref)
	if err != nil {
		m.Feedback.NotifyError(err.Error())
		return nil
	}
	status := m.Status
	resource := types.ResourceRef{Kind: types.ResourceAgent, ID: job.OwnerID, SessionID: owner.SessionID}
	return func() tea.Msg {
		if err := terminate(); err != nil {
			if status != nil {
				status.Notify(types.NotificationRecord{
					Owner: resource, Severity: types.SeverityWarning,
					Source: "background-command", Summary: fmt.Sprintf("Command %q was not terminated", job.Handle), Detail: err.Error(),
				})
			}
		}
		return nil
	}
}
