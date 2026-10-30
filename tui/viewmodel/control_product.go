package viewmodel

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/fileio"
	"crude/shellport"
	"crude/tui/ui"
	"crude/tui/window"
	"crude/types/control"
)

func productDescriptor(id string) (control.Descriptor, bool) {
	for _, d := range control.ProductCommands() {
		if d.ID == id {
			return d, true
		}
	}
	return control.Descriptor{}, false
}

// handleProductControl is called before the ordinary shell dispatch. It owns
// validation as well as effects because draft/window fences differ from topology.
func (m *App) handleProductControl(msg shellport.ControlRequestMsg) (tea.Cmd, bool) {
	r := msg.Envelope.Request
	d, ok := productDescriptor(r.CommandID)
	if !ok {
		return nil, false
	}
	fail := func(code control.FaultCode, text string) (tea.Cmd, bool) {
		return m.failShellControl(msg, code, text), true
	}
	port, attached := m.ws.(shellport.ShellControl)
	if !attached || port != msg.Owner || m.asyncIOStopped.Load() {
		return fail(control.Unavailable, "shell detached")
	}
	if !reflect.DeepEqual(d, msg.Envelope.Descriptor) {
		return fail(control.Stale, "product descriptor changed")
	}
	s := m.inspectShell()
	current := control.Current{Target: port.ControlIdentity(), Preconditions: control.Preconditions{Revision: &s.Revision, OpenEpoch: &s.OpenEpoch}}
	composer := strings.HasPrefix(r.CommandID, "composer.") || r.CommandID == "capability.picker"
	if composer {
		if err := m.productComposerOwner(r); err != nil {
			return fail(control.Denied, err.Error())
		}
		revision, epoch := m.Input.DraftRevision(), m.Input.DraftEpoch()
		current.Target.SessionID, current.Target.ResourceID = m.ActiveSession.SessionID, m.AgentView.ActiveID()
		current.Preconditions = control.Preconditions{Revision: &revision, OpenEpoch: &epoch}
	} else if d.Requires.Session {
		owner := m.ws.SessionForAgent(r.Target.ResourceID)
		if owner == nil {
			return fail(control.Unavailable, "agent owner unavailable")
		}
		current.Target.SessionID, current.Target.ResourceID = owner.SessionID, r.Target.ResourceID
	} else if d.Requires.Resource {
		current.Target.ResourceID = r.Target.ResourceID // resolved again below before any effect
	}
	if fault := r.ValidateAgainst(d, current); fault != nil {
		return m.replyShellControl(msg, control.Result{State: control.Failed, Fault: fault}), true
	}
	windowAction := strings.HasPrefix(r.CommandID, "window.") || r.CommandID == "terminal.dock"
	if m.workspaceAttachPending || m.sessionDeckBlocking() {
		return fail(control.Denied, "blocking owner owns input")
	}
	if m.windowManager != nil {
		for _, w := range m.windowManager.Windows() {
			if w.Visible() && w.Modal() {
				return fail(control.Denied, "modal owns input")
			}
		}
		if m.windowManager.Focused() != nil && !windowAction {
			return fail(control.Denied, "floating owner owns input")
		}
	}
	result := control.Result{State: control.Applied}
	invalid := func(err error) (tea.Cmd, bool) { return fail(control.Invalid, err.Error()) }
	switch r.CommandID {
	case "tab.move":
		var in struct {
			Tab    window.TabID   `json:"tab_id"`
			Group  window.GroupID `json:"group_id"`
			Before window.TabID   `json:"before_tab_id"`
		}
		if err := decodeShellInput(r.Input, &in); err != nil {
			return invalid(err)
		}
		if fmt.Sprint(in.Tab) != r.Target.ResourceID || m.Win == nil || !m.Win.MoveTabToGroup(in.Tab, in.Group, in.Before) {
			return fail(control.Unavailable, "exact tab/group placement unavailable")
		}
		m.focusMainGroup(in.Group)
		m.releaseTabBarFocus()
	case "window.move", "window.resize":
		var in struct {
			ID    string `json:"id"`
			Epoch uint64 `json:"epoch"`
			EX    int    `json:"expected_x"`
			EY    int    `json:"expected_y"`
			EW    int    `json:"expected_width"`
			EH    int    `json:"expected_height"`
			X     int    `json:"x"`
			Y     int    `json:"y"`
			W     int    `json:"width"`
			H     int    `json:"height"`
		}
		if err := decodeShellInput(r.Input, &in); err != nil {
			return invalid(err)
		}
		if m.windowManager == nil {
			return fail(control.Unavailable, "window manager unavailable")
		}
		w := m.windowManager.Window(in.ID)
		if w == nil || w != m.windowManager.Focused() || !w.Visible() || w.Modal() || in.ID != r.Target.ResourceID {
			return fail(control.Denied, "exact foreground nonmodal window required")
		}
		host, ok := w.Content.(interface{ Host() *ui.Host })
		x, y := w.Position()
		width, height := w.Size()
		if !ok || host.Host() == nil || host.Host().ControlLifetime() != in.Epoch || x != in.EX || y != in.EY || width != in.EW || height != in.EH {
			return fail(control.Stale, "window lifetime or bounds changed")
		}
		if r.CommandID == "window.move" {
			if !w.Movable() || in.X < 0 || in.Y < 0 || in.X+width > m.width || in.Y+height > m.height {
				return fail(control.Invalid, "window movement outside supported grid")
			}
			w.SetPosition(in.X, in.Y)
		} else {
			if !w.Resizable() || in.W < 1 || in.H < 1 || x+in.W > m.width || y+in.H > m.height {
				return fail(control.Invalid, "window size outside supported grid")
			}
			w.SetSize(in.W, in.H)
		}
		actualW, actualH := w.Size()
		actualX, actualY := w.Position()
		result.Output, _ = json.Marshal(control.Bounds{X: actualX, Y: actualY, Width: actualW, Height: actualH})
	case "terminal.dock", "terminal.float":
		var in struct {
			ID string `json:"id"`
		}
		if err := decodeShellInput(r.Input, &in); err != nil {
			return invalid(err)
		}
		if in.ID != r.Target.ResourceID || m.termMgr() == nil || m.termDock == nil || m.overlayHost == nil {
			return fail(control.Unavailable, "terminal owner unavailable")
		}
		if _, ok := m.terminalInfo(in.ID); !ok {
			return fail(control.Unavailable, "terminal missing")
		}
		if focused := m.windowManager.Focused(); focused != nil && focused.ID != string(ui.TerminalOverlayID(in.ID)) {
			return fail(control.Denied, "another floating owner owns input")
		}
		if r.CommandID == "terminal.dock" {
			m.dockTerminal(in.ID)
		} else {
			m.promoteTerminal(in.ID)
		}
		result.ResourceIDs = []string{in.ID}
	case "agent.select", "agent.minimize", "agent.restore":
		var in struct {
			ID string `json:"agent_id"`
		}
		if err := decodeShellInput(r.Input, &in); err != nil {
			return invalid(err)
		}
		if in.ID != r.Target.ResourceID || m.ws.AgentMgr().Get(in.ID) == nil {
			return fail(control.Unavailable, "exact agent missing")
		}
		if r.CommandID == "agent.minimize" {
			m.minimizeAgent(in.ID)
			m.handleTabSwitch()
			m.releaseTabBarFocus()
		} else {
			m.restoreAgent(in.ID)
		}
	case "agent.recover":
		var in struct {
			ID         string `json:"agent_id"`
			Incident   string `json:"incident_id"`
			Generation uint64 `json:"generation"`
		}
		if err := decodeShellInput(r.Input, &in); err != nil {
			return invalid(err)
		}
		if in.ID != r.Target.ResourceID || !m.canRetryAgentRecovery(in.ID) {
			return fail(control.Unavailable, "recovery unavailable")
		}
		status := m.ws.AgentMgr().Get(in.ID).State.RecoveryStatus()
		if status.ID != in.Incident || status.Generation != in.Generation {
			return fail(control.Stale, "recovery incident changed")
		}
		cmd := m.retryAgentRecovery(in.ID)
		if cmd == nil {
			return fail(control.Unavailable, "recovery owner unavailable")
		}
		return m.awaitShellControl(msg, cmd, nil), true
	case "job.stop":
		var in struct {
			Agent   string    `json:"agent_id"`
			Handle  string    `json:"handle"`
			PID     int       `json:"pid"`
			Started time.Time `json:"started_at"`
		}
		if err := decodeShellInput(r.Input, &in); err != nil {
			return invalid(err)
		}
		if r.Target.ResourceID != in.Agent || in.Handle == "" || in.PID <= 0 || in.Started.IsZero() {
			return fail(control.Invalid, "exact command identity required")
		}
		run, err := m.ws.PrepareAsyncCommandTermination(in.Agent, fileio.AsyncCmdRef{Handle: in.Handle, PID: in.PID, StartedAt: in.Started})
		if err != nil {
			return fail(control.Stale, err.Error())
		}
		return m.awaitShellControl(msg, func() tea.Msg { return productJobStopped{err: run()} }, nil), true
	case "editor.find", "project.search_open":
		var in struct {
			Replace *bool `json:"replace"`
		}
		if err := decodeShellInput(r.Input, &in); err != nil {
			return invalid(err)
		}
		if in.Replace == nil {
			return fail(control.Invalid, "replace required")
		}
		if r.CommandID == "editor.find" {
			ed := m.EditorState.Editor
			if ed == nil || !ed.IsOpen() || ed.DocumentState().DocumentID != r.Target.ResourceID {
				return fail(control.Unavailable, "focused document changed")
			}
			m.openFindReplace(*in.Replace)
		} else {
			var cmd tea.Cmd
			if *in.Replace {
				_, cmd = m.toggleReplaceInFiles()
			} else {
				_, cmd = m.toggleFindInFiles()
			}
			if m.findInFiles == nil {
				return fail(control.Unavailable, "project search unavailable")
			}
			result.State = control.Admitted
			m.forceFrame()
			return tea.Batch(cmd, m.replyShellControl(msg, result)), true
		}
	case "chat.follow", "chat.scroll":
		if m.AgentView.ActiveID() != r.Target.ResourceID || m.mainPanes == nil {
			return fail(control.Denied, "exact selected chat required")
		}
		view := m.mainPanes.agentViews[r.Target.ResourceID]
		if view == nil || view.chat == nil {
			return fail(control.Unavailable, "chat not mounted")
		}
		if r.CommandID == "chat.follow" {
			if err := decodeShellInput(r.Input, &struct{}{}); err != nil {
				return invalid(err)
			}
			view.chat.StickToBottom()
		} else {
			var in struct {
				Offset int `json:"offset"`
			}
			if err := decodeShellInput(r.Input, &in); err != nil {
				return invalid(err)
			}
			if in.Offset < 0 {
				return fail(control.Invalid, "negative offset")
			}
			view.chat.ScrollModel().ScrollTo(in.Offset)
			view.chat.ScrollModel().SetAutoScroll(false)
		}
	default:
		return m.handleComposerProduct(msg)
	}
	m.forceFrame()
	return m.replyShellControl(msg, result), true
}

type productJobStopped struct{ err error }

// Parent finishShellControl calls this from its default completion branch.
func (m *App) finishProductControl(done shellControlDoneMsg) (tea.Cmd, bool) {
	result := control.Result{State: control.Committed}
	var follow tea.Cmd
	switch completion := done.completion.(type) {
	case productJobStopped:
		if completion.err != nil {
			return m.failShellControl(done.request, control.OperationFailed, completion.err.Error()), true
		}
	case recoveryRetryResultMsg:
		if done.generation != m.workspaceSwitchGen.Load() || completion.generation != done.generation {
			return m.failShellControl(done.request, control.Stale, "recovery completion belongs to a replaced workspace"), true
		}
		if completion.err != nil {
			return m.failShellControl(done.request, control.Stale, completion.err.Error()), true
		}
		m.applyRecoveryRetryResult(completion)
		result.State = control.Admitted
	case inputAdmissionMsg:
		return m.finishComposerProduct(done, completion), true
	default:
		return nil, false
	}
	m.forceFrame()
	return tea.Batch(follow, m.replyShellControl(done.request, result)), true
}
