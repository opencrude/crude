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

const multiVisualSource = "word a 界é\r\nword b 界é\r\nword c 界é\r\n"

func TestEditorMultipleVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_MULTIPLE") != "1" {
		t.Skip("dedicated native multiple-selection journey")
	}
	runMultipleVisual(t, false, 0, 0)
}
func TestEditorMultipleVisualHeadless(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) { runMultipleVisual(t, true, size[0], size[1]) })
	}
}

type multipleVisualModel struct {
	*featureVisualModel
	phase     int
	x, y      int
	lastInput string
}
type multipleVisualDone struct {
	phase int
	err   error
}

func runMultipleVisual(t *testing.T, headless bool, width, height int) {
	root := t.TempDir()
	path := filepath.Join(root, "multiple.txt")
	if err := os.WriteFile(path, []byte(multiVisualSource), 0600); err != nil {
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
	m := &multipleVisualModel{featureVisualModel: &featureVisualModel{controlVisualModel: &controlVisualModel{App: shell.app, runtime: runtime, root: root, path: path, ctx: ctx, completed: map[int]bool{}}, settled: make(chan struct{}, 1), resultPath: os.Getenv("CRUDE_MULTIPLE_RESULT")}}
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
		if bytes, err := os.ReadFile(path); err != nil || string(bytes) != multiVisualSource {
			t.Error("native editing wrote disk", err)
		}
		if err := m.evidence(fmt.Sprintf("MULTIPLE_JOIN phase=%d passed=%t headless=%t", m.phase, !t.Failed(), headless)); err != nil {
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
			key(tea.KeyHome, tea.ModCtrl)
			for range 3 {
				key('n', tea.ModAlt)
			}
			if !observe(tea.KeyF4) {
				return
			}
			program.Send(tea.KeyPressMsg{Code: 'X', Text: "X"})
			if !observe(tea.KeyF4) {
				return
			}
			key('z', tea.ModCtrl)
			if !observe(tea.KeyF4) {
				return
			}
			key('d', tea.ModCtrl)
			if !observe(tea.KeyF4) {
				return
			}
			key('z', tea.ModCtrl)
			if !observe(tea.KeyF4) {
				return
			}
			key(tea.KeyEsc, 0)
			if !observe(tea.KeyF4) {
				return
			}
			key(tea.KeyHome, tea.ModCtrl)
			program.Send(tea.MouseClickMsg{X: m.x, Y: m.y, Button: tea.MouseLeft, Mod: tea.ModAlt})
			program.Send(tea.MouseMotionMsg{X: m.x + 1, Y: m.y + 2, Button: tea.MouseLeft, Mod: tea.ModAlt})
			program.Send(tea.MouseReleaseMsg{X: m.x + 1, Y: m.y + 2, Button: tea.MouseLeft, Mod: tea.ModAlt})
			if !observe(tea.KeyF4) {
				return
			}
			program.Send(tea.KeyPressMsg{Code: 'Z', Text: "Z"})
			if !observe(tea.KeyF4) {
				return
			}
			key('z', tea.ModCtrl)
			if !observe(tea.KeyF4) {
				return
			}
			key(tea.KeyEsc, 0)
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
	if m.phase != 11 || m.rootHost.HasPointerCapture() {
		t.Fatal("native multi-selection journey incomplete", m.phase)
	}
}
func (m *multipleVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case controlVisualCheck:
		v.reply <- v.check(m.controlVisualModel)
		return m, nil
	case multipleVisualDone:
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
			if err := m.evidence(fmt.Sprintf("MULTIPLE_READY phase=%d", phase)); err != nil {
				return multipleVisualDone{-1, err}
			}
			return nil
		}
	case tea.MouseClickMsg:
		m.lastInput = fmt.Sprintf("pointer=%d,%d mod=%d", v.X, v.Y, v.Mod)
	case tea.KeyPressMsg:
		if v.Code == tea.KeyF9 {
			if m.busy {
				return m, nil
			}
			return m, tea.Quit
		}
		if v.Code == tea.KeyF3 || v.Code == tea.KeyF4 {
			if m.busy || m.phase >= 11 || v.Code == tea.KeyF3 && m.phase != 0 || v.Code == tea.KeyF4 && m.phase == 0 {
				return m, nil
			}
			m.busy = true
			phase := m.phase
			return m, func() tea.Msg { return multipleVisualDone{phase, m.observeMultiple(phase)} }
		}
		m.lastInput = fmt.Sprintf("key=%s text=%q", v.String(), v.Text)
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
func (m *multipleVisualModel) observeMultiple(phase int) error {
	if phase == 0 {
		if err := m.connect(); err != nil {
			return err
		}
		m.program.Send(tea.KeyPressMsg{Code: 'P', Text: "Private multiple draft."})
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
		if ed == nil || !ed.IsOpen() {
			return fmt.Errorf("file closed")
		}
		state := ed.DocumentState()
		if state.InFlight || state.PendingEdits != 0 || state.HistoryQueued || state.UndoPending || state.Conflict != nil {
			return fmt.Errorf("source/history not settled")
		}
		want := multiVisualSource
		switch phase {
		case 2:
			want = strings.ReplaceAll(want, "word", "X")
		case 4:
			want = strings.ReplaceAll(want, "word", "wordword")
		case 8:
			for _, letter := range []string{"a", "b", "c"} {
				want = strings.Replace(want, "word "+letter, "word Z", 1)
			}
		}
		if ed.Value() != want || v.Input.Value() != "Private multiple draft." || v.focusTarget() != FocusEditor {
			return fmt.Errorf("native input changed wrong source/focus: %s %q", m.lastInput, ed.Value())
		}
		count := 3
		if phase == 0 || phase == 6 || phase == 10 {
			count = 1
		}
		selection := ""
		switch phase {
		case 1, 3, 4, 5:
			selection = "word\r\nword\r\nword"
		case 6:
			selection = "word"
		case 7, 9:
			selection = "a\r\nb\r\nc"
		case 10:
			selection = "c"
		}
		if ed.CaretCount() != count || ed.SelectedText() != selection {
			return fmt.Errorf("native caret/selection: count=%d selected=%q want %d %q %s", ed.CaretCount(), ed.SelectedText(), count, selection, m.lastInput)
		}
		frame = ansi.Strip(v.View().RenderContent())
		if count > 1 && !strings.Contains(frame, "3 carets") {
			return fmt.Errorf("header does not identify multiple carets")
		}
		r := v.editorRect
		x, y := r.X+ed.PadL+ed.GutterWidth(), r.Y
		if phase == 0 {
			m.x, m.y = x+5, y
			hit = fmt.Sprintf("MULTIPLE_RECT from=%d,%d to=%d,%d", m.x, m.y, m.x+1, m.y+2)
		}
		if phase == 1 || phase == 3 || phase == 5 {
			for line := 0; line < 3; line++ {
				selected := v.mainSurf.Buffer().Get(x+1, y+line)
				outside := v.mainSurf.Buffer().Get(x+5, y+line)
				if selected.BG.Equal(outside.BG) {
					return fmt.Errorf("selection %d did not paint", line)
				}
			}
		}
		if v.overlayHost.IsOpen(ui.OverlayContextMenu) || m.rootHost.HasPointerCapture() {
			return fmt.Errorf("stale popup or capture")
		}
		return nil
	})
	if err != nil {
		_ = m.evidence(fmt.Sprintf("MULTIPLE_FAILED phase=%d %v\n%s", phase, err, frame))
		return err
	}
	return m.evidence(fmt.Sprintf("MULTIPLE_VERIFIED phase=%d\n%s\n%s", phase, hit, frame))
}
