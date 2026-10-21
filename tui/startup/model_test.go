package startup

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/coreprotocol"
	"crude/third_party/pathwalk"
	"crude/tui/cell"
	"crude/tui/screens/filepicker"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

type fakeStartup struct {
	browse  func(context.Context, coreprotocol.StartupBrowseRequest) (coreprotocol.StartupBrowseResult, error)
	catalog func(context.Context, string, bool) (workspace.BrowserCatalog, error)
	inspect func(context.Context, string) (coreprotocol.StartupSnapshot, error)
	save    func(context.Context, []types.SettingEdit) (types.OnboardingSettings, error)
}

func (f *fakeStartup) Browse(ctx context.Context, r coreprotocol.StartupBrowseRequest) (coreprotocol.StartupBrowseResult, error) {
	if f.browse != nil {
		return f.browse(ctx, r)
	}
	return coreprotocol.StartupBrowseResult{Path: r.Path, NextOffset: -1, Entries: []coreprotocol.ClientDirectoryEntry{{Name: "project", Path: r.Path + "/project", IsDir: true}}}, nil
}
func (f *fakeStartup) Catalog(ctx context.Context, root string, hidden bool) (workspace.BrowserCatalog, error) {
	if f.catalog != nil {
		return f.catalog(ctx, root, hidden)
	}
	return workspace.BrowserCatalog{}, nil
}
func (f *fakeStartup) Inspect(ctx context.Context, root string) (coreprotocol.StartupSnapshot, error) {
	if f.inspect != nil {
		return f.inspect(ctx, root)
	}
	return coreprotocol.StartupSnapshot{Root: root}, nil
}
func (f *fakeStartup) Save(ctx context.Context, edits []types.SettingEdit) (types.OnboardingSettings, error) {
	if f.save != nil {
		return f.save(ctx, edits)
	}
	return types.OnboardingSettings{IntroductionCompleted: true}, nil
}

func fixture(t *testing.T, f *fakeStartup, intro bool) *Model {
	t.Helper()
	m := NewModel(context.Background(), f, coreprotocol.StartupSnapshot{Root: "/home/demo", FolderKind: "home", ShowIntroduction: intro})
	t.Cleanup(m.Close)
	return m
}
func key(code rune) tea.KeyPressMsg  { return tea.KeyPressMsg{Code: code} }
func ctrl(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code, Mod: tea.ModCtrl} }
func drive(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	switch v := msg.(type) {
	case tea.BatchMsg:
		for _, child := range v {
			drive(t, m, child)
		}
	case tea.QuitMsg:
		return
	default:
		_, next := m.Update(msg)
		drive(t, m, next)
	}
}
func send(t *testing.T, m *Model, msg tea.Msg) { t.Helper(); _, cmd := m.Update(msg); drive(t, m, cmd) }
func paintText(m *Model) string {
	_ = m.View()
	var b strings.Builder
	for y := 0; y < m.surf.Buffer().Height(); y++ {
		for _, c := range m.surf.Buffer().Row(y) {
			if c.Width > 0 {
				if c.Char == 0 {
					b.WriteRune(' ')
				} else {
					b.WriteRune(c.Char)
				}
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}
func click(t *testing.T, m *Model, id ui.ComponentID) {
	t.Helper()
	m.layout()
	r, ok := m.host.ScreenRect(id)
	if !ok {
		t.Fatalf("unmounted %s", id)
	}
	send(t, m, tea.MouseClickMsg{X: r.X, Y: r.Y, Button: tea.MouseLeft})
	send(t, m, tea.MouseReleaseMsg{X: r.X, Y: r.Y, Button: tea.MouseLeft})
}

func TestNativePaintThemesAndSizes(t *testing.T) {
	for _, preset := range []string{"dark", "light"} {
		for _, size := range [][2]int{{160, 40}, {80, 24}} {
			t.Run(fmt.Sprint(preset, size), func(t *testing.T) {
				m := fixture(t, &fakeStartup{}, true)
				m.initial.Theme.Preset = preset
				m.host.SetTheme(ui.ThemeFromPreset(preset))
				send(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				text := paintText(m)
				for _, label := range []string{"Welcome to Crude", "Edit project files.", "Compare changes with Git.", "Run commands in built-in terminals.", "Use AI agents, skills, and MCP tools.", "Let's go!"} {
					if !strings.Contains(text, label) {
						t.Fatalf("missing %q\n%s", label, text)
					}
				}
				if strings.Contains(text, "Previous workspaces") || strings.Contains(text, "New Session") {
					t.Fatal("IDE or selection content leaked into introduction")
				}
				found := false
				for _, c := range m.surf.Buffer().Cells() {
					if c.Char == '█' && c.FG == m.host.Tokens().Color(ui.ColorAccent) {
						found = true
					}
				}
				if !found {
					t.Fatal("heading lacks native theme accent")
				}
				send(t, m, key(tea.KeyEnter))
				text = paintText(m)
				for _, label := range []string{"OPEN WORKSPACE", "Search folders", "More folders", "Choose a project folder"} {
					if !strings.Contains(text, label) {
						t.Fatalf("missing picker %q\n%s", label, text)
					}
				}
				send(t, m, key(tea.KeyEsc))
				text = paintText(m)
				for _, label := range []string{"Previous workspaces", "Open Workspace", "Quit", "/home/demo", "home folder"} {
					if !strings.Contains(text, label) {
						t.Fatalf("missing selection %q\n%s", label, text)
					}
				}
				if strings.Contains(text, "OPEN WORKSPACE") || strings.Contains(text, "Welcome to Crude") {
					t.Fatal("closed content retained")
				}
			})
		}
	}
}

func TestSelectionEmptyStateAndUnfocusedQuitRemainReadable(t *testing.T) {
	for _, preset := range []string{"dark", "light"} {
		for _, size := range [][2]int{{160, 40}, {80, 24}} {
			t.Run(fmt.Sprint(preset, size), func(t *testing.T) {
				m := fixture(t, &fakeStartup{}, false)
				m.host.SetTheme(ui.ThemeFromPreset(preset))
				send(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				if text := paintText(m); !strings.Contains(text, "No previous workspaces. Open a project folder to begin.") {
					t.Fatalf("empty previous-project list erased its guidance:\n%s", text)
				}
				r, ok := m.host.ScreenRect(quitID)
				if !ok || m.host.Focused() == quitID {
					t.Fatal("Quit is not mounted as an unfocused action")
				}
				letter := m.surf.Buffer().Get(r.X+2, r.Y)
				if letter.Char != 'Q' || cell.ContrastRatio(letter.FG, letter.BG) < 4.5 {
					t.Fatal("Quit exists in text output but cannot be read on its surface")
				}
				click(t, m, quitID)
				if !m.closed {
					t.Fatal("visible Quit did not close startup")
				}
			})
		}
	}
}
func TestIntroductionMouseAndAsyncSaveFailure(t *testing.T) {
	var saved []types.SettingEdit
	f := &fakeStartup{save: func(_ context.Context, e []types.SettingEdit) (types.OnboardingSettings, error) {
		saved = e
		return types.OnboardingSettings{}, errors.New("read-only settings")
	}}
	m := fixture(t, f, true)
	click(t, m, goID)
	if m.root.mode != picking || !m.picker.IsOpen() {
		t.Fatal("Let's go did not open native folder picker directly")
	}
	if len(saved) != 1 || strings.Join(saved[0].Path, "/") != "onboarding/introduction_completed" || string(saved[0].After) != "true" {
		t.Fatalf("wrong preference edit: %+v", saved)
	}
	if !strings.Contains(paintText(m), "read-only settings") {
		t.Fatal("save failure is not visible")
	}
	send(t, m, ctrl('o'))
	if m.Result() != "/home/demo" {
		t.Fatalf("save failure blocked folder selection: %q", m.Result())
	}
}
func TestIntroductionSaveDoesNotBlockUpdate(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	f := &fakeStartup{save: func(ctx context.Context, _ []types.SettingEdit) (types.OnboardingSettings, error) {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return types.OnboardingSettings{}, ctx.Err()
	}}
	m := fixture(t, f, true)
	_, cmd := m.Update(key(tea.KeyEnter))
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatal("save and browse must be independent commands")
	}
	done := make(chan struct{})
	go func() { _ = batch[1](); close(done) }()
	<-started
	if m.root.mode != picking {
		t.Fatal("picker waits on save")
	}
	drive(t, m, batch[0])
	if m.picker.AsyncDirectoryLoading() {
		t.Fatal("browse waits on save")
	}
	close(release)
	<-done
}
func TestSearchEscClearCancelsAndRejectsLateResult(t *testing.T) {
	var catalogRoot string
	f := &fakeStartup{catalog: func(_ context.Context, root string, _ bool) (workspace.BrowserCatalog, error) {
		catalogRoot = root
		return workspace.BrowserCatalog{Entries: []pathwalk.Entry{{Name: "nested", Relative: "nested", Path: root + "/nested", IsDir: true}}}, nil
	}}
	m := fixture(t, f, false)
	send(t, m, key(tea.KeyEnter))
	send(t, m, ctrl('l'))
	send(t, m, ctrl('a'))
	send(t, m, tea.PasteMsg{Content: "/elsewhere"})
	send(t, m, key(tea.KeyEnter))
	m.picker.FocusFilter()
	_, intentCmd := m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	_, searchCmd := m.Update(intentCmd())
	late := searchCmd()
	if catalogRoot != "/elsewhere" {
		t.Fatalf("catalog used %q, not displayed root", catalogRoot)
	}
	send(t, m, key(tea.KeyEsc))
	if m.root.mode != picking {
		t.Fatal("first Esc should clear search")
	}
	send(t, m, late)
	if strings.Contains(strings.Join(m.picker.VisiblePaths(), " "), "nested") {
		t.Fatal("late search resurrected cleared query")
	}
	send(t, m, key(tea.KeyEsc))
	if m.root.mode != selection {
		t.Fatal("second Esc should cancel picker")
	}
}
func TestBrowseFailuresStaleAndReopen(t *testing.T) {
	f := &fakeStartup{browse: func(_ context.Context, r coreprotocol.StartupBrowseRequest) (coreprotocol.StartupBrowseResult, error) {
		if r.Path == "/bad" {
			return coreprotocol.StartupBrowseResult{}, errors.New("permission denied")
		}
		return coreprotocol.StartupBrowseResult{Path: r.Path, NextOffset: -1, Entries: []coreprotocol.ClientDirectoryEntry{{Name: "project", Path: r.Path + "/project", IsDir: true}}}, nil
	}}
	m := fixture(t, f, false)
	send(t, m, key(tea.KeyEnter))
	old := strings.Join(m.picker.VisiblePaths(), " ")
	intent := filepicker.IntentToken{Epoch: m.picker.OpenEpoch(), Sequence: m.picker.IntentSequence()}
	drive(t, m, m.browse("/bad", 0, false, intent))
	if got := strings.Join(m.picker.VisiblePaths(), " "); got != old {
		t.Fatalf("failed read discarded usable rows: %q != %q", got, old)
	}
	if !strings.Contains(paintText(m), "permission denied") {
		t.Fatal("directory failure absent")
	}
	cmd := m.browse("/stale", 0, false, intent)
	late := cmd()
	send(t, m, key(tea.KeyEsc))
	send(t, m, key(tea.KeyEnter))
	send(t, m, late)
	if m.picker.Current() != "/home/demo" {
		t.Fatal("closed/reopened picker accepted stale read")
	}
}
func TestInspectRejectsTopLevelAndPathErrors(t *testing.T) {
	for _, kind := range []string{"root", "home", "personal", "error"} {
		t.Run(kind, func(t *testing.T) {
			f := &fakeStartup{inspect: func(_ context.Context, root string) (coreprotocol.StartupSnapshot, error) {
				v := coreprotocol.StartupSnapshot{Root: root, FolderKind: kind}
				if kind == "error" {
					v.PathError = "folder vanished"
				}
				return v, nil
			}}
			m := fixture(t, f, false)
			send(t, m, key(tea.KeyEnter))
			send(t, m, ctrl('o'))
			if m.Result() != "" || m.closed || m.root.mode != picking {
				t.Fatal("non-project path selected")
			}
			text := paintText(m)
			label := "project folder"
			if kind == "error" {
				label = "folder vanished"
			}
			if !strings.Contains(text, label) {
				t.Fatalf("rejection not clear: %s", text)
			}
		})
	}
}
func TestProjectSelectionInspectsExactPath(t *testing.T) {
	var inspected string
	f := &fakeStartup{inspect: func(_ context.Context, root string) (coreprotocol.StartupSnapshot, error) {
		inspected = root
		return coreprotocol.StartupSnapshot{Root: "/canonical/project"}, nil
	}}
	m := fixture(t, f, false)
	send(t, m, key(tea.KeyEnter))
	send(t, m, ctrl('o'))
	if inspected != "/home/demo" || m.Result() != "/canonical/project" || !m.closed {
		t.Fatalf("handoff not inspected: %q %q", inspected, m.Result())
	}
}
func TestPreviousWorkspaceNativeListKeyboard(t *testing.T) {
	f := &fakeStartup{}
	m := NewModel(context.Background(), f, coreprotocol.StartupSnapshot{Root: "/home/demo", FolderKind: "home", Workspaces: []workspace.WorkspaceInfo{{ProjectName: "Previous project", ProjectDir: "/previous/project"}}})
	t.Cleanup(m.Close)
	if !strings.Contains(paintText(m), "Previous project") {
		t.Fatal("native recent row absent")
	}
	send(t, m, key(tea.KeyTab))
	send(t, m, key(tea.KeyEnter))
	if m.Result() != "/previous/project" {
		t.Fatalf("list failed: %q", m.Result())
	}
}
func TestPagedFolderMoreRetainsRowsAndBounds(t *testing.T) {
	f := &fakeStartup{browse: func(_ context.Context, r coreprotocol.StartupBrowseRequest) (coreprotocol.StartupBrowseResult, error) {
		page := coreprotocol.StartupBrowseResult{Path: r.Path, NextOffset: r.Offset + 512, Truncated: true}
		for i := 0; i < 512; i++ {
			name := fmt.Sprintf("folder-%04d", r.Offset+i)
			page.Entries = append(page.Entries, coreprotocol.ClientDirectoryEntry{Name: name, Path: r.Path + "/" + name, IsDir: true})
		}
		return page, nil
	}}
	m := fixture(t, f, false)
	send(t, m, key(tea.KeyEnter))
	click(t, m, moreID)
	paths := strings.Join(m.picker.VisiblePaths(), " ")
	if !strings.Contains(paths, "folder-0000") || !strings.Contains(paths, "folder-1023") {
		t.Fatal("More replaced instead of extending folder page")
	}
	for i := 0; i < 7; i++ {
		click(t, m, moreID)
	}
	if len(m.pageEntries) > retainedEntryLimit || m.nextOffset != -1 || !strings.Contains(paintText(m), "display limit") {
		t.Fatalf("paging retention unbounded or silent: %d %d", len(m.pageEntries), m.nextOffset)
	}
}
func TestCloseCancelsAndJoinsStartedReads(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})
	var once sync.Once
	f := &fakeStartup{browse: func(ctx context.Context, _ coreprotocol.StartupBrowseRequest) (coreprotocol.StartupBrowseResult, error) {
		once.Do(func() { close(started) })
		<-ctx.Done()
		defer close(finished)
		return coreprotocol.StartupBrowseResult{}, ctx.Err()
	}}
	m := fixture(t, f, false)
	_, cmd := m.Update(key(tea.KeyEnter))
	done := make(chan struct{})
	go func() { _ = cmd(); close(done) }()
	<-started
	m.Close()
	select {
	case <-finished:
	default:
		t.Fatal("Close returned before read joined")
	}
	<-done
	if cmd() != nil {
		t.Fatal("queued command ran after shutdown")
	}
}
func TestEscIntroductionQuitsAndCtrlCDoesNot(t *testing.T) {
	m := fixture(t, &fakeStartup{}, true)
	send(t, m, ctrl('c'))
	if m.closed {
		t.Fatal("Ctrl+C used as quit")
	}
	send(t, m, key(tea.KeyEsc))
	if !m.closed || m.Result() != "" {
		t.Fatal("Esc must quit introduction")
	}
}

// Keep a fixed latency guard on Update itself, not the asynchronous service.
func TestUpdateDoesNotRunServiceIO(t *testing.T) {
	f := &fakeStartup{browse: func(context.Context, coreprotocol.StartupBrowseRequest) (coreprotocol.StartupBrowseResult, error) {
		t.Fatal("service I/O ran in Update")
		return coreprotocol.StartupBrowseResult{}, nil
	}}
	m := fixture(t, f, false)
	start := time.Now()
	_, cmd := m.Update(key(tea.KeyEnter))
	if cmd == nil || time.Since(start) > time.Second {
		t.Fatal("opening picker blocked update")
	}
}

func TestNativeFolderTreeActivationAndPathFieldMouse(t *testing.T) {
	var inspected string
	f := &fakeStartup{inspect: func(_ context.Context, path string) (coreprotocol.StartupSnapshot, error) {
		inspected = path
		return coreprotocol.StartupSnapshot{Root: path}, nil
	}}
	m := fixture(t, f, false)
	click(t, m, openID)
	// The actual mounted path TextField owns mouse focus, paste and submit.
	click(t, m, pathID)
	send(t, m, ctrl('a'))
	send(t, m, tea.PasteMsg{Content: "/projects"})
	send(t, m, key(tea.KeyEnter))
	if m.picker.Current() != "/projects" {
		t.Fatalf("path entry did not navigate: %q", m.picker.Current())
	}
	m.picker.FocusFilter()
	send(t, m, key(tea.KeyDown))
	send(t, m, key(' '))
	if inspected != "/projects/project" || m.Result() != inspected {
		t.Fatalf("native tree selection did not inspect exact folder: %q %q", inspected, m.Result())
	}
}

func TestSearchFailureAndSupersededBrowse(t *testing.T) {
	f := &fakeStartup{catalog: func(context.Context, string, bool) (workspace.BrowserCatalog, error) {
		return workspace.BrowserCatalog{}, errors.New("catalog unavailable")
	}}
	m := fixture(t, f, false)
	send(t, m, key(tea.KeyEnter))
	send(t, m, tea.KeyPressMsg{Code: 'p', Text: "p"})
	if !strings.Contains(paintText(m), "catalog unavailable") {
		t.Fatal("search failure absent")
	}
	send(t, m, key(tea.KeyEsc))
	intent := filepicker.IntentToken{Epoch: m.picker.OpenEpoch(), Sequence: m.picker.IntentSequence()}
	old := m.browse("/old", 0, false, intent)()
	drive(t, m, m.browse("/new", 0, false, intent))
	send(t, m, old)
	if m.picker.Current() != "/new" {
		t.Fatal("old browse overtook replacement")
	}
}

func TestSearchClearCancelsRunningCatalog(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	f := &fakeStartup{catalog: func(ctx context.Context, _ string, _ bool) (workspace.BrowserCatalog, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return workspace.BrowserCatalog{}, ctx.Err()
	}}
	m := fixture(t, f, false)
	send(t, m, key(tea.KeyEnter))
	_, intentCmd := m.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	_, cmd := m.Update(intentCmd())
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	<-started
	send(t, m, key(tea.KeyEsc))
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("clear did not cancel pending catalog")
	}
	send(t, m, <-done)
	if strings.Contains(paintText(m), "context canceled") {
		t.Fatal("cancelled query painted error")
	}
}

func TestFastQuitStillJoinsIntroductionSaveAttempt(t *testing.T) {
	var calls int
	f := &fakeStartup{save: func(ctx context.Context, _ []types.SettingEdit) (types.OnboardingSettings, error) {
		calls++
		if ctx.Err() != nil {
			t.Fatalf("normal quit cancelled accepted save: %v", ctx.Err())
		}
		return types.OnboardingSettings{IntroductionCompleted: true}, nil
	}}
	m := fixture(t, f, true)
	// Leave both accepted Tea commands queued, then quit immediately.
	_, cmd := m.Update(key(tea.KeyEnter))
	batch := cmd().(tea.BatchMsg)
	send(t, m, ctrl('q'))
	m.Close()
	_ = batch[1]()
	if calls != 1 {
		t.Fatalf("save attempt count %d", calls)
	}
}

func TestSelectedProjectRetainsLateIntroductionSaveFailure(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	f := &fakeStartup{save: func(context.Context, []types.SettingEdit) (types.OnboardingSettings, error) {
		close(started)
		<-release
		return types.OnboardingSettings{}, errors.New("read-only settings after selection")
	}}
	m := fixture(t, f, true)
	_, cmd := m.Update(key(tea.KeyEnter))
	batch := cmd().(tea.BatchMsg)
	saved := make(chan tea.Msg, 1)
	go func() { saved <- batch[1]() }()
	<-started
	drive(t, m, batch[0])
	send(t, m, ctrl('o'))
	if m.Result() != "/home/demo" || !m.closed {
		t.Fatal("pending save blocked project confirmation")
	}
	done := make(chan Selection, 1)
	go func() { done <- m.finish() }()
	select {
	case <-done:
		t.Fatal("handoff returned before the accepted save finished")
	default:
	}
	close(release)
	selection := <-done
	if selection.Root != "/home/demo" || len(selection.Issues) != 1 {
		t.Fatalf("late issue lost: %+v", selection)
	}
	issue := selection.Issues[0]
	if issue.Title != "Introduction choice was not saved" || issue.Path != "onboarding.introduction_completed" || !strings.Contains(issue.Detail, "after selection") || !strings.Contains(issue.Summary, "appear again") {
		t.Fatalf("incomplete save failure: %+v", issue)
	}
	send(t, m, <-saved)
	m.Close()
	if len(m.Issues()) != 1 {
		t.Fatal("late delivery or repeated Close duplicated failure")
	}
}

func TestEarlyIntroductionSaveIssueIsNotDuplicatedAtClose(t *testing.T) {
	m := fixture(t, &fakeStartup{save: func(context.Context, []types.SettingEdit) (types.OnboardingSettings, error) {
		return types.OnboardingSettings{}, errors.New("read-only settings")
	}}, true)
	send(t, m, key(tea.KeyEnter))
	if !strings.Contains(paintText(m), "read-only settings") {
		t.Fatal("early issue not painted")
	}
	send(t, m, ctrl('o'))
	if selection := m.finish(); len(selection.Issues) != 1 {
		t.Fatalf("early and joined save duplicated: %+v", selection)
	}
}

func TestSnapshotIssuesRemainVisibleAndSurviveSelection(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			issue := types.OperationIssue{Title: "Configuration needs repair", Path: "/profile/config.json", Detail: "invalid character at line 7", Summary: "Crude could not read the saved application configuration.", Target: types.RepairConfigFile}
			later := types.OperationIssue{Title: "Profile state is not available", Detail: "saved workspace state is unreadable"}
			f := &fakeStartup{inspect: func(_ context.Context, root string) (coreprotocol.StartupSnapshot, error) {
				return coreprotocol.StartupSnapshot{Root: root, Issues: []types.OperationIssue{issue, later}}, nil
			}}
			m := NewModel(context.Background(), f, coreprotocol.StartupSnapshot{Root: "/home/demo", FolderKind: "home", Issues: []types.OperationIssue{issue}, Workspaces: []workspace.WorkspaceInfo{{ProjectName: "Previous project", ProjectDir: "/previous/project"}}})
			t.Cleanup(m.Close)
			send(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			for _, picker := range []bool{false, true} {
				if picker {
					send(t, m, key(tea.KeyEnter))
				}
				text := paintText(m)
				for _, label := range []string{issue.Title, issue.Path, issue.Detail} {
					if !strings.Contains(text, label) {
						t.Fatalf("repair evidence absent, picker=%v: %q\n%s", picker, label, text)
					}
				}
				if !picker && (!strings.Contains(text, "Previous project") || !strings.Contains(text, "Open Workspace") || !strings.Contains(text, "Quit")) {
					t.Fatalf("issues replaced navigation\n%s", text)
				}
			}
			send(t, m, ctrl('o'))
			selection := m.finish()
			if len(selection.Issues) != 2 || selection.Issues[0] != issue || selection.Issues[1] != later {
				t.Fatalf("snapshot issues lost or duplicated: %+v", selection)
			}
		})
	}
}

func TestLongStartupIssueScrollKeepsActionsAndPreviousRows(t *testing.T) {
	m := NewModel(context.Background(), &fakeStartup{}, coreprotocol.StartupSnapshot{Root: "/home/demo", FolderKind: "home", Issues: []types.OperationIssue{{Title: "Configuration needs repair", Path: "/profile/config.json", Detail: strings.Repeat("Long error evidence. ", 100) + "END OF ERROR"}}, Workspaces: []workspace.WorkspaceInfo{{ProjectName: "Previous project", ProjectDir: "/previous/project"}}})
	t.Cleanup(m.Close)
	text := paintText(m)
	if strings.Contains(text, "END OF ERROR") || !strings.Contains(text, "Previous project") || !strings.Contains(text, "Open Workspace") || !strings.Contains(text, "Quit") {
		t.Fatalf("long error escaped its viewport\n%s", text)
	}
	// Focus the mounted native reader through the same keyboard cycle as users.
	for i := 0; i < 3; i++ {
		send(t, m, key(tea.KeyTab))
	}
	if m.host.Focused() != issuesID {
		t.Fatal("repair reader not keyboard reachable")
	}
	send(t, m, tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModCtrl})
	text = paintText(m)
	if !strings.Contains(text, "END OF ERROR") || !strings.Contains(text, "Previous project") || !strings.Contains(text, "Quit") {
		t.Fatalf("complete error cannot be read without losing navigation\n%s", text)
	}
	// The same reader remains bounded inside the native folder picker.
	send(t, m, key(tea.KeyTab))
	send(t, m, key(tea.KeyEnter))
	// Filter -> tree -> Open Workspace -> Cancel -> issue reader. The
	// action bar must retain both keyboard-accessible decisions.
	for i := 0; i < 4; i++ {
		send(t, m, key(tea.KeyTab))
	}
	if m.host.Focused() != issuesID {
		t.Fatal("picker repair reader not keyboard reachable")
	}
	send(t, m, tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModCtrl})
	text = paintText(m)
	for _, label := range []string{"END OF ERROR", "project", "OPEN WORKSPACE", "More folders"} {
		if !strings.Contains(text, label) {
			t.Fatalf("long issue obscured picker navigation %q\n%s", label, text)
		}
	}
}

func TestPreviousTopLevelOpensInspectedLocationAndReopensThere(t *testing.T) {
	var browsed []string
	f := &fakeStartup{inspect: func(_ context.Context, root string) (coreprotocol.StartupSnapshot, error) {
		if root != "/saved/Downloads" {
			t.Fatalf("wrong previous path: %q", root)
		}
		return coreprotocol.StartupSnapshot{Root: "/relocated/Downloads", FolderKind: "personal"}, nil
	}, browse: func(_ context.Context, r coreprotocol.StartupBrowseRequest) (coreprotocol.StartupBrowseResult, error) {
		browsed = append(browsed, r.Path)
		return coreprotocol.StartupBrowseResult{Path: r.Path, NextOffset: -1}, nil
	}}
	m := NewModel(context.Background(), f, coreprotocol.StartupSnapshot{Root: "/launch/home", FolderKind: "home", Workspaces: []workspace.WorkspaceInfo{{ProjectName: "Downloads", ProjectDir: "/saved/Downloads"}}})
	t.Cleanup(m.Close)
	send(t, m, key(tea.KeyTab))
	send(t, m, key(tea.KeyEnter))
	if m.Result() != "" || m.closed || m.root.mode != picking || m.picker.Current() != "/relocated/Downloads" || m.root.path.Value() != "/relocated/Downloads" {
		t.Fatal("previous top-level entry did not become its inspected browsing location")
	}
	send(t, m, key(tea.KeyEsc))
	send(t, m, key(tea.KeyEnter))
	if len(browsed) != 2 || browsed[0] != "/relocated/Downloads" || browsed[1] != "/relocated/Downloads" {
		t.Fatalf("reopen returned to launch folder: %v", browsed)
	}
}

func TestPreviousPathFailureKeepsOtherRowsUsable(t *testing.T) {
	f := &fakeStartup{inspect: func(_ context.Context, root string) (coreprotocol.StartupSnapshot, error) {
		if root == "/missing" {
			return coreprotocol.StartupSnapshot{Root: root, PathError: "folder vanished"}, nil
		}
		return coreprotocol.StartupSnapshot{Root: root}, nil
	}}
	m := NewModel(context.Background(), f, coreprotocol.StartupSnapshot{Root: "/launch/home", FolderKind: "home", Workspaces: []workspace.WorkspaceInfo{{ProjectName: "Missing", ProjectDir: "/missing"}, {ProjectName: "Good project", ProjectDir: "/good/project"}}})
	t.Cleanup(m.Close)
	send(t, m, key(tea.KeyTab))
	send(t, m, key(tea.KeyEnter))
	if m.root.mode != selection || m.Result() != "" || !strings.Contains(paintText(m), "folder vanished") || !strings.Contains(paintText(m), "Good project") {
		t.Fatal("previous path failure discarded usable rows or selected a folder")
	}
	send(t, m, key(tea.KeyDown))
	send(t, m, key(tea.KeyEnter))
	if m.Result() != "/good/project" {
		t.Fatalf("other previous row no longer usable: %q", m.Result())
	}
}
