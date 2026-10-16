package tui

import (
	"container/list"
	"strings"
	"sync"
	"time"

	"crude/agent"
	"crude/clientstate"
	"crude/shellport"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/history"

	"crude/sessiondb"
	"crude/tui/rendering"
	"crude/tui/viewmodel"
	roottypes "crude/types"
	"crude/types/reasoning"
	"crude/workspace"
)

// TUI is the terminal UI layer. It holds the Bubble Tea program and
// provides methods for the app layer to send events into the TUI.
//
// The TUI does NOT own business state; it delegates everything to the
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
	mu                 sync.Mutex
	ready              *sync.Cond
	pending            list.List
	deliver            func(tea.Msg)
	closed             bool
	done               chan struct{}
	lastVisualDelivery time.Time
}

const agentVisualDeliveryInterval = time.Second / 60

type pendingAgentDelta struct {
	event  roottypes.AgentMsg
	chunks []string
	size   int
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
	if coalescePendingAgentDelta(p.pending.Back(), msg) {
		return true
	}
	p.pending.PushBack(msg)
	p.ready.Signal()
	return true
}

// coalescePendingAgentDelta losslessly combines only adjacent text deltas for
// the same agent while delivery is already backpressured. Normal live typing
// is untouched when the queue is empty; under load this bounds terminal-event
// latency without dropping content or crossing lifecycle/order boundaries.
func coalescePendingAgentDelta(tail *list.Element, msg tea.Msg) bool {
	if tail == nil {
		return false
	}
	next, ok := msg.(roottypes.AgentMsg)
	if !ok {
		return false
	}
	switch previous := tail.Value.(type) {
	case roottypes.AgentMsg:
		if previous.AgentID != next.AgentID || previous.Type != next.Type {
			return false
		}
		switch next.Type {
		case "thinking", "stream_token":
			left, right, compatible := adjacentDeltaText(previous.Data, next.Data)
			if !compatible {
				return false
			}
			tail.Value = &pendingAgentDelta{
				event:  previous,
				chunks: []string{left, right},
				size:   len(left) + len(right),
			}
			return true
		case "phase":
			// Structured upload/provider progress is a latest-value state, not a
			// textual delta. Consecutive updates can replace one another exactly.
			tail.Value = next
			return true
		default:
			return false
		}
	case *pendingAgentDelta:
		if previous == nil || previous.event.AgentID != next.AgentID || previous.event.Type != next.Type {
			return false
		}
		_, right, ok := adjacentDeltaText(previous.event.Data, next.Data)
		if !ok {
			return false
		}
		previous.chunks = append(previous.chunks, right)
		previous.size += len(right)
		return true
	default:
		return false
	}
}

func adjacentDeltaText(left, right any) (string, string, bool) {
	if a, ok := left.(reasoning.Delta); ok {
		b, valid := right.(reasoning.Delta)
		return a.Text, b.Text, valid && a.Kind == b.Kind && a.BlockID == b.BlockID
	}
	a, aOK := left.(string)
	b, bOK := right.(string)
	return a, b, aOK && bOK
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

		if pending, ok := msg.(*pendingAgentDelta); ok {
			var text strings.Builder
			text.Grow(pending.size)
			for _, chunk := range pending.chunks {
				text.WriteString(chunk)
			}
			if delta, ok := pending.event.Data.(reasoning.Delta); ok {
				delta.Text = text.String()
				pending.event.Data = delta
			} else {
				pending.event.Data = text.String()
			}
			msg = pending.event
		}
		if isAgentVisualUpdate(msg) {
			if wait := time.Until(p.lastVisualDelivery.Add(agentVisualDeliveryInterval)); wait > 0 {
				time.Sleep(wait)
			}
			p.lastVisualDelivery = time.Now()
		}
		p.deliver(msg)
	}
}

func isAgentVisualUpdate(msg tea.Msg) bool {
	event, ok := msg.(roottypes.AgentMsg)
	if !ok {
		return false
	}
	switch event.Type {
	case "thinking", "stream_token", "phase":
		return true
	default:
		return false
	}
}

// AddOrchestrator registers an orchestrator's presentation state in the TUI.
// Called by the app layer after the workspace creates a new orchestrator.
func (t *TUI) AddOrchestrator(orchID string) {
	t.app.AddOrchestrator(orchID)
}

var global *TUI

// StartOption configures shell-local state before the program's first frame.
type StartOption func(*viewmodel.App) error

// WithComposerDrafts binds the already exclusively acquired private profile.
// The caller retains the store and attachment until Wait completes.
func WithComposerDrafts(store *clientstate.Store, attachment clientstate.Attachment, workspaceID string) StartOption {
	return func(a *viewmodel.App) error {
		return a.ConfigureComposerDrafts(store, attachment, workspaceID)
	}
}

// Start creates and starts the terminal UI for the given workspace.
//
// The workspace must already be initialized. The app layer is responsible
// for setting workspace callbacks (OnNewSession, OnResumeSession) before
// calling Start.
func Start(ws shellport.Workspace, presets []ProviderPreset, prompts *history.Store, lg interface {
	Info(tag, msg string, pairs ...any)
	Warn(tag, msg string, pairs ...any)
	Error(tag, msg string, pairs ...any)
	Debug(tag, msg string, pairs ...any)
}, options ...StartOption) (*TUI, error) {
	t := &TUI{
		doneCh: make(chan struct{}),
	}

	a := viewmodel.NewApp(ws, presets)
	a.SetPromptHistory(prompts)
	for _, option := range options {
		if err := option(a); err != nil {
			a.StopAsyncIO()
			return nil, err
		}
	}
	global = t
	// Force UV to use the same grapheme-width method as Crude layout and
	// surface packing. See ../../docs/reference/terminal-width.md.
	program := tea.NewProgram(
		a,
		tea.WithWidthMethod(ansi.GraphemeWidth),
		tea.WithWidthProbe(rendering.TerminalWidthProbeCorpus()...),
	)
	t.app = a
	t.Program = program
	t.outbound = newProgramMessagePump(program.Send)
	a.SetProgram(program)
	// Bind the event transport before the first interactive frame. A user must
	// never race shell startup into a falsely unavailable session transport.
	ws.SetEventSender(t)

	go func() {
		if _, err := program.Run(); err != nil {
			lg.Error("TUI", "program_error", "error", err.Error())
		}
		t.outbound.close()
		t.outbound.wait()
		if t.app != nil {
			t.app.StopAsyncIO()
			if _, connected := ws.(shellport.Replica); !connected {
				t.app.StopLSP()
			}
			if err := t.app.CloseWorkspaceAttachment(); err != nil {
				lg.Warn("TUI", "detach_failed", "error", err.Error())
			}
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
	t.send(roottypes.AgentMsg(AgentEventMsg{Type: typ, Data: data, AgentID: agentID}))
}

// SendAgentEventEnvelope preserves the backend's exact owner/run/epoch/
// generation/sequence identity through the FIFO. The viewmodel never infers
// semantic ownership from payload maps or the currently selected tab.
func (t *TUI) SendAgentEventEnvelope(event agent.AgentEvent) {
	t.send(event)
}

func (t *TUI) SetError(err string) {
	t.send(roottypes.ErrorMsg(err))
}

// ShowOperationIssue sends recovery context through the same FIFO owner path
// as other application events; callers never mutate frontend/workspace queues.
func (t *TUI) ShowOperationIssue(issue roottypes.OperationIssue) {
	t.send(roottypes.OperationIssueMsg{Issue: issue})
}

func (t *TUI) ResumeStartupSession(id string) { t.send(roottypes.ResumeStartupMsg{SessionID: id}) }

func (t *TUI) UpdateModelName(name string) {
	t.send(roottypes.UpdateModelMsg(name))
}

func (t *TUI) HydrateMessages(msgs []workspace.ChatMessageView) {
	t.send(roottypes.HistoryMessagesMsg(viewmodel.ChatMessagesFromViews(msgs)))
}

func (t *TUI) OpenSettings() {
	t.send(roottypes.OpenSettingsMsg{})
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
