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
	"github.com/charmbracelet/x/ansi"

	"crude/tui/editor"
	"crude/tui/screens/diffview"
	"crude/tui/ui"
	"crude/tui/window"
	"crude/workspace"
)

const fileFlowSource = "one\nalpha\ncontext 1\ncontext 2\ncontext 3\ncontext 4\nomega\nend\n"

// Run close, disk, git, icons and search sequentially through scripts/tuishot.sh.
// F4 observes only; decisions use ordinary keys and pointer input. F9 joins
// the connected shell and checks the resulting file, not just dialog state.
func TestEditorFileFlowVisualAcceptance(t *testing.T) {
	mode := os.Getenv("CRUDE_VISUAL_FILE_FLOW")
	if mode == "" {
		t.Skip("dedicated connected editor file-flow capture")
	}
	runEditorFileFlow(t, mode, false, 0, 0)
}

func TestEditorFileFlowHeadless(t *testing.T) {
	for _, mode := range []string{"close", "disk", "git", "icons", "search", "dense-search"} {
		for _, size := range [][2]int{{160, 40}, {80, 24}} {
			t.Run(fmt.Sprintf("%s/%dx%d", mode, size[0], size[1]), func(t *testing.T) {
				runEditorFileFlow(t, mode, true, size[0], size[1])
			})
		}
	}
}

type fileFlowVisualModel struct {
	*featureVisualModel
	mode          string
	ed            *editor.Editor
	phase         int
	left, right   window.GroupID
	first, moving window.TabID
}

type fileFlowObserved struct{ err error }

func runEditorFileFlow(t *testing.T, mode string, headless bool, width, height int) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "review.go")
	if mode == "icons" {
		path = filepath.Join(root, "a.go")
	}
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(fileFlowSource)
	if mode == "dense-search" {
		body := strings.Repeat("const inputDirection = isRTL ? rtlValue : ltrValue;\nApply direction at each screen root; descendants use logical start/end layout.\n", 64)
		for i := range 512 {
			target := filepath.Join(root, fmt.Sprintf("part%02d", i/32), fmt.Sprintf("source%03d.txt", i))
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				t.Fatal(err)
			}
			text := body
			if i == 511 {
				text += "direction is exact\nrareNeedle state\n"
			}
			if err := os.WriteFile(target, []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if mode == "git" {
		runGitFixture(t, root, "init")
		runGitFixture(t, root, "config", "user.name", "Crude Test")
		runGitFixture(t, root, "config", "user.email", "crude@example.test")
		runGitFixture(t, root, "checkout", "-B", "work")
		runGitFixture(t, root, "add", ".")
		runGitFixture(t, root, "commit", "-m", "base")
		runGitFixture(t, root, "checkout", "-b", "incoming")
		write(strings.NewReplacer("alpha", "THEIR ALPHA", "omega", "THEIR OMEGA").Replace(fileFlowSource))
		runGitFixture(t, root, "commit", "-am", "incoming")
		runGitFixture(t, root, "checkout", "work")
		write(strings.NewReplacer("alpha", "MY ALPHA", "omega", "MY OMEGA").Replace(fileFlowSource))
		runGitFixture(t, root, "commit", "-am", "current")
	}
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1", func(w *workspace.Workspace) {
		settings := w.AppDefaults()
		settings.LSP.Enabled = false
		settings.UI.UseNerdFont = true
		w.SetAppDefaults(settings)
		restrictFixture(t, w)
	})
	shell := newRemoteJourneyShell(t, runtime, root)
	app := shell.app
	app.Tree.Visible = false
	m := &fileFlowVisualModel{
		featureVisualModel: &featureVisualModel{
			controlVisualModel: &controlVisualModel{App: app, runtime: runtime, root: root, path: path, ctx: t.Context()},
			settled:            make(chan struct{}, 1),
		},
		mode: mode,
	}
	if !headless {
		m.resultPath = os.Getenv("CRUDE_FILE_FLOW_RESULT")
	}
	switch mode {
	case "close", "disk", "icons":
		m.ed = liveTestOpen(t, app, path)
		if mode == "icons" {
			m.left, m.first = app.Win.ActiveGroup(), app.Win.ActiveTab().ID
			for _, name := range []string{"b.md", "c.txt", "d.go"} {
				if err := os.WriteFile(filepath.Join(root, name), []byte("// "+name+" stays in its own pane.\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			liveTestOpen(t, app, filepath.Join(root, "b.md"))
			m.moving = app.Win.ActiveTab().ID
			liveTestOpen(t, app, filepath.Join(root, "c.txt"))
			app.activateEditorTab(path)
			app.activateEditorTab(filepath.Join(root, "b.md"))
			m.right = app.splitMainGroup(ui.Horizontal, false, 0)
			liveTestOpen(t, app, filepath.Join(root, "d.go"))
		} else {
			m.ed.SetCursorPosition(0, 3)
			m.ed.InsertRune('!')
			settleLive(t, app)
			if mode == "disk" {
				write(strings.Replace(fileFlowSource, "omega", "DISK OMEGA", 1))
			}
		}
	case "search":
		text := "inputDirection = isRTL\ndirection is exact\nApply direction at each screen root; descendants use logical start/end layout.\n"
		if err := os.WriteFile(filepath.Join(root, "search.txt"), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		m.ed = liveTestOpen(t, app, path)
	case "dense-search":
		m.ed = liveTestOpen(t, app, path)
	case "git":
		app.ws.EnsureGit()
		result, err := app.ws.Git().Merge(t.Context(), "incoming")
		if err != nil || len(result.Conflicts) != 1 {
			t.Fatalf("create real Git conflict: %+v %v", result, err)
		}
		readGitSnapshotNow(t, app)
		runAppCommand(t, app, app.openGitMergeDialog())
	default:
		t.Fatalf("unknown file-flow mode %q", mode)
	}
	ctx, cancel := context.WithCancel(t.Context())
	var workers sync.WaitGroup
	defer func() {
		cancel()
		workers.Wait()
		app.StopAsyncIO()
		if err := app.CloseWorkspaceAttachment(); err != nil {
			t.Error(err)
		}
		if err := runtime.Close(); err != nil {
			t.Error(err)
		}
	}()
	options := []tea.ProgramOption{tea.WithWidthMethod(ansi.GraphemeWidth)}
	if headless {
		options = append(options, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithWindowSize(width, height))
	}
	program := tea.NewProgram(m, options...)
	m.program = program
	app.SetProgram(program)
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
	if headless {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := m.await(func(v *controlVisualModel) error {
				if v.width != width || v.height != height {
					return fmt.Errorf("initial terminal size not applied")
				}
				return nil
			}); err != nil {
				m.program.Send(fileFlowObserved{err})
				return
			}
			m.driveFileFlow(ctx)
		}()
	}
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if m.failure != nil {
		t.Fatal(m.failure)
	}
	wantPhases := map[string]int{"close": 3, "disk": 5, "git": 3, "icons": 2, "search": 1, "dense-search": 6}[mode]
	if m.phase != wantPhases || app.rootHost.HasPointerCapture() {
		t.Fatalf("incomplete file journey: %s phase=%d, want %d", mode, m.phase, wantPhases)
	}
	want := fileFlowSource
	if mode == "disk" {
		want = strings.NewReplacer("one", "one!", "omega", "DISK OMEGA").Replace(fileFlowSource)
	}
	if mode == "git" {
		want = strings.NewReplacer("alpha", "MY ALPHA", "omega", "THEIR OMEGA").Replace(fileFlowSource)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != want {
		t.Fatalf("file outcome = %q %v, want %q", data, err, want)
	}
	if err := m.evidence("FILE_FLOW_SUCCESS " + mode); err != nil {
		t.Fatal(err)
	}
}

func (m *fileFlowVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case controlVisualCheck:
		v.reply <- v.check(m.controlVisualModel)
		return m, nil
	case fileFlowObserved:
		m.busy = false
		if v.err != nil {
			m.failure = v.err
			return m, tea.Quit
		}
		m.phase++
		select {
		case m.settled <- struct{}{}:
		default:
		}
		phase := m.phase
		return m, func() tea.Msg {
			return fileFlowEvidenceWritten{m.evidence(fmt.Sprintf("FILE_FLOW_READY phase=%d", phase))}
		}
	case fileFlowEvidenceWritten:
		if v.err != nil {
			m.failure = v.err
			return m, tea.Quit
		}
		return m, nil
	case tea.KeyPressMsg:
		if v.Code == tea.KeyF9 && !m.busy {
			return m, tea.Quit
		}
		if v.Code == tea.KeyF4 && !m.busy {
			m.busy = true
			return m, func() tea.Msg { return fileFlowObserved{m.observeFileFlow()} }
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}

type fileFlowEvidenceWritten struct{ err error }

func (m *fileFlowVisualModel) observeFileFlow() error {
	var frame string
	err := m.await(func(v *controlVisualModel) error {
		frame = ansi.Strip(v.View().RenderContent())
		require := func(markers ...string) error { return featureVisualPaint(v, markers...) }
		switch m.mode {
		case "close":
			switch m.phase {
			case 0:
				return require("Unsaved changes", "Save", "Discard", "Cancel")
			case 1:
				if v.DirtyPromptPath != "" || !m.ed.Modified() || m.ed.Value() != "one!\n"+strings.TrimPrefix(fileFlowSource, "one\n") {
					return fmt.Errorf("Cancel did not preserve the changed editor")
				}
			case 2:
				if v.EditorState.Tabs[m.path] != nil || v.DirtyPromptPath != "" {
					return fmt.Errorf("Discard did not close the editor")
				}
			}
		case "disk":
			switch m.phase {
			case 0:
				return require("File changed on disk", "Overwrite", "Discard", "Compare", "Cancel")
			case 1:
				if v.diskConflict != nil || !m.ed.Modified() {
					return fmt.Errorf("Cancel did not keep unsaved work")
				}
			case 2, 3:
				view := v.activeLocalDiff()
				if view == nil || len(view.screen.ComparisonReader().Choices()) != 2 {
					return fmt.Errorf("Compare did not load both changes")
				}
				if m.phase == 3 {
					choices := view.screen.ComparisonReader().Choices()
					if choices[0] != diffview.ChoiceRight || choices[1] != diffview.ChoiceLeft {
						return fmt.Errorf("native choices = %v", choices)
					}
				}
				return require("Accept left", "Accept right", "Accept both", "Previous", "Next", "[x]", "[ ]")
			case 4:
				want := strings.NewReplacer("one", "one!", "omega", "DISK OMEGA").Replace(fileFlowSource)
				if m.ed.Modified() || m.ed.Value() != want || m.ed.DocumentState().InFlight {
					return fmt.Errorf("chosen text not saved: %q %+v", m.ed.Value(), m.ed.DocumentState())
				}
			}
		case "git":
			dialog := v.gitMerge
			if dialog == nil {
				return fmt.Errorf("Git resolver closed")
			}
			if m.phase < 2 {
				choices := dialog.resolver.Choices()
				if len(choices) != 2 {
					return fmt.Errorf("expected two Git conflict regions: %v", choices)
				}
				if m.phase == 1 && (choices[0] != workspace.GitChooseCurrent || choices[1] != workspace.GitChooseIncoming) {
					return fmt.Errorf("Git choices = %v", choices)
				}
				return require("Resolve conflicts", "Accept left", "Accept right", "Accept both", "Previous", "Next", "Save")
			}
			if len(dialog.resolver.Conflicts()) != 0 {
				return fmt.Errorf("Git choices not saved and staged")
			}
			return require("Every conflict is resolved")
		case "icons":
			if len(v.Win.GroupIDs()) != 2 {
				return fmt.Errorf("expected two split panes")
			}
			if m.phase == 1 && (v.Win.GroupOf(m.moving) != m.right || v.Win.GroupActiveTab(m.left).ID != m.first || v.Win.ActiveTab().ID != m.moving) {
				return fmt.Errorf("moving a tab did not restore the source pane's most recently selected file")
			}
			for _, tab := range v.Win.Tabs() {
				if tab.Resource.ID == m.path || strings.HasSuffix(tab.Resource.ID, "d.go") {
					icon, _ := v.tabIcon(tab)
					if icon == "" || !strings.Contains(frame, icon) {
						return fmt.Errorf("file icon missing from mounted pane: %s", tab.Resource.ID)
					}
				}
			}
			return require("a.go", "d.go")
		case "search":
			search := v.findInFiles
			if search == nil || search.Query() != "direction is" || len(search.Results()) != 2 {
				return fmt.Errorf("project search did not settle to the exact match and the valid fuzzy suggestion")
			}
			results := search.Results()
			if selected := search.Selected(); selected < 0 || results[selected].Line != 2 || results[selected].FuzzyOnly {
				return fmt.Errorf("initial search selection is not the exact match")
			}
			exact, fuzzy := strings.Index(frame, "2: direction is exact"), strings.Index(frame, "1: ≈ inputDirection")
			if exact < 0 || fuzzy < exact {
				return fmt.Errorf("exact result must paint before the earlier fuzzy line")
			}
		case "dense-search":
			search := v.findInFiles
			if m.phase == 5 {
				ed := v.EditorState.Editor
				if search != nil || ed == nil || !strings.HasSuffix(filepath.ToSlash(ed.FilePath()), "/part15/source511.txt") || ed.CursorLine() != 129 {
					return fmt.Errorf("Open did not land on the current exact result")
				}
				return require("rareNeedle state")
			}
			if search == nil {
				return fmt.Errorf("dense search closed before Open")
			}
			results := search.Results()
			switch m.phase {
			case 0, 1:
				if search.Query() != "direction is" || len(results) != 1000 || results[0].FuzzyOnly || results[0].Line != 129 {
					return fmt.Errorf("dense search lost the late exact hit or its result limit")
				}
				if m.phase == 1 && (search.Selected() < 0 || !results[search.Selected()].FuzzyOnly) {
					return fmt.Errorf("Down did not move the selection to a discovery result")
				}
				if m.phase == 1 {
					return require("inputDirection", "1000")
				}
				return require("direction is exact", "1000")
			case 2, 4:
				if search.Query() != "rareNeedle state" || len(results) != 1 || results[0].Line != 130 || results[0].FuzzyOnly {
					return fmt.Errorf("query change did not replace the dense result set")
				}
				return require("rareNeedle state", "1 match")
			case 3:
				if search.Query() != "noSuchStateMachine" || len(results) != 0 || search.SyntaxEditor() != nil {
					return fmt.Errorf("absent query retained old results or preview")
				}
				return require("No matches")
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("%s phase %d: %w\n%s", m.mode, m.phase, err, frame)
	}
	return m.evidence(frame)
}

func (m *fileFlowVisualModel) driveFileFlow(ctx context.Context) {
	key := func(code rune, mod tea.KeyMod) { m.program.Send(tea.KeyPressMsg{Code: code, Mod: mod}) }
	text := func(value string) { m.program.Send(tea.KeyPressMsg{Code: rune(value[0]), Text: value}) }
	observe := func() bool {
		key(tea.KeyF4, 0)
		select {
		case <-m.settled:
			return true
		case <-ctx.Done():
			return false
		}
	}
	switch m.mode {
	case "close":
		key('w', tea.ModCtrl)
		if !observe() {
			return
		}
		key(tea.KeyEsc, 0)
		if !observe() {
			return
		}
		key('w', tea.ModCtrl)
		text("d")
	case "disk":
		key('s', tea.ModCtrl)
		if !observe() {
			return
		}
		key(tea.KeyEsc, 0)
		if !observe() {
			return
		}
		key('s', tea.ModCtrl)
		key(tea.KeyLeft, 0)
		key(tea.KeyEnter, 0)
		if !observe() {
			return
		}
		text("n")
		text("1")
		if !observe() {
			return
		}
		key('s', tea.ModCtrl)
	case "icons":
		if !observe() {
			return
		}
		var fromX, fromY, toX, toY int
		if err := m.check(func(v *controlVisualModel) error {
			v.View()
			group := v.mainPanes.groups[m.left]
			r, _ := v.screenRectOf(group.tabs.TabsControl().ID())
			index := -1
			for i, tab := range v.Win.GroupTabs(m.left) {
				if tab.ID == m.moving {
					index = i
				}
			}
			for x := 0; x < r.W; x++ {
				if hit, closeHit := group.tabs.TabsControl().HitTest(x); hit == index && !closeHit {
					fromX, fromY = r.X+x, r.Y
					body := v.mainPanes.groups[m.right].body
					toX, toY = body.X+body.W/2, body.Y+body.H/2
					return nil
				}
			}
			return fmt.Errorf("moving tab has no visible hit target")
		}); err != nil {
			m.program.Send(fileFlowObserved{err})
			return
		}
		m.program.Send(tea.MouseClickMsg{X: fromX, Y: fromY, Button: tea.MouseLeft})
		m.program.Send(tea.MouseMotionMsg{X: toX, Y: toY, Button: tea.MouseLeft})
		m.program.Send(tea.MouseReleaseMsg{X: toX, Y: toY, Button: tea.MouseLeft})
	case "search":
		key('f', tea.ModCtrl|tea.ModAlt)
		text("direction is")
	case "dense-search":
		key('f', tea.ModCtrl|tea.ModAlt)
		text("direction is")
		if !observe() {
			return
		}
		key(tea.KeyDown, 0)
		if !observe() {
			return
		}
		for _, query := range []string{"rareNeedle state", "noSuchStateMachine", "rareNeedle state"} {
			key('f', tea.ModCtrl|tea.ModAlt)
			text(query)
			if !observe() {
				return
			}
		}
		key(tea.KeyEnter, 0)
	case "git":
		if !observe() {
			return
		}
		text("1")
		text("n")
		text("2")
		if !observe() {
			return
		}
		key('s', tea.ModCtrl)
	}
	if observe() {
		key(tea.KeyF9, 0)
	}
}
