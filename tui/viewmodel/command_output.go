package viewmodel

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"crude/fileio"
	"crude/shellport"
	"crude/tui/screens/backgroundjobs"
	"crude/tui/ui"
	"crude/types/command"
	"crude/workspace"
)

// Only opening/closing is command-specific. Mounted output uses the same
// terminal screen, acknowledged stream, page reads and window as a PTY.
type commandOutputWindow struct {
	key        string
	ws         shellport.Workspace
	service    shellport.TerminalService
	generation uint64
	cancel     context.CancelFunc
	id         string
}

type commandOutputOpenedMsg struct {
	view  *commandOutputWindow
	info  workspace.TerminalInfo
	lease workspace.TerminalLease
	err   error
}

func commandOutputKey(job backgroundjobs.Job) string {
	return job.OwnerID + "/" + command.ExecutionID(job.PID, job.StartedAt)
}

func (m *App) openCommandOutput(request backgroundjobs.OutputRequest) tea.Cmd {
	job := request.Job
	_, projection := m.sessionProjectionForAgent(job.OwnerID)
	if projection == nil || projection.backgroundJobs != request.Target || m.ws == nil || m.overlayHost == nil {
		return nil
	}
	key := commandOutputKey(job)
	if view := m.commandOutputs[key]; view != nil {
		if view.id != "" {
			m.windowManager.FocusWindow(string(ui.TerminalOverlayID(view.id)))
		}
		return nil
	}
	service := m.terminalService()
	if service == nil {
		return nil
	}
	controller := m.terminalController()
	w, h := m.floatingTerminalSize()
	open, err := m.ws.PrepareOpenCommandView(job.OwnerID, fileio.AsyncCmdRef{Handle: job.Handle, PID: job.PID, StartedAt: job.StartedAt}, controller, max(8, w-2), max(2, h-2))
	if err != nil {
		m.Feedback.NotifyError(err.Error())
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	view := &commandOutputWindow{key: key, ws: m.ws, service: service, generation: m.workspaceSwitchGen.Load(), cancel: cancel}
	if m.commandOutputs == nil {
		m.commandOutputs = make(map[string]*commandOutputWindow)
	}
	m.commandOutputs[key] = view
	m.Feedback.NotifyInfo("Opening output for " + job.Handle + "…")
	return m.clientIOCommand(ctx, func(ctx context.Context) tea.Msg {
		info, err := open(ctx)
		var lease workspace.TerminalLease
		if err == nil {
			lease, err = service.Acquire(info.ID, controller)
		}
		if err == nil {
			err = ctx.Err()
		}
		if err != nil && info.ID != "" {
			service.Close(info.ID)
		}
		return commandOutputOpenedMsg{view: view, info: info, lease: lease, err: err}
	})
}

func (m *App) applyCommandOutputOpened(msg commandOutputOpenedMsg) tea.Cmd {
	view := msg.view
	if view == nil {
		return nil
	}
	current := m.commandOutputs[view.key] == view && view.ws == m.ws && view.generation == m.workspaceSwitchGen.Load()
	if !current {
		if msg.err == nil && msg.info.ID != "" {
			m.runDetachedAsyncCmd(func() tea.Msg { view.service.Close(msg.info.ID); return nil })
		}
		return nil
	}
	if msg.err != nil {
		delete(m.commandOutputs, view.key)
		view.cancel()
		m.Feedback.NotifyError("Could not open command output: " + msg.err.Error())
		return nil
	}
	view.id = msg.info.ID
	if m.terminalLeases == nil {
		m.terminalLeases = make(map[string]workspace.TerminalLease)
	}
	m.terminalLeases[view.id] = msg.lease
	screen := m.ensureTermScreen(view.id, msg.info.DisplayName())
	screen.SetReadOnly(true)
	screen.SetFloating(true)
	screen.SetControlled(true) // this private lease permits only sizing
	m.openTerminalWindow(view.id, screen)
	return m.terminalRefresh(view.id)
}

func (m *App) forgetCommandOutput(id string) {
	for key, view := range m.commandOutputs {
		if view.id == id {
			view.cancel()
			delete(m.commandOutputs, key)
			return
		}
	}
}

func (m *App) cancelCommandOutputOpens() {
	for key, view := range m.commandOutputs {
		view.cancel()
		delete(m.commandOutputs, key)
	}
}
