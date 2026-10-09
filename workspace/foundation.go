package workspace

import (
	"fmt"
	"sync"
	"time"
)

//  ─ Mode enum

type Mode int

const (
	ModeChat Mode = iota
	ModeTextOverlay
	ModeFilePicker
)

func ModeName(m Mode) string {
	switch m {
	case ModeChat:
		return "chat"
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
	Generation  uint64
	FilePath    string
	Diagnostics []LSPDiagnostic
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

// MessageSink owns delivery of external messages to their consumer. Send is
// synchronous; the sink is responsible for its own dispatch and lifecycle.
type MessageSink interface {
	Send(any)
}

// EventBus is a single-sink ingress point for external events. It records a
// bounded log even when no sink is attached, in which case delivery is dropped.
// It does not queue messages or own the consumer's execution context.
type EventBus struct {
	sink MessageSink
	log  []eventLogEntry
	mu   sync.Mutex
}

// NewEventBus creates an EventBus with no sink attached.
func NewEventBus() *EventBus {
	return &EventBus{
		log: make([]eventLogEntry, 0, eventLogCapacity),
	}
}

// SetSink replaces the delivery target. A nil sink clears the target. A Send
// already in progress may still deliver to the sink it captured before replacement.
func (b *EventBus) SetSink(sink MessageSink) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sink = sink
}

// Send logs a message and synchronously delivers it to the captured sink, if
// attached. Delivery happens outside the bus lock so sinks may reenter the bus.
func (b *EventBus) Send(msg any) {
	b.mu.Lock()
	sink := b.sink
	b.appendLog(msg)
	b.mu.Unlock()

	if sink != nil {
		sink.Send(msg)
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
func (b *EventBus) appendLog(msg any) {
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
func msgDetail(msg any) string {
	switch m := msg.(type) {
	case LSPDiagnosticsMsg:
		return fmt.Sprintf("file=%s diags=%d", m.FilePath, len(m.Diagnostics))
	case FileWatcherMsg:
		return fmt.Sprintf("file=%s change=%d", m.FilePath, m.ChangeType)
	case ProjectSearchCompletedMsg:
		return fmt.Sprintf("request=%d matches=%d", m.Result.RequestID, len(m.Result.Matches))
	case TerminalOutputMsg:
		return fmt.Sprintf("id=%s bytes=%d", m.ID, len(m.Data))
	case TerminalExitedMsg:
		return fmt.Sprintf("id=%s code=%d", m.ID, m.ExitCode)
	case TerminalTitleMsg:
		return fmt.Sprintf("id=%s title=%s", m.ID, m.Title)
	default:
		return ""
	}
}
