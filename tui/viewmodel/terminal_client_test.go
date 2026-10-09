package viewmodel

import (
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/workspace"
)

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
	projection, err := service.Rows(info.ID, -1, 0)
	if err != nil {
		return info, err
	}
	screen := app.ensureTermScreen(info.ID, info.Title)
	screen.SetProjection(projection)
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
	bus := workspace.NewEventBus()
	sink := &terminalJourneySink{}
	bus.SetSink(sink)
	app.TermMgr.SetEventBus(bus)
	done := make(chan struct{})
	go func() { app.terminalService().Send(workspace.TerminalOutputMsg{ID: id, Data: data}); close(done) }()
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
}

func waitAppTerminalClosed(t *testing.T, app *App, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := app.TermMgr.Get(id); !ok {
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
