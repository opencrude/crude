package viewmodel

import (
	"fmt"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
)

// ─── External event message types ─────────────────────────────────────────
//
// These messages originate from goroutines outside the Bubble Tea event loop
// (LSP server process, file watcher, background tasks). They are sent through
// the EventBus so all mutations happen on the main goroutine and the event
// flow is traceable.

// lspDiagnosticsMsg carries LSP diagnostics for a file. The LSP server's
// diagnostic callback (running on the server's reader goroutine) sends this
// through the EventBus. The handler in update.go applies the diagnostics to
// the file tree and active editor on the main goroutine.
type lspDiagnosticsMsg struct {
	filePath    string
	diagnostics []editor.LSPDiagnostic
}

// fileWatcherMsg carries a file system watcher event. The file tree's watcher
// goroutine sends this through the EventBus so the LSP notification runs as a
// tea.Cmd (not blocking the watcher) and the UI re-renders.
type fileWatcherMsg struct {
	filePath   string
	changeType int
}

// ─── EventBus ──────────────────────────────────────────────────────────────

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
//   1. Ensures all state mutations happen on the main goroutine (via program.Send)
//   2. Maintains a ring-buffer log of recent events for debugging
//   3. Drops events gracefully if the program is not yet initialized
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
	case lspDiagnosticsMsg:
		return fmt.Sprintf("file=%s diags=%d", m.filePath, len(m.diagnostics))
	case fileWatcherMsg:
		return fmt.Sprintf("file=%s change=%d", m.filePath, m.changeType)
	default:
		return ""
	}
}
