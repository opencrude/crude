package viewmodel

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"crude/terminalvt"
	"crude/workspace"

	tea "charm.land/bubbletea/v2"
)

// A real raw-mode child receives exactly one six-byte DSR reply for each of
// 500 queries before any shell projection exists. Reading/painting the same
// retained snapshot cannot generate another reply or enter the synchronous bus.
func TestTerminalReplyStormDoesNotDeadlockOrReenterBus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX raw-mode PTY workload")
	}
	app := newFullTestApp(t)
	service := app.terminalService()
	script := `stty raw -echo; i=0; while [ "$i" -lt 500 ]; do printf '\033[6n'; i=$((i+1)); done; n=$(dd bs=1 count=3000 2>/dev/null | wc -c); stty min 0 time 1; extra=$(dd bs=1 count=100 2>/dev/null | wc -c); printf '\r\nREPLY_BYTES:%s EXTRA:%s\r\n' "$n" "$extra"; sleep 30`
	info, err := service.Create(workspace.TerminalCreateOpts{Command: "/bin/sh", Args: []string{"-c", script}, CWD: t.TempDir(), Cols: 80, Rows: 10})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close(info.ID) })
	deadline := time.Now().Add(5 * time.Second)
	var projection workspace.TerminalProjection
	for time.Now().Before(deadline) {
		projection, err = service.Rows(info.ID, terminalvt.RowRequest{Start: -1, Count: 0})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(terminalProjectionText(projection), "REPLY_BYTES:") {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if text := terminalProjectionText(projection); !strings.Contains(text, "REPLY_BYTES:3000 EXTRA:0") {
		t.Fatalf("query replies missing/duplicated: %q", text)
	}
	bus := workspace.NewEventBus()
	wsOf(app).TermMgr.SetEventBus(bus)
	bus.SetSink(programMessageSink{program: tea.NewProgram(noopTeaModel{})})
	defer bus.SetSink(nil)
	done := make(chan struct{})
	go func() {
		_, cmd := app.Update(workspace.TerminalChangedMsg{ID: info.ID, Generation: projection.Generation})
		runTerminalCommand(t, app, cmd)
		app.dockTerminal(info.ID)
		app.paintShellFrame()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("projection Update re-entered event bus")
	}
}
