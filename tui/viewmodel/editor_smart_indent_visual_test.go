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
	"crude/types"
	"crude/workspace"
	"github.com/charmbracelet/x/ansi"
)

func TestEditorSmartIndentVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_SMART_INDENT") != "1" {
		t.Skip("dedicated native Smart Enter and auto-dedent journey")
	}
	runEditorSmartIndentVisual(t, false, 0, 0)
}
func TestEditorSmartIndentVisualHeadless(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) { runEditorSmartIndentVisual(t, true, size[0], size[1]) })
	}
}

type smartIndentVisualModel struct {
	*featureVisualModel
	phase int
}
type smartIndentVisualDone struct {
	phase int
	err   error
}

func runEditorSmartIndentVisual(t *testing.T, headless bool, width, height int) {
	root := t.TempDir()
	path := filepath.Join(root, "block.py")
	sources := map[string]string{"block.py": "if ready:", "block.js": "if (ready) {\n  work();\n  ", "comment.js": "/** */"}
	for name, source := range sources {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".editorconfig"), []byte("root=true\n[*]\nindent_style=space\nindent_size=2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) {
		settings := w.AppDefaults()
		settings.Editor.LineNumbers = true
		settings.LSP.Enabled = false
		w.SetAppDefaults(settings)
		restrictFixture(t, w)
	})
	shell := newRemoteJourneyShell(t, runtime, root)
	remoteJourneyCreate(t, shell)
	ctx, cancel := context.WithCancel(t.Context())
	m := &smartIndentVisualModel{featureVisualModel: &featureVisualModel{controlVisualModel: &controlVisualModel{App: shell.app, runtime: runtime, root: root, path: path, ctx: ctx, completed: map[int]bool{}}, settled: make(chan struct{}, 1), resultPath: os.Getenv("CRUDE_SMART_INDENT_RESULT")}}
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
		for name, source := range sources {
			if data, err := os.ReadFile(filepath.Join(root, name)); err != nil || string(data) != source {
				t.Error("indent journey changed disk", name, err)
			}
		}
		if err := m.evidence(fmt.Sprintf("SMART_JOIN phase=%d passed=%t headless=%t", m.phase, !t.Failed(), headless)); err != nil {
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
			key(tea.KeyEnter, 0)
			if !observe(tea.KeyF4) {
				return
			}
			key('z', tea.ModCtrl)
			if !observe(tea.KeyF4) {
				return
			}
			// The observer opens the next file only after Undo is acknowledged.
			if !observe(tea.KeyF4) {
				return
			}
			program.Send(tea.KeyPressMsg{Code: '}', Text: "}"})
			if !observe(tea.KeyF4) {
				return
			}
			key('z', tea.ModCtrl)
			if !observe(tea.KeyF4) {
				return
			}
			key('y', tea.ModCtrl)
			if !observe(tea.KeyF4) {
				return
			}
			if !observe(tea.KeyF4) {
				return
			}
			key(tea.KeyEnter, 0)
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
	if m.phase != 10 {
		t.Fatal("Smart Enter journey incomplete", m.phase)
	}
}
func (m *smartIndentVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case controlVisualCheck:
		v.reply <- v.check(m.controlVisualModel)
		return m, nil
	case smartIndentVisualDone:
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
			if err := m.evidence(fmt.Sprintf("SMART_READY phase=%d", phase)); err != nil {
				return smartIndentVisualDone{-1, err}
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
			if m.busy || m.phase >= 10 || v.Code == tea.KeyF3 && m.phase != 0 || v.Code == tea.KeyF4 && m.phase == 0 {
				return m, nil
			}
			m.busy = true
			phase := m.phase
			return m, func() tea.Msg { return smartIndentVisualDone{phase, m.observeSmartIndent(phase)} }
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
func (m *smartIndentVisualModel) observeSmartIndent(phase int) error {
	if phase == 0 {
		if err := m.connect(); err != nil {
			return err
		}
		m.program.Send(tea.KeyPressMsg{Code: 'P', Text: "Private editor draft."})
		if _, err := m.execute("file.open", map[string]any{"path": m.path}, nil); err != nil {
			return err
		}
		if _, err := m.execute("tree.set_visible", map[string]any{"visible": false}, nil); err != nil {
			return err
		}
	}
	if phase == 3 || phase == 7 {
		name := "block.js"
		if phase == 7 {
			name = "comment.js"
		}
		m.path = filepath.Join(m.root, name)
		if _, err := m.execute("file.open", map[string]any{"path": m.path}, nil); err != nil {
			return err
		}
	}
	wants := []string{"if ready:", "if ready:\n  ", "if ready:", "if (ready) {\n  work();\n  ", "if (ready) {\n  work();\n}", "if (ready) {\n  work();\n  ", "if (ready) {\n  work();\n}", "/** */", "/**\n * \n */", "/** */"}
	var frame string
	err := m.await(func(v *controlVisualModel) error {
		ed := v.EditorState.Editor
		if ed == nil || ed.FilePath() != m.path {
			return fmt.Errorf("target file not open")
		}
		state := ed.DocumentState()
		syntax, _ := ed.InsightStatus()
		if syntax != types.InsightCurrent || state.PendingEdits != 0 || state.InFlight || state.UndoPending || state.HistoryQueued {
			return fmt.Errorf("source/analysis/history not settled")
		}
		if ed.Value() != wants[phase] || v.Input.Value() != "Private editor draft." || v.focusTarget() != FocusEditor {
			return fmt.Errorf("native source/focus/draft differs: %q", ed.Value())
		}
		if ed.Indentation().Size != 2 {
			return fmt.Errorf("project indentation lost")
		}
		switch phase {
		case 0, 2:
			ed.SetCursorPosition(0, 9)
		case 3, 5:
			ed.SetCursorPosition(2, 2)
		case 7:
			ed.SetCursorPosition(0, 3)
		}
		frame = ansi.Strip(v.View().RenderContent())
		if !strings.Contains(frame, "Spaces: 2") {
			return fmt.Errorf("file policy not painted")
		}
		return nil
	})
	if err != nil {
		_ = m.evidence(fmt.Sprintf("SMART_FAILED phase=%d: %v\n%s", phase, err, frame))
		return err
	}
	return m.evidence(fmt.Sprintf("SMART_VERIFIED phase=%d\n%s", phase, frame))
}
