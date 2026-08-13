package workspace

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"
)

// TerminalPlacement is presentation-owned; workspace only stores it so the
// activity list can show docked vs floating without importing tui.
type TerminalPlacement uint8

const (
	TerminalDocked TerminalPlacement = iota
	TerminalFloating
)

// TerminalInfo is a presentation-neutral snapshot of one PTY session.
type TerminalInfo struct {
	ID         string
	Title      string
	CWD        string
	PID        int
	Cols       int
	Rows       int
	Placement  TerminalPlacement
	Alive      bool
	ExitCode   int
	ExitErr    string
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

// TerminalClosedMsg is published after Close removes the session.
type TerminalClosedMsg struct {
	ID string
}

// TerminalTitleMsg is published when the child sets the window title (OSC).
type TerminalTitleMsg struct {
	ID    string
	Title string
}

// TerminalManager owns workspace-scoped interactive PTY sessions.
type TerminalManager struct {
	mu      sync.Mutex
	seq     atomic.Uint64
	items   map[string]*terminalSession
	order   []string
	bus     *EventBus
	root    string
	host    HostInfo
}

type terminalSession struct {
	id        string
	title     string
	cwd       string
	cols      int
	rows      int
	placement TerminalPlacement
	alive     bool
	exitCode  int
	exitErr   string
	io        terminalIO
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

// Host returns the detected outer terminal / multiplexer.
func (m *TerminalManager) Host() HostInfo {
	if m == nil {
		return DetectHost()
	}
	return m.host
}

// Create starts a new PTY session. Command empty means the user shell.
func (m *TerminalManager) Create(opts TerminalCreateOpts) (TerminalInfo, error) {
	if m == nil {
		return TerminalInfo{}, fmt.Errorf("terminal manager is nil")
	}
	cols, rows := opts.Cols, opts.Rows
	if cols < 8 {
		cols = 80
	}
	if rows < 2 {
		rows = 24
	}
	cwd := opts.CWD
	if cwd == "" {
		cwd = m.root
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	id := fmt.Sprintf("t%d", m.seq.Add(1))
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
	}
	m.mu.Lock()
	m.items[id] = sess
	m.order = append(m.order, id)
	m.mu.Unlock()
	go m.readLoop(sess)
	go m.waitLoop(sess)
	return sess.info(), nil
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
	if m == nil || len(data) == 0 {
		return nil
	}
	m.mu.Lock()
	s := m.items[id]
	m.mu.Unlock()
	if s == nil || s.io == nil {
		return fmt.Errorf("terminal %s not found", id)
	}
	_, err := s.io.Write(data)
	return err
}

// Resize updates the PTY winsize.
func (m *TerminalManager) Resize(id string, cols, rows int) error {
	if m == nil {
		return nil
	}
	if cols < 8 {
		cols = 8
	}
	if rows < 2 {
		rows = 2
	}
	m.mu.Lock()
	s := m.items[id]
	m.mu.Unlock()
	if s == nil || s.io == nil {
		return fmt.Errorf("terminal %s not found", id)
	}
	s.cols, s.rows = cols, rows
	return s.io.SetSize(cols, rows)
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
	if s.io != nil {
		_ = s.io.Close()
	}
	m.emit(TerminalClosedMsg{ID: id})
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
	buf := make([]byte, 8192)
	for {
		n, err := s.io.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			m.emit(TerminalOutputMsg{ID: s.id, Data: data})
		}
		if err != nil {
			return
		}
	}
}

func (m *TerminalManager) waitLoop(s *terminalSession) {
	code, err := s.io.Wait()
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	m.mu.Lock()
	if existing := m.items[s.id]; existing != nil {
		existing.alive = false
		existing.exitCode = code
		existing.exitErr = errStr
	}
	m.mu.Unlock()
	m.emit(TerminalExitedMsg{ID: s.id, ExitCode: code, Err: errStr})
}

func (m *TerminalManager) emit(msg tea.Msg) {
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

var _ tea.Msg = TerminalOutputMsg{}
var _ tea.Msg = TerminalExitedMsg{}
var _ tea.Msg = TerminalClosedMsg{}
var _ tea.Msg = TerminalTitleMsg{}
