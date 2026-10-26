package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/history"
	"crude/metrics"
	"crude/shellport/remote"
	"crude/tui"
	"crude/tui/viewmodel"
	"crude/types/control"
)

// Visual mode must run in scripts/tuishot.sh's dedicated terminal, sequentially
// at desktop and constrained sizes. Example (precompile outside capture):
//
// CRUDE_BIN=/path/app.test CRUDE_ARGS='-test.run=^TestLiveDiagnosticsActualModel$ -test.timeout=15m' \
// CRUDE_LIVE_DIAGNOSTICS=1 CRUDE_VISUAL_DIAGNOSTICS=1 \
// CRUDE_DIAGNOSTICS_CONFIG=/explicit/config.json scripts/tuishot.sh /evidence sleep:90 shot:diagnostics await-exit
//
// A success marker establishes assertions, not pixels: inspect native captures.
type liveDiagnosticShell struct {
	port      *remote.Workspace
	program   *tea.Program
	terminal  *tui.TUI
	joined    chan struct{}
	once      sync.Once
	inspector *coreclient.Client
	target    control.Target
	sequence  atomic.Uint64
}

func startLiveDiagnosticShell(t *testing.T, ctx context.Context, home, project, orchID string) *liveDiagnosticShell {
	t.Helper()
	port, err := remote.Attach(ctx, remote.Options{Root: project, Dial: diagnosticDialer(home)})
	if err != nil {
		t.Fatal(err)
	}
	s := &liveDiagnosticShell{port: port, joined: make(chan struct{})}
	if orchID != "" {
		s.target = port.ControlIdentity()
		s.target.ResourceID = orchID
		owner := port.SessionForAgent(orchID)
		if owner == nil {
			t.Fatal("selected diagnostic session missing from actual replica")
		}
		s.target.SessionID = owner.SessionID
	}
	metrics.Start("shell")
	if os.Getenv("CRUDE_VISUAL_DIAGNOSTICS") == "1" {
		terminal, err := tui.Start(port, providerPresets(), history.New(), &appLogger{}, func(a *viewmodel.App) error {
			if orchID != "" {
				a.AddOrchestrator(orchID)
			}
			return nil
		})
		if err != nil {
			_ = port.Close()
			t.Fatal(err)
		}
		s.terminal, s.program = terminal, terminal.Program
		go func() { terminal.Wait(); close(s.joined) }()
	} else {
		// Same production App, input routing, remote control handlers and replica
		// lifecycle without a physical terminal. Never a fake diagnostic responder.
		a := viewmodel.NewApp(port, providerPresets())
		a.SetPromptHistory(history.New())
		if orchID != "" {
			a.AddOrchestrator(orchID)
		}
		program := tea.NewProgram(a, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithWindowSize(160, 40))
		s.program = program
		a.SetProgram(program)
		port.StartShellControl()
		go func() {
			defer close(s.joined)
			if _, err := program.Run(); err != nil {
				t.Errorf("headless production shell: %v", err)
			}
			a.StopAsyncIO()
			if err := a.CloseWorkspaceAttachment(); err != nil {
				t.Errorf("shell detach: %v", err)
			}
		}()
	}
	t.Cleanup(s.close)
	if orchID != "" {
		s.inspector, err = diagnosticDialer(home)(ctx, "diagnostic-composer-"+s.target.Shell.ClientID)
		if err != nil {
			t.Fatal(err)
		}
		var attached coreprotocol.AttachResult
		if err := s.inspector.Call(ctx, "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: project}, &attached); err != nil {
			t.Fatal(err)
		}
		// Only registration refusals are retried before admission. Transport
		// uncertainty or an admitted immutable grant is never repeated.
		deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		for {
			err := s.inspector.Call(deadline, attached.WorkspaceID, coreprotocol.ControlClientRegister, &coreprotocol.ControlClientGrant{Effects: []control.Effect{control.Read}, Shell: s.target.Shell}, nil)
			if err == nil {
				break
			}
			if !strings.Contains(err.Error(), "exact control shell is not registered") {
				t.Fatal("composer observer admission", err)
			}
			liveDiagnosticPause(t, deadline, 100*time.Millisecond)
		}
		s.awaitComposer(t, ctx, func(d diagnosticComposer) bool { return !d.PendingPastes })
	} else {
		liveDiagnosticPause(t, ctx, 2*time.Second)
	}
	return s
}

func (s *liveDiagnosticShell) submit(t *testing.T, ctx context.Context, text string) {
	t.Helper()
	before := s.awaitComposer(t, ctx, func(d diagnosticComposer) bool { return !d.PendingPastes })
	s.program.Send(tea.PasteMsg{Content: text})
	// Wait on the actual same-shell paste/send barrier, not a presumed RTT. This
	// is test-owned readiness, not diagnostic polling by the task model. Enter
	// is sent exactly once, after attachment completion is observed.
	s.awaitComposer(t, ctx, func(d diagnosticComposer) bool {
		return !d.PendingPastes && d.Revision != before.Revision && (d.Text == text || len(d.References) > len(before.References))
	})
	s.program.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
}

type diagnosticComposer struct {
	Text          string   `json:"text"`
	References    []string `json:"reference_ids"`
	Revision      uint64   `json:"revision"`
	PendingPastes bool     `json:"pending_pastes"`
}

func (s *liveDiagnosticShell) awaitComposer(t *testing.T, parent context.Context, ready func(diagnosticComposer) bool) diagnosticComposer {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	for {
		id := fmt.Sprintf("diagnostic-composer-%d", s.sequence.Add(1))
		var result control.Result
		_, err := s.inspector.CallWith(ctx, s.target.WorkspaceID, coreprotocol.ControlExecute, &control.Request{ID: id, CommandID: "composer.inspect", Target: s.target, Input: json.RawMessage(`{}`)}, &result, coreclient.CallOptions{ID: id})
		if err != nil {
			t.Fatal("same-shell composer readiness observation failed", err)
		}
		if result.Fault == nil {
			var draft diagnosticComposer
			if err := json.Unmarshal(result.Output, &draft); err != nil {
				t.Fatal(err)
			}
			if ready(draft) {
				return draft
			}
		} else if !strings.Contains(result.Fault.Message, "composer") {
			t.Fatal("composer readiness refused", result.Fault)
		}
		liveDiagnosticPause(t, ctx, 100*time.Millisecond)
	}
}

func (s *liveDiagnosticShell) close() {
	s.once.Do(func() {
		if s.inspector != nil {
			_ = s.inspector.Close()
		}
		if s.terminal != nil {
			s.terminal.Stop()
		} else {
			s.program.Send(tea.Quit())
		}
		select {
		case <-s.joined:
		case <-time.After(10 * time.Second):
			s.program.Kill()
			<-s.joined
		}
		_ = s.port.Close()
	})
}

func liveDiagnosticPause(t *testing.T, ctx context.Context, duration time.Duration) {
	t.Helper()
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func (s *liveDiagnosticShell) hold(t *testing.T, ctx context.Context) {
	t.Helper()
	if s.terminal == nil {
		return
	}
	seconds, err := strconv.Atoi(os.Getenv("CRUDE_DIAGNOSTICS_VISUAL_HOLD_SECONDS"))
	if err != nil || seconds <= 0 {
		return
	}
	if seconds > 120 {
		t.Fatal("visual hold must be at most 120 seconds")
	}
	liveDiagnosticPause(t, ctx, time.Duration(seconds)*time.Second)
}

func TestDiagnosticsHarnessDensePasteSize(t *testing.T) {
	if got := len(denseDiagnosticContext()); got <= 200<<10 {
		t.Fatalf("dense context = %d bytes, require over 200 KiB of real submitted text", got)
	}
}
