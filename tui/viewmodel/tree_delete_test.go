package viewmodel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/document"
	"crude/localhistory"
	"crude/shellport"
	"crude/tui/cell"
	"crude/types"
	"crude/workspace"
)

type blockedTreeDelete struct {
	shellport.Workspace
	release <-chan struct{}
	started chan<- struct{}
}

func (w *blockedTreeDelete) DeletePath(ctx context.Context, path string, progress func(string)) workspace.FileOperationResultMsg {
	if progress != nil {
		progress("Keeping safety copy of")
	}
	if w.started != nil {
		close(w.started)
	}
	select {
	case <-ctx.Done():
		return workspace.FileOperationResultMsg{Op: workspace.FileOpDelete, Path: path, Err: ctx.Err()}
	case <-w.release:
		return w.Workspace.DeletePath(ctx, path, progress)
	}
}

func TestTreeDeletePaintsProgressUntilCompletion(t *testing.T) {
	for _, width := range []int{80, 160} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, ".claude")
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
			app, _ := newUndrainedTreeMutationApp(t, root)
			app.width, app.height = width, 24
			release, started := make(chan struct{}), make(chan struct{})
			app.ws = &blockedTreeDelete{Workspace: app.ws, release: release, started: started}
			app.startDeleteInTree(path)
			cmd := app.confirmTreeDelete()
			if !app.wantsFastTick() {
				t.Fatal("file deletion uses the idle animation clock")
			}
			done := make(chan tea.Msg, 1)
			go func() { done <- cmd() }()
			<-started
			app.drainTreeDeleteProgress()
			app.paintShellFrame()
			if row := rowText(app, app.height-1); !strings.Contains(row, "Keeping safety copy of .claude") {
				close(release)
				t.Fatalf("running delete has no visible phase: %q", row)
			}
			// Paint motion while the filesystem is still busy, not just on phase changes.
			before := append([]cell.Cell(nil), app.mainSurf.Buffer().Row(app.height-2)...)
			app.SpinFrame += 500
			app.configureFooterScreen()
			app.paintShellFrame()
			if reflect.DeepEqual(before, app.mainSurf.Buffer().Row(app.height-2)) {
				close(release)
				t.Fatal("delete progress did not animate")
			}
			close(release)
			msg := <-done
			app.Update(msg)
			app.paintShellFrame()
			if strings.Contains(rowText(app, app.height-1), "Keeping safety copy") || app.treeDeleteProgress() != "" {
				t.Fatal("completed delete retained running progress")
			}
			if app.wantsFastTick() {
				t.Fatal("completed delete retained the fast animation clock")
			}
			requireMissing(t, path)
		})
	}
}

func TestConnectedTreeDeleteSavedClaudeFolder(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".claude")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) {
		settings := w.AppDefaults()
		settings.LocalHistory = types.LocalHistorySettings{Enabled: "on"}
		w.SetAppDefaults(settings)
	})
	app := newRemoteJourneyShell(t, runtime, root).app
	docs := app.ws.(shellport.LiveDocuments)
	// Match New File -> type -> Save File -> close, not just os.WriteFile.
	draft, err := app.ws.(shellport.UntitledDocuments).CreateUntitledDocument(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	edit, err := docs.EditDocument(t.Context(), document.Change{Owner: docs.DocumentOwner(), DocumentID: draft.ID, BaseRevision: draft.Revision, RequestID: "type-test", Edits: []document.Edit{{Text: "test"}}})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := app.ws.(shellport.UntitledDocumentSaver).SaveUntitledDocument(t.Context(), draft.ID, edit.Revision, path, "test", "", "save-test")
	if err != nil || saved.Dirty || saved.Conflict {
		t.Fatalf("saved file: %+v %v", saved, err)
	}
	if err := docs.CloseDocument(t.Context(), saved.ID); err != nil {
		t.Fatal(err)
	}
	runAppCommand(t, app, app.beginClientFileTreeLoad())
	runAppCommand(t, app, app.Tree.Model.Expand(path))
	requireFile(t, filepath.Join(path, "test"), "test")
	app.startDeleteInTree(path)
	app.TreeDeleteChoice = 0
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, app, cmd)
	requireMissing(t, path)
	assertTreeContainsPath(t, app.Tree.Model, path, false)
	records, err := app.ws.LocalHistory().FileHistory(".claude/test", 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.Kind != "safety" {
			continue
		}
		plan, err := app.ws.LocalHistory().PlanRestorePaths(t.Context(), record.ID, localhistory.SnapshotOptions{}, []string{".claude/test"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := app.ws.ApplyHistoryRestoreChoices(t.Context(), plan.ID, []localhistory.RestoreChoice{{Path: ".claude/test"}}); err != nil {
			t.Fatal(err)
		}
		requireFile(t, filepath.Join(path, "test"), "test")
		return
	}
	t.Fatal("delete lost the Local History safety copy")
}

// A slow safety snapshot or core request must not hold Update. The file stays
// visible until the command completes; only the returned result changes the UI.
func TestTreeDeleteDoesNotBlockUpdate(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".hidden")
	if err := os.WriteFile(path, []byte("keep until confirmed"), 0600); err != nil {
		t.Fatal(err)
	}
	app, _ := newUndrainedTreeMutationApp(t, root)
	release := make(chan struct{})
	app.ws = &blockedTreeDelete{Workspace: app.ws, release: release}
	app.startDeleteInTree(path)
	app.TreeDeleteChoice = 0
	done := make(chan tea.Cmd, 1)
	go func() {
		_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		done <- cmd
	}()
	var cmd tea.Cmd
	select {
	case cmd = <-done:
		close(release)
	case <-time.After(time.Second):
		close(release)
		<-done
		t.Fatal("Delete blocked Update while waiting for the filesystem")
	}
	if cmd == nil {
		t.Fatal("Delete did not return a command")
	}
	requireFile(t, path, "keep until confirmed")
	assertTreeContainsPath(t, app.Tree.Model, path, true)
	runAppCommand(t, app, cmd)
	requireMissing(t, path)
	assertTreeContainsPath(t, app.Tree.Model, path, false)
}

// Exercise the actual connected port and a loaded dot-folder, not a local tree
// backed by the same disk as the test. A successful delete must disappear from
// that loaded directory without waiting for a filesystem watcher.
func TestConnectedTreeDeleteHiddenFolder(t *testing.T) {
	checkConnectedTreeDeleteHiddenFolder(t, t.TempDir())
}

func TestConnectedTreeDeleteLinkedWorktree(t *testing.T) {
	repo := newGitRepoFixture(t)
	root := filepath.Join(t.TempDir(), "checkout")
	runGitFixture(t, repo, "worktree", "add", "--detach", root)
	checkConnectedTreeDeleteHiddenFolder(t, root)
	requireFile(t, filepath.Join(repo, "tracked.txt"), "two\n")
	requireFile(t, filepath.Join(root, "tracked.txt"), "one\n")
}

func checkConnectedTreeDeleteHiddenFolder(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, ".hidden")
	path := filepath.Join(dir, ".cache")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "content"), []byte("recover me"), 0600); err != nil {
		t.Fatal(err)
	}
	runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) {
		settings := w.AppDefaults()
		settings.LocalHistory = types.LocalHistorySettings{Enabled: "on"}
		w.SetAppDefaults(settings)
	})
	shell := newRemoteJourneyShell(t, runtime, root)
	app := shell.app
	runAppCommand(t, app, app.beginClientFileTreeLoad())
	runAppCommand(t, app, app.Tree.Model.Expand(dir))
	assertTreeContainsPath(t, app.Tree.Model, path, true)
	app.startDeleteInTree(path)
	app.TreeDeleteChoice = 0
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, app, cmd)
	requireMissing(t, path)
	assertTreeContainsPath(t, app.Tree.Model, path, false)
	// The successful async result must still have passed the core's safety
	// snapshot, including content excluded by ordinary history ignore rules.
	records, err := app.ws.LocalHistory().FileHistory(".hidden/.cache/content", 20)
	if err != nil {
		t.Fatal(err)
	}
	safety := ""
	for _, record := range records {
		if record.Kind == "safety" {
			safety = record.ID
			break
		}
	}
	if safety == "" {
		t.Fatal("delete did not keep a safety copy")
	}
	plan, err := app.ws.LocalHistory().PlanRestorePaths(t.Context(), safety, localhistory.SnapshotOptions{}, []string{".hidden/.cache/content"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.ws.ApplyHistoryRestoreChoices(t.Context(), plan.ID, []localhistory.RestoreChoice{{Path: ".hidden/.cache/content"}}); err != nil {
		t.Fatal(err)
	}
	requireFile(t, filepath.Join(path, "content"), "recover me")
}

func TestTreeDeleteCancellationAndStaleResults(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(map[bool]string{false: "queued", true: "running"}[running], func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "kept")
			if err := os.WriteFile(path, []byte("keep me"), 0600); err != nil {
				t.Fatal(err)
			}
			app, _ := newUndrainedTreeMutationApp(t, root)
			started := make(chan struct{})
			app.ws = &blockedTreeDelete{Workspace: app.ws, release: make(chan struct{}), started: started}
			app.startDeleteInTree(path)
			cmd := app.handleTreeDeleteConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
			if cmd == nil {
				t.Fatal("Yes did not schedule deletion")
			}
			var message tea.Msg
			if running {
				done := make(chan tea.Msg, 1)
				go func() { done <- cmd() }()
				<-started
				app.cancelClientIOOperations()
				message = <-done
			} else {
				app.cancelClientIOOperations()
				message = cmd()
				select {
				case <-started:
					t.Fatal("cancelled queued deletion started")
				default:
				}
			}
			app.Update(message)
			if app.treeDeleteProgress() != "" {
				t.Fatal("cancelled delete retained running progress")
			}
			requireFile(t, path, "keep me")
			assertTreeContainsPath(t, app.Tree.Model, path, true)
			// A completion from the previous workspace cannot remove its row.
			stale := treeDeleteResultMsg{owner: app.ws, generation: app.workspaceSwitchGen.Load(), path: path}
			app.workspaceSwitchGen.Add(1)
			app.Update(stale)
			assertTreeContainsPath(t, app.Tree.Model, path, true)
		})
	}
}

func TestConnectedTreeDeleteSafetyFailureKeepsFolder(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".cache")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "content"), []byte("keep me"), 0600); err != nil {
		t.Fatal(err)
	}
	// An invalid Git pointer makes the safety scan fail before deletion.
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) {
		settings := w.AppDefaults()
		settings.LocalHistory = types.LocalHistorySettings{Enabled: "on"}
		w.SetAppDefaults(settings)
	})
	shell := newRemoteJourneyShell(t, runtime, root)
	app := shell.app
	runAppCommand(t, app, app.beginClientFileTreeLoad())
	app.startDeleteInTree(path)
	runAppCommand(t, app, app.handleTreeDeleteConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"}))
	if app.treeDeleteProgress() != "" {
		t.Fatal("failed safety copy retained running progress")
	}
	requireFile(t, filepath.Join(path, "content"), "keep me")
	assertTreeContainsPath(t, app.Tree.Model, path, true)
	found := false
	for _, notice := range app.ws.Status().Notifications(true) {
		found = found || strings.Contains(notice.Summary, "delete .cache:")
	}
	if !found {
		t.Fatal("failed safety copy did not explain why the folder was kept")
	}
}
