package viewmodel

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/localhistory"
	"crude/shellport"
	"crude/tui/rendering"
	"crude/tui/screens/perfdash"
	"crude/tui/screens/shellfooter"
	"crude/tui/screens/termdock"
	"crude/types"
	"crude/workspace"
)

// Capture sequentially with scripts/tuishot.sh and this prebuilt test binary:
// CRUDE_FEATURE_VISUAL=1 CRUDE_ARGS=-test.run=^TestFeatureVisualAcceptance$
// F2 x3: Git changes/review/history; F3 x2: tree+file/project search;
// F4 x4: workspace/Problems/Notices/Board; F5 x3: Performance overview/core/resources;
// F6: real capabilities snapshot (no activation); F7 x2: history/restore preview;
// F8: saved workspace settings; quit:F9 verifies every phase and exits cleanly.
// CRUDE_FEATURE_VISUAL_RESULT receives per-phase proof and a final success line.
// This local framed-core fixture is not SSH/QUIC, Windows or full-matrix evidence.
func TestFeatureVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_FEATURE_VISUAL") != "1" {
		t.Skip("dedicated sequential connected feature capture")
	}
	runFeatureVisual(t, false, 0, 0)
}

func TestFeatureVisualHarnessHeadless(t *testing.T) {
	for _, size := range [][2]int{{160, 45}, {60, 20}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			runFeatureVisual(t, true, size[0], size[1])
		})
	}
}

var featureVisualPhases = map[int]int{2: 3, 3: 2, 4: 4, 5: 3, 6: 1, 7: 2, 8: 1}

func runFeatureVisual(t *testing.T, headless bool, width, height int) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CRUDE_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := newGitRepoFixture(t)
	path := filepath.Join(root, "tracked.txt")
	seed := workspace.NewWorkspace(root, "", "tester", types.Settings{}, nil)
	seed.Status.Notify(types.NotificationRecord{ID: "feature-notice", Summary: "Saved feature notice", Detail: "Read from the retained workspace status owner.", Source: "acceptance"})
	seed.Close()
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	shell := newRemoteJourneyShell(t, runtime, root)
	_, _, err := shell.app.ws.LocalHistory().SnapshotIfChanged(t.Context(), localhistory.SnapshotOptions{Kind: "manual", Label: "Before feature edit"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("FEATURE NEEDLE\n\nConnected file and project-search evidence.\nNo production files are changed.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m := &featureVisualModel{controlVisualModel: &controlVisualModel{App: shell.app, runtime: runtime, root: root, path: path, completed: map[int]bool{}, ctx: t.Context()}, phases: map[int]int{}, settled: make(chan struct{}, 1)}
	if !headless {
		m.resultPath = os.Getenv("CRUDE_FEATURE_VISUAL_RESULT")
	}
	t.Cleanup(func() {
		if m.client != nil {
			if err := m.client.Close(); err != nil {
				t.Error(err)
			}
		}
	})
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
			for key := 2; key <= 8; key++ {
				for phase := 0; phase < featureVisualPhases[key]; phase++ {
					program.Send(tea.KeyPressMsg{Code: tea.KeyF2 + rune(key-2)})
					select {
					case <-m.settled:
					case <-t.Context().Done():
						return
					}
				}
			}
			program.Send(tea.KeyPressMsg{Code: tea.KeyF9})
		}()
	}
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	stopPump()
	if m.client != nil {
		if err := m.client.Close(); err != nil {
			t.Error(err)
		}
		m.client = nil
	}
	m.StopAsyncIO()
	if err := m.CloseWorkspaceAttachment(); err != nil {
		t.Error(err)
	}
	if m.failure != nil {
		_ = m.evidence("FEATURE_VISUAL_FAILED " + m.failure.Error())
		t.Fatal(m.failure)
	}
	for key, phases := range featureVisualPhases {
		if m.phases[key] != phases {
			t.Errorf("F%d completed %d/%d phases", key, m.phases[key], phases)
		}
	}
	if m.rootHost.HasPointerCapture() {
		t.Error("pointer capture remained armed")
	}
	if !t.Failed() {
		if err := m.evidence("FEATURE_VISUAL_SUCCESS all 16 phases; local framed core; no external inference, capability activation or history restore"); err != nil {
			t.Fatal(err)
		}
	}
}

type featureVisualAction struct{ run func(*App) tea.Cmd }
type featureVisualDone struct {
	key, phase int
	err        error
}
type featureVisualModel struct {
	*controlVisualModel
	phases     map[int]int
	resultPath string
	settled    chan struct{}
}

func (m *featureVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case controlVisualCheck:
		v.reply <- v.check(m.controlVisualModel)
		return m, nil
	case featureVisualAction:
		return m, v.run(m.App)
	case featureVisualDone:
		m.busy = false
		if v.err != nil {
			m.failure = fmt.Errorf("F%d phase %d: %w", v.key, v.phase, v.err)
			return m, tea.Quit
		}
		m.phases[v.key] = v.phase
		select {
		case m.settled <- struct{}{}:
		default:
		}
		return m, nil
	case tea.KeyPressMsg:
		if v.Code == tea.KeyF9 {
			if !m.busy {
				return m, tea.Quit
			}
			return m, nil
		}
		if v.Code >= tea.KeyF2 && v.Code <= tea.KeyF8 {
			key := int(v.Code-tea.KeyF2) + 2
			phase := m.phases[key] + 1
			if m.busy || phase > featureVisualPhases[key] {
				return m, nil
			}
			m.busy = true
			return m, func() tea.Msg { return featureVisualDone{key, phase, m.journeyFeature(key, phase)} }
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}

func (m *featureVisualModel) evidence(line string) error {
	if m.resultPath == "" {
		return nil
	}
	f, err := os.OpenFile(m.resultPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := fmt.Fprintln(f, line)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

// All mutations and reads of App state run on the actual Program's Update lane.
// Await only observes asynchronous product work; it never replays an operation.
func (m *featureVisualModel) action(run func(*App) tea.Cmd) {
	m.program.Send(featureVisualAction{run})
}
func (m *featureVisualModel) await(check func(*controlVisualModel) error) error {
	ctx, cancel := context.WithTimeout(m.ctx, 8*time.Second)
	defer cancel()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	var last error
	for {
		last = m.check(check)
		if last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return last
		case <-tick.C:
		}
	}
}
func featureVisualPaint(m *controlVisualModel, markers ...string) error {
	paint := ansi.Strip(m.View().RenderContent())
	for _, marker := range markers {
		if !strings.Contains(paint, marker) {
			return fmt.Errorf("%dx%d frame missing %q:\n%s", m.width, m.height, marker, paint)
		}
	}
	return nil
}

func (m *featureVisualModel) journeyFeature(key, phase int) error {
	if err := m.connect(); err != nil {
		return err
	}
	// Escape is the real product back path for previous non-destructive overlays.
	if phase == 1 {
		m.action(func(app *App) tea.Cmd {
			_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
			return cmd
		})
	}
	do := func(id string, input any) error { _, err := m.execute(id, input, nil); return err }
	var err error
	label := ""
	switch key {
	case 2:
		label = []string{"Git changes tracked.txt", "Git review FEATURE NEEDLE", "Git history initial"}[phase-1]
		switch phase {
		case 1:
			if err = do("tree.set_visible", map[string]any{"visible": false}); err != nil {
				return err
			}
			m.action(func(app *App) tea.Cmd { return app.executePaletteCommand("view.activity.git") })
			err = m.await(func(v *controlVisualModel) error { return featureVisualPaint(v, "tracked.txt") })
		case 2:
			m.action(func(app *App) tea.Cmd {
				app.hideWorkspacePanel()
				return app.executePaletteCommand("git.review")
			})
			err = m.await(func(v *controlVisualModel) error { return featureVisualPaint(v, "FEATURE NEEDLE") })
		case 3:
			m.action(func(app *App) tea.Cmd { return app.executePaletteCommand("git.history") })
			err = m.await(func(v *controlVisualModel) error { return featureVisualPaint(v, "initial") })
		}
	case 3:
		if phase == 1 {
			label = "file tree tracked.txt and connected editor FEATURE NEEDLE"
			m.action(func(app *App) tea.Cmd { app.hideWorkspacePanel(); return nil })
			if err = do("file.open", map[string]any{"path": m.path}); err != nil {
				return err
			}
			if err = do("tree.set_visible", map[string]any{"visible": true}); err != nil {
				return err
			}
			err = m.await(func(v *controlVisualModel) error {
				if v.Tree.Model == nil || v.Tree.Model.NodeByPath(m.path) == nil {
					return fmt.Errorf("real client file tree has no tracked file")
				}
				if v.EditorState.Editor == nil || !strings.HasPrefix(v.EditorState.Editor.Value(), "FEATURE NEEDLE\n") {
					return fmt.Errorf("connected editor content is not the tracked file")
				}
				// At 60 columns the tree leaves a deliberately clipped editor line.
				return featureVisualPaint(v, "tracked.txt", "FEATURE")
			})
		} else {
			label = "project search NEEDLE result from core"
			m.action(func(app *App) tea.Cmd { _, cmd := app.toggleFindInFiles(); return cmd })
			m.action(func(app *App) tea.Cmd { _, cmd := app.Update(tea.PasteMsg{Content: "NEEDLE"}); return cmd })
			err = m.await(func(v *controlVisualModel) error {
				if v.findInFiles == nil || len(v.findInFiles.Results()) != 1 {
					return fmt.Errorf("project search did not return one real file result")
				}
				if v.findInFiles.Results()[0].Path != m.path {
					return fmt.Errorf("project search returned the wrong file")
				}
				return featureVisualPaint(v, "Find in Files", "1 match in 1 file", "FEATURE NEEDLE")
			})
		}
	case 4:
		panel := []string{termdock.PanelWorkspace, termdock.PanelProblems, termdock.PanelNotices, termdock.PanelBoard}[phase-1]
		label = "retained panel " + panel
		if err = do("tree.set_visible", map[string]any{"visible": false}); err != nil {
			return err
		}
		if err = do("panel.show", map[string]any{"id": panel}); err != nil {
			return err
		}
		err = m.await(func(v *controlVisualModel) error {
			if !v.panelShown(panel) {
				return fmt.Errorf("panel %s not mounted", panel)
			}
			if phase == 2 {
				return featureVisualPaint(v, "Workspace health")
			}
			if phase == 3 {
				return featureVisualPaint(v, "Saved feature notice")
			}
			if phase == 4 {
				if v.boardBrowser == nil {
					return fmt.Errorf("Board missing")
				}
				return featureVisualPaint(v, "Board")
			}
			return featureVisualPaint(v, "Workspace")
		})
	case 5:
		sections := []string{perfdash.SectionOverview, perfdash.SectionCore, perfdash.SectionResources}
		label = "Performance " + sections[phase-1] + " real core metrics"
		if phase == 1 {
			m.action(func(app *App) tea.Cmd {
				_, cmd := app.Update(shellfooter.ActionMsg{Action: "performance"})
				return cmd
			})
		} else {
			m.action(func(app *App) tea.Cmd {
				app.View()
				r, ok := app.rootHost.ScreenRect(perfdash.TabsID)
				if !ok || r.Empty() {
					return nil
				}
				row := rowText(app, r.Y)
				// The shared Tabs chrome truncates Resources to Resourc… at
				// 60 columns; click its visible label, not an assumed width.
				index := strings.Index(row, []string{"Overview", "Core", "Resour"}[phase-1])
				if index < 0 {
					return nil
				}
				_, cmd := app.Update(tea.MouseClickMsg{X: rendering.VisibleWidth(row[:index]), Y: r.Y, Button: tea.MouseLeft})
				return cmd
			})
		}
		err = m.await(func(v *controlVisualModel) error {
			if v.perf.screen == nil || v.perf.inFlight || !strings.Contains(v.perf.screen.Report(), fmt.Sprintf("Core process (pid %d)", os.Getpid())) {
				return fmt.Errorf("framed core.metrics snapshot not installed")
			}
			if v.perf.screen.Section() != sections[phase-1] {
				return fmt.Errorf("Performance pointer did not select %s (current=%s focus=%s):\n%s", sections[phase-1], v.perf.screen.Section(), v.rootHost.Focused(), ansi.Strip(v.View().RenderContent()))
			}
			return featureVisualPaint(v, []string{"Memory", "Workspace actor step", "Crude memory"}[phase-1])
		})
	case 6:
		label = "Skills real capability snapshot; no activation"
		m.action(func(app *App) tea.Cmd { app.hideWorkspacePanel(); return app.openCapabilityPicker() })
		err = m.await(func(v *controlVisualModel) error {
			if v.capabilityPicker == nil || v.capabilitySnapshot.Generation == 0 || v.capabilitySnapshot.Error != "" {
				return fmt.Errorf("real capability snapshot not installed")
			}
			return featureVisualPaint(v, "Skills")
		})
	case 7:
		if phase == 1 {
			label = "Local History real immutable snapshot timeline"
			m.action(func(app *App) tea.Cmd { return app.executePaletteCommand("history.open") })
			err = m.await(func(v *controlVisualModel) error { return featureVisualPaint(v, "Before feature edit") })
		} else {
			label = "Local History restore preview tracked.txt; not applied"
			m.action(func(app *App) tea.Cmd {
				_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				return cmd
			})
			err = m.await(func(v *controlVisualModel) error { return featureVisualPaint(v, "Restore preview", "tracked.txt") })
		}
	case 8:
		label = "workspace settings persisted tab_size=8"
		// History preview Esc goes back before the next Esc closes its dialog.
		m.action(func(app *App) tea.Cmd { _, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEsc}); return cmd })
		err = m.journey(4)
	}
	if err != nil {
		return err
	}
	// Every read-only journey must leave the actual tracked disk bytes intact.
	bytes, err := os.ReadFile(m.path)
	if err != nil || !strings.HasPrefix(string(bytes), "FEATURE NEEDLE\n") {
		return fmt.Errorf("read-only journey changed tracked file: %v", err)
	}
	return m.evidence(fmt.Sprintf("F%d.%d VERIFIED %s", key, phase, label))
}
