package workspace

import (
	"crude/terminalvt"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type terminalTestSink struct {
	mu   sync.Mutex
	msgs []any
}

func (s *terminalTestSink) Send(msg any) { s.mu.Lock(); s.msgs = append(s.msgs, msg); s.mu.Unlock() }
func terminalText(p TerminalProjection) string {
	var b strings.Builder
	for _, row := range p.Grid.Cells {
		for _, c := range row {
			b.WriteString(c.Text)
		}
		b.WriteByte('\n')
	}
	return b.String()
}
func testTerminalService(t *testing.T) (*TerminalService, *terminalTestSink) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("unix PTY workload")
	}
	m := NewTerminalManager(t.TempDir())
	sink := &terminalTestSink{}
	bus := NewEventBus()
	bus.SetSink(sink)
	m.SetEventBus(bus)
	s, err := NewTerminalService(m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.CloseAll)
	return s, sink
}
func TestTerminalServiceHeadlessQueriesAndFinalBytes(t *testing.T) {
	s, sink := testTerminalService(t)
	// Child disables echo and reads a real parser-generated DSR reply. It has no
	// client/controller; final output must be in the engine before exit delivery.
	info, err := s.Create(TerminalCreateOpts{Command: "/bin/sh", Args: []string{"-c", `stty raw -echo; printf '\033[6n'; answer=$(dd bs=1 count=6 2>/dev/null); printf 'REPLY:%s\r\nFINAL' "$answer"`}, Cols: 40, Rows: 4})
	if errors.Is(err, terminalvt.ErrNoCGO) {
		t.Skip("CGO unavailable")
	}
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		sink.mu.Lock()
		exited := false
		for _, msg := range sink.msgs {
			if _, ok := msg.(TerminalOutputMsg); ok {
				t.Fatal("raw bytes broadcast")
			}
			if exit, ok := msg.(TerminalExitedMsg); ok && exit.ID == info.ID {
				exited = true
			}
		}
		sink.mu.Unlock()
		if exited {
			p, err := s.Rows(info.ID, terminalvt.RowRequest{Start: -1, Count: 4})
			if err != nil {
				t.Fatal(err)
			}
			if p.Info.Alive || !strings.Contains(terminalText(p), "FINAL") || !strings.Contains(terminalText(p), "REPLY:") {
				t.Fatalf("final projection=%+v text=%q", p, terminalText(p))
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("headless child never received query reply/exited")
}
func TestTerminalServiceExactLeasesDetachAndReaders(t *testing.T) {
	s, _ := testTerminalService(t)
	info, err := s.Create(TerminalCreateOpts{Command: "/bin/cat", Cols: 20, Rows: 4})
	if errors.Is(err, terminalvt.ErrNoCGO) {
		t.Skip("CGO unavailable")
	}
	if err != nil {
		t.Fatal(err)
	}
	a := TerminalController{"a", 1}
	b := TerminalController{"b", 1}
	lease, err := s.Acquire(info.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Acquire(info.ID, b); !errors.Is(err, ErrTerminalControl) {
		t.Fatal("second controller admitted")
	}
	wrong := lease
	wrong.Owner.Epoch++
	if err = s.Input(wrong, []byte("wrong")); !errors.Is(err, ErrTerminalControl) {
		t.Fatal("stale epoch admitted")
	}
	if err = s.Resize(lease, 30, 6); err != nil {
		t.Fatal(err)
	}
	s.Detach(TerminalController{"a", 2})
	if err = s.Input(lease, []byte("ok\n")); err != nil {
		t.Fatal("foreign detach revoked controller")
	}
	s.Detach(a)
	if err = s.Resize(lease, 80, 24); !errors.Is(err, ErrTerminalControl) {
		t.Fatal("detached lease resized")
	}
	p, err := s.Rows(info.ID, terminalvt.RowRequest{Start: -1, Count: 6})
	if err != nil || !p.Info.Alive || p.Grid.Cols != 30 {
		t.Fatalf("detach killed process/grid: %+v %v", p, err)
	}
	next, err := s.Acquire(info.ID, b)
	if err != nil || next.Generation == lease.Generation {
		t.Fatal("lease generation reused")
	}
	if err = s.Input(lease, []byte("old")); !errors.Is(err, ErrTerminalControl) {
		t.Fatal("old lease admitted")
	}
	if err = s.Release(next); err != nil {
		t.Fatal(err)
	}
	same, err := s.Acquire(info.ID, b)
	if err != nil || same.Generation == next.Generation {
		t.Fatal("ABA lease admitted")
	}
	if _, err = s.Rows(info.ID, terminalvt.RowRequest{Start: 0, Count: terminalvt.MaxSnapshotRows + 1}); !errors.Is(err, terminalvt.ErrBounds) {
		t.Fatal("snapshot unbounded")
	}
	s.Close(info.ID)
	if err = s.Input(same, []byte("closed")); !errors.Is(err, ErrTerminalControl) {
		t.Fatal("closed lease admitted")
	}
}
func TestTerminalPreparedFailureRemovesExactIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix workload")
	}
	m := NewTerminalManager(t.TempDir())
	defer m.CloseAll()
	prior, err := m.Create(TerminalCreateOpts{Command: "/bin/cat"})
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("prepare rejected")
	seen := ""
	_, err = m.CreatePrepared(TerminalCreateOpts{Command: "/bin/cat"}, func(info TerminalInfo) error {
		seen = info.ID
		if _, ok := m.Get(info.ID); !ok {
			t.Fatal("identity not allocated before prepare")
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) || seen == "" {
		t.Fatal(err)
	}
	if _, ok := m.Get(seen); ok {
		t.Fatal("failed identity remains")
	}
	if _, ok := m.Get(prior.ID); !ok {
		t.Fatal("neighbor removed")
	}
}

func TestTerminalServicePreparationUnavailableCleansPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix workload")
	}
	e, err := terminalvt.New(terminalvt.Options{})
	if err == nil {
		e.Close()
		t.Skip("CGO engine available")
	}
	if !errors.Is(err, terminalvt.ErrNoCGO) {
		t.Fatal(err)
	}
	s, _ := testTerminalService(t)
	if _, err := s.Create(TerminalCreateOpts{Command: "/bin/cat"}); !errors.Is(err, terminalvt.ErrNoCGO) {
		t.Fatal(err)
	}
	if len(s.manager.List()) != 0 || len(s.items) != 0 {
		t.Fatal("unavailable engine left a live PTY/resource")
	}
}

type terminalBlockingSink struct {
	entered, release chan struct{}
	once             sync.Once
}

func (s *terminalBlockingSink) Send(any) { s.once.Do(func() { close(s.entered) }); <-s.release }

func TestTerminalCloseDoesNotJoinBlockedOutputConsumer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix workload")
	}
	m := NewTerminalManager(t.TempDir())
	sink := &terminalBlockingSink{entered: make(chan struct{}), release: make(chan struct{})}
	m.SetOutputSink(sink)
	info, err := m.CreatePrepared(TerminalCreateOpts{Command: "/bin/sh", Args: []string{"-c", "printf final"}}, func(info TerminalInfo) error {
		select {
		case <-sink.entered:
			t.Fatal("output preceded preparation")
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer close(sink.release)
	select {
	case <-sink.entered:
	case <-time.After(time.Second):
		t.Fatal("output did not arrive")
	}
	done := make(chan struct{})
	go func() { m.Close(info.ID); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close joined blocked output consumer")
	}
}
