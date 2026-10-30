package viewmodel

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/types/control"
	"crude/types/handover"
)

// The application owns staging and the private intent. This controller owns
// only the original shell's nonsecret projection and asynchronous presentation.
type coreRestartController struct {
	host                             shellport.CoreRestartHost
	owner                            shellport.Workspace
	generation                       uint64
	startupOperation, startupSession string
	startupAgent                     string
	startupQueued, startupAttempted  bool
	operation                        *coreRestartOperation
	exitID                           string
}

type coreRestartOperation struct {
	owner      shellport.Workspace
	generation uint64
	sessionID  string
	shell      control.ShellOwner
	state      handover.State
	sequence   uint64
	live       bool
	blocked    bool
}

type coreRestartResultMsg struct {
	controller *coreRestartController
	operation  *coreRestartOperation
	owner      shellport.Workspace
	generation uint64
	sequence   uint64
	kind       string
	request    *shellport.ControlRequestMsg
	state      handover.State
	ready      bool
	err        error
}

type coreRestartPollMsg struct {
	controller *coreRestartController
	operation  *coreRestartOperation
	sequence   uint64
}

// ConfigureCoreRestart is called by the local application before Update starts.
// Startup metadata is read here, never through an I/O-capable host on Update.
func (m *App) ConfigureCoreRestart(host shellport.CoreRestartHost) error {
	if m.coreRestart != nil {
		return errors.New("core restart host is already configured")
	}
	if host == nil {
		return nil
	}
	c := &coreRestartController{host: host, owner: m.ws, generation: m.workspaceSwitchGen.Load()}
	c.startupOperation, c.startupSession = host.StartupOperationID(), host.StartupSessionID()
	if (c.startupOperation == "") != (c.startupSession == "") {
		return errors.New("core restart startup requires exact operation and session")
	}
	m.coreRestart = c
	return nil
}

// CoreRestartOperationID is read by the TUI only after its terminal Wait barrier.
func (m *App) CoreRestartOperationID() string {
	if m == nil || m.coreRestart == nil || m.composerDraftErr != nil {
		return ""
	}
	return m.coreRestart.exitID
}

func (m *App) coreRestartAvailability() control.Availability {
	if m.coreRestart == nil || m.coreRestart.host == nil || m.coreRestart.owner != m.ws ||
		m.coreRestart.generation != m.workspaceSwitchGen.Load() || m.asyncIOStopped.Load() {
		return control.Availability{Reason: control.TargetUnavailable, Message: "Original local terminal restart host is unavailable"}
	}
	return control.Availability{Available: true}
}

// coreRestartBlocked never saves, discards, stops a run or resolves a decision.
// It observes the existing owners, including byte-clean editors with pending
// saves/history, rather than assuming Modified is the complete I/O barrier.
func (m *App) coreRestartBlocked() string {
	if m.asyncIOStopped.Load() || m.workspaceAttachPending || m.workspaceSwitchIntent != nil ||
		m.sessionDeckBlocking() || (m.windowManager != nil && m.windowManager.Focused() != nil) {
		return "a workspace transition, modal or blocking decision owns this shell"
	}
	if m.Input.HasPendingPastes() {
		return "a paste is still being attached"
	}
	if m.sessionLifecyclePending || len(m.pendingInputAdmissions) > 0 || len(m.pendingFirstInputs) > 0 || len(m.pastedTextRequests) > 0 {
		return "session input or attachment I/O is still in progress"
	}
	for _, draft := range m.composerDraftOwners {
		if draft.loading || (draft.input != nil && draft.input.HasPendingPastes()) {
			return "a retained composer draft or paste is still loading"
		}
	}
	for _, tab := range m.EditorState.Tabs {
		if tab == nil || tab.Editor == nil || !tab.Editor.IsOpen() {
			continue
		}
		ed := tab.Editor
		if ed.Modified() {
			return "save all modified editors before restarting"
		}
		if pending, err := m.workspaceSwitchEditorPending(ed); pending || err != nil {
			return "an editor save, format, edit or history operation is unsettled"
		}
		if live := m.liveEditors[ed]; live != nil && live.format {
			return "an editor format is unsettled"
		}
	}
	if m.shellControlPending > 0 || m.terminalBusy || m.projectReplace != nil ||
		m.gitRemoteCancel != nil || m.gitCommitWriteCancel != nil || m.toolLayoutSaving {
		return "an owned shell operation is still in progress"
	}
	if state := m.asyncIO.Load(); state != nil {
		state.mu.Lock()
		busy := len(state.clientIOJobs) > 0 || len(state.latestClipboard) > 0
		state.mu.Unlock()
		if busy {
			return "owned asynchronous I/O is still in progress"
		}
	}
	return ""
}

// Called only after the ordinary descriptor, owner, current-shell, lifecycle
// precondition and modal admission pipeline in handleShellControl succeeds.
func (m *App) handleCoreRestartControl(msg shellport.ControlRequestMsg) tea.Cmd {
	if a := m.coreRestartAvailability(); !a.Available {
		return m.failShellControl(msg, control.Unavailable, a.Message)
	}
	r, c := msg.Envelope.Request, m.coreRestart
	if r.Target.Shell == nil || r.Target.SessionID == "" {
		return m.failShellControl(msg, control.Invalid, "exact original shell and target session required")
	}
	if r.Target.ResourceID != "" {
		return m.failShellControl(msg, control.Invalid, "restart operations use operation_id, not a resource target")
	}
	if r.CommandID == "core.restart" {
		var in shellport.CoreRestartInput
		if err := decodeShellInput(r.Input, &in); err != nil {
			return m.failShellControl(msg, control.Invalid, err.Error())
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(r.Input, &fields) != nil || fields["delay_seconds"] == nil || string(fields["delay_seconds"]) == "null" {
			return m.failShellControl(msg, control.Invalid, "delay_seconds is required")
		}
		digest, err := hex.DecodeString(in.SHA256)
		if strings.TrimSpace(in.Executable) == "" || strings.TrimSpace(in.GoalID) == "" ||
			err != nil || len(digest) != 32 || strings.ToLower(in.SHA256) != in.SHA256 || in.DelaySeconds < 0 || in.DelaySeconds > 300 {
			return m.failShellControl(msg, control.Invalid, "exact executable, lowercase SHA-256, goal_id and delay_seconds 0..300 required")
		}
		if c.operation != nil && c.operation.live {
			return m.failShellControl(msg, control.Denied, "an original-terminal restart is already scheduled")
		}
		if reason := m.coreRestartBlocked(); reason != "" {
			return m.failShellControl(msg, control.Denied, reason)
		}
		op := &coreRestartOperation{owner: m.ws, generation: m.workspaceSwitchGen.Load(), sessionID: r.Target.SessionID, shell: *r.Target.Shell,
			state: handover.State{GoalID: in.GoalID}, live: true}
		c.operation = op
		return m.coreRestartCommand(c, op, "schedule", &msg, func(ctx context.Context) (handover.State, error) {
			return c.host.Schedule(ctx, r)
		})
	}
	var in struct {
		OperationID string `json:"operation_id"`
	}
	if err := decodeShellInput(r.Input, &in); err != nil {
		return m.failShellControl(msg, control.Invalid, err.Error())
	}
	op := c.operation
	if in.OperationID == "" || op == nil || in.OperationID != op.state.OperationID || r.Target.SessionID != op.sessionID ||
		op.owner != m.ws || op.generation != m.workspaceSwitchGen.Load() {
		return m.failShellControl(msg, control.Unavailable, "exact restart operation is not owned by this shell session")
	}
	if r.CommandID == "core.restart.cancel" {
		// Fence a Ready result already in flight before dispatching cancellation.
		op.sequence++
		op.live = false
		c.exitID = ""
		return m.coreRestartCommand(c, op, "cancel", &msg, func(ctx context.Context) (handover.State, error) {
			return c.host.Cancel(ctx, in.OperationID)
		})
	}
	return m.coreRestartCommand(c, op, "inspect", &msg, func(ctx context.Context) (handover.State, error) {
		return c.host.Inspect(ctx, in.OperationID)
	})
}

func (m *App) coreRestartCommand(c *coreRestartController, op *coreRestartOperation, kind string, request *shellport.ControlRequestMsg, run func(context.Context) (handover.State, error)) tea.Cmd {
	result := coreRestartResultMsg{controller: c, operation: op, owner: m.ws, generation: m.workspaceSwitchGen.Load(), sequence: op.sequence, kind: kind, request: request}
	return m.clientIOCommand(context.Background(), func(ctx context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		result.state, result.err = run(ctx)
		return result
	})
}

func (m *App) coreRestartOwned(msg coreRestartResultMsg) bool {
	if msg.controller == nil || msg.operation == nil {
		return false
	}
	port, attached := m.ws.(shellport.ShellControl)
	return attached && port.ControlIdentity().Shell != nil && *port.ControlIdentity().Shell == msg.operation.shell &&
		!m.asyncIOStopped.Load() && m.coreRestart == msg.controller && msg.owner == m.ws &&
		msg.generation == m.workspaceSwitchGen.Load() && msg.controller.operation == msg.operation &&
		msg.sequence == msg.operation.sequence
}

func (m *App) coreRestartTimer(c *coreRestartController, op *coreRestartOperation) tea.Cmd {
	msg := coreRestartPollMsg{controller: c, operation: op, sequence: op.sequence}
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return msg })
}

func (m *App) pollCoreRestart(msg coreRestartPollMsg) tea.Cmd {
	c, op := msg.controller, msg.operation
	if !m.coreRestartOwned(coreRestartResultMsg{controller: c, operation: op, owner: op.owner, generation: op.generation, sequence: msg.sequence}) || !op.live {
		return nil
	}
	if !op.state.ExpiresAt.IsZero() && !time.Now().Before(op.state.ExpiresAt) {
		op.live = false
		m.Feedback.NotifyError("Core restart expired; this terminal and drafts are retained")
		return nil
	}
	allowReady := true
	if reason := m.coreRestartBlocked(); reason != "" {
		if !op.blocked {
			m.Feedback.NotifyInfo("Core restart scheduled, waiting: " + reason)
			op.blocked = true
		}
		allowReady = false
	} else {
		op.blocked = false
	}
	id := op.state.OperationID
	result := coreRestartResultMsg{controller: c, operation: op, owner: op.owner, generation: op.generation, sequence: op.sequence, kind: "poll"}
	return m.clientIOCommand(context.Background(), func(ctx context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		result.state, result.err = c.host.Inspect(ctx, id)
		if result.err == nil && result.state.OperationID == id && result.state.Phase == handover.Ready && allowReady {
			result.err = c.host.Ready(ctx, id)
			result.ready = result.err == nil
		}
		return result
	})
}

func (m *App) applyCoreRestartResult(msg coreRestartResultMsg) tea.Cmd {
	if !m.coreRestartOwned(msg) {
		if msg.request != nil {
			if msg.err == nil && msg.state.OperationID != "" && msg.kind != "inspect" {
				output, _ := json.Marshal(msg.state)
				return m.replyShellControl(*msg.request, control.Result{State: control.Partial, OperationID: msg.state.OperationID, Output: output,
					Fault: &control.Fault{Code: control.Stale, Message: "restart operation committed but original shell presentation changed; inspect the exact operation"}})
			}
			return m.failShellControl(*msg.request, control.Stale, "original restart shell owner changed; observe the exact operation")
		}
		return nil
	}
	c, op := msg.controller, msg.operation
	if msg.kind == "ack" {
		if msg.err != nil {
			op.live = false
			m.Feedback.NotifyError("Core restart scheduled, but acknowledgement failed; inspect or cancel the exact operation")
			return nil
		}
		return m.coreRestartTimer(c, op)
	}
	if msg.err != nil {
		if msg.kind == "poll" && errors.Is(msg.err, shellport.ErrCoreRestartPending) {
			return m.coreRestartTimer(c, op)
		}
		if msg.kind != "inspect" {
			op.live = false
		}
		m.Feedback.NotifyError("Core restart " + msg.kind + " failed: " + msg.err.Error())
		if msg.kind == "continue" {
			// The failed narrow permit never invokes broad goal resume.
			m.offerGoalResume(c.startupAgent)
		}
		if msg.request != nil {
			return m.failShellControl(*msg.request, control.OperationFailed, msg.err.Error())
		}
		return nil
	}
	if msg.state.OperationID == "" || (op.state.OperationID != "" && msg.state.OperationID != op.state.OperationID) {
		op.live = false
		if msg.request != nil {
			return m.failShellControl(*msg.request, control.Stale, "restart host returned a different operation")
		}
		m.Feedback.NotifyError("Core restart operation changed; terminal retained")
		return nil
	}
	if msg.kind != "continue" && (msg.state.SourceShell != op.shell || msg.state.GoalID != op.state.GoalID) {
		op.live = false
		if msg.request != nil {
			return m.failShellControl(*msg.request, control.Stale, "restart host returned a foreign shell or goal")
		}
		m.Feedback.NotifyError("Core restart shell or goal changed; terminal retained")
		return nil
	}
	if msg.kind == "schedule" && (msg.state.ExpiresAt.IsZero() || !time.Now().Before(msg.state.ExpiresAt) ||
		(msg.state.Phase != handover.Reserved && msg.state.Phase != handover.Ready)) {
		op.live = false
		return m.failShellControl(*msg.request, control.OperationFailed, "restart reservation is not live with a bounded deadline")
	}
	if msg.kind == "continue" {
		op.live = false
		if msg.state.OperationID != c.startupOperation || msg.state.Phase != handover.Consumed {
			m.Feedback.NotifyError("Core restart continuation was not consumed; goal remains passive")
			m.offerGoalResume(c.startupAgent)
			return nil
		}
		m.Feedback.NotifyInfo("Core restart continuation accepted")
		return nil
	}
	op.state = msg.state
	terminal := msg.state.Phase != handover.Reserved && msg.state.Phase != handover.Ready
	if terminal {
		op.live = false
		m.Feedback.NotifyInfo("Core restart " + string(msg.state.Phase) + "; terminal retained")
	}
	if msg.request != nil {
		result := control.Result{State: control.Applied, OperationID: msg.state.OperationID}
		result.Output, _ = json.Marshal(msg.state)
		if msg.kind == "schedule" {
			result.State = control.Committed
			m.Feedback.NotifyInfo("Core restart scheduled; waiting for the source root to finish naturally")
			reply := m.replyShellControl(*msg.request, result)
			msg.kind, msg.request = "ack", nil
			return func() tea.Msg {
				if replyResult, ok := reply().(shellControlReplyMsg); ok {
					msg.err = replyResult.err
				}
				return msg
			}
		}
		return m.replyShellControl(*msg.request, result)
	}
	if msg.kind == "poll" && op.live {
		if msg.ready && msg.state.Phase == handover.Ready && m.coreRestartBlocked() == "" {
			c.exitID = msg.state.OperationID
			op.live = false
			m.Feedback.NotifyInfo("Core restart ready; releasing this terminal safely")
			return nil
		}
		return m.coreRestartTimer(c, op)
	}
	return nil
}

// The outer Update can enqueue autosave/layout work after applying a message.
// Recheck after those owners have changed, before publishing Quit.
func (m *App) finishCoreRestartUpdate() tea.Cmd {
	c := m.coreRestart
	if c == nil || c.exitID == "" {
		return nil
	}
	op := c.operation
	if op == nil || !m.coreRestartOwned(coreRestartResultMsg{controller: c, operation: op, owner: op.owner, generation: op.generation, sequence: op.sequence}) {
		c.exitID = ""
		return nil
	}
	if reason := m.coreRestartBlocked(); reason != "" {
		c.exitID, op.live = "", true
		return m.coreRestartTimer(c, op)
	}
	return tea.Quit
}

// Only the exact session named by the private startup intent skips the ordinary
// prompt, and only once. Other sessions and later ordinary reopens are unchanged.
func (m *App) queueCoreRestartStartup(agentID string) bool {
	c := m.coreRestart
	if c == nil || !m.coreRestartAvailability().Available || c.startupOperation == "" || c.startupAttempted {
		return false
	}
	session := m.ws.SessionsByAgent()[agentID]
	if session == nil || session.SessionID != c.startupSession {
		return false
	}
	c.startupAgent, c.startupQueued = agentID, true
	return true
}

func (m *App) drainCoreRestartStartup() tea.Cmd {
	c := m.coreRestart
	if c == nil || !m.coreRestartAvailability().Available || !c.startupQueued || c.startupAttempted || m.ActiveSession == nil ||
		m.ActiveSession.SessionID != c.startupSession || m.ws.SessionsByAgent()[c.startupAgent] != m.ActiveSession {
		return nil
	}
	port, ok := m.ws.(shellport.ShellControl)
	if !ok || port.ControlIdentity().Shell == nil {
		return nil
	}
	c.startupAttempted, c.startupQueued = true, false
	op := &coreRestartOperation{owner: m.ws, generation: m.workspaceSwitchGen.Load(), sessionID: c.startupSession,
		shell: *port.ControlIdentity().Shell, state: handover.State{OperationID: c.startupOperation}}
	c.operation = op
	return m.coreRestartCommand(c, op, "continue", nil, c.host.Continue)
}
