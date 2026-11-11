package viewmodel

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/shellport"
	"crude/tui/editor"
	"crude/types"
	"crude/workspace"
	"github.com/charmbracelet/x/ansi"
)

func TestEditorRowsVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_EDITOR_ROWS") != "1" {
		t.Skip("dedicated native wrapped/folded editor journey")
	}
	runEditorRowsVisual(t, false, 0, 0)
}
func TestEditorRowsVisualHeadless(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) { runEditorRowsVisual(t, true, size[0], size[1]) })
	}
}

type rowsVisualModel struct {
	*featureVisualModel
	phase        int
	foldX, foldY int
	lastPointer  string
}
type rowsVisualDone struct {
	phase int
	err   error
}

func runEditorRowsVisual(t *testing.T, headless bool, width, height int) {
	root := t.TempDir()
	path := filepath.Join(root, "rows.go")
	source := editorRowsSource()
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) {
		settings := w.AppDefaults()
		settings.Editor.WordWrap = true
		settings.Editor.LineNumbers = true
		settings.LSP.Enabled = false
		w.SetAppDefaults(settings)
		restrictFixture(t, w)
	})
	shell := newRemoteJourneyShell(t, runtime, root)
	remoteJourneyCreate(t, shell)
	ctx, cancel := context.WithCancel(t.Context())
	m := &rowsVisualModel{featureVisualModel: &featureVisualModel{controlVisualModel: &controlVisualModel{App: shell.app, runtime: runtime, root: root, path: path, ctx: ctx, completed: map[int]bool{}}, settled: make(chan struct{}, 1), resultPath: os.Getenv("CRUDE_ROWS_RESULT")}}
	var workers sync.WaitGroup
	defer func() {
		if m.client != nil {
			if err := m.client.Close(); err != nil {
				t.Error(err)
			}
		}
		cancel()
		workers.Wait()
		m.StopAsyncIO()
		if err := m.CloseWorkspaceAttachment(); err != nil {
			t.Error(err)
		}
		if err := runtime.Close(); err != nil {
			t.Error(err)
		}
		if data, err := os.ReadFile(path); err != nil || string(data) != source {
			t.Error("row journey changed disk", err)
		}
		if err := m.evidence(fmt.Sprintf("ROWS_JOIN phase=%d passed=%t headless=%t", m.phase, !t.Failed(), headless)); err != nil {
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
	workers.Go(func() {
		for {
			select {
			case msg := <-shell.fifo:
				program.Send(msg)
			case <-ctx.Done():
				return
			}
		}
	})
	m.ws.(shellport.ShellControl).StartShellControl()
	if headless {
		workers.Go(func() {
			key := func(code rune, mod tea.KeyMod) { program.Send(tea.KeyPressMsg{Code: code, Mod: mod}) }
			observe := func(code rune) bool {
				key(code, 0)
				select {
				case <-m.settled:
					return true
				case <-ctx.Done():
					return false
				}
			}
			if !observe(tea.KeyF3) {
				return
			}
			key('-', tea.ModAlt)
			if !observe(tea.KeyF4) {
				return
			}
			key('=', tea.ModAlt)
			if !observe(tea.KeyF4) {
				return
			}
			// F4 publishes the mounted source gutter coordinate before this press.
			program.Send(tea.MouseClickMsg{X: m.foldX, Y: m.foldY, Button: tea.MouseLeft})
			program.Send(tea.MouseReleaseMsg{X: m.foldX, Y: m.foldY, Button: tea.MouseLeft})
			if !observe(tea.KeyF4) {
				return
			}
			key('g', tea.ModCtrl)
			program.Send(tea.KeyPressMsg{Code: '4', Text: "4"})
			key(tea.KeyEnter, 0)
			if !observe(tea.KeyF4) {
				return
			}
			key('d', tea.ModCtrl)
			if !observe(tea.KeyF4) {
				return
			}
			key('z', tea.ModCtrl)
			if observe(tea.KeyF4) {
				key(tea.KeyF9, 0)
			}
		})
	}
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if m.failure != nil {
		t.Fatal(m.failure)
	}
	if m.phase != 7 {
		t.Fatal("row journey incomplete", m.phase)
	}
}
func (m *rowsVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.MouseClickMsg:
		m.lastPointer = fmt.Sprintf("pointer x=%d y=%d mod=%d button=%v", v.X, v.Y, v.Mod, v.Button)
	case controlVisualCheck:
		v.reply <- v.check(m.controlVisualModel)
		return m, nil
	case rowsVisualDone:
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
			if err := m.evidence(fmt.Sprintf("ROWS_READY phase=%d", phase)); err != nil {
				return rowsVisualDone{-1, err}
			}
			return nil
		}
	case tea.KeyPressMsg:
		if v.Code == tea.KeyF9 {
			if m.busy {
				return m, nil
			}
			return m, tea.Quit
		}
		if v.Code == tea.KeyF3 || v.Code == tea.KeyF4 {
			if m.busy || m.phase >= 7 || v.Code == tea.KeyF3 && m.phase != 0 || v.Code == tea.KeyF4 && m.phase == 0 {
				return m, nil
			}
			m.busy = true
			phase := m.phase
			return m, func() tea.Msg { return rowsVisualDone{phase, m.observeRows(phase)} }
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
func (m *rowsVisualModel) observeRows(phase int) error {
	if phase == 0 {
		if err := m.connect(); err != nil {
			return err
		}
		m.program.Send(tea.KeyPressMsg{Code: 'P', Text: "Private row draft."})
		if _, err := m.execute("file.open", map[string]any{"path": m.path}, nil); err != nil {
			return err
		}
		if _, err := m.execute("tree.set_visible", map[string]any{"visible": false}, nil); err != nil {
			return err
		}
	}
	var frame string
	var hit string
	err := m.await(func(v *controlVisualModel) error {
		ed := v.EditorState.Editor
		if ed == nil {
			return fmt.Errorf("editor not open")
		}
		state := ed.DocumentState()
		syntax, _ := ed.InsightStatus()
		if ed.RowsDone() != nil || syntax != types.InsightCurrent || state.InFlight || state.PendingEdits != 0 || state.UndoPending || state.HistoryQueued {
			return fmt.Errorf("source/rows not settled")
		}
		expected := editorRowsSource()
		if phase == 5 {
			line := strings.Split(expected, "\n")[3]
			expected = strings.Replace(expected, line+"\n", line+"\n"+line+"\n", 1)
		}
		if ed.Value() != expected || v.Input.Value() != "Private row draft." || v.focusTarget() != FocusEditor {
			return fmt.Errorf("native input changed wrong source or focus")
		}
		if phase == 0 {
			ed.SetCursorPosition(1, 0)
		}
		frame = ansi.Strip(v.View().RenderContent())
		rows := ed.ScrollModel().Content()
		switch phase {
		case 1:
			if rows != 3 || !strings.Contains(frame, "4 lines") || strings.Contains(frame, "call(") {
				return fmt.Errorf("outer fold not painted: rows=%d", rows)
			}
		case 3:
			if rows != 5 || !strings.Contains(frame, "2 lines") || strings.Contains(frame, "call(") {
				return fmt.Errorf("pointer fold missed hidden source: rows=%d %s", rows, m.lastPointer)
			}
		default:
			if rows <= ed.LineCount() || !strings.Contains(frame, "↩") {
				return fmt.Errorf("wrapped continuation missing: rows=%d", rows)
			}
			if phase >= 4 && ed.CursorLine() != 3 && phase != 5 {
				return fmt.Errorf("navigation/Undo missed source line: %d", ed.CursorLine())
			}
		}
		if phase == 2 {
			r := v.editorRect
			x, y, visible := ed.PositionScreenPosition(editor.Position{Line: 2}, r.X, r.Y)
			if !visible {
				return fmt.Errorf("inner fold header not visible")
			}
			m.foldX, m.foldY = x-ed.GutterWidth()+1, y
			hit = fmt.Sprintf("ROWS_CLICK col=%d row=%d", m.foldX, m.foldY)
		}
		return nil
	})
	if err != nil {
		_ = m.evidence(fmt.Sprintf("ROWS_FAILED phase=%d: %v\n%s", phase, err, frame))
		return err
	}
	return m.evidence(fmt.Sprintf("ROWS_VERIFIED phase=%d\n%s\n%s", phase, hit, frame))
}
