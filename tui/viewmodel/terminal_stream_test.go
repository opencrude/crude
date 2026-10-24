package viewmodel

import (
	"context"
	"errors"
	"testing"

	"crude/shellport"
	"crude/terminalvt"
	"crude/tui/screens/shellterm"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/workspace"
)

type streamTestService struct {
	shellport.TerminalService
	inputs      []workspace.TerminalInput
	starts      int
	reads       int
	predictions int
	prediction  func() (workspace.TerminalProjection, bool)
	apply       func(*shellport.TerminalFrameMsg) error
}

func (s *streamTestService) PredictiveKey(string, terminalvt.Key) (workspace.TerminalProjection, bool) {
	s.predictions++
	if s.prediction != nil {
		return s.prediction()
	}
	return workspace.TerminalProjection{}, false
}

func (s *streamTestService) StartGrid(string)                          { s.starts++ }
func (s *streamTestService) SetViewport(string, terminalvt.RowRequest) {}
func (s *streamTestService) ApplyTerminalFrame(msg *shellport.TerminalFrameMsg) error {
	if s.apply != nil {
		return s.apply(msg)
	}
	return nil
}
func (s *streamTestService) SendInput(input workspace.TerminalInput) error {
	s.inputs = append(s.inputs, input)
	return nil
}
func (s *streamTestService) Rows(string, terminalvt.RowRequest) (workspace.TerminalProjection, error) {
	s.reads++
	return workspace.TerminalProjection{}, nil
}

type streamTestWorkspace struct {
	shellport.Workspace
	terminal *streamTestService
}

func (w *streamTestWorkspace) Terminals() shellport.TerminalService { return w.terminal }
func (w *streamTestWorkspace) EnsureTerminals() (shellport.TerminalService, error) {
	return w.terminal, nil
}

func TestTerminalStreamingAdmissionDoesNotQueueGridReadsAfterInput(t *testing.T) {
	app := newFullTestApp(t)
	service := &streamTestService{}
	app.ws = &streamTestWorkspace{Workspace: app.ws, terminal: service}
	app.terminalLeases = map[string]workspace.TerminalLease{"stream": {TerminalID: "stream", Generation: 1}}
	for i := range 300 {
		if cmd := app.terminalCommand(shellterm.Command{ID: "stream", Kind: shellterm.CommandMouse, Mouse: terminalvt.Mouse{Action: terminalvt.MouseMotion, X: i}}); cmd != nil {
			t.Fatal("motion admitted to command/RPC worker")
		}
	}
	if cmd := app.terminalCommand(shellterm.Command{ID: "stream", Kind: shellterm.CommandKey, Key: terminalvt.Key{Code: 'q', Text: "q"}}); cmd != nil {
		t.Fatal("key waited for grid/RPC")
	}
	if service.reads != 0 || len(app.terminalQueue) != 0 || app.terminalBusy || len(service.inputs) != 301 || service.inputs[300].Key.Text != "q" {
		t.Fatalf("stream used old queue/read path: reads=%d queue=%d inputs=%d", service.reads, len(app.terminalQueue), len(service.inputs))
	}
	if service.predictions != 1 {
		t.Fatalf("key admission did not enter presentation prediction hook: %d", service.predictions)
	}
}

func TestTerminalFrameReceiptFollowsUICommitAndRejectsStaleOwners(t *testing.T) {
	app := newFullTestApp(t)
	service := &streamTestService{}
	app.ws = &streamTestWorkspace{Workspace: app.ws, terminal: service}
	row := make([]terminalvt.Cell, 8)
	for i := range row {
		row[i] = terminalvt.Cell{Text: "X", Width: 1}
	}
	p := workspace.TerminalProjection{Info: workspace.TerminalInfo{ID: "stream", Title: "committed", Alive: true}, Generation: 1, Grid: terminalvt.Projection{Cols: 8, GridRows: 2, Total: 2, Cells: [][]terminalvt.Cell{row, row}, Wrapped: make([]bool, 2)}}
	done := make(chan error, 1)
	service.apply = func(*shellport.TerminalFrameMsg) error {
		if app.termScreens["stream"] != nil {
			t.Fatal("screen installed before owner validation")
		}
		select {
		case <-done:
			t.Fatal("ACK released before owner commit")
		default:
		}
		return nil
	}
	app.applyTerminalFrame(shellport.TerminalFrameMsg{Service: service, ID: "stream", Projection: p, Applied: done})
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("owning Update did not release application receipt")
	}
	screen := app.termScreens["stream"]
	if screen == nil || screen.Title() != "committed" {
		t.Fatal("receipt preceded screen installation")
	}
	screen.Layout(ui.LayoutContext{}, ui.Rect{W: 8, H: 2})
	paint := func() string {
		buf := surface.NewCellBuffer(8, 2)
		screen.Paint(buf, 0, 0, ui.PaintContext{})
		return buf.Get(0, 0).Content
	}
	if got := paint(); got != "X" {
		t.Fatalf("committed frame not painted: %q", got)
	}
	for _, stale := range []string{"generation", "closed", "service"} {
		t.Run(stale, func(t *testing.T) {
			msg := shellport.TerminalFrameMsg{Service: service, ID: "stream", Projection: p, Applied: done}
			msg.Projection.Generation++
			msg.Projection.Info.Title = "stale"
			service.apply = func(*shellport.TerminalFrameMsg) error { return context.Canceled }
			switch stale {
			case "closed":
				app.terminalClosed = map[string]bool{"stream": true}
				defer func() { app.terminalClosed = nil }()
			case "service":
				msg.Service = &streamTestService{}
			}
			app.applyTerminalFrame(msg)
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatal("stale frame ACKed", err)
				}
			default:
				t.Fatal("rejected frame callback left waiting")
			}
			if screen.Title() != "committed" || paint() != "X" {
				t.Fatal("stale owner changed painted terminal")
			}
		})
	}
	// Even a consumer which abandoned its receipt must not block Update.
	done <- nil
	app.applyTerminalFrame(shellport.TerminalFrameMsg{Service: &streamTestService{}, ID: "stream", Applied: done})
}
