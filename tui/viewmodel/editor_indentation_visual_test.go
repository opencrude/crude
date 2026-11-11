package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"crude/shellport"
	"crude/tui/rendering"
	"crude/tui/ui"
	"crude/workspace"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const indentVisualSource = "\tword 界 é\r\n  other\r\n"

func TestEditorIndentationVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_INDENTATION") != "1" {
		t.Skip("dedicated native indentation journey")
	}
	runIndentationVisual(t, false, 0, 0)
}
func TestEditorIndentationVisualHeadless(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) { runIndentationVisual(t, true, size[0], size[1]) })
	}
}

type indentationVisualModel struct {
	*featureVisualModel
	phase, headerX int
}
type indentationVisualDone struct {
	phase int
	err   error
}

func runIndentationVisual(t *testing.T, headless bool, width, height int) {
	root := t.TempDir()
	path := filepath.Join(root, "policy.txt")
	if err := os.WriteFile(path, []byte(indentVisualSource), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".editorconfig"), []byte("root=true\n[*]\nindent_style=space\nindent_size=2\ntab_width=8\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) {
		s := w.AppDefaults()
		s.LSP.Enabled = false
		s.Editor.LineNumbers = true
		w.SetAppDefaults(s)
		restrictFixture(t, w)
	})
	shell := newRemoteJourneyShell(t, runtime, root)
	remoteJourneyCreate(t, shell)
	ctx, cancel := context.WithCancel(t.Context())
	m := &indentationVisualModel{featureVisualModel: &featureVisualModel{controlVisualModel: &controlVisualModel{App: shell.app, runtime: runtime, root: root, path: path, ctx: ctx, completed: map[int]bool{}}, settled: make(chan struct{}, 1), resultPath: os.Getenv("CRUDE_INDENTATION_RESULT")}}
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
		if bytes, err := os.ReadFile(path); err != nil || string(bytes) != indentVisualSource {
			t.Error("indentation journey changed disk", err)
		}
		if err := m.evidence(fmt.Sprintf("INDENT_JOIN phase=%d passed=%t headless=%t", m.phase, !t.Failed(), headless)); err != nil {
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
			action := func(text string) {
				key('/', tea.ModAlt)
				for _, r := range text {
					program.Send(tea.KeyPressMsg{Code: r, Text: string(r)})
				}
				key(tea.KeyEnter, 0)
			}
			if !observe(tea.KeyF3) {
				return
			}
			program.Send(tea.MouseClickMsg{X: m.headerX, Y: 0, Button: tea.MouseLeft})
			program.Send(tea.MouseReleaseMsg{X: m.headerX, Y: 0, Button: tea.MouseLeft})
			if !observe(tea.KeyF4) {
				return
			}
			key(tea.KeyHome, 0)
			key(tea.KeyDown, 0)
			key(tea.KeyDown, 0)
			key(tea.KeyEnter, 0)
			if !observe(tea.KeyF4) {
				return
			}
			key('a', tea.ModCtrl)
			program.Send(tea.KeyPressMsg{Code: '4', Text: "4"})
			key(tea.KeyEnter, 0)
			if !observe(tea.KeyF4) {
				return
			}
			action("Convert indentation to spaces")
			if !observe(tea.KeyF4) {
				return
			}
			key('z', tea.ModCtrl)
			if !observe(tea.KeyF4) {
				return
			}
			action("Use project indentation")
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
		t.Fatal("native indentation journey incomplete", m.phase)
	}
}
func (m *indentationVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case controlVisualCheck:
		v.reply <- v.check(m.controlVisualModel)
		return m, nil
	case indentationVisualDone:
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
			if err := m.evidence(fmt.Sprintf("INDENT_READY phase=%d", phase)); err != nil {
				return indentationVisualDone{-1, err}
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
			return m, func() tea.Msg { return indentationVisualDone{phase, m.observeIndentation(phase)} }
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
func (m *indentationVisualModel) observeIndentation(phase int) error {
	if phase == 0 {
		if err := m.connect(); err != nil {
			return err
		}
		m.program.Send(tea.KeyPressMsg{Code: 'P', Text: "Private indentation draft."})
		if _, err := m.execute("file.open", map[string]any{"path": m.path}, nil); err != nil {
			return err
		}
		if _, err := m.execute("tree.set_visible", map[string]any{"visible": false}, nil); err != nil {
			return err
		}
	}
	var frame, hit string
	err := m.await(func(v *controlVisualModel) error {
		ed := v.EditorState.Editor
		if ed == nil {
			return fmt.Errorf("file not open")
		}
		want := indentVisualSource
		if phase == 4 {
			want = strings.Replace(want, "\t", "        ", 1)
		}
		ds := ed.DocumentState()
		if ed.Value() != want || ds.PendingEdits != 0 || ds.InFlight || ds.UndoPending || ds.HistoryQueued {
			return fmt.Errorf("native edit/history not settled")
		}
		size := 2
		if phase >= 3 && phase < 6 {
			size = 4
		}
		if ed.TabWidth() != 8 || ed.Indentation().Size != size || v.Input.Value() != "Private indentation draft." {
			return fmt.Errorf("wrong policy or draft: %+v", ed.Indentation())
		}
		if phase == 0 {
			ed.SetCursorPosition(0, 12)
			ed.SetSelection(0, 8, 0, 12)
		}
		frame = ansi.Strip(v.View().RenderContent())
		if phase == 0 {
			header := strings.Split(frame, "\n")[0]
			i := strings.Index(header, "Spaces: 2")
			if i < 0 {
				return fmt.Errorf("indentation status not visible")
			}
			m.headerX = rendering.VisibleWidth(header[:i]) + 2
			hit = fmt.Sprintf("INDENT_CLICK col=%d row=0", m.headerX)
		}
		switch phase {
		case 1:
			if !v.overlayHost.IsOpen(ui.OverlayContextMenu) || !strings.Contains(frame, "EditorConfig") || !strings.Contains(frame, "Convert to spaces") {
				return fmt.Errorf("status click did not open the policy menu")
			}
		case 2:
			if !strings.Contains(frame, "Indent size") || !strings.Contains(frame, "Enter 1 to 32 columns.") || v.TextPrompt == nil || v.TextPrompt.ValueText() != "2" || !v.TextPrompt.Edit.HasSelection() {
				return fmt.Errorf("indent width chooser not usable")
			}
		default:
			if v.overlayHost.IsOpen(ui.OverlayContextMenu) || v.TextPrompt != nil || v.commandPaletteModel != nil || v.focusTarget() != FocusEditor || ed.SelectedText() != "word" {
				return fmt.Errorf("policy action lost selected source or editor focus")
			}
		}
		return nil
	})
	if err != nil {
		_ = m.evidence(fmt.Sprintf("INDENT_FAILED phase=%d: %v\n%s", phase, err, frame))
		return err
	}
	return m.evidence(fmt.Sprintf("INDENT_VERIFIED phase=%d\n%s\n%s", phase, hit, frame))
}
