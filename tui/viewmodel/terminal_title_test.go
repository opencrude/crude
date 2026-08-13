package viewmodel

import (
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/workspace"
)

// noopTeaModel satisfies tea.Model so tests can stand up a Program whose
// msgs channel (unbuffered) is never drained — exactly the hazard a
// synchronous bus.Send from inside Update would hit.
type noopTeaModel struct{}

func (noopTeaModel) Init() tea.Cmd                            { return nil }
func (noopTeaModel) Update(tea.Msg) (tea.Model, tea.Cmd)    { return noopTeaModel{}, nil }
func (noopTeaModel) View() tea.View                         { return tea.View{} }

// Regression: an OSC title arriving via TerminalOutputMsg must NOT be
// published synchronously. Feed runs inside Bubble Tea's update goroutine and
// the event bus delivers via Program.Send on an unbuffered channel, so a
// synchronous TerminalManager.SetTitle there deadlocks the loop (an otherwise
// idle terminal "hung" as soon as the shell set its OSC title). The title is
// published from an async command instead; exactly one TerminalTitleMsg must
// reach the bus.
func TestTerminalTitleOutputDoesNotDeadlockOrDuplicate(t *testing.T) {
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
	app.ensureTermScreen(info.ID, info.Title)

	bus := workspace.NewEventBus()
	mgr.SetEventBus(bus)
	// A connected program that never drains msgs: any synchronous Send from
	// Update blocks forever. The fixed code must not call it on this path.
	bus.SetProgram(tea.NewProgram(noopTeaModel{}))

	done := make(chan tea.Cmd, 1)
	go func() {
		_, cmd := app.Update(workspace.TerminalOutputMsg{
			ID:   info.ID,
			Data: []byte("\x1b]2;crude-title\x07"),
		})
		done <- cmd
	}()
	var batch tea.Cmd
	select {
	case batch = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Update deadlocked on OSC title arrival (synchronous event-bus publish)")
	}

	if batch == nil {
		t.Fatal("expected an async title-publish command")
	}
	// Deliver with no program connected: the bus logs the event without
	// blocking, which is exactly how we observe it here.
	bus.SetProgram(nil)
	if msg := batch(); msg != nil {
		t.Fatalf("title-publish command returned %T; want nil (it only republishes)", msg)
	}

	found := 0
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && found == 0 {
		for _, e := range bus.RecentEvents(64) {
			if strings.Contains(e, "TerminalTitleMsg") {
				found++
			}
		}
		if found == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if found != 1 {
		t.Fatalf("observed %d TerminalTitleMsg events; want exactly 1", found)
	}
	if got, _ := mgr.Get(info.ID); got.Title != "crude-title" {
		t.Fatalf("manager title=%q want %q", got.Title, "crude-title")
	}
}
