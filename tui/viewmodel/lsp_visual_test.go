package viewmodel

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"crude/shellport"
	"crude/tui/screens/statusoverview"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Capture sequentially with scripts/tuishot.sh, a prebuilt viewmodel test binary,
// CRUDE_VISUAL_LSP=1 and CRUDE_ARGS=-test.run=^TestLSPVisualAcceptance$.
// F2: live diagnostics; F3: hover; F4: references; F5: definition; F6: completion;
// quit:F9 verifies all phases and joins the real gopls process. Optional
// CRUDE_LSP_VISUAL_RESULT receives per-phase proof and the final cleanup result.
// CRUDE_LSP_VISUAL_GOPLS selects an installed executable, never a protocol fake.
func TestLSPVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_LSP") != "1" {
		t.Skip("dedicated-terminal visual acceptance only")
	}
	runLSPVisual(t, false, 0, 0)
}

func TestLSPVisualHarnessHeadless(t *testing.T) {
	for _, size := range [][2]int{{160, 45}, {60, 20}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			runLSPVisual(t, true, size[0], size[1])
		})
	}
}

const lspVisualMain = "package journey\n\nvar Result = Answer() + missing\n"
const lspVisualLib = "package journey\n\n// Answer returns the LSP_HOVER_SENTINEL value.\nfunc Answer() int { return 42 }\n"

func runLSPVisual(t *testing.T, headless bool, width, height int) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("exact process-join oracle currently requires Linux /proc")
	}
	gopls := os.Getenv("CRUDE_LSP_VISUAL_GOPLS")
	if gopls == "" {
		gopls, _ = exec.LookPath("gopls")
		if gopls == "" {
			gopls = "/home/lenovo/go/bin/gopls"
		}
	}
	gopls, err := filepath.Abs(gopls)
	if err != nil {
		t.Fatal(err)
	}
	gopls, err = filepath.EvalSymlinks(gopls)
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
	sources := map[string]string{"go.mod": "module journey\n\ngo 1.22\n", "main.go": lspVisualMain, "lib.go": lspVisualLib}
	for name, text := range sources {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	coreRuntime := remoteJourneyRuntime(t, "http://127.0.0.1:1", func(w *workspace.Workspace) {
		settings := w.AppDefaults()
		settings.LSP = types.DefaultLSPSettings()
		settings.LSP.AutoDetect, settings.LSP.AutoComplete = false, false
		settings.LSP.Languages = map[string]types.LSPLanguageSettings{"go": {
			Enabled: true, Command: gopls, Args: []string{"serve"}, RootMarkers: []string{"go.mod"},
			Environment: map[string]string{"HOME": home, "GOROOT": strings.TrimSpace(string(goroot)), "GOPATH": filepath.Join(home, "go"), "GOCACHE": filepath.Join(home, "cache"), "GOENV": "off", "GOPROXY": "off", "GOSUMDB": "off", "GOTOOLCHAIN": "local"},
		}}
		// Launch settings are fixture-private. Execution separately requires a
		// durable explicit grant for this exact temporary workspace directory.
		w.SetAppDefaults(settings)
		grantSemanticFixture(t, w)
	})
	shell := newRemoteJourneyShell(t, coreRuntime, root)
	m := &lspVisualModel{featureVisualModel: &featureVisualModel{
		controlVisualModel: &controlVisualModel{App: shell.app, runtime: coreRuntime, root: root, path: filepath.Join(root, "main.go"), ctx: t.Context(), completed: map[int]bool{}},
		settled:            make(chan struct{}, 1),
	}, executable: gopls}
	if !headless {
		m.resultPath = os.Getenv("CRUDE_LSP_VISUAL_RESULT")
	}
	// Run before Test.Context cancellation: the control carrier must remain alive
	// for its graceful close receipt. This also runs on every phase failure.
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
		deadline := time.Now().Add(5 * time.Second)
		for {
			pids, err := lspVisualProcesses(root, gopls)
			if err != nil {
				t.Error(err)
				break
			}
			if len(pids) == 0 {
				if !t.Failed() {
					if err := m.evidence(fmt.Sprintf("LSP_VISUAL_SUCCESS F2 F3 F4 F5 F6; real gopls pids=%v joined; local framed core only", m.pids)); err != nil {
						t.Error(err)
					}
				}
				break
			}
			if time.Now().After(deadline) {
				for _, pid := range pids {
					proc := filepath.Join("/proc", strconv.Itoa(pid))
					command, _ := os.ReadFile(filepath.Join(proc, "cmdline"))
					status, _ := os.ReadFile(filepath.Join(proc, "stat"))
					t.Logf("surviving gopls command=%q stat=%s", strings.ReplaceAll(string(command), "\x00", " "), status)
				}
				t.Errorf("gopls survived core shutdown: %v", pids)
				break
			}
			time.Sleep(10 * time.Millisecond)
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
			for key := tea.KeyF2; key <= tea.KeyF6; key++ {
				program.Send(tea.KeyPressMsg{Code: key})
				select {
				case <-m.settled:
				case <-pumpCtx.Done():
					return
				}
			}
			program.Send(tea.KeyPressMsg{Code: tea.KeyF9})
		}()
	}
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if m.failure != nil {
		if err := m.evidence("LSP_VISUAL_FAILED " + m.failure.Error()); err != nil {
			t.Error(err)
		}
		t.Fatal(m.failure)
	}
	for key := 2; key <= 6; key++ {
		if !m.completed[key] {
			t.Errorf("F%d not completed", key)
		}
	}
	if m.rootHost.HasPointerCapture() {
		t.Error("pointer capture remained armed")
	}
	for name, want := range sources {
		got, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(got) != want {
			t.Errorf("read-only journey changed %s: %q, %v", name, got, err)
		}
	}
}

type lspVisualModel struct {
	*featureVisualModel
	executable string
	pids       []int
}
type lspVisualDone struct {
	key int
	err error
}

func (m *lspVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case controlVisualCheck:
		v.reply <- v.check(m.controlVisualModel)
		return m, nil
	case featureVisualAction:
		return m, v.run(m.App)
	case lspVisualDone:
		m.busy = false
		if v.err != nil {
			m.failure = fmt.Errorf("F%d: %w", v.key, v.err)
			return m, tea.Quit
		}
		m.completed[v.key] = true
		select {
		case m.settled <- struct{}{}:
		default: // Native capture advances with keys, not the headless driver.
		}
		return m, nil
	case tea.KeyPressMsg:
		if v.Code == tea.KeyF9 && !m.busy {
			return m, tea.Quit
		}
		if v.Code >= tea.KeyF2 && v.Code <= tea.KeyF6 {
			key := int(v.Code-tea.KeyF2) + 2
			if m.busy || m.completed[key] {
				return m, nil
			}
			if key > 2 && !m.completed[key-1] {
				m.failure = fmt.Errorf("F%d requires previous phase", key)
				return m, tea.Quit
			}
			m.busy = true
			return m, func() tea.Msg { return lspVisualDone{key, m.journeyLSP(key)} }
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}

func (m *lspVisualModel) journeyLSP(key int) error {
	if err := m.connect(); err != nil {
		return err
	}
	do := func(id string, input any) error { _, err := m.execute(id, input, nil); return err }
	paint := func(v *controlVisualModel, markers ...string) error {
		insights := v.ws.LSPMgr().ProjectInsights()
		if !insights.ServerReady || insights.ServerStarting || insights.ServerSyncing || insights.ServerFailed || insights.ServerError != "" {
			return fmt.Errorf("language server not settled: %+v", insights)
		}
		frame := ansi.Strip(v.View().RenderContent())
		for _, absent := range []string{"LSP not ready", "No matching language server", "No symbol documentation", "No references found", "No completions", "No definition found", "Loading…", "Hover:"} {
			if strings.Contains(frame, absent) {
				return fmt.Errorf("unexpected %q in settled frame:\n%s", absent, frame)
			}
		}
		return featureVisualPaint(v, markers...)
	}
	// Position only on Update; real keyboard shortcuts own every LSP request.
	position := func(line, col int, input tea.KeyPressMsg) {
		m.action(func(app *App) tea.Cmd {
			app.hideWorkspacePanel()
			app.setFocusEditor()
			app.EditorState.Editor.SetCursorPosition(line, col)
			app.View()
			_, cmd := app.Update(input)
			return cmd
		})
	}
	var err error
	label := ""
	switch key {
	case 2:
		if err = do("file.open", map[string]any{"path": m.path}); err != nil {
			return err
		}
		if err = do("tree.set_visible", map[string]any{"visible": false}); err != nil {
			return err
		}
		if err = do("panel.show", map[string]any{"id": termdock.PanelProblems}); err != nil {
			return err
		}
		err = m.await(func(v *controlVisualModel) error {
			diags := diagnosticList(v.ws.LSPMgr(), m.path)
			if len(diags) != 1 || diags[0].Message != "undefined: missing" || diags[0].Line != 2 || diags[0].Col != 24 || diags[0].Severity != types.DiagError || diags[0].Source != "compiler" {
				return fmt.Errorf("real compiler diagnostics not installed: %+v", diags)
			}
			if got := v.EditorState.Editor.Diagnostics(); len(got) != 1 || got[0].Message != "undefined: missing" {
				return fmt.Errorf("diagnostic publication did not reach editor: %+v", got)
			}
			if v.EditorState.Editor.Value() != lspVisualMain {
				return fmt.Errorf("document API source differs")
			}
			return nil
		})
		if err != nil {
			return err
		}
		if err = m.check(func(v *controlVisualModel) error {
			app := v.App
			app.syncWorkspacePanels()
			app.View()
			screen := app.termDock.Panel(termdock.PanelProblems).(*statusoverview.Surface)
			facts, ok := app.ws.LSPMgr().FileDiagnostics(m.path)
			if !ok || facts.ID == "" || !screen.Select("problem:"+facts.ID+":0") {
				return fmt.Errorf("diagnostic row not in Problems projection")
			}
			app.rootHost.Focus("status-overview/list")
			return nil
		}); err != nil {
			return err
		}
		m.action(func(app *App) tea.Cmd { _, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyRight}); return cmd })
		m.action(func(app *App) tea.Cmd {
			app.View()
			r, ok := app.rootHost.ScreenRect("status-overview/list")
			if ok {
				// The full diagnostic details are below the fold at 60x20.
				// Scroll the actual mounted list, rather than accept its clipped title.
				for i := 0; i < 30; i++ {
					app.Update(tea.MouseWheelMsg{X: r.X + 2, Y: r.Y + 1, Button: tea.MouseWheelDown})
				}
			}
			return nil
		})
		err = m.await(func(v *controlVisualModel) error { return paint(v, "undefined: missing") })
		if err == nil {
			m.pids, err = lspVisualProcesses(m.root, m.executable)
			if err == nil && len(m.pids) == 0 {
				err = fmt.Errorf("no real gopls process; shutdown check would be vacuous")
			}
		}
		label = "compiler undefined: missing at main.go:3:25; diagnostic topic and editor agree"
	case 3:
		position(2, 14, tea.KeyPressMsg{Code: 'k', Mod: tea.ModAlt})
		err = m.await(func(v *controlVisualModel) error {
			if !v.overlayHost.IsOpen(ui.OverlayID("lsp-hover")) {
				return fmt.Errorf("hover not mounted")
			}
			return paint(v, "Symbol documentation", "Answer", "LSP_HOVER_SENTINEL")
		})
		label = "Alt+K real hover Answer() int and LSP_HOVER_SENTINEL from lib.go"
	case 4:
		m.action(func(app *App) tea.Cmd { _, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEsc}); return cmd })
		position(2, 14, tea.KeyPressMsg{Code: ';', Mod: tea.ModAlt})
		err = m.await(func(v *controlVisualModel) error {
			rows := v.LSPPopup.RefRows
			if len(rows) != 2 {
				return fmt.Errorf("want declaration and call, got %+v", rows)
			}
			seen := map[string]bool{}
			for _, row := range rows {
				seen[fmt.Sprintf("%s:%d:%d", filepath.Base(row.File), row.Line, row.Col)] = true
			}
			if !seen["lib.go:3:5"] || !seen["main.go:2:13"] {
				return fmt.Errorf("references disagree with independent source oracle: %+v", rows)
			}
			return paint(v, "References (2)", "lib.go:4:6", "main.go:3:14")
		})
		label = "Alt+; references lib.go:4:6 and main.go:3:14"
	case 5:
		m.action(func(app *App) tea.Cmd { _, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEsc}); return cmd })
		position(2, 14, tea.KeyPressMsg{Code: '.', Mod: tea.ModAlt})
		err = m.await(func(v *controlVisualModel) error {
			ed := v.EditorState.Editor
			if filepath.Base(ed.FilePath()) != "lib.go" || ed.Value() != lspVisualLib || ed.CursorLine() != 3 || ed.CursorCol() != 5 {
				return fmt.Errorf("definition did not navigate to literal source lib.go:4:6; path=%s cursor=%d:%d", ed.FilePath(), ed.CursorLine(), ed.CursorCol())
			}
			if v.LSPPopup.ReferencesID != "" || v.overlayHost.IsOpen(ui.OverlayID("lsp-hover")) {
				return fmt.Errorf("previous popup remained mounted")
			}
			return paint(v, "lib.go", "func Answer() int", "return 42")
		})
		label = "Alt+. cross-file definition at exact lib.go:4:6, source and cursor verified"
	case 6:
		if err = do("file.open", map[string]any{"path": m.path}); err != nil {
			return err
		}
		position(2, 16, tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
		err = m.await(func(v *controlVisualModel) error {
			if v.LSPPopup.CompletionID == "" {
				return fmt.Errorf("Ctrl+Space completion not mounted")
			}
			for _, item := range v.LSPPopup.CompletionItems {
				if strings.HasPrefix(item.Label, "Answer") && strings.Contains(item.Detail, "int") {
					return paint(v, "Completions", "Answer", "func() int")
				}
			}
			return fmt.Errorf("actual completion missing Answer() int: %+v", v.LSPPopup.CompletionItems)
		})
		label = "Ctrl+Space actual Answer completion and int signature; no insertion or disk mutation"
	}
	if err != nil {
		return err
	}
	return m.evidence(fmt.Sprintf("F%d VERIFIED %s", key, label))
}

func lspVisualProcesses(root, executable string) ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		proc := filepath.Join("/proc", entry.Name())
		exe, err := os.Readlink(filepath.Join(proc, "exe"))
		if err != nil || exe != executable {
			continue
		}
		if cwd, err := os.Readlink(filepath.Join(proc, "cwd")); err == nil && cwd == root {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

// Retain the former no-server/settings fixture under an honest separate name.
func TestLSPSettingsVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_LSP_SETTINGS") != "1" {
		t.Skip("dedicated-terminal settings-only visual acceptance")
	}
	app, _ := newRealShellApp(t)
	model := &lspSettingsVisualModel{App: app}
	p := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(p)
	if _, err := p.Run(); err != nil {
		t.Fatal(err)
	}
}

type lspSettingsVisualModel struct{ *App }

func (m *lspSettingsVisualModel) Init() tea.Cmd { return tickCmd() }
func (m *lspSettingsVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF6:
			m.openApplicationSettings()
			fw := m.overlayHost.Window(ui.OverlayWorkspaceSettings)
			if fw != nil {
				body := m.settingsSurface(fw)
				for i, category := range body.Model().Categories() {
					if category.Name == "Language Servers" {
						body.Model().SelectCategory(i)
						break
					}
				}
				body.SetModel(body.Model())
				if frame, ok := fw.Content.(*ui.WindowFrameWidget); ok {
					frame.Host().MarkLayout()
				}
				fw.MarkContentDirty()
			}
			return m, nil
		case tea.KeyF9:
			return m, tea.Quit
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
