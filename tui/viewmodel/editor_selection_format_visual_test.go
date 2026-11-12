package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"crude/core"
	"crude/tui/editor"
	"crude/types"
	"crude/workspace"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const rangeVisualSource = "int alpha=1; // 界é\r\nint beta=2; // 界é\r\nint omega=3; // 界é\r\n"
const rangeVisualFormatted = "int alpha=1; // 界é\r\nint beta = 2; // 界é\r\nint omega=3; // 界é\r\n"

func installedRangeFixture(t *testing.T) (remoteJourneyShell, *editor.Editor, *core.Runtime, string, string) {
	t.Helper()
	path := os.Getenv("CRUDE_FORMAT_CLANGD")
	if path == "" {
		t.Skip("set CRUDE_FORMAT_CLANGD to an installed server")
	}
	executable, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		t.Fatal(err)
	}
	version, err := exec.Command(executable, "--version").CombinedOutput()
	if err != nil || !strings.Contains(string(version), "clangd") {
		t.Fatal(string(version), err)
	}
	root := t.TempDir()
	source := writePaneFile(t, root, "main.c", rangeVisualSource)
	writePaneFile(t, root, ".clang-format", "BasedOnStyle: LLVM\nIndentWidth: 2\nColumnLimit: 80\nUseCRLF: true\n")
	var retained *workspace.Workspace
	runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) {
		retained = w
		s := w.AppDefaults()
		s.Editor.LineNumbers = true
		s.LSP = types.DefaultLSPSettings()
		s.LSP.AutoDetect = false
		s.LSP.AutoComplete = false
		s.LSP.Languages = map[string]types.LSPLanguageSettings{"c": {Enabled: true, Command: executable, Args: []string{"--background-index=false", "--clang-tidy=false", "--log=error"}, RootMarkers: []string{".clang-format"}}}
		w.SetAppDefaults(s)
		grantSemanticFixture(t, w)
	})
	shell := newRemoteJourneyShell(t, runtime, root)
	ed := liveTestOpen(t, shell.app, source)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, _, err := retained.LSPMgr.ProbeHover(ctx, source, types.LSPSourcePosition{Line: 2, Column: 5}); err != nil {
		t.Fatal(err)
	}
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		_, ready := ed.CurrentInsightSource()
		return ready && shell.app.ws.LSPMgr().ServerReady()
	})
	shell.app.setFocusEditor()
	shell.app.Input.SetInput("private format draft")
	return shell, ed, runtime, source, executable
}
func TestInstalledSelectionFormatting(t *testing.T) {
	shell, ed, _, path, _ := installedRangeFixture(t)
	ed.SetCursorPosition(1, 11)
	ed.SetSelection(1, 0, 1, editor.LineVisualWidth(ed.LineText(1), ed.TabWidth()))
	renameConnectedKey(t, shell, tea.KeyPressMsg{Code: tea.KeyF8, Mod: tea.ModAlt})
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return ed.Value() == rangeVisualFormatted })
	renameConnectedKey(t, shell, tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return ed.Value() == rangeVisualSource })
	if disk, err := os.ReadFile(path); err != nil || string(disk) != rangeVisualSource {
		t.Fatal("range formatting wrote disk", err)
	}
}
func TestSelectionFormattingVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_RANGE_FORMAT") != "1" {
		t.Skip("dedicated native range-formatting journey")
	}
	runRangeVisual(t, false, 0, 0)
}
func TestSelectionFormattingVisualHeadless(t *testing.T) {
	if os.Getenv("CRUDE_FORMAT_CLANGD") == "" {
		t.Skip("installed server acceptance")
	}
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) { runRangeVisual(t, true, size[0], size[1]) })
	}
}

type rangeVisualModel struct {
	*featureVisualModel
	phase  int
	inputs []string
}
type rangeVisualDone struct {
	phase int
	err   error
}

func runRangeVisual(t *testing.T, headless bool, width, height int) {
	var m *rangeVisualModel
	t.Cleanup(func() {
		if m != nil {
			if err := m.evidence(fmt.Sprintf("RANGE_JOIN phase=%d passed=%t headless=%t", m.phase, !t.Failed(), headless)); err != nil {
				t.Error(err)
			}
		}
	})
	shell, _, runtime, path, executable := installedRangeFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	m = &rangeVisualModel{featureVisualModel: &featureVisualModel{controlVisualModel: &controlVisualModel{App: shell.app, ctx: ctx, path: path, runtime: runtime}, settled: make(chan struct{}, 1), resultPath: os.Getenv("CRUDE_RANGE_RESULT")}}
	if err := m.evidence("RANGE_PROVIDER executable=" + executable); err != nil {
		t.Fatal(err)
	}
	pids, err := lspVisualProcesses(filepath.Dir(path), executable)
	if err != nil || len(pids) == 0 {
		t.Fatal("provider process not observed", pids, err)
	}
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
			case msg := <-shell.fifo:
				program.Send(msg)
			case <-ctx.Done():
				return
			}
		}
	})
	if headless {
		workers.Go(func() {
			key := func(code rune, mod tea.KeyMod) { program.Send(tea.KeyPressMsg{Code: code, Mod: mod}) }
			observe := func() bool {
				key(tea.KeyF4, 0)
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
			key(tea.KeyHome, tea.ModCtrl)
			key(tea.KeyDown, 0)
			key(tea.KeyHome, 0)
			for range 17 {
				key(tea.KeyRight, tea.ModShift)
			}
			if !observe() {
				return
			}
			key(tea.KeyF8, tea.ModAlt)
			if !observe() {
				return
			}
			key('z', tea.ModCtrl)
			if !observe() {
				return
			}
			key('/', tea.ModAlt)
			for _, r := range "Format selection" {
				program.Send(tea.KeyPressMsg{Code: r, Text: string(r)})
			}
			key(tea.KeyEnter, 0)
			if !observe() {
				return
			}
			key('z', tea.ModCtrl)
			if observe() {
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
	if m.phase != 6 {
		t.Fatal("incomplete native formatting journey", m.phase)
	}
	m.StopAsyncIO()
	if err := m.CloseWorkspaceAttachment(); err != nil {
		t.Error(err)
	}
	if err := runtime.Close(); err != nil {
		t.Error(err)
	}
	remaining, err := lspVisualProcesses(filepath.Dir(path), executable)
	if err != nil || len(remaining) != 0 {
		t.Error("provider not joined", remaining, err)
	}
	if err := m.evidence(fmt.Sprintf("RANGE_PROVIDER_JOIN started=%v remaining=%v", pids, remaining)); err != nil {
		t.Error(err)
	}
	if disk, err := os.ReadFile(path); err != nil || string(disk) != rangeVisualSource {
		t.Fatal("native journey saved disk", err)
	}
}
func (m *rangeVisualModel) Init() tea.Cmd { return tickCmd() }
func (m *rangeVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case controlVisualCheck:
		v.reply <- v.check(m.controlVisualModel)
		return m, nil
	case rangeVisualDone:
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
			if err := m.evidence(fmt.Sprintf("RANGE_READY phase=%d", phase)); err != nil {
				return rangeVisualDone{-1, err}
			}
			return nil
		}
	case tea.KeyPressMsg:
		if v.Code != tea.KeyF4 && len(m.inputs) < 64 {
			m.inputs = append(m.inputs, fmt.Sprintf("%s/%U/%d", v.String(), v.Code, v.Mod))
		}
		if v.Code == tea.KeyF9 && v.Mod == 0 {
			if m.busy {
				return m, nil
			}
			return m, tea.Quit
		}
		if v.Code == tea.KeyF4 && v.Mod == 0 {
			if m.busy || m.phase >= 6 {
				return m, nil
			}
			m.busy = true
			phase := m.phase
			return m, func() tea.Msg { return rangeVisualDone{phase, m.observeRange(phase)} }
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
func (m *rangeVisualModel) observeRange(phase int) error {
	var frame string
	err := m.await(func(v *controlVisualModel) error {
		ed := v.EditorState.Editor
		if ed == nil {
			return fmt.Errorf("editor closed")
		}
		state := ed.DocumentState()
		want := rangeVisualSource
		if phase == 2 || phase == 4 {
			want = rangeVisualFormatted
		}
		if ed.Value() != want || state.PendingEdits != 0 || state.InFlight || state.UndoPending || state.HistoryQueued || v.selectionFormatting != nil {
			return fmt.Errorf("format/history not settled: %q %+v", ed.Value(), state)
		}
		if phase > 0 {
			selection := "int beta=2; // 界é"
			if phase == 2 || phase == 4 {
				selection = "int beta = 2; // 界é"
			}
			if ed.SelectedText() != selection {
				return fmt.Errorf("selection %q, want %q; inputs=%v", ed.SelectedText(), selection, m.inputs)
			}
		}
		if v.Input.Value() != "private format draft" || v.focusTarget() != FocusEditor || v.commandPaletteModel != nil {
			return fmt.Errorf("formatting lost focus or draft")
		}
		frame = ansi.Strip(v.View().RenderContent())
		if !strings.Contains(frame, "int alpha=1;") || !strings.Contains(frame, "int omega=3;") {
			return fmt.Errorf("unselected lines changed or not painted")
		}
		return nil
	})
	if err != nil {
		_ = m.evidence(fmt.Sprintf("RANGE_FAILED phase=%d %v\n%s", phase, err, frame))
		return err
	}
	return m.evidence(fmt.Sprintf("RANGE_VERIFIED phase=%d\n%s", phase, frame))
}
