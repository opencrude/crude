package viewmodel

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/terminalvt"
	"crude/tui/rendering"
	"crude/tui/screens/shellterm"
	"crude/tui/ui"
	"crude/workspace"
)

func newScrollingTerminalApp(t *testing.T) (*App, string) {
	t.Helper()
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	_, cmd := app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	runTerminalCommand(t, app, cmd)
	app.Tree.Visible = false
	opts := workspace.TerminalCreateOpts{Command: "/bin/sleep", Args: []string{"300"}, CWD: t.TempDir(), Cols: 80, Rows: 24}
	if runtime.GOOS == "windows" {
		opts.Command = "powershell.exe"
		opts.Args = []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "Start-Sleep -Seconds 300"}
	}
	info, err := createAppTerminal(t, app, opts)
	if errors.Is(err, terminalvt.ErrNoCGO) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.discardTerminalUI)
	app.dockTerminal(info.ID)
	app.forceFrame()
	app.View()
	runTerminalCommand(t, app, app.drainTerminalCommands())
	if runtime.GOOS == "windows" {
		time.Sleep(300 * time.Millisecond) // ConPTY's initial size/erase publication.
	}
	var source strings.Builder
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&source, "scroll-line-%05d\r\n", i)
	}
	feedAppTerminal(t, app, info.ID, []byte("\x1bc"+source.String()))
	return app, info.ID
}

func scrollingAppPaint(app *App) string {
	app.forceFrame()
	return rendering.StripAnsi(app.View().RenderContent())
}

func TestTerminalStreamingScrollViewportNeverClearsBetweenAsyncPages(t *testing.T) {
	app, id := newScrollingTerminalApp(t)
	screen := app.termScreens[id]
	before := scrollingAppPaint(app)
	screen.Handle(ui.EventContext{}, ui.KeyEvent(tea.KeyPressMsg{Code: tea.KeyHome, Mod: tea.ModShift}))
	// A row request is asynchronous. The preceding usable viewport must remain
	// painted until the complete requested viewport is returned, not an empty grid.
	if got := scrollingAppPaint(app); !strings.Contains(got, "scroll-line-") {
		t.Fatalf("scroll intent blanked the presented viewport before its page arrived; previous=%q got=%q", before, got)
	}
	runTerminalCommand(t, app, app.drainTerminalCommands())
	if got := scrollingAppPaint(app); !strings.Contains(got, "scroll-line-00000") {
		t.Fatalf("Home did not commit the requested oldest row: %q", got)
	}
	for n := 2000; n < 2024; n++ {
		// Install only the invalidation refresh first. Deliberately hold any
		// secondary page commands, as happens over RPC while output keeps arriving.
		service := app.terminalService()
		termSvc(app).Send(workspace.TerminalOutputMsg{ID: id, Data: []byte(fmt.Sprintf("scroll-line-%05d\r\n", n))})
		cmd := app.terminalRefresh(id)
		if cmd == nil {
			t.Fatal("refresh unexpectedly pending")
		}
		msg := cmd()
		if _, ok := msg.(terminalProjectionMsg); !ok {
			t.Fatalf("refresh result %T", msg)
		}
		_, followup := app.Update(msg)
		if got := scrollingAppPaint(app); !strings.Contains(got, "scroll-line-00000") || strings.Contains(got, fmt.Sprintf("scroll-line-%05d", n)) {
			t.Fatalf("stream %d displaced/blanked anchored history before secondary read: %q (service %T)", n, got, service)
		}
		runTerminalCommand(t, app, followup)
	}
	// End is an explicit follow intent: it must present the tail and continue
	// following new output rather than retain the old numeric offset.
	screen.Handle(ui.EventContext{}, ui.KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModShift}))
	runTerminalCommand(t, app, app.drainTerminalCommands())
	feedAppTerminal(t, app, id, []byte("scroll-line-02024\r\n"))
	if got := scrollingAppPaint(app); !strings.Contains(got, "scroll-line-02024") || strings.Contains(got, "scroll-line-00000") {
		t.Fatalf("End did not resume tail follow: %q", got)
	}
}

func TestTerminalStreamingScrollLateRefreshDirtyAndPlacement(t *testing.T) {
	app, id := newScrollingTerminalApp(t)
	screen := app.termScreens[id]
	key := func(code rune) {
		screen.Handle(ui.EventContext{}, ui.KeyEvent(tea.KeyPressMsg{Code: code, Mod: tea.ModShift}))
	}
	key(tea.KeyHome)
	runTerminalCommand(t, app, app.drainTerminalCommands())
	// Capture an old Home refresh but do not deliver it. New wheel intent and
	// its page must win even if the old refresh completes afterwards.
	old := app.terminalRefresh(id)
	if old == nil {
		t.Fatal("missing old refresh")
	}
	oldResult := old()
	screen.Handle(ui.EventContext{LocalX: 3, LocalY: 3}, ui.MouseWheelEvent(tea.MouseWheelMsg{Button: tea.MouseWheelDown}))
	if got := scrollingAppPaint(app); !strings.Contains(got, "scroll-line-00000") {
		t.Fatal("unread wheel request blanked old viewport")
	}
	runTerminalCommand(t, app, app.drainTerminalCommands())
	if got := scrollingAppPaint(app); !strings.Contains(got, "scroll-line-00003") {
		t.Fatalf("wheel did not move three rows: %q", got)
	}
	_, next := app.Update(oldResult)
	if got := scrollingAppPaint(app); !strings.Contains(got, "scroll-line-00003") {
		t.Fatal("old Home refresh replaced newer scroll intent")
	}
	runTerminalCommand(t, app, next)
	// Coalesced dirty output receives one latest-state followup. Unlike the
	// original two-reader pipeline, both reads carry the historical viewport.
	read := app.terminalRefresh(id)
	if app.terminalRefresh(id) != nil || !app.terminalDirty[id] {
		t.Fatal("dirty refresh was not coalesced")
	}
	termSvc(app).Send(workspace.TerminalOutputMsg{ID: id, Data: []byte("dirty-tail\r\n")})
	runTerminalCommand(t, app, read)
	if app.terminalReads[id] || app.terminalDirty[id] {
		t.Fatal("dirty reader did not settle")
	}
	if got := scrollingAppPaint(app); !strings.Contains(got, "scroll-line-00003") || strings.Contains(got, "dirty-tail") {
		t.Fatal("dirty stream lost anchored viewport")
	}
	// The retained screen and scroll intent survive dock -> float -> dock with
	// acknowledged resize. Primary history is not replaced by the active grid.
	for _, floating := range []bool{true, false} {
		if floating {
			app.promoteTerminal(id)
		} else {
			app.dockTerminal(id)
		}
		scrollingAppPaint(app)
		runTerminalCommand(t, app, app.drainTerminalCommands())
		if app.termScreens[id] != screen {
			t.Fatal("placement replaced retained terminal screen")
		}
		if got := scrollingAppPaint(app); !strings.Contains(got, "scroll-line-00003") {
			t.Fatalf("placement floating=%v lost history: %q", floating, got)
		}
	}
	// End followed by a new read explicitly tracks the latest tail. A stale
	// sequence page may not resurrect an earlier viewport or wedge later reads.
	key(tea.KeyEnd)
	runTerminalCommand(t, app, app.drainTerminalCommands())
	feedAppTerminal(t, app, id, []byte("follow-resumed\r\n"))
	if got := scrollingAppPaint(app); !strings.Contains(got, "follow-resumed") {
		t.Fatal("End did not follow latest output")
	}
	if screen.ViewportRead().Start != -1 || screen.ViewportRead().Kind != shellterm.CommandReadRows {
		t.Fatal("follow request lost active-grid sentinel")
	}
}
