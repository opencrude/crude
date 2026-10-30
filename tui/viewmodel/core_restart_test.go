package viewmodel

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/coreprotocol"
	"crude/shellport"
	"crude/tui/editor"
	"crude/tui/input"
	"crude/types"
	"crude/types/control"
	"crude/types/handover"
)

// Host behavior is independently controlled: Ready does not follow the shell's
// timers, Inspect does not fabricate Ready, and no fixture launches a process.
type restartTestHost struct {
	state                                               handover.State
	startupOperation, startupSession                    string
	schedules, inspections, cancels, readies, continues int
	readyErr, continueErr                               error
}

func (h *restartTestHost) StartupOperationID() string { return h.startupOperation }
func (h *restartTestHost) StartupSessionID() string   { return h.startupSession }
func (h *restartTestHost) Schedule(context.Context, control.Request) (handover.State, error) {
	h.schedules++
	return h.state, nil
}
func (h *restartTestHost) Inspect(_ context.Context, id string) (handover.State, error) {
	h.inspections++
	if id != h.state.OperationID {
		return handover.State{}, errors.New("foreign operation")
	}
	return h.state, nil
}
func (h *restartTestHost) Cancel(_ context.Context, id string) (handover.State, error) {
	h.cancels++
	if id != h.state.OperationID {
		return handover.State{}, errors.New("foreign cancellation")
	}
	h.state.Phase = handover.Cancelled
	return h.state, nil
}
func (h *restartTestHost) Ready(_ context.Context, id string) error {
	h.readies++
	if id != h.state.OperationID {
		return errors.New("foreign readiness")
	}
	return h.readyErr
}
func (h *restartTestHost) Continue(context.Context) (handover.State, error) {
	h.continues++
	state := h.state
	state.Phase = handover.Consumed
	return state, h.continueErr
}

type restartTestPort struct {
	shellport.Workspace
	target       control.Target
	broadResumes int
}

func (p *restartTestPort) ControlIdentity() control.Target                       { return p.target }
func (*restartTestPort) StartShellControl()                                      {}
func (*restartTestPort) StopShellControl()                                       {}
func (*restartTestPort) ReplyShellControl(context.Context, control.Result) error { return nil }
func (*restartTestPort) GoalForAgent(string) (types.Goal, bool) {
	return types.Goal{ID: "exact-goal", Objective: "Retain the exact goal", Status: types.GoalPaused}, true
}
func (p *restartTestPort) GoalAction(string, string, string) (types.Goal, bool, error) {
	p.broadResumes++
	return types.Goal{}, false, errors.New("broad goal action is not permitted by this fixture")
}

func restartTestApp(t *testing.T) (*App, *restartTestHost, *restartTestPort, string) {
	t.Helper()
	m := newFullTestApp(t)
	agent := addBackendTestOrchestrator(t, m)
	m.ActivateSessionByAgent(agent)
	base := m.ws
	p := &restartTestPort{Workspace: base, target: control.Target{WorkspaceID: "original-workspace", Shell: &control.ShellOwner{ClientID: "original-client", Epoch: 7}}}
	m.ws = p
	t.Cleanup(func() { m.StopAsyncIO(); m.ws = base })
	h := &restartTestHost{state: handover.State{OperationID: "exact-operation", GoalID: "exact-goal", Phase: handover.Reserved,
		SourceShell: *p.target.Shell, ExpiresAt: time.Now().Add(time.Minute)}}
	return m, h, p, agent
}

func restartEnvelope(t *testing.T, m *App, id string, input any) (shellport.ControlRequestMsg, *control.Result) {
	t.Helper()
	d, ok := ShellCommandDescriptor(id)
	if !ok {
		t.Fatal("missing descriptor", id)
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	s := m.inspectShell()
	p := m.ws.(shellport.ShellControl)
	target := p.ControlIdentity()
	target.SessionID = m.ActiveSession.SessionID
	result := new(control.Result)
	return shellport.ControlRequestMsg{Owner: p, Envelope: coreprotocol.ControlEnvelope{Descriptor: d,
		Request: control.Request{ID: id + "-request", CommandID: id, Caller: control.Caller{Kind: control.ExternalCaller, ClientID: "controller"},
			Target: target, Input: raw, Preconditions: control.Preconditions{Revision: &s.Revision, OpenEpoch: &s.OpenEpoch}}},
		Reply: func(_ context.Context, got control.Result) error { *result = got; return nil }}, result
}

func restartInput() shellport.CoreRestartInput {
	return shellport.CoreRestartInput{Executable: "/exact/image", SHA256: strings.Repeat("a", 64), GoalID: "exact-goal", DelaySeconds: 3}
}

func restartPendingPaste(m *App) func() {
	var event input.Event
	m.Input, _, event = m.Input.Update(tea.PasteMsg{Content: strings.Repeat("paste", 1000)}, false, false)
	return func() { m.Input.CompletePaste(event.(input.PasteAttachmentEvent)) }
}

func restartEditor(t *testing.T, m *App) *editor.Editor {
	t.Helper()
	path := filepath.Join(t.TempDir(), "file.go")
	if err := os.WriteFile(path, []byte("clean"), 0600); err != nil {
		t.Fatal(err)
	}
	ed := editor.New(m.Theme)
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ed.Close)
	m.EditorState.Tabs[path] = &EditorTab{Path: path, Editor: ed}
	return ed
}

// Execute one command body, not its successor timer. This exercises the real
// App.Update envelope/completion path while giving tests control over interleaved
// human edits, host facts and late asynchronous delivery.
func restartCommandMessage(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("missing command")
	}
	message := cmd()
	if batch, ok := message.(tea.BatchMsg); ok {
		var relevant tea.Msg
		for _, next := range batch {
			got := restartCommandMessage(t, next)
			switch got.(type) {
			case coreRestartResultMsg, shellControlReplyMsg, tea.QuitMsg:
				if relevant != nil {
					t.Fatal("multiple restart commands in one transition")
				}
				relevant = got
			}
		}
		return relevant
	}
	return message
}

func restartSchedule(t *testing.T, m *App, h *restartTestHost) {
	t.Helper()
	if err := m.ConfigureCoreRestart(h); err != nil {
		t.Fatal(err)
	}
	msg, reply := restartEnvelope(t, m, "core.restart", restartInput())
	_, command := m.Update(msg)
	if h.schedules != 0 || m.CoreRestartOperationID() != "" {
		t.Fatal("host I/O or quit ran on Update")
	}
	completion := restartCommandMessage(t, command)
	if h.schedules != 1 {
		t.Fatal("schedule command did not reach exact host")
	}
	_, ack := m.Update(completion)
	if reply.State != "" || m.CoreRestartOperationID() != "" {
		t.Fatal("quit or acknowledgement before command reply")
	}
	ackMessage := restartCommandMessage(t, ack)
	if reply.State != control.Committed || reply.OperationID != "exact-operation" {
		t.Fatalf("not scheduled: %+v", reply)
	}
	if strings.Contains(string(reply.Output), "token") || strings.Contains(string(reply.Output), "nonce") {
		t.Fatal("private credential exposed")
	}
	_, timer := m.Update(ackMessage)
	if timer == nil || m.CoreRestartOperationID() != "" {
		t.Fatal("ack did not arm bounded projection polling")
	}
	if !strings.Contains(ansi.Strip(m.View().RenderContent()), "Core restart scheduled") {
		t.Fatal("scheduled feedback is not visible in the shared shell status")
	}
}

func restartPoll(t *testing.T, m *App) coreRestartResultMsg {
	t.Helper()
	c := m.coreRestart
	_, command := m.Update(coreRestartPollMsg{controller: c, operation: c.operation, sequence: c.operation.sequence})
	result, ok := restartCommandMessage(t, command).(coreRestartResultMsg)
	if !ok {
		t.Fatal("poll did not use owned host command")
	}
	return result
}

func TestCoreRestartScheduleAcknowledgesBeforeNaturalReadyExit(t *testing.T) {
	m, h, _, _ := restartTestApp(t)
	m.Input.SetInput("retain composer draft")
	restartSchedule(t, m, h)
	_, next := m.Update(restartPoll(t, m))
	if next == nil || h.readies != 0 || m.CoreRestartOperationID() != "" || m.Input.Value() != "retain composer draft" {
		t.Fatal("reserved/running operation quit or consumed the draft")
	}
	h.state.Phase, h.readyErr = handover.Ready, shellport.ErrCoreRestartPending
	_, next = m.Update(restartPoll(t, m))
	if next == nil || h.readies != 1 || m.CoreRestartOperationID() != "" {
		t.Fatal("guessed delay completion")
	}
	h.readyErr = nil
	_, quit := m.Update(restartPoll(t, m))
	if _, ok := restartCommandMessage(t, quit).(tea.QuitMsg); !ok || m.CoreRestartOperationID() != "exact-operation" {
		t.Fatal("ready exact operation did not quit safely")
	}
	if m.Input.Value() != "retain composer draft" {
		t.Fatal("restart discarded draft")
	}
}

func TestCoreRestartLateDirtyStateAndStaleResultsNeverQuit(t *testing.T) {
	for _, boundary := range []string{"dirty", "save", "paste", "workspace", "shell_epoch", "cancel"} {
		t.Run(boundary, func(t *testing.T) {
			m, h, p, _ := restartTestApp(t)
			restartSchedule(t, m, h)
			h.state.Phase = handover.Ready
			completion := restartPoll(t, m)
			switch boundary {
			case "dirty":
				restartEditor(t, m).InsertRune('x')
			case "save":
				ed := restartEditor(t, m)
				m.editorSaves = map[string]*editorSaveState{ed.BufferID(): {inFlight: true}}
			case "paste":
				finish := restartPendingPaste(m)
				defer finish()
			case "workspace":
				m.workspaceSwitchGen.Add(1)
			case "shell_epoch":
				p.target.Shell = &control.ShellOwner{ClientID: "original-client", Epoch: 8}
			case "cancel":
				cancel, reply := restartEnvelope(t, m, "core.restart.cancel", map[string]string{"operation_id": "exact-operation"})
				_, cmd := m.Update(cancel)
				if h.cancels != 0 {
					t.Fatal("cancel did I/O on Update")
				}
				result := restartCommandMessage(t, cmd)
				_, cmd = m.Update(result)
				restartCommandMessage(t, cmd)
				if reply.State != control.Applied || h.cancels != 1 {
					t.Fatal("cancel failed", reply)
				}
			}
			_, cmd := m.Update(completion)
			if m.CoreRestartOperationID() != "" {
				t.Fatal("late ready result quit after boundary")
			}
			if (boundary == "workspace" || boundary == "shell_epoch" || boundary == "cancel") && cmd != nil {
				t.Fatal("stale ready result scheduled more work")
			}
		})
	}
}

func TestCoreRestartRejectsUnsafeAdmissionBeforeHostIO(t *testing.T) {
	for _, reason := range []string{"missing_host", "foreign_host", "foreign_shell", "revision", "epoch", "descriptor", "session", "dirty", "save", "paste", "modal", "async_io", "draft_loading", "session_io", "invalid_delay"} {
		t.Run(reason, func(t *testing.T) {
			m, h, _, _ := restartTestApp(t)
			if reason != "missing_host" {
				if err := m.ConfigureCoreRestart(h); err != nil {
					t.Fatal(err)
				}
			}
			if reason == "foreign_host" {
				m.workspaceSwitchGen.Add(1)
			}
			request, result := restartEnvelope(t, m, "core.restart", restartInput())
			switch reason {
			case "foreign_shell":
				request.Envelope.Request.Target.Shell = &control.ShellOwner{ClientID: "foreign", Epoch: 7}
			case "revision":
				request.Envelope.Request.Preconditions.Revision = nil
			case "epoch":
				request.Envelope.Request.Preconditions.OpenEpoch = nil
			case "descriptor":
				request.Envelope.Descriptor.Effect = control.Read
			case "session":
				request.Envelope.Request.Target.SessionID = ""
			case "dirty", "save":
				ed := restartEditor(t, m)
				if reason == "dirty" {
					ed.InsertRune('x')
				} else {
					m.editorSaves = map[string]*editorSaveState{ed.BufferID(): {inFlight: true}}
				}
			case "paste":
				finish := restartPendingPaste(m)
				defer finish()
			case "modal":
				m.openApplicationSettings()
			case "draft_loading":
				m.draftOwner(m.ActiveSession).loading = true
			case "session_io":
				m.sessionLifecyclePending = true
			case "async_io":
				state := asyncIOStateFor(m)
				state.mu.Lock()
				state.clientIOJobs[900] = func() {}
				state.mu.Unlock()
				defer func() { state.mu.Lock(); delete(state.clientIOJobs, 900); state.mu.Unlock() }()
			case "invalid_delay":
				request.Envelope.Request.Input = json.RawMessage(`{"executable":"/image","sha256":"` + strings.Repeat("a", 64) + `","goal_id":"g","delay_seconds":301}`)
			}
			_, cmd := m.Update(request)
			restartCommandMessage(t, cmd)
			if result.State != control.Failed || h.schedules != 0 || m.CoreRestartOperationID() != "" {
				t.Fatalf("unsafe admission reached host: %+v", result)
			}
		})
	}
}

func TestCoreRestartFatalReadyAndTerminalStateStayInOriginalShell(t *testing.T) {
	for _, phase := range []handover.Phase{handover.Ready, handover.Cancelled, handover.Expired, handover.Invalidated} {
		t.Run(string(phase), func(t *testing.T) {
			m, h, _, _ := restartTestApp(t)
			restartSchedule(t, m, h)
			h.state.Phase = phase
			if phase == handover.Ready {
				h.readyErr = errors.New("changed image")
			}
			_, cmd := m.Update(restartPoll(t, m))
			if cmd != nil || m.CoreRestartOperationID() != "" || m.coreRestart.operation.live {
				t.Fatal("failed/terminal operation kept polling or quit")
			}
		})
	}
}

func TestCoreRestartStartupExactPermitDoesNotBroadlyResume(t *testing.T) {
	for _, scenario := range []string{"ordinary", "foreign", "permitted", "failed"} {
		t.Run(scenario, func(t *testing.T) {
			m, h, p, agent := restartTestApp(t)
			if scenario != "ordinary" {
				h.startupOperation, h.startupSession = "exact-operation", m.ActiveSession.SessionID
				if scenario == "foreign" {
					h.startupSession = "foreign-session"
				}
				if scenario == "failed" {
					h.continueErr = errors.New("goal permit changed")
				}
				if err := m.ConfigureCoreRestart(h); err != nil {
					t.Fatal(err)
				}
			}
			m.offerGoalResume(agent)
			prompt := m.activeSessionProjection().decisionAgentID != ""
			if scenario == "ordinary" || scenario == "foreign" {
				if !prompt || m.drainCoreRestartStartup() != nil {
					t.Fatal("normal reopen did not retain resume prompt")
				}
				if !strings.Contains(ansi.Strip(m.View().RenderContent()), "Resume the goal?") {
					t.Fatal("resume prompt was not rendered")
				}
				return
			}
			if prompt || h.continues != 0 {
				t.Fatal("permit showed broad prompt or did I/O on Update")
			}
			cmd := m.drainCoreRestartStartup()
			if cmd == nil {
				t.Fatal("mounted permitted session did not queue narrow continuation")
			}
			_, follow := m.Update(restartCommandMessage(t, cmd))
			if follow != nil || h.continues != 1 || p.broadResumes != 0 || m.drainCoreRestartStartup() != nil {
				t.Fatal("continuation repeated or broadened authority")
			}
			if (m.activeSessionProjection().decisionAgentID != "") != (scenario == "failed") {
				t.Fatal("failed continuation did not restore ordinary user resume")
			}
		})
	}
}

func TestCoreRestartExactInspectAndCancelCannotAddressForeignOperation(t *testing.T) {
	m, h, _, _ := restartTestApp(t)
	restartSchedule(t, m, h)
	for _, command := range []string{"core.restart.inspect", "core.restart.cancel"} {
		foreign, reply := restartEnvelope(t, m, command, map[string]string{"operation_id": "foreign-operation"})
		_, cmd := m.Update(foreign)
		restartCommandMessage(t, cmd)
		if reply.State != control.Failed || h.cancels != 0 || h.inspections != 0 {
			t.Fatal("foreign operation reached host", reply)
		}
	}
	inspect, reply := restartEnvelope(t, m, "core.restart.inspect", map[string]string{"operation_id": "exact-operation"})
	_, command := m.Update(inspect)
	if h.inspections != 0 {
		t.Fatal("Inspect ran on Update")
	}
	completion := restartCommandMessage(t, command)
	_, command = m.Update(completion)
	restartCommandMessage(t, command)
	if reply.State != control.Applied || reply.OperationID != "exact-operation" || h.inspections != 1 || h.readies != 0 ||
		strings.Contains(string(reply.Output), "token") || m.CoreRestartOperationID() != "" {
		t.Fatal("inspection altered lifecycle or exposed a credential", reply)
	}
}

func TestCoreRestartLateScheduleRetainsCommittedEvidenceWithoutPresentation(t *testing.T) {
	m, h, _, _ := restartTestApp(t)
	if err := m.ConfigureCoreRestart(h); err != nil {
		t.Fatal(err)
	}
	msg, reply := restartEnvelope(t, m, "core.restart", restartInput())
	_, command := m.Update(msg)
	completion := restartCommandMessage(t, command)
	m.workspaceSwitchGen.Add(1)
	_, command = m.Update(completion)
	restartCommandMessage(t, command)
	if reply.State != control.Partial || reply.OperationID != "exact-operation" || m.CoreRestartOperationID() != "" || h.schedules != 1 {
		t.Fatal("late committed reservation was lost or presented", reply)
	}
}
