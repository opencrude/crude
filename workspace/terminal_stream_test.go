package workspace

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/terminalvt"
)

func TestTerminalDirectSubscriptionConflatesWithoutActor(t *testing.T) {
	s, sink := testTerminalService(t)
	info, err := s.Create(TerminalCreateOpts{Command: "/bin/sleep", Args: []string{"300"}, Cols: 20, Rows: 4})
	if errors.Is(err, terminalvt.ErrNoCGO) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	dirty, closeSub, err := s.Subscribe(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSub()
	<-dirty
	for range 300 {
		s.Send(TerminalOutputMsg{ID: info.ID, Data: []byte("x")})
	}
	if len(dirty) != 1 {
		t.Fatal("subscriber accumulated intermediate frames")
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	for _, msg := range sink.msgs {
		if _, ok := msg.(TerminalChangedMsg); ok {
			t.Fatal("PTY read entered workspace event queue")
		}
	}
}

func TestTerminalOrderedInputResumeDeduplicatesAndRetainsErrors(t *testing.T) {
	s, _ := testTerminalService(t)
	info, err := s.Create(TerminalCreateOpts{Command: "/bin/sh", Args: []string{"-c", "stty raw -echo; printf READY; cat"}, Cols: 40, Rows: 4})
	if errors.Is(err, terminalvt.ErrNoCGO) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	dirty, closeSub, err := s.Subscribe(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSub()
	waitText := func(want string) TerminalProjection {
		t.Helper()
		deadline := time.NewTimer(3 * time.Second)
		defer deadline.Stop()
		for {
			p, err := s.Rows(info.ID, terminalvt.RowRequest{Start: -1})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(terminalText(p), want) {
				return p
			}
			select {
			case <-dirty:
			case <-deadline.C:
				t.Fatalf("missing %q, got %q", want, terminalText(p))
			}
		}
	}
	waitText("READY")
	lease, err := s.Acquire(info.ID, TerminalController{"ordered", 1})
	if err != nil {
		t.Fatal(err)
	}
	a := TerminalInput{Lease: lease, Kind: "paste", Data: []byte("UNIQUE-")}
	if err := s.ApplyInput("stream", 1, 0, a); err != nil {
		t.Fatal(err)
	}
	// Lost acknowledgement: replay the same sequence before the next one.
	if err := s.ApplyInput("stream", 1, 0, a); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyInput("stream", 2, 0, TerminalInput{Lease: lease, Kind: "paste", Data: []byte("END")}); err != nil {
		t.Fatal(err)
	}
	p := waitText("UNIQUE-END")
	if strings.Count(terminalText(p), "UNIQUE-") != 1 {
		t.Fatal("resumed input duplicated", terminalText(p))
	}
	bad := TerminalInput{Lease: lease, Kind: "invalid"}
	if err := s.ApplyInput("stream", 3, 2, bad); err == nil {
		t.Fatal("invalid intent accepted")
	}
	if err := s.ApplyInput("stream", 3, 2, bad); err == nil {
		t.Fatal("rejected intent forgotten on resume")
	}
	if err := s.ApplyInput("replacement", 4, 3, a); !errors.Is(err, ErrTerminalInputSequence) {
		t.Fatal("stream identity changed", err)
	}
	if err := s.ApplyInput("stream", 5, 3, a); !errors.Is(err, ErrTerminalInputSequence) {
		t.Fatal("sequence gap accepted", err)
	}
	if err := s.Release(lease); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyInput("stream", 4, 3, a); !errors.Is(err, ErrTerminalControl) {
		t.Fatal("old lease accepted", err)
	}
	lease, err = s.Acquire(info.ID, TerminalController{"new-controller", 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyInput("new-stream", 1, 0, TerminalInput{Lease: lease, Kind: "focus"}); err != nil {
		t.Fatal("replacement lease did not start a fresh sequence", err)
	}
}

func TestTerminalConcurrentInputReceiptIsAtomicWithGridExport(t *testing.T) {
	s, _ := testTerminalService(t)
	info, err := s.Create(TerminalCreateOpts{Command: "/bin/sh", Args: []string{"-c", "stty raw -echo; printf READY; cat"}, Cols: 80, Rows: 24})
	if errors.Is(err, terminalvt.ErrNoCGO) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	dirty, cancel, err := s.Subscribe(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		p, err := s.Rows(info.ID, terminalvt.RowRequest{Start: -1})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(terminalText(p), "READY") {
			break
		}
		select {
		case <-dirty:
		case <-deadline.C:
			t.Fatal("raw child did not start")
		}
	}
	lease, err := s.Acquire(info.ID, TerminalController{"atomic-input", 1})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var workers sync.WaitGroup
	for range 16 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for range 8 {
				if _, err := s.StreamRows(info.ID, terminalvt.RowRequest{Start: -1}); err != nil {
					t.Error(err)
				}
			}
			if err := s.ApplyInput("same-stream", 1, 0, TerminalInput{Lease: lease, Kind: "paste", Data: []byte("EXACTLY-ONCE")}); err != nil {
				t.Error(err)
			}
		}()
	}
	close(start)
	workers.Wait()
	if err := s.ApplyInput("same-stream", 2, 1, TerminalInput{Lease: lease, Kind: "paste", Data: []byte("-END")}); err != nil {
		t.Fatal(err)
	}
	for {
		p, err := s.Rows(info.ID, terminalvt.RowRequest{Start: -1})
		if err != nil {
			t.Fatal(err)
		}
		text := terminalText(p)
		if strings.Contains(text, "-END") {
			if strings.Count(text, "EXACTLY-ONCE") != 1 || !strings.Contains(text, "EXACTLY-ONCE-END") {
				t.Fatalf("concurrent receipt or export reordered/duplicated PTY bytes: %q", text)
			}
			return
		}
		select {
		case <-dirty:
		case <-deadline.C:
			t.Fatalf("ordered bytes did not reach raw child: %q", text)
		}
	}
}

func TestTerminalStreamExportsOnlyGhosttyDirtyRows(t *testing.T) {
	e, err := terminalvt.New(terminalvt.Options{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	e.Feed([]byte("first\r\nsecond"))
	p, err := e.StreamRows(terminalvt.RowRequest{Start: -1})
	if err != nil {
		t.Fatal(err)
	}
	q, err := e.StreamRows(terminalvt.RowRequest{Start: -1})
	if err != nil {
		t.Fatal(err)
	}
	for i := range p.Cells {
		if &p.Cells[i][0] != &q.Cells[i][0] {
			t.Fatalf("clean row %d exported again", i)
		}
	}
	e.Feed([]byte("\x1b[1;1Hchanged"))
	r, err := e.StreamRows(terminalvt.RowRequest{Start: -1})
	if err != nil {
		t.Fatal(err)
	}
	if r.Cells[0][0].Text != "c" || p.Cells[0][0].Text != "f" {
		t.Fatal("wrong or mutated detached grid")
	}
	// Ghostty also dirties the old cursor row when the cursor moves away.
	for i := 2; i < len(p.Cells); i++ {
		if &q.Cells[i][0] != &r.Cells[i][0] {
			t.Fatalf("unchanged row %d exported again", i)
		}
	}
}
