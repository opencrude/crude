package viewmodel

import (
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/terminalvt"
	"crude/workspace"
)

// noopTeaModel satisfies tea.Model so tests can stand up a Program whose
// msgs channel (unbuffered) is never drained — exactly the hazard a
// synchronous bus.Send from inside Update would hit.
type noopTeaModel struct{}

func (noopTeaModel) Init() tea.Cmd                       { return nil }
func (noopTeaModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return noopTeaModel{}, nil }
func (noopTeaModel) View() tea.View                      { return tea.View{} }

// Parser effects arrive from the service worker; applying the immutable title
// must not echo it through the synchronous bus on the update thread.
func TestTerminalTitleOutputDoesNotDeadlockOrDuplicate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("/bin/cat PTY workload")
	}
	app := newFullTestApp(t)
	info, err := createAppTerminal(t, app, workspace.TerminalCreateOpts{Command: "/bin/cat", CWD: t.TempDir(), Cols: 40, Rows: 10, Title: "cat"})
	if err != nil {
		t.Fatal(err)
	}
	app.dockTerminal(info.ID)
	sink := &terminalJourneySink{}
	bus := workspace.NewEventBus()
	bus.SetSink(sink)
	app.TermMgr.SetEventBus(bus)
	done := make(chan struct{})
	go func() {
		app.terminalService().Send(workspace.TerminalOutputMsg{ID: info.ID, Data: []byte("\x1b]2;crude-title\x07")})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("service blocked publishing title")
	}
	sink.mu.Lock()
	messages := append([]any(nil), sink.messages...)
	sink.mu.Unlock()
	titles := 0
	var title workspace.TerminalEffectsMsg
	for _, msg := range messages {
		if fx, ok := msg.(workspace.TerminalEffectsMsg); ok && fx.Effects.TitleChanged {
			titles++
			title = fx
		}
	}
	if titles != 1 {
		t.Fatalf("observed %d title effects; want exactly one", titles)
	}
	bus.SetSink(programMessageSink{program: tea.NewProgram(noopTeaModel{})})
	defer bus.SetSink(nil)
	updated := make(chan struct{})
	go func() { _, _ = app.Update(title); close(updated) }()
	select {
	case <-updated:
	case <-time.After(5 * time.Second):
		t.Fatal("Update re-entered synchronous bus on title effect")
	}
	if got, _ := app.TermMgr.Get(info.ID); got.Title != "crude-title" {
		t.Fatalf("manager title=%q", got.Title)
	}
	if got := app.termScreens[info.ID].Title(); got != "crude-title" {
		t.Fatalf("screen title=%q", got)
	}
}

// OSC 52 is an inert offer, not permission to write this shell's clipboard.
func TestTerminalClipboardOutputRequiresExplicitCopy(t *testing.T) {
	app := newTestApp(t)
	screen := app.ensureTermScreen("osc52", "cat")
	app.terminalLeases = map[string]workspace.TerminalLease{"osc52": {TerminalID: "osc52", Owner: app.terminalController(), Generation: 1}}
	writes := 0
	screen.SetCopy(func(string) tea.Cmd { writes++; return func() tea.Msg { return nil } })
	done := make(chan tea.Cmd, 1)
	go func() {
		_, cmd := app.Update(workspace.TerminalEffectsMsg{ID: "osc52", Effects: terminalvt.Effects{Clipboard: [][]byte{[]byte("hello")}}})
		done <- cmd
	}()
	select {
	case cmd := <-done:
		runTerminalCommand(t, app, cmd)
		if writes != 0 {
			t.Fatal("child offer automatically wrote system clipboard")
		}
		if !strings.Contains(testStatusText(app), "Copy offered text") {
			t.Fatal("clipboard offer was not explained to the user")
		}
		if app.terminalClipboard["osc52"] != "hello" {
			t.Fatal("complete clipboard offer was not retained")
		}
		app.openTerminalControlMenu("osc52")
		items := app.LastContextMenu.Sections[0].Items
		if len(items) != 2 || items[1].Label != "Copy offered text" {
			t.Fatal("clipboard offer has no explicit copy action")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("clipboard offer blocked Update")
	}
}

// Regression: selecting another workspace from the launcher calls
// Workspace.SwitchProject on the Bubble Tea update goroutine. That closes all
// terminals. Close must not synchronously echo TerminalClosedMsg through the
// event bus: Program.Send waits for the update loop that is already inside
// CloseAll, freezing the whole TUI.
func TestWorkspaceSwitchTerminalCloseAllDoesNotReenterEventBus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("in-app PTYs are unix-only")
	}
	app := newTestApp(t)
	mgr := app.TermMgr
	info, err := createAppTerminal(t, app, workspace.TerminalCreateOpts{
		Command: "/bin/cat",
		CWD:     t.TempDir(),
		Cols:    40,
		Rows:    10,
		Title:   "cat",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	bus := workspace.NewEventBus()
	mgr.SetEventBus(bus)
	// Never run this program. Its message channel has no receiver, exactly as
	// when Program.Send is called recursively from the active Update pass.
	bus.SetSink(programMessageSink{program: tea.NewProgram(noopTeaModel{})})

	done := make(chan struct{})
	go func() {
		app.terminalService().CloseAll()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("CloseAll deadlocked by publishing back into the active update loop")
	}
	bus.SetSink(nil)

	if _, ok := mgr.Get(info.ID); ok {
		t.Fatal("terminal survived CloseAll")
	}
	for _, event := range bus.RecentEvents(16) {
		if strings.Contains(event, "TerminalClosedMsg") {
			t.Fatalf("explicit close echoed a lifecycle event: %s", event)
		}
	}
}
