package tui

import (
	"container/list"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/history"
	"crude/provider"
	"crude/sessiondb"
	"crude/tui/rendering"
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
	app      *viewmodel.App
	Program  *tea.Program
	outbound *programMessagePump
	doneCh   chan struct{}
	stopOnce sync.Once

	// OnQuit is called when the TUI program exits.
	OnQuit func()

	// OnExitMessage is called after OnQuit to display a farewell message.
	OnExitMessage func()
}

// programMessagePump serializes messages from background producers without
// making them wait for Bubble Tea's Update loop. The queue is intentionally
// unbounded: agent lifecycle events are critical and must not be dropped under
// temporary UI backpressure.
//
// close drains every message accepted before shutdown and rejects later
// enqueues. closeAfter additionally places one final message (normally
// tea.QuitMsg) after all accepted work, then closes the queue atomically.
type programMessagePump struct {
	mu      sync.Mutex
	ready   *sync.Cond
	pending list.List
	deliver func(tea.Msg)
	closed  bool
	done    chan struct{}
}

func newProgramMessagePump(deliver func(tea.Msg)) *programMessagePump {
	p := &programMessagePump{
		deliver: deliver,
		done:    make(chan struct{}),
	}
	p.ready = sync.NewCond(&p.mu)
	go p.run()
	return p
}

// enqueue adds msg in O(1) without waiting for delivery.
func (p *programMessagePump) enqueue(msg tea.Msg) bool {
	if p == nil || p.deliver == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false
	}
	p.pending.PushBack(msg)
	p.ready.Signal()
	return true
}

func (p *programMessagePump) close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	if !p.closed {
		p.closed = true
		p.ready.Broadcast()
	}
	p.mu.Unlock()
}

func (p *programMessagePump) closeAfter(final tea.Msg) {
	if p == nil {
		return
	}
	p.mu.Lock()
	if !p.closed {
		p.pending.PushBack(final)
		p.closed = true
		p.ready.Broadcast()
	}
	p.mu.Unlock()
}

func (p *programMessagePump) wait() {
	if p != nil {
		<-p.done
	}
}

func (p *programMessagePump) run() {
	defer close(p.done)
	for {
		p.mu.Lock()
		for p.pending.Len() == 0 && !p.closed {
			p.ready.Wait()
		}
		if p.pending.Len() == 0 {
			p.mu.Unlock()
			return
		}
		next := p.pending.Front()
		msg := next.Value
		p.pending.Remove(next)
		p.mu.Unlock()

		p.deliver(msg)
	}
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
func Start(ws *workspace.Workspace, presets []ProviderPreset, prompts *history.Store, lg interface {
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
	a.SetPromptHistory(prompts)
	// Force UV to use the same grapheme-width method as Crude layout and
	// surface packing. See TERMINAL_WIDTH.md.
	program := tea.NewProgram(
		a,
		tea.WithWidthMethod(ansi.GraphemeWidth),
		tea.WithWidthProbe(rendering.TerminalWidthProbeCorpus()...),
	)
	t.app = a
	t.Program = program
	t.outbound = newProgramMessagePump(program.Send)
	a.SetProgram(program)

	go func() {
		if _, err := program.Run(); err != nil {
			lg.Error("TUI", "program_error", "error", err.Error())
		}
		t.outbound.close()
		t.outbound.wait()
		if t.app != nil {
			t.app.StopAsyncIO()
			t.app.StopLSP()
		}
		if t.OnQuit != nil {
			t.OnQuit()
		}
		if t.OnExitMessage != nil {
			t.OnExitMessage()
		}
		close(t.doneCh)
	}()

	return t, nil
}

func (t *TUI) Stop() {
	if t == nil {
		return
	}
	t.stopOnce.Do(func() {
		// Closing with Quit as the final queued message guarantees that every
		// event accepted before Stop is delivered first. Enqueues racing after
		// shutdown begins are rejected without a send-on-closed panic.
		t.outbound.closeAfter(tea.Quit())
	})
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
	t.send(types.AgentMsg(AgentEventMsg{Type: typ, Data: data, AgentID: agentID}))
}

func (t *TUI) SetError(err string) {
	t.send(types.ErrorMsg(err))
}

func (t *TUI) UpdateModelName(name string) {
	t.send(types.UpdateModelMsg(name))
}

func (t *TUI) HydrateMessages(msgs []workspace.ChatMessageView) {
	t.send(types.HistoryMessagesMsg(viewmodel.ChatMessagesFromViews(msgs)))
}

func (t *TUI) OpenSettings() {
	t.send(types.OpenSettingsMsg{})
}

func (t *TUI) send(msg tea.Msg) {
	if t != nil {
		t.outbound.enqueue(msg)
	}
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
