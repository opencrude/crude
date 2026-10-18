package viewmodel

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/fileio"
	"crude/tui/editor"
	"crude/tui/filetree"
	findinfiles "crude/tui/screens/findinfiles"
	"crude/workspace"
)

// fileOpsUndrainedProgramModel backs a Program whose unbuffered message
// channel is intentionally never drained. A synchronous EventBus.Send from
// App.Update would therefore block forever.
type fileOpsUndrainedProgramModel struct{}

func (fileOpsUndrainedProgramModel) Init() tea.Cmd { return nil }
func (fileOpsUndrainedProgramModel) Update(tea.Msg) (tea.Model, tea.Cmd) {
	return fileOpsUndrainedProgramModel{}, nil
}
func (fileOpsUndrainedProgramModel) View() tea.View { return tea.View{} }

// Regression: Replace in Files formerly applied disk edits synchronously while handling
// findinfiles.ReplaceMsg in App.Update. FileOpsService used to echo its returned
// FileOperationResultMsg through EventBus, whose Program.Send waited for the
// same Update loop and permanently deadlocked the TUI.
func TestProjectReplaceFileOperationDoesNotReenterEventBus(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newFullTestApp(t)
	wsOf(app).RootPath = root
	bus := workspace.NewEventBus()
	wsOf(app).FileOps = workspace.NewFileOpsService(root, bus, nil)
	app.ws.SetEventBus(bus)
	setUndrainedProgram(t, bus)

	app.toggleReplaceInFiles()
	search := app.findInFiles
	search.SetQuery("alpha")
	search.SetReplacement("omega")
	search.ApplySearchResults(search.Generation(), []fileio.ProjectSearchMatch{{
		Path:         path,
		RelativePath: "main.go",
		Line:         1,
		Column:       0,
		EndColumn:    5,
		LineText:     "alpha",
		TextMatches:  []int{0, 1, 2, 3, 4},
	}}, nil, 0)

	done := make(chan tea.Cmd, 1)
	go func() {
		_, cmd := app.Update(findinfiles.ReplaceMsg{
			Screen:      search,
			Generation:  search.Generation(),
			All:         true,
			Query:       "alpha",
			Replacement: "omega",
		})
		done <- cmd
	}()

	select {
	case cmd := <-done:
		if cmd == nil {
			t.Fatal("replacement planning command missing")
		}
		runAppCommand(t, app, cmd)
	case <-time.After(5 * time.Second):
		t.Fatal("Update deadlocked by echoing a file operation result through EventBus")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "alpha\n" || app.projectReplace == nil {
		t.Fatalf("planning must show review without writing; disk=%q review=%v", got, app.projectReplace != nil)
	}
	if events := bus.RecentEvents(1); len(events) != 0 {
		t.Fatalf("file operation result unexpectedly reached EventBus: %v", events)
	}
}

// Regression: tree create/folder/rename/delete handlers run synchronously from
// App.Update. Their immediate tree refresh must not reuse the background
// watcher path, because its callback publishes through EventBus.Send and would
// block against this intentionally undrained Program.
func TestTreeMutationsDoNotReenterEventBus(t *testing.T) {
	t.Run("create file", func(t *testing.T) {
		root := t.TempDir()
		app, bus := newUndrainedTreeMutationApp(t, root)
		path := filepath.Join(root, "created.go")

		app.startNewFileInTree(root)
		app.TreePrompt.Edit.SetValue(filepath.Base(path))
		updateTreeMutationWithoutDeadlock(t, app, "create file")

		assertPathExists(t, path, false)
		assertTreeContainsPath(t, app.Tree.Model, path, true)
		assertNoEventBusReentry(t, bus)
	})

	t.Run("create folder", func(t *testing.T) {
		root := t.TempDir()
		app, bus := newUndrainedTreeMutationApp(t, root)
		path := filepath.Join(root, "created-folder")

		app.startNewFolderInTree(root)
		app.TreePrompt.Edit.SetValue(filepath.Base(path))
		updateTreeMutationWithoutDeadlock(t, app, "create folder")

		assertPathExists(t, path, true)
		assertTreeContainsPath(t, app.Tree.Model, path, true)
		assertNoEventBusReentry(t, bus)
	})

	t.Run("rename", func(t *testing.T) {
		root := t.TempDir()
		oldPath := filepath.Join(root, "before.go")
		if err := os.WriteFile(oldPath, []byte("package before\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		app, bus := newUndrainedTreeMutationApp(t, root)
		newPath := filepath.Join(root, "after.go")
		if _, err := app.openEditorTab(oldPath); err != nil {
			t.Fatal(err)
		}
		app.EditorState.Editor.InsertRune('x')

		app.startRenameInTree(oldPath)
		app.TreePrompt.Edit.SetValue(filepath.Base(newPath))
		updateTreeMutationWithoutDeadlock(t, app, "rename")

		if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
			t.Fatalf("old path still exists after rename: %v", err)
		}
		assertPathExists(t, newPath, false)
		assertTreeContainsPath(t, app.Tree.Model, oldPath, false)
		assertTreeContainsPath(t, app.Tree.Model, newPath, true)
		if app.EditorState.ActiveView != newPath || app.EditorState.Editor.FilePath() != newPath {
			t.Fatalf("editor was not retargeted: active=%q file=%q", app.EditorState.ActiveView, app.EditorState.Editor.FilePath())
		}
		if app.BufferMgr.Get(oldPath) != nil || app.BufferMgr.Get(newPath) == nil {
			t.Fatal("buffer manager retained stale rename path")
		}
		finishSaveCommand(t, app, app.requestEditorSave(app.EditorState.Editor, workspace.DocumentSaveManual, false))
		if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
			t.Fatal("save recreated renamed source")
		}
		assertNoEventBusReentry(t, bus)
	})

	t.Run("delete folder", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "delete-folder")
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "child.txt"), []byte("child\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		app, bus := newUndrainedTreeMutationApp(t, root)

		app.startDeleteInTree(path)
		app.TreeDeleteChoice = 0
		updateTreeMutationWithoutDeadlock(t, app, "delete folder")

		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("deleted folder still exists: %v", err)
		}
		assertTreeContainsPath(t, app.Tree.Model, path, false)
		assertNoEventBusReentry(t, bus)
	})
}

func newUndrainedTreeMutationApp(t *testing.T, root string) (*App, *workspace.EventBus) {
	t.Helper()

	app := newFullTestApp(t)
	bus := workspace.NewEventBus()
	wsOf(app).RootPath = root
	wsOf(app).FileOps = workspace.NewFileOpsService(root, bus, nil)
	app.ws.SetEventBus(bus)
	app.eventBus = bus
	app.BufferMgr = editor.NewBufferManager()

	model := filetree.NewFileTreeModel(root)
	app.Tree = NewTreeState(model, 30)
	app.sidebarScreen.SetModel(model)
	model.SetOnFileChanged(func(filePath string, changeType int) {
		bus.Send(workspace.FileWatcherMsg{FilePath: filePath, ChangeType: changeType})
	})
	setUndrainedProgram(t, bus)

	return app, bus
}

func setUndrainedProgram(t *testing.T, bus *workspace.EventBus) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	bus.SetSink(programMessageSink{program: tea.NewProgram(fileOpsUndrainedProgramModel{}, tea.WithContext(ctx))})
	t.Cleanup(func() {
		cancel()
		bus.SetSink(nil)
	})
}

func updateTreeMutationWithoutDeadlock(t *testing.T, app *App, operation string) {
	t.Helper()

	done := make(chan struct{})
	go func() {
		app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s Update deadlocked by re-entering EventBus from a local tree mutation", operation)
	}
}

func assertPathExists(t *testing.T, path string, wantDir bool) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	if info.IsDir() != wantDir {
		t.Fatalf("%q IsDir=%v, want %v", path, info.IsDir(), wantDir)
	}
}

func assertTreeContainsPath(t *testing.T, model *filetree.FileTreeModel, path string, want bool) {
	t.Helper()

	if got := model.NodeByPath(path) != nil; got != want {
		t.Fatalf("tree contains %q=%v, want %v", path, got, want)
	}
}

func assertNoEventBusReentry(t *testing.T, bus *workspace.EventBus) {
	t.Helper()

	if events := bus.RecentEvents(1); len(events) != 0 {
		t.Fatalf("local tree mutation unexpectedly reached EventBus: %v", events)
	}
}
