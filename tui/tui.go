package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/provider"
	"crude/sessiondb"
	"crude/tui/types"
	"crude/tui/viewmodel"
	"crude/workspace"
)

// TUI is the terminal UI layer. It holds the Bubble Tea program and
// provides methods for the app layer to send events into the TUI.
//
// The TUI does NOT own business state — it delegates everything to the
// workspace via the embedded viewmodel.App.
type TUI struct {
	app     *viewmodel.App
	Program *tea.Program
	doneCh  chan struct{}

	// OnQuit is called when the TUI program exits.
	OnQuit func()

	// OnExitMessage is called after OnQuit to display a farewell message.
	OnExitMessage func()
}

// AddOrchestrator wires a backend orchestrator's channels into the TUI.
// Called by the app layer after the workspace creates a new orchestrator.
func (t *TUI) AddOrchestrator(orchID string, inputCh chan<- provider.UserInput, btwCh chan<- provider.UserInput, cancelCh chan<- struct{}, blockResolveCh chan string) {
	t.app.AddOrchestrator(orchID, inputCh, btwCh, cancelCh, blockResolveCh)
}

var global *TUI

// Start creates and starts the terminal UI for the given workspace.
//
// The workspace must already be initialized. The app layer is responsible
// for setting workspace callbacks (OnNewSession, OnResumeSession) before
// calling Start.
func Start(ws *workspace.Workspace, presets []ProviderPreset, history []string, lg interface {
	Info(tag, msg string, pairs ...any)
	Warn(tag, msg string, pairs ...any)
	Error(tag, msg string, pairs ...any)
	Debug(tag, msg string, pairs ...any)
}) (*TUI, error) {
	t := &TUI{
		doneCh: make(chan struct{}),
	}
	global = t

	a := viewmodel.NewApp(ws, presets)
	a.SetHistory(history)
	// Force UV to use the same grapheme-width method as Crude layout and
	// surface packing. See TERMINAL_WIDTH.md.
	program := tea.NewProgram(a, tea.WithWidthMethod(ansi.GraphemeWidth))
	t.app = a
	t.Program = program
	a.SetProgram(program)

	go func() {
		if _, err := program.Run(); err != nil {
			lg.Error("TUI", "program_error", "error", err.Error())
		}
		if t.app != nil {
			t.app.StopLSP()
		}
		if t.OnQuit != nil {
			t.OnQuit()
		}
		if t.OnExitMessage != nil {
			t.OnExitMessage()
		}
		select {
		case <-t.doneCh:
		default:
			close(t.doneCh)
		}
	}()

	return t, nil
}

func (t *TUI) Stop() {
	select {
	case <-t.doneCh:
		return
	default:
		close(t.doneCh)
	}
	t.Program.Quit()
}

func (t *TUI) Wait() {
	<-t.doneCh
}

func (t *TUI) ReleaseTerminal() {
	_ = t.Program.ReleaseTerminal()
}

func (t *TUI) SendAgentEvent(typ string, data any) {
	t.SendAgentEventWithID(typ, data, "")
}

func (t *TUI) SendAgentEventWithID(typ string, data any, agentID string) {
	t.Program.Send(types.AgentMsg(AgentEventMsg{Type: typ, Data: data, AgentID: agentID}))
}

func (t *TUI) SetError(err string) {
	t.Program.Send(types.ErrorMsg(err))
}

func (t *TUI) UpdateModelName(name string) {
	t.Program.Send(types.UpdateModelMsg(name))
}

func (t *TUI) HydrateMessages(msgs []workspace.ChatMessageView) {
	t.Program.Send(types.HistoryMessagesMsg(viewmodel.ChatMessagesFromViews(msgs)))
}

func (t *TUI) OpenSettings() {
	t.Program.Send(types.OpenSettingsMsg{})
}

func (t *TUI) SetReferences(refs []sessiondb.Reference) {
	t.app.SetReferences(refs)
}

// ApplyThemePreset hot-applies a reference theme by preset name.
func (t *TUI) ApplyThemePreset(preset string) {
	if t == nil || t.app == nil || preset == "" {
		return
	}
	t.app.ApplyThemePreset(preset)
}

func GetGlobal() *TUI {
	return global
}
