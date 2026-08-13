package viewmodel

import (
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/workspace"
)

// Regression: a storm of terminal query replies (DA/DSR/...) generated
// synchronously inside Feed must never block the update goroutine or
// re-enter the event bus. The reply path only enqueues bytes on the async
// ptyWriter — with a connected, undrained Program any synchronous
// Program.Send would deadlock exactly like the OSC-title bug.
func TestTerminalReplyStormDoesNotDeadlockOrReenterBus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("in-app PTYs are unix-only")
	}
	app := newTestApp(t)
	mgr := app.Workspace.TermMgr
	info, err := mgr.Create(workspace.TerminalCreateOpts{
		Command: "/bin/cat",
		CWD:     t.TempDir(),
		Cols:    40,
		Rows:    10,
		Title:   "cat",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { mgr.Close(info.ID) })
	screen := app.ensureTermScreen(info.ID, info.Title)

	// Keep the production sink (manager write → async ptyWriter) AND count
	// replies so the test proves they flowed during Feed.
	replies := 0
	screen.SetWrite(func(data []byte) {
		replies++
		_ = mgr.Write(info.ID, data)
	})

	bus := workspace.NewEventBus()
	mgr.SetEventBus(bus)
	// A connected program that never drains msgs: any synchronous Send from
	// Update blocks forever.
	bus.SetProgram(tea.NewProgram(noopTeaModel{}))

	const queries = 500
	done := make(chan struct{})
	go func() {
		_, _ = app.Update(workspace.TerminalOutputMsg{
			ID:   info.ID,
			Data: []byte(strings.Repeat("\x1b[c", queries)),
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Update deadlocked on a DA reply storm (reply path blocked or re-entered the bus)")
	}
	// Detach the undrained program before cleanup: Close legitimately
	// publishes TerminalClosedMsg through the bus.
	bus.SetProgram(nil)
	if replies != queries {
		t.Fatalf("observed %d replies, want %d (one per DA query)", replies, queries)
	}
}
