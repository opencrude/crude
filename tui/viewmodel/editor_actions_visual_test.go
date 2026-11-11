package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"crude/shellport"
	"crude/tui/cell"
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

const productivityVisualSource = "package sample\n\nfunc work() {\n    call(arg + 1)\n}\n"

// F3 prepares the disposable connected document. F4 only observes. All editing,
// picker filtering and motion are ordinary terminal input; F9 closes and joins.
func TestEditorActionsVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_EDITOR_ACTIONS") != "1" {
		t.Skip("dedicated terminal editor actions acceptance")
	}
	runEditorActionsVisual(t, false, 0, 0)
}
func TestEditorActionsVisualHeadless(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) { runEditorActionsVisual(t, true, size[0], size[1]) })
	}
}

type editorActionsVisualModel struct {
	*featureVisualModel
	phase     int
	lastInput string
}
type editorActionsVisualDone struct {
	phase int
	err   error
}

func runEditorActionsVisual(t *testing.T, headless bool, width, height int) {
	root := t.TempDir()
	path := filepath.Join(root, "actions.go")
	if err := os.WriteFile(path, []byte(productivityVisualSource), 0600); err != nil {
		t.Fatal(err)
	}
	runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) {
		settings := w.AppDefaults()
		settings.LSP.Enabled = false
		w.SetAppDefaults(settings)
		restrictFixture(t, w)
	})
	shell := newRemoteJourneyShell(t, runtime, root)
	remoteJourneyCreate(t, shell)
	ctx, cancel := context.WithCancel(t.Context())
	m := &editorActionsVisualModel{featureVisualModel: &featureVisualModel{
		controlVisualModel: &controlVisualModel{App: shell.app, runtime: runtime, root: root, path: path, ctx: ctx, completed: map[int]bool{}},
		settled:            make(chan struct{}, 1), resultPath: os.Getenv("CRUDE_EDITOR_ACTIONS_RESULT"),
	}}
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
		data, err := os.ReadFile(path)
		if err != nil || string(data) != productivityVisualSource {
			t.Errorf("unsaved native journey changed disk: %q %v", data, err)
		}
		if err := m.evidence(fmt.Sprintf("EDITOR_ACTIONS_JOIN phase=%d passed=%t headless=%t", m.phase, !t.Failed(), headless)); err != nil {
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
	workers.Add(1)
	go func() {
		defer workers.Done()
		for {
			select {
			case msg := <-shell.fifo:
				program.Send(msg)
			case <-ctx.Done():
				return
			}
		}
	}()
	m.ws.(shellport.ShellControl).StartShellControl()
	if headless {
		workers.Add(1)
		go func() { defer workers.Done(); m.driveEditorActions(ctx) }()
	}
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if m.failure != nil {
		t.Fatal(m.failure)
	}
	if m.phase != 10 || m.rootHost.HasPointerCapture() {
		t.Fatal("editor journey incomplete", m.phase)
	}
}

func (m *editorActionsVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case controlVisualCheck:
		v.reply <- v.check(m.controlVisualModel)
		return m, nil
	case editorActionsVisualDone:
		m.busy = false
		if v.err != nil {
			m.failure = fmt.Errorf("editor actions phase %d: %w", v.phase, v.err)
			return m, tea.Quit
		}
		m.phase = v.phase + 1
		select {
		case m.settled <- struct{}{}:
		default:
		}
		phase := m.phase
		return m, func() tea.Msg {
			if err := m.evidence(fmt.Sprintf("EDITOR_ACTIONS_READY phase=%d", phase)); err != nil {
				return editorActionsVisualDone{phase: -1, err: err}
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
			if m.busy || v.Code == tea.KeyF3 && m.phase != 0 || v.Code == tea.KeyF4 && (m.phase == 0 || m.phase >= 10) {
				return m, nil
			}
			m.busy = true
			phase := m.phase
			return m, func() tea.Msg { return editorActionsVisualDone{phase, m.observeEditorActions(phase)} }
		}
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
		m.lastInput = fmt.Sprintf("key=%s code=%U modifiers=%d text=%q", key.String(), key.Code, key.Mod, key.Text)
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}

func (m *editorActionsVisualModel) observeEditorActions(phase int) error {
	if phase == 0 {
		if err := m.connect(); err != nil {
			return err
		}
		m.program.Send(tea.KeyPressMsg{Code: 'P', Text: "Private editor draft stays unsent."})
		if _, err := m.execute("file.open", map[string]any{"path": m.path}, nil); err != nil {
			return err
		}
		if _, err := m.execute("tree.set_visible", map[string]any{"visible": false}, nil); err != nil {
			return err
		}
	}
	var frame, lastInput string
	err := m.await(func(v *controlVisualModel) error {
		ed := v.EditorState.Editor
		if ed == nil || !ed.IsOpen() {
			return fmt.Errorf("editor not open")
		}
		want := productivityVisualSource
		if phase == 1 {
			want = strings.Replace(want, "    call(arg + 1)\n", "    call(arg + 1)\n    call(arg + 1)\n", 1)
		}
		if phase == 4 {
			want = strings.Replace(want, "    call(arg + 1)\n}", "    call(arg + 1) }", 1)
		}
		state := ed.DocumentState()
		syntax, _ := ed.InsightStatus()
		if ed.Value() != want || state.PendingEdits != 0 || state.InFlight || state.UndoPending || state.HistoryQueued || state.Conflict != nil || syntax != types.InsightCurrent {
			return fmt.Errorf("source not settled: %q %+v; %s", ed.Value(), state, m.lastInput)
		}
		if v.Input.Value() != "Private editor draft stays unsent." {
			return fmt.Errorf("private draft changed")
		}
		selected := ""
		if phase == 7 || phase == 9 {
			selected = "arg"
		}
		if phase == 8 {
			selected = "arg + 1"
		}
		if ed.SelectedText() != selected {
			return fmt.Errorf("selection %q, want %q; %s", ed.SelectedText(), selected, m.lastInput)
		}
		lastInput = m.lastInput
		frame = ansi.Strip(v.View().RenderContent())
		if phase == 3 {
			if v.commandPaletteModel == nil || !strings.Contains(frame, "Editor actions") || !strings.Contains(frame, "Join lines") {
				return fmt.Errorf("searchable action picker absent: %s", frame)
			}
			model := v.commandPaletteModel
			if model.filterText() != "join lines" || len(model.filtered) != 1 || model.filtered[0].ID != "edit.joinLines" {
				return fmt.Errorf("native filter %q did not select Join lines; %s", model.filterText(), m.lastInput)
			}
		} else if v.focusTarget() != FocusEditor || v.commandPaletteModel != nil {
			return fmt.Errorf("editor lost focus: target=%v picker=%t frame=\n%s", v.focusTarget(), v.commandPaletteModel != nil, frame)
		}
		if phase == 6 {
			if ed.CursorLine() != 3 || ed.CursorCol() != 16 {
				return fmt.Errorf("bracket jump missed: %d:%d", ed.CursorLine(), ed.CursorCol())
			}
			r := v.editorRect
			painted := v.mainSurf.Buffer().Get(r.X+ed.PadL+ed.GutterWidth()+8, r.Y+3)
			if !painted.Attr.Has(cell.AttrBold) {
				return fmt.Errorf("matched opening bracket not painted: cell=%+v padding=%d frame=\n%s", painted, ed.PadL, frame)
			}
		}
		return nil
	})
	if err != nil {
		return errors.Join(err, m.evidence(fmt.Sprintf("EDITOR_ACTIONS_FAILED phase=%d: %v", phase, err)))
	}
	return m.evidence(fmt.Sprintf("F4 VERIFIED phase=%d input=%s\n%s", phase, lastInput, frame))
}

func (m *editorActionsVisualModel) driveEditorActions(ctx context.Context) {
	send := func(code rune, mod tea.KeyMod) { m.program.Send(tea.KeyPressMsg{Code: code, Mod: mod}) }
	observe := func(key rune) bool {
		send(key, 0)
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
	send(tea.KeyHome, tea.ModCtrl)
	for range 3 {
		send(tea.KeyDown, 0)
	}
	send(tea.KeyEnd, 0)
	send('d', tea.ModCtrl)
	if !observe(tea.KeyF4) {
		return
	}
	send('z', tea.ModCtrl)
	if !observe(tea.KeyF4) {
		return
	}
	send('/', tea.ModAlt)
	for _, r := range "join lines" {
		m.program.Send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if !observe(tea.KeyF4) {
		return
	}
	send(tea.KeyEnter, 0)
	if !observe(tea.KeyF4) {
		return
	}
	send('z', tea.ModCtrl)
	if !observe(tea.KeyF4) {
		return
	}
	send(tea.KeyHome, 0)
	for range 4 {
		send(tea.KeyRight, 0)
	}
	send('b', tea.ModCtrl|tea.ModAlt)
	if !observe(tea.KeyF4) {
		return
	}
	send(tea.KeyHome, 0)
	for range 5 {
		send(tea.KeyRight, 0)
	}
	send('w', tea.ModAlt)
	if !observe(tea.KeyF4) {
		return
	}
	send('w', tea.ModAlt)
	if !observe(tea.KeyF4) {
		return
	}
	send('q', tea.ModAlt)
	if observe(tea.KeyF4) {
		send(tea.KeyF9, 0)
	}
}
