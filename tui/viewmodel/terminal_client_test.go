package viewmodel

import (
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/terminalvt"
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/screens/shellterm"
	"crude/tui/ui"
	"crude/workspace"
)

func TestTerminalSynchronizedServiceJourneyDockFloatResizeAndTimeout(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	_, sizeCmd := app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	runTerminalCommand(t, app, sizeCmd)
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
	render := func() string {
		t.Helper()
		app.forceFrame()
		out := rendering.StripAnsi(app.View().RenderContent())
		runTerminalCommand(t, app, app.drainTerminalCommands())
		return out
	}
	render()
	// ConPTY emits an initial erase/size report while its silent child starts.
	// Let that actual native startup settle before deterministic ingress; it
	// must not be mistaken for movie-frame parser behavior.
	if runtime.GOOS == "windows" {
		time.Sleep(300 * time.Millisecond)
		runTerminalCommand(t, app, app.terminalRefresh(info.ID))
	}
	frame := func(n int) string {
		return fmt.Sprintf("\x1b[H\x1b[2 q\x1b[?25hFRAME%03d\x1b[2;1Hmovie-row-%03d\x1b[H", n, n)
	}
	feedAppTerminal(t, app, info.ID, []byte("\x1b[?1049h"+frame(0)))
	for n := 1; n <= 24; n++ {
		feedAppTerminal(t, app, info.ID, []byte("\x1b[?2026h\x1b[?25l\x1b[2J"))
		switch n {
		case 9:
			app.promoteTerminal(info.ID)
		case 17:
			app.dockTerminal(info.ID)
		}
		// Placement resize is allowed to release synchronization; after its
		// acknowledged grid publish another complete frame, then begin again.
		if n == 9 || n == 17 {
			render()
			feedAppTerminal(t, app, info.ID, []byte("\x1b[?2026l"+frame(n-1)+"\x1b[?2026h\x1b[?25l\x1b[2J"))
		}
		for _, chunk := range []string{"", frame(n)[:8], frame(n)[8:]} {
			if chunk != "" {
				feedAppTerminal(t, app, info.ID, []byte(chunk))
			}
			if out := render(); !strings.Contains(out, fmt.Sprintf("movie-row-%03d", n-1)) || strings.Contains(out, fmt.Sprintf("movie-row-%03d", n)) {
				p, _ := termSvc(app).Rows(info.ID, terminalvt.RowRequest{Start: -1, Count: 0})
				t.Fatalf("placement/frame %d presented in-progress or absent grid; projection=%q sync=%v paint=%q", n, terminalProjectionText(p), p.Grid.Synchronized, out)
			}
			if window := app.overlayHost.Window(ui.TerminalOverlayID(info.ID)); window != nil {
				// A clean retained window must composite the same completed
				// terminal again, not alternate with a cleared surface.
				if out := render(); !strings.Contains(out, fmt.Sprintf("movie-row-%03d", n-1)) {
					t.Fatal("retained floating frame disappeared")
				}
			}
		}
		feedAppTerminal(t, app, info.ID, []byte("\x1b[?2026l"))
		if out := render(); !strings.Contains(out, fmt.Sprintf("movie-row-%03d", n)) {
			t.Fatal("committed frame did not paint")
		}
		if n == 4 {
			app.termScreens[info.ID].SetTermFocused(false)
			render()
			app.termScreens[info.ID].SetTermFocused(true)
		}
	}
	// With no more PTY ingress, normal cursor-tick admission + the real
	// async row-command result path must still publish a lost-reset update.
	feedAppTerminal(t, app, info.ID, []byte("\x1b[?2026h\x1b[2J"+frame(999)))
	if out := render(); !strings.Contains(out, "movie-row-024") {
		t.Fatal("lost-reset fixture did not retain completed frame")
	}
	time.Sleep(terminalvt.SynchronizedOutputTimeout + 20*time.Millisecond)
	app.tickTerminalCursors(time.Now())
	runTerminalCommand(t, app, app.drainTerminalCommands())
	if out := render(); !strings.Contains(out, "movie-row-999") {
		t.Fatal("abandoned synchronized output did not release without more output")
	}
	// The committed steady cursor is painted as a reverse-video cell, never a
	// host cursor whose movement could race full-frame output.
	seen := false
	for _, c := range app.mainSurf.Buffer().Cells() {
		if c.Char == 'F' && c.Attr&cell.AttrReverse != 0 {
			seen = true
		}
	}
	if !seen {
		t.Fatal("steady terminal cursor missing after timeout publication")
	}
	// ConPTY may put sync-reset, then cursor-home/show in separate reads.
	// Projection completion must obey the same existing 16ms presentation gate
	// as ingress. Model mutations still apply on every result.
	baseline := app.View().RenderContent()
	before := app.frameGate.presented
	// Keep this negative/deadline-message assertion independent of race-build
	// execution time. Native presentation gates measure the real 16 ms bound;
	// here the two completions precede an explicitly delivered deadline.
	app.frameGate.lastPresented = time.Now().Add(time.Minute)
	service := app.terminalService()
	for _, chunk := range []string{"\x1b[?2026h\x1b[?25l\x1b[2J" + frame(100) + "\x1b[?25l\x1b[?2026l", "\x1b[H\x1b[?25h"} {
		termSvc(app).Send(workspace.TerminalOutputMsg{ID: info.ID, Data: []byte(chunk)})
		p, err := service.Rows(info.ID, terminalvt.RowRequest{Start: -1, Count: 0})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = app.Update(terminalProjectionMsg{service: service, generation: app.workspaceSwitchGen.Load(), command: shellterm.Command{ID: info.ID, Kind: shellterm.CommandReadRows, Start: -1}, projection: p})
		if got := app.View().RenderContent(); got != baseline {
			t.Fatal("projection completion bypassed the ingress frame deadline")
		}
	}
	if app.frameGate.presented != before || !app.frameGate.wakePending {
		t.Fatal("adjacent native end/show projections were not coalesced")
	}
	_, _ = app.Update(interactiveFrameMsg{generation: app.frameGate.generation})
	if out := app.View().RenderContent(); !strings.Contains(rendering.StripAnsi(out), "movie-row-100") || app.frameGate.presented != before+1 {
		t.Fatal("frame deadline did not publish the latest accepted terminal mutation")
	}
}

// Only command containers are decoded here. Every product result goes through
// App.Update, including row pages and create acknowledgements.
func runTerminalCommand(t *testing.T, app *App, command tea.Cmd) {
	t.Helper()
	queue := []tea.Cmd{command}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 256 {
			t.Fatal("terminal command journey did not settle")
		}
		cmd := queue[0]
		queue = queue[1:]
		if cmd == nil {
			continue
		}
		msg := cmd()
		if msg == nil {
			continue
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		// Bubble Tea's sequenceMsg is intentionally private, but is a slice of
		// exported Cmd values. Preserve its order without decoding product data.
		value := reflect.ValueOf(msg)
		if value.Kind() == reflect.Slice && value.Type().Elem() == reflect.TypeOf(tea.Cmd(nil)) {
			for i := 0; i < value.Len(); i++ {
				queue = append(queue, value.Index(i).Interface().(tea.Cmd))
			}
			continue
		}
		model, next := app.Update(msg)
		if model != app {
			t.Fatalf("command replaced App with %T", model)
		}
		queue = append(queue, next)
	}
}

func createAppTerminal(t *testing.T, app *App, opts workspace.TerminalCreateOpts) (workspace.TerminalInfo, error) {
	t.Helper()
	service := app.terminalService()
	if service == nil {
		t.Fatal("terminal service unavailable")
	}
	info, err := service.Create(opts)
	if err != nil {
		return info, err
	}
	t.Cleanup(func() { service.Close(info.ID) })
	lease, err := service.Acquire(info.ID, app.terminalController())
	if err != nil {
		return info, err
	}
	if app.terminalLeases == nil {
		app.terminalLeases = make(map[string]workspace.TerminalLease)
	}
	app.terminalLeases[info.ID] = lease
	projection, err := service.Rows(info.ID, terminalvt.RowRequest{Start: -1, Count: 0})
	if err != nil {
		return info, err
	}
	screen := app.ensureTermScreen(info.ID, info.Title)
	screen.SetProjection(projection)
	// Standalone fixtures have no mounted Host layout yet. Establish the real
	// created grid before enabling screen control can queue a zero-bounds resize;
	// otherwise the first key's acknowledgement shrinks it and can move its echo
	// into history instead of testing whether the child accepted the input.
	screen.Layout(ui.LayoutContext{}, ui.Rect{W: info.Cols, H: info.Rows})
	screen.SetControlled(true)
	return info, nil
}

type terminalJourneySink struct {
	mu       sync.Mutex
	messages []any
}

func (s *terminalJourneySink) Send(msg any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, msg)
}

func feedAppTerminal(t *testing.T, app *App, id string, data []byte) {
	t.Helper()
	dirty, unsubscribe, err := termSvc(app).Subscribe(id)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	bus := workspace.NewEventBus()
	sink := &terminalJourneySink{}
	bus.SetSink(sink)
	wsOf(app).TermMgr.SetEventBus(bus)
	done := make(chan struct{})
	go func() { termSvc(app).Send(workspace.TerminalOutputMsg{ID: id, Data: data}); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("terminal parser ingress blocked")
	}
	sink.mu.Lock()
	messages := append([]any(nil), sink.messages...)
	sink.mu.Unlock()
	for _, msg := range messages {
		_, cmd := app.Update(msg)
		runTerminalCommand(t, app, cmd)
	}
	select {
	case <-dirty:
		runTerminalCommand(t, app, app.terminalRefresh(id))
	default:
		t.Fatal("terminal ingress failed to notify its direct subscriber")
	}
}

func waitAppTerminalClosed(t *testing.T, app *App, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := app.ws.TermMgr().Get(id); !ok {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("terminal %s survived asynchronous close", id)
}

func terminalProjectionText(p workspace.TerminalProjection) string {
	var text strings.Builder
	for _, row := range p.Grid.Cells {
		for _, cell := range row {
			text.WriteString(cell.Text)
		}
		text.WriteByte('\n')
	}
	return text.String()
}
