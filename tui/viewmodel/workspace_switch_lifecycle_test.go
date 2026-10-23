package viewmodel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/editor"
	"crude/tui/filetree"
	findinfiles "crude/tui/screens/findinfiles"
	"crude/tui/theme"
	"crude/tui/ui"
	ttypes "crude/types"
	"crude/workspace"
)

type workspaceSwitchLifecycleLogger struct{}

func (workspaceSwitchLifecycleLogger) Debug(string, string, ...any) {}
func (workspaceSwitchLifecycleLogger) Info(string, string, ...any)  {}
func (workspaceSwitchLifecycleLogger) Warn(string, string, ...any)  {}
func (workspaceSwitchLifecycleLogger) Error(string, string, ...any) {}

func newWorkspaceSwitchLifecycleApp(t *testing.T, root string) *App {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	ws := workspace.NewWorkspace(root, "test-model", "tester", ttypes.Settings{}, workspaceSwitchLifecycleLogger{})
	app := NewApp(shellport.Local(ws), nil)
	app.width = 100
	app.height = 32
	t.Cleanup(func() {
		app.StopAsyncIO()
		if app.overlayHost != nil {
			app.overlayHost.CloseAll()
		}
		ws.Close()
	})
	return app
}

func writeWorkspaceSwitchFile(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func fileWatcherEventCount(app *App) int {
	if app == nil || app.eventBus == nil {
		return 0
	}
	count := 0
	for _, event := range app.eventBus.RecentEvents(200) {
		if strings.Contains(event, "workspace.FileWatcherMsg") {
			count++
		}
	}
	return count
}

func TestWorkspaceSwitchStopsOldTreeAndStartsGenerationBoundReplacement(t *testing.T) {
	oldRoot := t.TempDir()
	oldFile := writeWorkspaceSwitchFile(t, oldRoot, "old.txt", "old\n")
	newRoot := t.TempDir()
	newFile := writeWorkspaceSwitchFile(t, newRoot, "new.txt", "new\n")
	app := newWorkspaceSwitchLifecycleApp(t, oldRoot)
	detached := make(chan tea.Msg, 8)
	restoreSink := installDetachedMessageSinkForTest(app, func(msg tea.Msg) {
		detached <- msg
	})
	defer restoreSink()

	_ = app.Init()
	oldModel := app.Tree.Model
	oldWatcher := oldModel.Watcher()
	if oldWatcher == nil {
		t.Fatal("Init did not start the original file-tree watcher")
	}

	if err := app.SwitchProject(newRoot); err != nil {
		t.Fatal(err)
	}

	select {
	case <-oldWatcher.Done():
	default:
		t.Fatal("old file-tree watcher is still running after switch")
	}
	if oldModel.Watcher() != nil {
		t.Fatal("old model still retains its watcher")
	}
	if app.Tree == nil || app.Tree.Model == nil || app.Tree.Model == oldModel {
		t.Fatal("switch did not install a new file-tree projection")
	}
	if app.Tree.Model.Watcher() != nil {
		t.Fatal("placeholder tree unexpectedly started a watcher on the update goroutine")
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case raw := <-detached:
			msg, ok := raw.(workspaceTreeReadyMsg)
			if !ok {
				continue
			}
			_, _ = app.Update(msg)
			goto treeReady
		case <-deadline:
			t.Fatal("asynchronous replacement tree did not complete")
		}
	}

treeReady:
	var newWatcher = app.Tree.Model.Watcher()
	watcherDeadline := time.Now().Add(2 * time.Second)
	for newWatcher == nil && time.Now().Before(watcherDeadline) {
		time.Sleep(5 * time.Millisecond)
		newWatcher = app.Tree.Model.Watcher()
	}
	if newWatcher == nil {
		t.Fatal("replacement file-tree watcher was not started asynchronously")
	}
	select {
	case <-newWatcher.Done():
		t.Fatal("replacement file-tree watcher stopped during startup")
	default:
	}

	before := fileWatcherEventCount(app)
	oldModel.ProcessWatcherEvent(filetree.FileChangeEvent{Type: filetree.FileModified, Path: oldFile})
	if got := fileWatcherEventCount(app); got != before {
		t.Fatalf("stale tree callback published across generation: before=%d after=%d", before, got)
	}
	app.Tree.Model.ProcessWatcherEvent(filetree.FileChangeEvent{Type: filetree.FileModified, Path: newFile})
	if got := fileWatcherEventCount(app); got != before+1 {
		t.Fatalf("replacement tree callback not wired: got %d watcher events, want %d", got, before+1)
	}
}

func TestWorkspaceSwitchValidationFailureRestoresOldWatcherAndGenerations(t *testing.T) {
	oldRoot := t.TempDir()
	newRoot := t.TempDir()
	app := newWorkspaceSwitchLifecycleApp(t, oldRoot)
	_ = app.Init()

	oldModel := app.Tree.Model
	oldWatcher := oldModel.Watcher()
	if oldWatcher == nil {
		t.Fatal("Init did not start the original file-tree watcher")
	}
	app.gitRefreshGen.Store(41)
	app.editorGitGen.Store(17)
	app.launcherLoadGen.Store(73)
	app.workspaceSwitchGen.Store(19)

	blockedHome := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedHome, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", blockedHome)
	t.Setenv("USERPROFILE", blockedHome)

	if err := app.SwitchProject(newRoot); err == nil {
		t.Fatal("workspace switch unexpectedly succeeded with an unusable data root")
	}
	if app.ws.RootPath() != oldRoot || app.Tree.Model != oldModel {
		t.Fatalf("failed switch replaced old workspace: root=%q modelChanged=%v", app.ws.RootPath(), app.Tree.Model != oldModel)
	}
	select {
	case <-oldWatcher.Done():
	default:
		t.Fatal("failed switch did not stop the original watcher instance")
	}
	if replacement := oldModel.Watcher(); replacement == nil {
		t.Fatal("failed switch did not restore a watcher on the old tree")
	}
	if got := app.gitRefreshGen.Load(); got != 41 {
		t.Fatalf("git generation=%d, want restored 41", got)
	}
	if got := app.editorGitGen.Load(); got != 17 {
		t.Fatalf("editor git generation=%d, want restored 17", got)
	}
	if got := app.launcherLoadGen.Load(); got != 73 {
		t.Fatalf("launcher generation=%d, want restored 73", got)
	}
	if got := app.workspaceSwitchGen.Load(); got != 19 {
		t.Fatalf("workspace generation=%d, want restored 19", got)
	}
}

func TestWorkspaceSwitchAppliesTargetThemeAndClearsProjectDraft(t *testing.T) {
	oldRoot := t.TempDir()
	newRoot := t.TempDir()
	app := newWorkspaceSwitchLifecycleApp(t, oldRoot)
	app.ApplyThemePreset("dark")
	app.Input.SetValuePreserveReferences("old project secret")
	app.FocusStack.Push(FocusTree)
	app.ActiveSession = &workspace.Session{SessionID: "old-selected-session"}

	target := workspace.NewWorkspace(
		newRoot, "test-model", "tester", ttypes.Settings{}, workspaceSwitchLifecycleLogger{},
	)
	if err := target.SaveWorkspaceSettings(ttypes.WorkspaceOverrides{
		Theme: json.RawMessage(`{"preset":"light"}`),
	}); err != nil {
		t.Fatal(err)
	}
	target.Close()

	if err := app.SwitchProject(newRoot); err != nil {
		t.Fatal(err)
	}
	if got := theme.PresetName(app.Theme); got != "light" {
		t.Fatalf("target workspace theme=%q, want light", got)
	}
	if got := app.Input.Value(); got != "" {
		t.Fatalf("old workspace draft survived switch: %q", got)
	}
	if app.FocusStack.Top() != FocusNone || app.ActiveSession != nil {
		t.Fatal("successful workspace switch retained old shell selection or focus")
	}
}

func TestTreeReadyReplacesAndStopsRestoredPlaceholderWatcher(t *testing.T) {
	oldRoot := t.TempDir()
	newRoot := t.TempDir()
	rejectedRoot := t.TempDir()
	app := newWorkspaceSwitchLifecycleApp(t, oldRoot)
	home := os.Getenv("HOME")

	detached := make(chan tea.Msg, 8)
	restoreSink := installDetachedMessageSinkForTest(app, func(msg tea.Msg) {
		detached <- msg
	})
	defer restoreSink()
	if err := app.SwitchProject(newRoot); err != nil {
		t.Fatal(err)
	}
	placeholder := app.Tree.Model

	var ready workspaceTreeReadyMsg
	deadline := time.After(2 * time.Second)
	for ready.model == nil {
		select {
		case raw := <-detached:
			if msg, ok := raw.(workspaceTreeReadyMsg); ok {
				ready = msg
			}
		case <-deadline:
			t.Fatal("target tree build did not complete")
		}
	}

	blockedHome := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedHome, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", blockedHome)
	t.Setenv("USERPROFILE", blockedHome)
	if err := app.SwitchProject(rejectedRoot); err == nil {
		t.Fatal("invalid persistence root unexpectedly switched")
	}
	restoredWatcher := placeholder.Watcher()
	if restoredWatcher == nil {
		t.Fatal("failed switch did not restore placeholder watcher")
	}

	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	_, _ = app.Update(ready)
	select {
	case <-restoredWatcher.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("ready tree replacement leaked the restored placeholder watcher")
	}
	if app.Tree.Model == placeholder {
		t.Fatal("ready tree did not replace placeholder")
	}
}

func TestWorkspaceSwitchClosesEditorsAndClearsProjectProjections(t *testing.T) {
	oldRoot := t.TempDir()
	pathA := writeWorkspaceSwitchFile(t, oldRoot, "a.txt", "a\n")
	pathB := writeWorkspaceSwitchFile(t, oldRoot, "b.txt", "b\n")
	pathC := writeWorkspaceSwitchFile(t, oldRoot, "c.txt", "c\n")
	newRoot := t.TempDir()
	app := newWorkspaceSwitchLifecycleApp(t, oldRoot)

	if _, err := app.openEditorTab(pathA); err != nil {
		t.Fatal(err)
	}
	if _, err := app.openEditorTab(pathB); err != nil {
		t.Fatal(err)
	}
	tabA := app.EditorState.Tabs[pathA].Editor
	tabB := app.EditorState.Tabs[pathB].Editor
	orphan := editor.New(app.Theme)
	if err := orphan.Open(pathC); err != nil {
		t.Fatal(err)
	}
	app.EditorState.Editor = orphan // distinct active projection outside Tabs
	oldEditorState := app.EditorState
	oldBufferMgr := app.BufferMgr

	oldSessionRegistry := app.sessionProjections
	oldSession := &workspace.Session{
		SessionID: "old-session",
		Settings:  workspace.NewSessionSettingsState(ttypes.Settings{}, ttypes.Settings{}, nil),
	}
	oldSessionProjection := oldSessionRegistry.Get(oldSession)
	oldSubPaneRegistry := app.subPaneProjections
	oldSubPaneRegistry.Get("old-pane", workspace.PaneAgent)

	_, _ = app.toggleFindInFiles()
	oldSearch := app.findInFiles
	app.findInFilesRequest = 91
	app.findInFilesGen = 7
	_, _ = app.toggleCommandPalette() // application-global overlay must survive a domain switch
	if !app.overlayHost.IsOpen(ui.OverlayCommandPalette) {
		t.Fatal("command palette precondition failed")
	}

	oldGitGen := app.gitRefreshGen.Add(1)
	app.gitPanel.SetSnapshot(workspace.GitSnapshot{
		RepositoryRoot: oldRoot,
		Branch:         "old-branch",
	})

	if err := app.SwitchProject(newRoot); err != nil {
		t.Fatal(err)
	}

	for name, ed := range map[string]*editor.Editor{
		"tab A":                tabA,
		"tab B":                tabB,
		"orphan active editor": orphan,
	} {
		if ed.IsOpen() || ed.FilePath() != "" {
			t.Errorf("%s survived switch: open=%v path=%q", name, ed.IsOpen(), ed.FilePath())
		}
		if ed.DiskChanged() {
			t.Errorf("%s retained its file watcher signal", name)
		}
	}
	if app.EditorState == oldEditorState || app.BufferMgr == oldBufferMgr {
		t.Fatal("editor state and buffer manager were not replaced")
	}
	if len(app.EditorState.Tabs) != 0 || app.EditorState.Editor == nil || app.EditorState.Editor.IsOpen() {
		t.Fatal("replacement editor state is not an empty, closed projection")
	}
	if app.Win.TabCount() != 0 {
		t.Fatalf("old workspace tabs survived: %d", app.Win.TabCount())
	}

	if app.sessionProjections == oldSessionRegistry || len(app.sessionProjections.items) != 0 || len(oldSessionRegistry.items) != 0 {
		t.Fatal("session projection registry was not cleared and recreated")
	}
	if owner, projection := app.sessionProjections.projectionForSettings(oldSessionProjection.settings); owner != nil || projection != nil {
		t.Fatal("stale session projection was accepted by replacement registry")
	}
	if app.subPaneProjections == oldSubPaneRegistry ||
		len(app.subPaneProjections.projections) != 0 ||
		len(oldSubPaneRegistry.projections) != 0 {
		t.Fatal("sub-pane projection registry was not cleared and recreated")
	}

	if app.findInFiles != nil || app.findInFilesRequest != 0 || app.findInFilesGen != 0 {
		t.Fatal("find-in-files identity survived workspace switch")
	}
	if app.overlayHost.IsOpen(ui.OverlayFindInFiles) || app.overlayHost.IsOpen(ui.OverlayReplaceInFiles) {
		t.Fatal("project-search overlay survived workspace switch")
	}
	if !app.overlayHost.IsOpen(ui.OverlayCommandPalette) {
		t.Fatal("workspace switch closed the application-global command palette")
	}
	_, _ = app.Update(findinfiles.SearchRequestMsg{
		Screen:     oldSearch,
		Generation: oldSearch.Generation(),
		Query:      "stale",
	})
	if app.findInFilesRequest != 0 {
		t.Fatal("stale project-search screen started a new-workspace request")
	}

	if app.gitRefreshGen.Load() == oldGitGen {
		t.Fatal("git generation was not advanced")
	}
	app.applySourceControlResult(sourceControlResultMsg{
		gen: oldGitGen,
		snapshot: workspace.GitSnapshot{
			RepositoryRoot: oldRoot,
			Branch:         "stale-branch",
		},
	})
	if snapshot := app.gitPanel.Snapshot(); snapshot.RepositoryRoot != "" || snapshot.Branch != "" {
		t.Fatalf("stale git result mutated replacement projection: %+v", snapshot)
	}
}
