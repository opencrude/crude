package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"crude/shellport"
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

func orientationSource() string {
	var out strings.Builder
	for i := 0; i < 400; i++ {
		fmt.Fprintf(&out, "    alpha 界é = value; // %03d\r\n", i)
	}
	return out.String()
}
func TestEditorOrientationVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_ORIENTATION") != "1" {
		t.Skip("dedicated native minimap and indentation journey")
	}
	runOrientationVisual(t, false, 0, 0)
}
func TestEditorOrientationVisualHeadless(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) { runOrientationVisual(t, true, size[0], size[1]) })
	}
}

type orientationVisualModel struct {
	*featureVisualModel
	phase, x, y, endY int
	source            string
	lastInput         string
}
type orientationVisualDone struct {
	phase int
	err   error
}

func runOrientationVisual(t *testing.T, headless bool, width, height int) {
	root := t.TempDir()
	source := orientationSource()
	path := filepath.Join(root, "orientation.txt")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) {
		s := w.AppDefaults()
		s.LSP.Enabled = false
		s.Editor.LineNumbers = true
		s.Editor.IndentGuides = false
		s.Editor.Minimap = false
		w.SetAppDefaults(s)
		restrictFixture(t, w)
	})
	shell := newRemoteJourneyShell(t, runtime, root)
	remoteJourneyCreate(t, shell)
	ctx, cancel := context.WithCancel(t.Context())
	m := &orientationVisualModel{featureVisualModel: &featureVisualModel{controlVisualModel: &controlVisualModel{App: shell.app, runtime: runtime, root: root, path: path, ctx: ctx, completed: map[int]bool{}}, settled: make(chan struct{}, 1), resultPath: os.Getenv("CRUDE_ORIENTATION_RESULT")}, source: source}
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
			t.Error("orientation journey changed disk", err)
		}
		if err := m.evidence(fmt.Sprintf("ORIENTATION_JOIN phase=%d passed=%t headless=%t", m.phase, !t.Failed(), headless)); err != nil {
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
			key := func(code rune, mod tea.KeyMod) {
				text := ""
				if code == ' ' && mod == 0 {
					text = " "
				}
				program.Send(tea.KeyPressMsg{Code: code, Mod: mod, Text: text})
			}
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
			key(' ', 0)
			key(tea.KeyTab, 0)
			key(' ', 0)
			if !observe(tea.KeyF4) {
				return
			}
			key(tea.KeyF5, 0)
			key(tea.KeyEnter, 0)
			if !observe(tea.KeyF4) {
				return
			}
			key(tea.KeyF5, 0)
			key(tea.KeyRight, 0)
			key(tea.KeyEnter, 0)
			if !observe(tea.KeyF4) {
				return
			}
			program.Send(tea.MouseClickMsg{X: m.x, Y: m.y, Button: tea.MouseLeft})
			program.Send(tea.MouseReleaseMsg{X: m.x, Y: m.y, Button: tea.MouseLeft})
			if !observe(tea.KeyF4) {
				return
			}
			program.Send(tea.MouseClickMsg{X: m.x, Y: m.y, Button: tea.MouseLeft})
			program.Send(tea.MouseMotionMsg{X: m.x, Y: m.endY, Button: tea.MouseLeft})
			program.Send(tea.MouseReleaseMsg{X: m.x, Y: m.endY, Button: tea.MouseLeft})
			if !observe(tea.KeyF4) {
				return
			}
			program.Send(tea.KeyPressMsg{Code: 'X', Text: "X"})
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
	if m.phase != 8 {
		t.Fatal("native orientation journey incomplete", m.phase)
	}
}
func (m *orientationVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case controlVisualCheck:
		v.reply <- v.check(m.controlVisualModel)
		return m, nil
	case orientationVisualDone:
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
			if err := m.evidence(fmt.Sprintf("ORIENTATION_READY phase=%d", phase)); err != nil {
				return orientationVisualDone{-1, err}
			}
			return nil
		}
	case tea.MouseClickMsg:
		m.lastInput = fmt.Sprintf("pointer %d,%d", v.X, v.Y)
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
			if m.busy || m.phase >= 8 || v.Code == tea.KeyF3 && m.phase != 0 || v.Code == tea.KeyF4 && m.phase == 0 {
				return m, nil
			}
			m.busy = true
			phase := m.phase
			return m, func() tea.Msg { return orientationVisualDone{phase, m.observeOrientation(phase)} }
		}
		m.lastInput = v.String()
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
func (m *orientationVisualModel) observeOrientation(phase int) error {
	if phase == 0 {
		if err := m.connect(); err != nil {
			return err
		}
		m.program.Send(tea.KeyPressMsg{Code: 'P', Text: "Private orientation draft."})
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
		state := ed.DocumentState()
		want := m.source
		if phase == 6 {
			want = strings.Replace(want, "alpha", "X", 1)
		}
		if ed.Value() != want || state.InFlight || state.PendingEdits != 0 || state.UndoPending || state.HistoryQueued {
			return fmt.Errorf("source/history not settled after %s", m.lastInput)
		}
		if v.Input.Value() != "Private orientation draft." {
			return fmt.Errorf("private draft changed")
		}
		if phase == 0 {
			ed.SetSelection(0, 4, 0, 9)
			if _, err := openOrientationSettings(v.App); err != nil {
				return err
			}
		}
		frame = ansi.Strip(v.View().RenderContent())
		switch phase {
		case 0, 1, 2:
			body := v.settingsSurface(v.overlayHost.Window(ui.OverlayWorkspaceSettings))
			if body == nil {
				return fmt.Errorf("Settings closed before verification")
			}
			s := body.Model().AppSettings().Editor
			if phase == 1 && (!s.IndentGuides || !s.Minimap || len(body.Model().Edits()) != 2 || v.editorSettings.Minimap) {
				return fmt.Errorf("native toggles changed wrong fields")
			}
			if phase == 2 && (body.Model().Saving() || len(body.Model().Edits()) != 0 || !v.editorSettings.Minimap || !v.editorSettings.IndentGuides) {
				return fmt.Errorf("preferences not saved")
			}
		default:
			if v.overlayHost.IsOpen(ui.OverlayWorkspaceSettings) || v.focusTarget() != FocusEditor || m.rootHost.HasPointerCapture() {
				return fmt.Errorf("lost editor focus or pointer capture")
			}
			guide, x := orientationInk(v.App, ed)
			if !guide || x < 0 {
				return fmt.Errorf("guide or minimap ink absent")
			}
			if phase == 3 {
				r := v.editorRect
				m.x, m.y, m.endY = x, r.Y+r.H/2, r.Y+r.H-3
				hit = fmt.Sprintf("ORIENTATION_POINTER size=%dx%d col=%d row=%d end=%d", v.width, v.height, m.x, m.y, m.endY)
			}
			if phase == 4 && ed.ScrollModel().Offset() < 150 {
				return fmt.Errorf("map click missed source: %s offset=%d", m.lastInput, ed.ScrollModel().Offset())
			}
			if phase == 5 && ed.ScrollModel().Offset() < 300 {
				return fmt.Errorf("map drag missed final source")
			}
			if phase >= 6 && ed.ScrollModel().Offset() != 0 {
				return fmt.Errorf("typing/Undo did not reveal original caret")
			}
		}
		if phase != 6 && ed.SelectedText() != "alpha" {
			return fmt.Errorf("presentation changed selected source %q", ed.SelectedText())
		}
		return nil
	})
	if err != nil {
		_ = m.evidence(fmt.Sprintf("ORIENTATION_FAILED phase=%d %v\n%s", phase, err, frame))
		return err
	}
	return m.evidence(fmt.Sprintf("ORIENTATION_VERIFIED phase=%d\n%s\n%s", phase, hit, frame))
}
