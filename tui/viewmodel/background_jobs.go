package viewmodel

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/backgroundjobs"
	"crude/types/command"
	"crude/types/statuscenter"
	"crude/workspace/fileio"
)

func (m *App) terminateBackgroundJob(request backgroundjobs.TerminateRequest) tea.Cmd {
	job := request.Job
	owner, projection := m.sessionProjectionForAgent(job.OwnerID)
	if projection == nil || projection.backgroundJobs != request.Target || job.Status != "running" {
		return nil
	}
	ref := fileio.AsyncCmdRef{Handle: job.Handle, PID: job.PID, StartedAt: job.StartedAt}
	terminate, err := m.ws.PrepareAsyncCommandTermination(job.OwnerID, ref)
	if err != nil {
		m.Feedback.NotifyError(err.Error())
		return nil
	}
	status := m.ws.Status()
	resource := statuscenter.ResourceRef{Kind: statuscenter.ResourceAgent, ID: job.OwnerID, SessionID: owner.SessionID}
	return func() tea.Msg {
		if err := terminate(); err != nil {
			if status != nil {
				status.Notify(statuscenter.NotificationRecord{
					Owner: resource, Severity: statuscenter.SeverityWarning,
					Source: "background-command", Summary: fmt.Sprintf("Command %q was not terminated", job.Handle), Detail: err.Error(),
				})
			}
		}
		return nil
	}
}

// revealBackgroundJob shows the command's originating call in its agent's
// conversation: the call is expanded and scrolled into view near the top.
func (m *App) revealBackgroundJob(request backgroundjobs.RevealRequest) {
	job := request.Job
	_, projection := m.sessionProjectionForAgent(job.OwnerID)
	if projection == nil || projection.backgroundJobs != request.Target {
		return
	}
	sub := m.subPaneProjections.Existing(job.OwnerID)
	if sub == nil {
		m.ensureAgentProjection(job.OwnerID)
		m.syncAgentMessages(job.OwnerID, false)
		sub = m.subPaneProjections.Existing(job.OwnerID)
	}
	if sub == nil || sub.renderer == nil {
		return
	}
	_, ok := sub.renderer.RevealCommand(command.ExecutionID(job.PID, job.StartedAt))
	if !ok {
		m.Feedback.NotifyInfo("The conversation does not show this command.")
		return
	}
	m.switchToAgentView(job.OwnerID)
	m.setSessionDeckFocused(false)
	m.ensureMainPanes().agentView(job.OwnerID).chat.ClearSelection()
	m.forceFrame()
}
