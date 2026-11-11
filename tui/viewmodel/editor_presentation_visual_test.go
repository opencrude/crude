package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"crude/shellport"
	"crude/tui/cell"
	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
	"errors"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const presentationVisualSource = "package sample\n\nfunc work() {\n\tcall(\"é界\")  \n}\n"

func TestEditorPresentationVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_EDITOR_PRESENTATION") != "1" {
		t.Skip("dedicated native editor preferences journey")
	}
	runEditorPresentationVisual(t, false, 0, 0)
}
func TestEditorPresentationVisualHeadless(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) { runEditorPresentationVisual(t, true, size[0], size[1]) })
	}
}

type presentationVisualModel struct {
	*featureVisualModel
	phase            int
	originalWidth    int
	originalInk      cell.Color
	originalRevision uint64
}
type presentationVisualDone struct {
	phase int
	err   error
}

func runEditorPresentationVisual(t *testing.T, headless bool, width, height int) {
	root := t.TempDir()
	path := filepath.Join(root, "presentation.go")
	if err := os.WriteFile(path, []byte(presentationVisualSource), 0600); err != nil {
		t.Fatal(err)
	}
	runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) {
		settings := w.AppDefaults()
		settings.LSP.Enabled = false
		settings.Editor.LineNumbers = true
		w.SetAppDefaults(settings)
		restrictFixture(t, w)
	})
	shell := newRemoteJourneyShell(t, runtime, root)
	remoteJourneyCreate(t, shell)
	ctx, cancel := context.WithCancel(t.Context())
	m := &presentationVisualModel{featureVisualModel: &featureVisualModel{controlVisualModel: &controlVisualModel{App: shell.app, runtime: runtime, root: root, path: path, ctx: ctx, completed: map[int]bool{}}, settled: make(chan struct{}, 1), resultPath: os.Getenv("CRUDE_PRESENTATION_RESULT")}}
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
		if data, err := os.ReadFile(path); err != nil || string(data) != presentationVisualSource {
			t.Error("display settings changed disk", err)
		}
		if err := m.evidence(fmt.Sprintf("PRESENTATION_JOIN phase=%d passed=%t headless=%t", m.phase, !t.Failed(), headless)); err != nil {
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
			send := func(code rune, text string) { program.Send(tea.KeyPressMsg{Code: code, Text: text}) }
			observe := func(code rune) bool {
				send(code, "")
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
			send(' ', " ")
			send(tea.KeyTab, "")
			send(tea.KeyRight, "")
			send(tea.KeyTab, "")
			send(tea.KeyRight, "")
			send(tea.KeyRight, "")
			if !observe(tea.KeyF4) {
				return
			}
			send(tea.KeyF5, "")
			send(tea.KeyEnter, "")
			if !observe(tea.KeyF4) {
				return
			}
			send(tea.KeyF5, "")
			send(tea.KeyRight, "")
			send(tea.KeyEnter, "")
			if observe(tea.KeyF4) {
				send(tea.KeyF9, "")
			}
		})
	}
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if m.failure != nil {
		t.Fatal(m.failure)
	}
	if m.phase != 4 {
		t.Fatal("presentation journey incomplete", m.phase)
	}
}
func (m *presentationVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case controlVisualCheck:
		v.reply <- v.check(m.controlVisualModel)
		return m, nil
	case presentationVisualDone:
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
			if err := m.evidence(fmt.Sprintf("PRESENTATION_READY phase=%d", phase)); err != nil {
				return presentationVisualDone{-1, err}
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
		if v.Code == tea.KeyF5 {
			if body := m.settingsSurface(m.overlayHost.Window(ui.OverlayWorkspaceSettings)); body != nil {
				body.FocusActions()
			}
			return m, nil
		}
		if v.Code == tea.KeyF3 || v.Code == tea.KeyF4 {
			if m.busy || m.phase >= 4 || v.Code == tea.KeyF3 && m.phase != 0 || v.Code == tea.KeyF4 && m.phase == 0 {
				return m, nil
			}
			m.busy = true
			phase := m.phase
			return m, func() tea.Msg { return presentationVisualDone{phase, m.observePresentation(phase)} }
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
func (m *presentationVisualModel) observePresentation(phase int) error {
	if phase == 0 {
		if err := m.connect(); err != nil {
			return err
		}
		m.program.Send(tea.KeyPressMsg{Code: 'P', Text: "Private display draft."})
		if _, err := m.execute("file.open", map[string]any{"path": m.path}, nil); err != nil {
			return err
		}
		if _, err := m.execute("tree.set_visible", map[string]any{"visible": false}, nil); err != nil {
			return err
		}
	}
	var frame string
	err := m.await(func(v *controlVisualModel) error {
		ed := v.EditorState.Editor
		if ed == nil || ed.Value() != presentationVisualSource || ed.Modified() || v.Input.Value() != "Private display draft." {
			return fmt.Errorf("source or draft changed")
		}
		state, _ := ed.InsightStatus()
		if state != types.InsightCurrent {
			return fmt.Errorf("source syntax not ready")
		}
		if phase == 0 {
			v.View()
			r := v.editorRect
			m.originalWidth = ed.GutterWidth()
			m.originalRevision = ed.Revision()
			m.originalInk = v.mainSurf.Buffer().Get(r.X+ed.PadL+ed.GutterWidth()+1, r.Y).FG
			v.openWorkspaceSettings()
			body := v.settingsSurface(v.overlayHost.Window(ui.OverlayWorkspaceSettings))
			for index, category := range body.Model().Categories() {
				if category.Name == "Editor" {
					body.Model().SelectCategory(index)
					body.SetModel(body.Model())
				}
			}
			body.FocusForm()
			for _, child := range body.Children() {
				if form, ok := child.(*ui.Form); ok && form.ID() == settingsui.WorkspaceSettingsFormID {
					if !form.FocusField("line-numbers") {
						return fmt.Errorf("line-number control missing")
					}
				}
			}
		}
		frame = ansi.Strip(v.View().RenderContent())
		if phase == 1 {
			body := v.settingsSurface(v.overlayHost.Window(ui.OverlayWorkspaceSettings))
			if body == nil {
				return fmt.Errorf("preferences closed before Save")
			}
			settings := body.Model().AppSettings().Editor
			if settings.LineNumbers || settings.SyntaxTheme != "dracula" || settings.RenderWhitespace != "all" || len(body.Model().Edits()) != 3 {
				return fmt.Errorf("native preference input missed its fields: %+v", settings)
			}
			if !v.editorSettings.LineNumbers || ed.GutterWidth() != m.originalWidth {
				return fmt.Errorf("unsaved preference changed editor")
			}
		}
		if phase == 2 {
			body := v.settingsSurface(v.overlayHost.Window(ui.OverlayWorkspaceSettings))
			if body == nil || body.Model().Saving() || len(body.Model().Edits()) != 0 || v.editorSettings.LineNumbers || v.editorSettings.SyntaxTheme != "dracula" || v.editorSettings.RenderWhitespace != "all" {
				return fmt.Errorf("preference Save has not settled")
			}
		}
		if phase == 3 {
			if v.overlayHost.IsOpen(ui.OverlayWorkspaceSettings) || v.focusTarget() != FocusEditor || ed.Revision() != m.originalRevision || ed.GutterWidth() >= m.originalWidth {
				return fmt.Errorf("saved settings/focus not applied")
			}
			r := v.editorRect
			x := r.X + ed.PadL + ed.GutterWidth()
			if v.mainSurf.Buffer().Get(x, r.Y+3).Char != '→' || v.mainSurf.Buffer().Get(x+1, r.Y).FG.Equal(m.originalInk) || !strings.Contains(frame, "call") {
				return fmt.Errorf("source palette or whitespace not painted")
			}
		}
		return nil
	})
	if err != nil {
		return errors.Join(err, m.evidence(fmt.Sprintf("PRESENTATION_FAILED phase=%d: %v", phase, err)))
	}
	return m.evidence(fmt.Sprintf("PRESENTATION_VERIFIED phase=%d\n%s", phase, frame))
}
