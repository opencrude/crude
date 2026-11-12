package viewmodel

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/rendering"
	"crude/tui/theme"
)

// F4 observes actual provider output and the connected editor. Ordinary native
// text and Undo keys perform the only edits; F9 exits the disposable shell.
func TestSemanticVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_SEMANTIC") != "1" {
		t.Skip("dedicated native semantic color journey")
	}
	runSemanticVisual(t, false, 0, 0)
}
func TestSemanticVisualHeadless(t *testing.T) {
	if os.Getenv("CRUDE_SEMANTIC_GOPLS") == "" {
		t.Skip("installed provider acceptance")
	}
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) { runSemanticVisual(t, true, size[0], size[1]) })
	}
}

type semanticVisualModel struct {
	*featureVisualModel
	fixture installedSemanticFixture
	phase   int
}
type semanticVisualDone struct {
	phase int
	err   error
}

func runSemanticVisual(t *testing.T, headless bool, width, height int) {
	t.Helper()
	f := newInstalledSemanticFixture(t)
	f.shell.app.setFocusEditor()
	f.shell.app.Input.SetInput("Private semantic draft.")
	// Byte and display columns are equal for this ASCII source prefix. The user
	// types after the declaration and then undoes that exact source edit.
	f.editor.SetCursorPosition(2, 15)
	ctx, cancel := context.WithCancel(t.Context())
	m := &semanticVisualModel{featureVisualModel: &featureVisualModel{controlVisualModel: &controlVisualModel{App: f.shell.app, ctx: ctx, path: f.path}, settled: make(chan struct{}, 1), resultPath: os.Getenv("CRUDE_SEMANTIC_RESULT")}, fixture: f}
	started, err := lspVisualProcesses(f.root, f.executable)
	if err != nil || len(started) == 0 {
		t.Fatal("installed provider process was not observed", started, err)
	}
	defer func() {
		m.StopAsyncIO()
		if err := m.CloseWorkspaceAttachment(); err != nil {
			t.Error(err)
		}
		if err := f.runtime.Close(); err != nil {
			t.Error(err)
		}
		remaining, err := lspVisualProcesses(f.root, f.executable)
		joined := err == nil && len(remaining) == 0
		if !joined {
			t.Error("provider cleanup was not confirmed", remaining, err)
		}
		if data, err := os.ReadFile(f.path); err != nil || string(data) != installedSemanticSource {
			t.Error("semantic journey wrote disk", err)
		}
		if err := m.evidence(fmt.Sprintf("SEMANTIC_JOIN phase=%d passed=%t headless=%t started=%v remaining=%v", m.phase, !t.Failed() && joined, headless, started, remaining)); err != nil {
			t.Error(err)
		}
	}()
	opts := []tea.ProgramOption{tea.WithWidthMethod(ansi.GraphemeWidth)}
	if headless {
		opts = append(opts, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithWindowSize(width, height))
	}
	program := tea.NewProgram(m, opts...)
	m.program = program
	m.SetProgram(program)
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	workers.Go(func() {
		for {
			select {
			case msg := <-f.shell.fifo:
				program.Send(msg)
			case <-ctx.Done():
				return
			}
		}
	})
	if headless {
		workers.Go(func() {
			send := func(msg tea.Msg) { program.Send(msg) }
			observe := func() bool {
				send(tea.KeyPressMsg{Code: tea.KeyF4})
				select {
				case <-m.settled:
					return true
				case <-ctx.Done():
					return false
				}
			}
			if !observe() {
				return
			}
			send(tea.KeyPressMsg{Code: 'X', Text: "X"})
			if !observe() {
				return
			}
			send(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
			if observe() {
				send(tea.KeyPressMsg{Code: tea.KeyF9})
			}
		})
	}
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if m.failure != nil {
		t.Fatal(m.failure)
	}
	if m.phase != 3 {
		t.Fatal("semantic journey incomplete", m.phase)
	}
}
func (m *semanticVisualModel) Init() tea.Cmd { return tickCmd() }
func (m *semanticVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case controlVisualCheck:
		v.reply <- v.check(m.controlVisualModel)
		return m, nil
	case semanticVisualDone:
		m.busy = false
		if v.err != nil {
			m.failure = v.err
			return m, tea.Quit
		}
		m.phase = v.phase + 1
		select {
		case m.settled <- struct{}{}:
		default:
		}
		phase := m.phase
		return m, func() tea.Msg {
			if err := m.evidence(fmt.Sprintf("SEMANTIC_READY phase=%d", phase)); err != nil {
				return semanticVisualDone{-1, err}
			}
			return nil
		}
	case tea.KeyPressMsg:
		if v.Code == tea.KeyF9 && v.Mod == 0 {
			if m.busy {
				return m, nil
			}
			return m, tea.Quit
		}
		if v.Code == tea.KeyF4 && v.Mod == 0 {
			if m.busy || m.phase >= 3 {
				return m, nil
			}
			m.busy = true
			phase := m.phase
			return m, func() tea.Msg { return semanticVisualDone{phase, m.observeSemantic(phase)} }
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
func (m *semanticVisualModel) observeSemantic(phase int) error {
	want, name := installedSemanticSource, "name"
	if phase == 1 {
		want = strings.Replace(want, "name string", "nameX string", 1)
		name = "nameX"
	}
	var frame, documentID string
	var revision uint64
	err := m.await(func(v *controlVisualModel) error {
		ed := v.EditorState.Editor
		if ed == nil || !ed.IsOpen() {
			return fmt.Errorf("editor is not open")
		}
		state := ed.DocumentState()
		documentID, revision = state.DocumentID, state.Revision
		if ed.Value() != want || state.InFlight || state.PendingEdits != 0 || state.HistoryQueued || state.UndoPending || state.Conflict != nil {
			return fmt.Errorf("source or Undo is not settled")
		}
		if v.Input.Value() != "Private semantic draft." || v.focusTarget() != FocusEditor {
			return fmt.Errorf("editor or private draft lost focus")
		}
		view := v.semanticViews[ed]
		if view == nil || view.request == nil || !view.request.installed || view.request.result == nil {
			return fmt.Errorf("semantic result not installed")
		}
		set := view.request.result
		if set.Boundary.Source.Revision != state.Revision {
			return fmt.Errorf("semantic result refers to different source")
		}
		target := strings.Index(want, name+" string")
		found := false
		for _, token := range set.Spans {
			if token.Start == target && token.End == target+len(name) && token.Type >= 0 && token.Type < len(set.Types) && set.Types[token.Type] == "parameter" {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("installed provider did not identify the parameter")
		}
		frame = ansi.Strip(v.View().RenderContent())
		r := v.editorRect
		painted := v.mainSurf.Buffer().Get(r.X+ed.PadL+ed.GutterWidth()+11, r.Y+2)
		color := theme.SyntaxPalette(v.Theme, v.editorSettings.SyntaxTheme).Colors.Code.Property
		if painted.Char != 'n' || !painted.FG.Equal(rendering.ThemeColorToCell(color)) {
			return fmt.Errorf("parameter token is not painted")
		}
		return nil
	})
	if err != nil {
		_ = m.evidence(fmt.Sprintf("SEMANTIC_FAILED phase=%d %v\n%s", phase, err, frame))
		return err
	}
	snapshot, err := m.fixture.retained.LiveDocuments.Snapshot(documentID)
	if err != nil || snapshot.Revision != revision || string(snapshot.Text) != want {
		return fmt.Errorf("canonical source differs: %v", err)
	}
	return m.evidence(fmt.Sprintf("SEMANTIC_VERIFIED phase=%d provider=installed revision=%d\n%s", phase, revision, frame))
}
