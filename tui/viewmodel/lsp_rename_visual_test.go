package viewmodel

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/localhistory"
	"crude/shellport"
	"crude/types"
	"crude/workspace"
	"github.com/charmbracelet/x/ansi"
)

// Real engine, core, editor and ordinary input. F3 prepares the fixture, F4
// observes four boundaries (preview, cancelled, fresh preview, committed).
// Between observations native capture supplies F2/type/Enter, default Cancel,
// another F2/type/Enter, then Left/Enter Apply. F9 is the final joined exit.
// No verifier phase performs a scored rename mutation.
func TestLSPRenameVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_RENAME") != "1" {
		t.Skip("dedicated terminal real-provider rename acceptance only")
	}
	runRenameVisual(t, false, 0, 0)
}

func TestLSPRenameVisualHeadless(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			runRenameVisual(t, true, size[0], size[1])
		})
	}
}

const renameVisualBefore = "package journey\n\nvar renameOld = 1\n\nfunc RenameUse() int { return renameOld }\n"
const renameVisualAfter = "package journey\n\nvar renameNew = 1\n\nfunc RenameUse() int { return renameNew }\n"

type renameVisualModel struct {
	*featureVisualModel
	phase      int
	firstID    string
	plan       workspace.ReviewedEditPreview
	lastResult workspace.ReviewedEditResult
	executable string
	pids       []int
}

type renameVisualDone struct {
	phase int
	err   error
}

func runRenameVisual(t *testing.T, headless bool, width, height int) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("process join oracle uses Linux /proc")
	}
	gopls := os.Getenv("CRUDE_LSP_VISUAL_GOPLS")
	if gopls == "" {
		gopls, _ = exec.LookPath("gopls")
	}
	if gopls == "" {
		gopls = "/home/lenovo/go/bin/gopls"
	}
	executable, err := filepath.EvalSymlinks(gopls)
	if err != nil {
		if headless {
			t.Skipf("installed gopls unavailable: %v", err)
		}
		t.Fatal(err)
	}
	goroot, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatal(err)
	}
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CRUDE_HOME", home)
	path := filepath.Join(root, "rename.go")
	for name, text := range map[string]string{"go.mod": "module journey\n\ngo 1.22\n", "rename.go": renameVisualBefore} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	coreRuntime := remoteJourneyRuntime(t, "http://127.0.0.1:1", func(w *workspace.Workspace) {
		settings := w.AppDefaults()
		settings.LSP = types.DefaultLSPSettings()
		settings.LSP.AutoDetect, settings.LSP.AutoComplete = false, false
		settings.LSP.Languages = map[string]types.LSPLanguageSettings{"go": {Enabled: true, Command: executable,
			Args: []string{"serve"}, RootMarkers: []string{"go.mod"},
			Environment: map[string]string{"HOME": home, "GOROOT": strings.TrimSpace(string(goroot)), "GOPATH": filepath.Join(home, "go"),
				"GOCACHE": filepath.Join(home, "cache"), "GOENV": "off", "GOPROXY": "off", "GOSUMDB": "off", "GOTOOLCHAIN": "local"}}}
		w.SetAppDefaults(settings)
		grantSemanticFixture(t, w)
	})
	shell := newRemoteJourneyShell(t, coreRuntime, root)
	m := &renameVisualModel{featureVisualModel: &featureVisualModel{
		controlVisualModel: &controlVisualModel{App: shell.app, runtime: coreRuntime, root: root, path: path,
			ctx: t.Context(), completed: map[int]bool{}}, settled: make(chan struct{}, 1)}, executable: executable}
	if !headless {
		m.resultPath = os.Getenv("CRUDE_RENAME_VISUAL_RESULT")
	}
	defer func() {
		if m.client != nil {
			if err := m.client.Close(); err != nil {
				t.Error(err)
			}
		}
		m.StopAsyncIO()
		if err := m.CloseWorkspaceAttachment(); err != nil {
			t.Error(err)
		}
		if err := coreRuntime.Close(); err != nil {
			t.Error(err)
		}
		remaining, err := lspVisualProcesses(root, executable)
		if err != nil || len(remaining) != 0 || len(m.pids) == 0 {
			t.Errorf("real rename provider ownership unproven: started=%v remaining=%v err=%v", m.pids, remaining, err)
		} else if !t.Failed() {
			if err := m.evidence(fmt.Sprintf("RENAME_VISUAL_SUCCESS F3 F4x4; real gopls pids=%v joined; retained selected safety and exact original receipt; local framed core only", m.pids)); err != nil {
				t.Error(err)
			}
		}
	}()
	opts := []tea.ProgramOption{tea.WithWidthMethod(ansi.GraphemeWidth)}
	if headless {
		opts = append(opts, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithWindowSize(width, height))
	}
	program := tea.NewProgram(m, opts...)
	m.program = program
	m.SetProgram(program)
	pumpCtx, stopPump := context.WithCancel(t.Context())
	defer stopPump()
	go func() {
		for {
			select {
			case msg := <-shell.fifo:
				program.Send(msg)
			case <-pumpCtx.Done():
				return
			}
		}
	}()
	m.ws.(shellport.ShellControl).StartShellControl()
	if headless {
		go func() {
			observe := func(code rune) bool {
				program.Send(tea.KeyPressMsg{Code: code})
				select {
				case <-m.settled:
					return true
				case <-pumpCtx.Done():
					return false
				}
			}
			name := func() {
				program.Send(tea.KeyPressMsg{Code: tea.KeyF2})
				for _, r := range "renameNew" {
					program.Send(tea.KeyPressMsg{Code: r, Text: string(r)})
				}
				program.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
			}
			if !observe(tea.KeyF3) {
				return
			}
			name()
			if !observe(tea.KeyF4) {
				return
			}
			program.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
			if !observe(tea.KeyF4) {
				return
			}
			name()
			if !observe(tea.KeyF4) {
				return
			}
			program.Send(tea.KeyPressMsg{Code: tea.KeyLeft})
			program.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
			if observe(tea.KeyF4) {
				program.Send(tea.KeyPressMsg{Code: tea.KeyF9})
			}
		}()
	}
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if m.failure != nil {
		_ = m.evidence("RENAME_VISUAL_FAILED " + m.failure.Error())
		t.Fatal(m.failure)
	}
	if m.phase != 5 {
		t.Fatalf("rename phases incomplete: %d", m.phase)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != renameVisualAfter {
		t.Fatal("native scored rename did not reach literal final source", string(got), err)
	}
}

func (m *renameVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case lspRenameAppliedMsg:
		m.lastResult = v.result
	case controlVisualCheck:
		v.reply <- v.check(m.controlVisualModel)
		return m, nil
	case featureVisualAction:
		return m, v.run(m.App)
	case renameVisualDone:
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
		return m, nil
	case tea.KeyPressMsg:
		if v.Code == tea.KeyF9 && !m.busy {
			return m, tea.Quit
		}
		if v.Code == tea.KeyF3 && m.phase == 0 || v.Code == tea.KeyF4 && m.phase > 0 && m.phase < 5 {
			if m.busy {
				return m, nil
			}
			m.busy = true
			phase := m.phase
			work := func() tea.Msg { return renameVisualDone{phase, m.observeRename(phase)} }
			return m, work
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}

func (m *renameVisualModel) observeRename(phase int) error {
	if phase == 0 {
		if err := m.connect(); err != nil {
			return err
		}
		if _, err := m.execute("file.open", map[string]any{"path": m.path}, nil); err != nil {
			return err
		}
		m.action(func(app *App) tea.Cmd {
			app.hideWorkspacePanel()
			app.setFocusEditor()
			app.EditorState.Editor.SetCursorPosition(2, 7)
			return nil
		})
		err := m.await(func(v *controlVisualModel) error {
			facts, ok := v.ws.LSPMgr().FileDiagnostics(m.path)
			source, current := v.EditorState.Editor.CurrentInsightSource()
			if !ok || !current || !facts.HasExactSource() || facts.Boundary.Source != source {
				return fmt.Errorf("real rename source not yet synchronized")
			}
			return featureVisualPaint(v, "var renameOld = 1")
		})
		if err != nil {
			return err
		}
		seen, err := lspVisualProcesses(m.root, m.executable)
		if err != nil || len(seen) == 0 {
			return fmt.Errorf("real rename provider identity not observed: %v", err)
		}
		if err := m.check(func(*controlVisualModel) error { m.pids = seen; return nil }); err != nil {
			return err
		}
		return m.evidence("F3 VERIFIED real gopls synchronized canonical source before scored rename keys")
	}
	if phase == 1 || phase == 3 {
		err := m.await(func(v *controlVisualModel) error {
			p := v.LSPPopup.RenamePreview
			if p == nil || p.request.preview.ID == "" || len(p.request.preview.Files) != 1 {
				return fmt.Errorf("real rename immutable preview not mounted")
			}
			if phase == 3 && p.request.preview.ID == m.firstID {
				return fmt.Errorf("cancelled preview was revived")
			}
			if phase == 1 {
				m.firstID = p.request.preview.ID
			} else {
				m.plan = p.request.preview
			}
			return featureVisualPaint(v, "Review rename", "renameNew", "Apply rename", "Cancel")
		})
		data, readErr := os.ReadFile(m.path)
		if readErr != nil || string(data) != renameVisualBefore {
			return fmt.Errorf("preview changed disk bytes: %v", readErr)
		}
		return err
	}
	if phase == 2 {
		return m.await(func(v *controlVisualModel) error {
			if v.LSPPopup.RenamePreview != nil || v.EditorState.Editor.Value() != renameVisualBefore {
				return fmt.Errorf("default Cancel did not leave the canonical preimage")
			}
			return featureVisualPaint(v, "var renameOld = 1")
		})
	}
	err := m.await(func(v *controlVisualModel) error {
		out := m.lastResult
		if out.ID != m.plan.ID || out.Digest != m.plan.Digest || out.State != "committed" ||
			out.SafetySnapshotID == "" || out.SafetyReference == "" || v.LSPPopup.RenamePreview != nil ||
			v.EditorState.Editor.Value() != renameVisualAfter || v.EditorState.Editor.Modified() {
			return fmt.Errorf("real rename effect/safety/publication not confirmed: %+v", out)
		}
		return featureVisualPaint(v, "var renameNew = 1")
	})
	if err != nil {
		return err
	}
	var out workspace.ReviewedEditResult
	if err := m.check(func(*controlVisualModel) error { out = m.lastResult; return nil }); err != nil {
		return err
	}
	observed, err := m.ws.(shellport.ReviewedEdits).InspectReviewedEdit(m.ctx, out.ID, out.Digest)
	if err != nil || observed != out {
		return fmt.Errorf("original rename receipt differs: %+v %v", observed, err)
	}
	plan, err := m.ws.LocalHistory().PlanRestore(m.ctx, out.SafetySnapshotID, localhistory.SnapshotOptions{})
	if err != nil || len(plan.Changes) != 1 || plan.Changes[0].Path != "rename.go" ||
		plan.Changes[0].Target == nil || plan.Changes[0].Target.Hash != fmt.Sprintf("%x", sha256.Sum256([]byte(renameVisualBefore))) {
		return fmt.Errorf("native rename recovery preimage is not exact selected source: %+v %v", plan, err)
	}
	return m.evidence("F4x4 VERIFIED actual F2/name/Enter, Cancel default, fresh exact review, Left/Enter Apply, canonical source and original selected-safety receipt")
}
