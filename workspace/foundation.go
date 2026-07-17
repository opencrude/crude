package workspace

import (
	"fmt"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
)

//  ─ Mode enum

type Mode int

const (
	ModeChat Mode = iota
	ModeSettings
	ModeConfirm
	ModeQuestion
	ModeYolo
	ModeTextOverlay
	ModeFilePicker
)

func ModeName(m Mode) string {
	switch m {
	case ModeChat:
		return "chat"
	case ModeSettings:
		return "settings"
	case ModeConfirm:
		return "confirm"
	case ModeQuestion:
		return "question"
	case ModeYolo:
		return "yolo"
	case ModeTextOverlay:
		return "text-overlay"
	case ModeFilePicker:
		return "file-picker"
	default:
		return "unknown"
	}
}

//  Logger interface ─

type Logger interface {
	Info(tag, msg string, pairs ...any)
	Warn(tag, msg string, pairs ...any)
	Error(tag, msg string, pairs ...any)
	Debug(tag, msg string, pairs ...any)
}

//  FocusTarget enum ─

type FocusTarget int

const (
	FocusNone FocusTarget = iota
	FocusTree
	FocusEditor
	FocusTabBar
	FocusInput
	FocusOverlay
	FocusFloatingWindow
	FocusLauncher
)

//  PaneType enum ─

// PaneType distinguishes subpane variants. Only PaneAgent gets background color
// applied in view.go; PaneThinking (the thinking indicator) has no background.
type PaneType string

const (
	PaneThinking PaneType = "thinking"
	PaneAgent    PaneType = "agent"
)

//  External event message types ──
//
// These messages originate from goroutines outside the Bubble Tea event loop
// (LSP server process, file watcher, background tasks). They are sent through
// the EventBus so all mutations happen on the main goroutine and the event
// flow is traceable.

// LSPDiagnosticsMsg carries LSP diagnostics for a file. The LSP server's
// diagnostic callback (running on the server's reader goroutine) sends this
// through the EventBus. The handler in update.go applies the diagnostics to
// the file tree and active editor on the main goroutine.
type LSPDiagnosticsMsg struct {
	FilePath    string
	Diagnostics []editor.LSPDiagnostic
}

// FileWatcherMsg carries a file system watcher event. The file tree's watcher
// goroutine sends this through the EventBus so the LSP notification runs as a
// tea.Cmd (not blocking the watcher) and the UI re-renders.
type FileWatcherMsg struct {
	FilePath   string
	ChangeType int
}

//  EventBus ──

const eventLogCapacity = 200

// eventLogEntry records a single event that passed through the EventBus.
type eventLogEntry struct {
	time    time.Time
	msgType string
	detail  string
}

// EventBus is the single ingress point for external events into the Bubble Tea
// event loop. Goroutine-driven callbacks (LSP diagnostics, file watcher) send
// messages through the bus instead of mutating App state directly.
//
// The bus:
//  1. Ensures all state mutations happen on the main goroutine (via program.Send)
//  2. Maintains a ring-buffer log of recent events for debugging
//  3. Drops events gracefully if the program is not yet initialized
type EventBus struct {
	program *tea.Program
	log     []eventLogEntry
	mu      sync.Mutex
}

// NewEventBus creates an EventBus with no program attached. Call SetProgram
// before the program starts so callbacks wired in Init() can use the bus.
func NewEventBus() *EventBus {
	return &EventBus{
		log: make([]eventLogEntry, 0, eventLogCapacity),
	}
}

// SetProgram connects the bus to the Bubble Tea program. Must be called
// before program.Run() so that Init()'s callbacks (which use the bus) work.
func (b *EventBus) SetProgram(p *tea.Program) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.program = p
}

// Send delivers a message to the Bubble Tea event loop. If the program is not
// yet connected (SetProgram not called or program has quit), the event is
// logged but not delivered — this prevents silent data loss from callbacks
// that fire before Init() or after shutdown.
func (b *EventBus) Send(msg tea.Msg) {
	b.mu.Lock()
	prog := b.program
	b.appendLog(msg)
	b.mu.Unlock()

	if prog != nil {
		prog.Send(msg)
	}
}

// RecentEvents returns a summary of the last N events that passed through the
// bus, newest first. For debugging: when a signal doesn't reach its handler,
// call RecentEvents() to see whether the event was even sent.
func (b *EventBus) RecentEvents(n int) []string {
	b.mu.Lock()
	defer b.mu.Unlock()

	if n > len(b.log) {
		n = len(b.log)
	}
	out := make([]string, 0, n)
	start := len(b.log) - n
	for i := len(b.log) - 1; i >= start; i-- {
		e := b.log[i]
		out = append(out, fmt.Sprintf("[%s] %s %s",
			e.time.Format("15:04:05.000"), e.msgType, e.detail))
	}
	return out
}

// appendLog records an event in the ring buffer. Caller must hold b.mu.
func (b *EventBus) appendLog(msg tea.Msg) {
	entry := eventLogEntry{
		time:    time.Now(),
		msgType: fmt.Sprintf("%T", msg),
		detail:  msgDetail(msg),
	}
	if len(b.log) >= eventLogCapacity {
		copy(b.log, b.log[1:])
		b.log[len(b.log)-1] = entry
	} else {
		b.log = append(b.log, entry)
	}
}

// msgDetail extracts a human-readable detail string from common event types.
func msgDetail(msg tea.Msg) string {
	switch m := msg.(type) {
	case LSPDiagnosticsMsg:
		return fmt.Sprintf("file=%s diags=%d", m.FilePath, len(m.Diagnostics))
	case FileWatcherMsg:
		return fmt.Sprintf("file=%s change=%d", m.FilePath, m.ChangeType)
	case ProjectSearchCompletedMsg:
		return fmt.Sprintf("request=%d matches=%d", m.Result.RequestID, len(m.Result.Matches))
	default:
		return ""
	}
}

var _ tea.Msg = LSPDiagnosticsMsg{}
var _ tea.Msg = FileWatcherMsg{}
var _ tea.Msg = ProjectSearchCompletedMsg{}
