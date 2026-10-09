package workspace

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// MinTerminalCols and MinTerminalRows are the shared emulator/PTY grid
	// floor. Presentation code must normalize through NormalizeTerminalSize
	// before resizing either side of the terminal pair.
	MinTerminalCols = 8
	MinTerminalRows = 2

	DefaultTerminalCols = 80
	DefaultTerminalRows = 24

	ptyWriteQueueCap = 1 << 20
)

var (
	// ErrTerminalBackpressure means the bounded asynchronous PTY input queue
	// cannot accept a complete write without exceeding its memory limit. No
	// bytes from the rejected write were queued.
	ErrTerminalBackpressure = errors.New("terminal input queue full")

	// terminalIDSequence is process-scoped rather than manager-scoped. A
	// workspace switch replaces TerminalManager while old Bubble Tea messages
	// may still be queued; IDs must not be reused by the replacement manager.
	terminalIDSequence atomic.Uint64

	errPTYWriterClosed = errors.New("terminal writer closed")
	errPTYWriterNoSink = errors.New("terminal writer has no sink")
)

// NormalizeTerminalSize applies the one grid-size floor shared by the
// workspace PTY and the TUI emulator.
func NormalizeTerminalSize(cols, rows int) (int, int) {
	if cols < MinTerminalCols {
		cols = MinTerminalCols
	}
	if rows < MinTerminalRows {
		rows = MinTerminalRows
	}
	return cols, rows
}

// TerminalPlacement is presentation-owned; workspace only stores it so the
// activity list can show docked vs floating without importing tui.
type TerminalPlacement uint8

const (
	TerminalDocked TerminalPlacement = iota
	TerminalFloating
)

// TerminalInfo is a presentation-neutral snapshot of one PTY session.
type TerminalInfo struct {
	ID        string
	Title     string
	CWD       string
	PID       int
	Cols      int
	Rows      int
	Placement TerminalPlacement
	Alive     bool
	ExitCode  int
	ExitErr   string
}

// TerminalCreateOpts configures a new in-app terminal.
type TerminalCreateOpts struct {
	CWD     string
	Command string
	Args    []string
	Cols    int
	Rows    int
	Title   string
}

// TerminalOutputMsg is PTY stdout/stderr bytes for one session.
type TerminalOutputMsg struct {
	ID   string
	Data []byte
}

// TerminalExitedMsg is published when the PTY child process ends.
type TerminalExitedMsg struct {
	ID       string
	ExitCode int
	Err      string
}

// TerminalTitleMsg is published when the child sets the window title (OSC).
type TerminalTitleMsg struct {
	ID    string
	Title string
}

// TerminalManager owns workspace-scoped interactive PTY sessions.
type TerminalManager struct {
	mu         sync.Mutex
	items      map[string]*terminalSession
	order      []string
	bus        *EventBus
	outputSink MessageSink
	root       string
	host       HostInfo
}

type terminalSession struct {
	id         string
	title      string
	cwd        string
	cols       int
	rows       int
	placement  TerminalPlacement
	alive      bool
	exitCode   int
	exitErr    string
	io         terminalIO
	writer     *ptyWriter
	outputSink MessageSink
	readDone   chan struct{}
}

// ptyWriter serializes PTY stdin on a dedicated goroutine so the UI
// thread (and Ghostty write_pty replies during Feed) never block on a
// full slave input buffer.
type ptyWriter struct {
	mu        sync.Mutex
	cond      *sync.Cond
	sink      terminalIO
	queue     [][]byte
	queued    int
	accepting bool
	writeErr  error
	wg        sync.WaitGroup
}

func newPtyWriter(sink terminalIO) *ptyWriter {
	w := &ptyWriter{sink: sink, accepting: true}
	w.cond = sync.NewCond(&w.mu)
	w.wg.Add(1)
	go w.run()
	return w
}

func (w *ptyWriter) Write(p []byte) (int, error) {
	if w == nil {
		return 0, errPTYWriterClosed
	}
	if len(p) == 0 {
		return 0, nil
	}
	w.mu.Lock()
	if !w.accepting {
		err := w.writeErr
		if err == nil {
			err = errPTYWriterClosed
		}
		w.mu.Unlock()
		return 0, err
	}
	if len(p) > ptyWriteQueueCap-w.queued {
		err := fmt.Errorf(
			"%w: queued=%d incoming=%d capacity=%d",
			ErrTerminalBackpressure,
			w.queued,
			len(p),
			ptyWriteQueueCap,
		)
		w.mu.Unlock()
		return 0, err
	}
	buf := make([]byte, len(p))
	copy(buf, p)
	w.queue = append(w.queue, buf)
	w.queued += len(buf)
	w.cond.Signal()
	w.mu.Unlock()
	return len(p), nil
}

func (w *ptyWriter) stop() {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.accepting = false
	w.queue = nil
	w.queued = 0
	w.cond.Broadcast()
	w.mu.Unlock()
}

func (w *ptyWriter) wait(d time.Duration) {
	if w == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d):
	}
}

func (w *ptyWriter) run() {
	defer w.wg.Done()
	for {
		w.mu.Lock()
		for len(w.queue) == 0 && w.accepting {
			w.cond.Wait()
		}
		if len(w.queue) == 0 {
			w.mu.Unlock()
			return
		}
		buf := w.queue[0]
		w.queue = w.queue[1:]
		w.queued -= len(buf)
		sink := w.sink
		w.mu.Unlock()
		if sink == nil {
			w.fail(errPTYWriterNoSink)
			return
		}
		if err := writeTerminalBytes(sink, buf); err != nil {
			w.fail(fmt.Errorf("terminal writer: %w", err))
			return
		}
	}
}

func (w *ptyWriter) fail(err error) {
	w.mu.Lock()
	if w.writeErr == nil {
		w.writeErr = err
	}
	w.accepting = false
	w.queue = nil
	w.queued = 0
	w.cond.Broadcast()
	w.mu.Unlock()
}

func writeTerminalBytes(sink terminalIO, p []byte) error {
	for len(p) > 0 {
		n, err := sink.Write(p)
		if n < 0 || n > len(p) {
			return io.ErrShortWrite
		}
		if n > 0 {
			p = p[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

type terminalIO interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	SetSize(cols, rows int) error
	Close() error
	Wait() (exitCode int, err error)
	Pid() int
}

// NewTerminalManager creates an empty manager. Call SetEventBus so PTY
// output reaches the TUI.
func NewTerminalManager(rootPath string) *TerminalManager {
	return &TerminalManager{
		items: make(map[string]*terminalSession),
		root:  rootPath,
		host:  DetectHost(),
	}
}

// SetEventBus attaches the workspace event bus (same bus as LSP/file watcher).
func (m *TerminalManager) SetEventBus(bus *EventBus) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.bus = bus
	m.mu.Unlock()
}

// SetOutputSink selects the sole PTY byte consumer for subsequently created
// terminals. Existing sessions retain their captured sink. A nil sink uses the
// event bus for the current shell path; status events always use the event bus.
func (m *TerminalManager) SetOutputSink(sink MessageSink) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.outputSink = sink
	m.mu.Unlock()
}

// Host returns the detected outer terminal / multiplexer.
func (m *TerminalManager) Host() HostInfo {
	if m == nil {
		return DetectHost()
	}
	return m.host
}

// Create starts a new PTY session. Command empty means the user shell.
func (m *TerminalManager) Create(opts TerminalCreateOpts) (TerminalInfo, error) {
	return m.CreatePrepared(opts, nil)
}

// CreatePrepared installs an exact-identity consumer before any PTY reader or
// exit publisher starts. A failed preparation closes and removes only that PTY.
// prepare must not start consuming the PTY itself.
func (m *TerminalManager) CreatePrepared(opts TerminalCreateOpts, prepare func(TerminalInfo) error) (TerminalInfo, error) {
	if m == nil {
		return TerminalInfo{}, fmt.Errorf("terminal manager is nil")
	}
	cols, rows := opts.Cols, opts.Rows
	if cols <= 0 {
		cols = DefaultTerminalCols
	}
	if rows <= 0 {
		rows = DefaultTerminalRows
	}
	cols, rows = NormalizeTerminalSize(cols, rows)
	cwd := opts.CWD
	if cwd == "" {
		cwd = m.root
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	id := fmt.Sprintf("t%d", terminalIDSequence.Add(1))
	title := opts.Title
	if title == "" {
		title = "Terminal"
	}
	io, err := startTerminalIO(opts.Command, opts.Args, cwd, cols, rows)
	if err != nil {
		return TerminalInfo{}, err
	}
	sess := &terminalSession{
		id:        id,
		title:     title,
		cwd:       cwd,
		cols:      cols,
		rows:      rows,
		placement: TerminalDocked,
		alive:     true,
		io:        io,
		writer:    newPtyWriter(io),
		readDone:  make(chan struct{}),
	}
	m.mu.Lock()
	sess.outputSink = m.outputSink
	if sess.outputSink == nil && m.bus != nil {
		sess.outputSink = m.bus
	}
	m.items[id] = sess
	m.order = append(m.order, id)
	info := sess.info()
	m.mu.Unlock()
	if prepare != nil {
		if err := prepare(info); err != nil {
			m.Close(id)
			return TerminalInfo{}, fmt.Errorf("prepare terminal %s: %w", id, err)
		}
	}
	go m.readLoop(sess)
	go m.waitLoop(sess)
	return info, nil
}

// List returns terminals in creation order.
func (m *TerminalManager) List() []TerminalInfo {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]TerminalInfo, 0, len(m.order))
	for _, id := range m.order {
		if s := m.items[id]; s != nil {
			out = append(out, s.info())
		}
	}
	return out
}

// Get returns one session snapshot.
func (m *TerminalManager) Get(id string) (TerminalInfo, bool) {
	if m == nil {
		return TerminalInfo{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.items[id]
	if s == nil {
		return TerminalInfo{}, false
	}
	return s.info(), true
}

// Write sends bytes to the PTY (keystrokes, paste).
func (m *TerminalManager) Write(id string, data []byte) error {
	if m == nil {
		return fmt.Errorf("terminal manager is nil")
	}
	if len(data) == 0 {
		return nil
	}
	m.mu.Lock()
	s := m.items[id]
	m.mu.Unlock()
	if s == nil || s.writer == nil {
		return fmt.Errorf("terminal %s not found", id)
	}
	_, err := s.writer.Write(data)
	if err != nil {
		return fmt.Errorf("terminal %s write: %w", id, err)
	}
	return nil
}

// Resize updates the PTY winsize.
func (m *TerminalManager) Resize(id string, cols, rows int) error {
	if m == nil {
		return fmt.Errorf("terminal manager is nil")
	}
	cols, rows = NormalizeTerminalSize(cols, rows)
	m.mu.Lock()
	s := m.items[id]
	m.mu.Unlock()
	if s == nil || s.io == nil {
		return fmt.Errorf("terminal %s not found", id)
	}
	if err := s.io.SetSize(cols, rows); err != nil {
		return fmt.Errorf("terminal %s resize to %dx%d: %w", id, cols, rows, err)
	}
	m.mu.Lock()
	if m.items[id] == s {
		s.cols, s.rows = cols, rows
	}
	m.mu.Unlock()
	return nil
}

// SetTitle stores a title from OSC (TUI VT) or the user.
func (m *TerminalManager) SetTitle(id, title string) {
	if m == nil || title == "" {
		return
	}
	m.mu.Lock()
	changed := false
	if s := m.items[id]; s != nil && s.title != title {
		s.title = title
		changed = true
	}
	m.mu.Unlock()
	if changed {
		m.emit(TerminalTitleMsg{ID: id, Title: title})
	}
}

// SetCWD records the child's reported working directory (OSC 7). No event
// is published: snapshots pick it up on the next List/Get.
func (m *TerminalManager) SetCWD(id, cwd string) {
	if m == nil || cwd == "" {
		return
	}
	m.mu.Lock()
	if s := m.items[id]; s != nil {
		s.cwd = cwd
	}
	m.mu.Unlock()
}

// SetPlacement records docked vs floating for the activity list.
func (m *TerminalManager) SetPlacement(id string, p TerminalPlacement) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if s := m.items[id]; s != nil {
		s.placement = p
	}
	m.mu.Unlock()
}

// Close kills the PTY and removes the session.
//
// Close is an explicit command, often called from Bubble Tea's Update
// goroutine (terminal close actions and workspace switches). It deliberately
// does not echo a completion event through EventBus: EventBus.Send calls
// Program.Send synchronously, which would wait on the same Update goroutine
// and deadlock. Callers already own the corresponding presentation teardown.
func (m *TerminalManager) Close(id string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	s := m.items[id]
	if s == nil {
		m.mu.Unlock()
		return
	}
	delete(m.items, id)
	for i, existing := range m.order {
		if existing == id {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	m.mu.Unlock()
	if s.writer != nil {
		s.writer.stop()
	}
	if s.io != nil {
		_ = s.io.Close()
	}
	if s.writer != nil {
		s.writer.wait(2 * time.Second)
	}
}

// CloseAll tears down every PTY. Safe to call from Workspace.Close.
func (m *TerminalManager) CloseAll() {
	if m == nil {
		return
	}
	m.mu.Lock()
	ids := append([]string(nil), m.order...)
	m.mu.Unlock()
	for _, id := range ids {
		m.Close(id)
	}
}

func (m *TerminalManager) readLoop(s *terminalSession) {
	defer close(s.readDone)
	buf := make([]byte, 8192)
	for {
		n, err := s.io.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			if s.outputSink != nil {
				s.outputSink.Send(TerminalOutputMsg{ID: s.id, Data: data})
			}
		}
		if err != nil {
			return
		}
	}
}

func (m *TerminalManager) waitLoop(s *terminalSession) {
	code, err := s.io.Wait()
	// Never publish exit ahead of the reader's final accepted bytes. Close does
	// not join these publishers: a downstream synchronous bus may be UI-owned.
	<-s.readDone
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	m.mu.Lock()
	if existing := m.items[s.id]; existing == s {
		existing.alive = false
		existing.exitCode = code
		existing.exitErr = errStr
		m.mu.Unlock()
		m.emit(TerminalExitedMsg{ID: s.id, ExitCode: code, Err: errStr})
		return
	}
	m.mu.Unlock()
}

func (m *TerminalManager) emit(msg any) {
	m.mu.Lock()
	bus := m.bus
	m.mu.Unlock()
	if bus != nil {
		bus.Send(msg)
	}
}

func (s *terminalSession) info() TerminalInfo {
	pid := 0
	if s.io != nil {
		pid = s.io.Pid()
	}
	return TerminalInfo{
		ID:        s.id,
		Title:     s.title,
		CWD:       s.cwd,
		PID:       pid,
		Cols:      s.cols,
		Rows:      s.rows,
		Placement: s.placement,
		Alive:     s.alive,
		ExitCode:  s.exitCode,
		ExitErr:   s.exitErr,
	}
}
