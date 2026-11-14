package workspace

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/fileio"
	"crude/terminalvt"
)

type commandViewSource struct {
	mu      sync.Mutex
	data    []byte
	info    fileio.AsyncCmdInfo
	changed chan struct{}
	err     error
}

func (s *commandViewSource) ObserveAsyncOutput(ref fileio.AsyncCmdRef, start, count int) ([]byte, int, fileio.AsyncCmdInfo, <-chan struct{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, 0, fileio.AsyncCmdInfo{}, nil, s.err
	}
	if ref != s.info.Ref() {
		return nil, 0, fileio.AsyncCmdInfo{}, nil, fileio.ErrAsyncCommandReplaced
	}
	return append([]byte(nil), s.data[min(start, len(s.data)):min(start+count, len(s.data))]...), len(s.data), s.info, s.changed, nil
}

func TestCommandViewSourceRemovalKeepsAlreadyDisplayedOutput(t *testing.T) {
	service, _ := testTerminalService(t)
	source := &commandViewSource{
		info:    fileio.AsyncCmdInfo{Handle: "build", Command: "first\r\nsecond", PID: 3, StartedAt: time.Now(), Status: fileio.AsyncRunning},
		changed: make(chan struct{}),
		data:    []byte("accepted output\n"),
	}
	info, err := service.openCommandView(t.Context(), TerminalController{"reader", 1}, source, source.info.Ref(), 50, 6)
	if err != nil {
		t.Fatal(err)
	}
	dirty, unsubscribe, err := service.Subscribe(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	removed := false
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		p, err := service.Rows(info.ID, terminalvt.RowRequest{Start: -1})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(terminalText(p), "accepted output") {
			if !removed {
				source.mu.Lock()
				source.err = fileio.ErrAsyncCommandNotFound
				close(source.changed)
				source.mu.Unlock()
				removed = true
			} else if !p.Info.Alive {
				if p.Error != "" || strings.Contains(terminalText(p), "�") {
					t.Fatal("source loss hid accepted evidence or CRLF corrupted the echoed command")
				}
				service.Close(info.ID) // closes and joins even after source removal
				return
			}
		}
		select {
		case <-dirty:
		case <-deadline.C:
			t.Fatal("source removal did not settle the viewer")
		}
	}
}

func (s *commandViewSource) write(text string, done bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = append(s.data, text...)
	if done {
		s.info.Status = fileio.AsyncCompleted
	}
	close(s.changed)
	s.changed = make(chan struct{})
}

func TestCommandViewUsesTerminalStreamAndOwnsOnlyPresentation(t *testing.T) {
	service, sink := testTerminalService(t)
	source := &commandViewSource{info: fileio.AsyncCmdInfo{Handle: "build", Command: "build --all\nverify", PID: 42, StartedAt: time.Now(), Status: fileio.AsyncRunning}, changed: make(chan struct{})}
	owner := TerminalController{ClientID: "shell", Epoch: 1}
	info, err := service.openCommandView(t.Context(), owner, source, source.info.Ref(), 60, 12)
	if err != nil {
		t.Fatal(err)
	}
	dirty, unsubscribe, err := service.Subscribe(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	observe := func(want string, alive bool) TerminalProjection {
		t.Helper()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for {
			p, err := service.StreamRows(info.ID, terminalvt.RowRequest{Start: -1})
			if err != nil {
				t.Fatal(err)
			}
			if p.Info.Alive == alive && strings.Contains(terminalText(p), want) {
				return p
			}
			select {
			case <-dirty:
			case <-timer.C:
				t.Fatalf("missing %q: %s", want, terminalText(p))
			}
		}
	}
	source.write("\x1b[31mred\x1b[0m\nprogress 10%\rprogress 90%\n", false)
	p := observe("progress 90%", true)
	if !strings.Contains(strings.Join(strings.Fields(terminalText(p)), " "), "$ build --all verify") || strings.Contains(terminalText(p), "progress 10%") {
		t.Fatalf("command echo or carriage-return output is wrong: %q", terminalText(p))
	}
	if !p.Grid.Cells[2][0].FG.Explicit {
		t.Fatal("command ANSI color did not reach the terminal engine")
	}
	if len(service.manager.List()) != 0 {
		t.Fatal("a viewer created an interactive terminal")
	}
	lease, err := service.Acquire(info.ID, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Key(lease, terminalvt.Key{Text: "x"}); !errors.Is(err, ErrTerminalControl) {
		t.Fatal("a passive viewer accepted input")
	}
	if err := service.CheckViewer(info.ID, TerminalController{"other", 1}); !errors.Is(err, ErrTerminalControl) {
		t.Fatal("a peer attachment can read a private view")
	}
	source.write("\x1b]52;c;c2VjcmV0\x07\x1b[6nfinal\n", true)
	observe("final", false)
	if err := service.Resize(lease, 80, 14); err != nil {
		t.Fatalf("finished output cannot be resized: %v", err)
	}
	sink.mu.Lock()
	if len(sink.msgs) != 0 {
		t.Error("read-only output emitted clipboard or terminal effects")
	}
	sink.mu.Unlock()
	service.Detach(TerminalController{"shell", 2})
	if _, err := service.Rows(info.ID, terminalvt.RowRequest{Start: -1}); err != nil {
		t.Fatal("a different attachment epoch closed the view")
	}
	service.Detach(owner)
	if _, err := service.Rows(info.ID, terminalvt.RowRequest{Start: -1}); err == nil {
		t.Fatal("detach retained the view")
	}
	if _, err := service.openCommandView(context.Background(), owner, source, source.info.Ref(), 60, 12); err == nil {
		t.Fatal("a late open revived a detached owner")
	}
}
