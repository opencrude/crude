package viewmodel

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/window"
	"crude/tui/wm"
	"crude/workspace"
)

// ── ViewModel integration tests for Phase 10 filesystem mutation seam ──
//
// These tests verify that tree operations (create file, create folder,
// rename, delete) route through workspace methods and consume their returned
// results rather than performing direct filesystem mutations or echoing
// command results through EventBus.

func TestViewModel_CreateFile_RoutesThroughWorkspace(t *testing.T) {
	dir := t.TempDir()
	app := newTestAppWithFileOps(t, dir)

	// Create a file through the workspace method.
	path := filepath.Join(dir, "test-file.txt")
	msg := app.CreateFile(path)
	if !msg.Succeeded() {
		t.Fatalf("CreateFile failed: %v", msg.Err)
	}

	// Verify file exists on disk.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file does not exist after CreateFile: %v", err)
	}

	// Verify the operation type.
	if msg.Op != workspace.FileOpCreate {
		t.Fatalf("expected FileOpCreate, got %v", msg.Op)
	}
}

func TestViewModel_CreateFolder_RoutesThroughWorkspace(t *testing.T) {
	dir := t.TempDir()
	app := newTestAppWithFileOps(t, dir)

	// Create a folder through the workspace method.
	path := filepath.Join(dir, "test-folder")
	msg := app.CreateFolder(path)
	if !msg.Succeeded() {
		t.Fatalf("CreateFolder failed: %v", msg.Err)
	}

	// Verify folder exists on disk.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("folder does not exist after CreateFolder: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("created path is not a directory")
	}
}

func TestViewModel_RenamePath_RoutesThroughWorkspace(t *testing.T) {
	dir := t.TempDir()
	app := newTestAppWithFileOps(t, dir)

	// Create a file to rename.
	oldPath := filepath.Join(dir, "old-name.txt")
	if err := os.WriteFile(oldPath, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Rename through workspace method.
	newPath := filepath.Join(dir, "new-name.txt")
	msg := app.RenamePath(oldPath, newPath)
	if !msg.Succeeded() {
		t.Fatalf("RenamePath failed: %v", msg.Err)
	}

	// Verify rename happened.
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatal("old path still exists after rename")
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("new path does not exist after rename: %v", err)
	}
}

func TestViewModel_DeletePath_RoutesThroughWorkspace(t *testing.T) {
	dir := t.TempDir()
	app := newTestAppWithFileOps(t, dir)

	// Create a file to delete.
	path := filepath.Join(dir, "delete-me.txt")
	if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Delete through workspace method.
	msg := app.DeletePath(path)
	if !msg.Succeeded() {
		t.Fatalf("DeletePath failed: %v", msg.Err)
	}

	// Verify deletion.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file still exists after deletion")
	}
}

func TestViewModel_ApplyWorkspaceEdit_RoutesThroughWorkspace(t *testing.T) {
	dir := t.TempDir()
	app := newTestAppWithFileOps(t, dir)

	// Create a file to edit.
	path := filepath.Join(dir, "edit-me.txt")
	if err := os.WriteFile(path, []byte("old content"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Apply edit through workspace method.
	edits := []workspace.FileEdit{
		{Path: path, OldContent: "old content", NewContent: "new content"},
	}
	msg := app.ApplyWorkspaceEdit(edits)
	if !msg.Succeeded() {
		t.Fatalf("ApplyWorkspaceEdit failed: %v", msg.Err)
	}

	// Verify content was updated.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new content" {
		t.Fatalf("content mismatch: got %q", string(data))
	}
}

func TestViewModel_CreateFile_DoesNotEchoReturnedResult(t *testing.T) {
	dir := t.TempDir()
	bus := workspace.NewEventBus()
	app := newTestAppWithFileOpsAndBus(t, dir, bus)

	path := filepath.Join(dir, "event-file.txt")
	msg := app.CreateFile(path)
	if !msg.Succeeded() {
		t.Fatalf("CreateFile failed: %v", msg.Err)
	}

	if events := bus.RecentEvents(1); len(events) != 0 {
		t.Fatalf("CreateFile echoed its returned result through EventBus: %v", events)
	}
}

func TestViewModel_DeletePath_DoesNotEchoReturnedResult(t *testing.T) {
	dir := t.TempDir()
	bus := workspace.NewEventBus()
	app := newTestAppWithFileOpsAndBus(t, dir, bus)

	// Create and then delete a file.
	path := filepath.Join(dir, "delete-event.txt")
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	msg := app.DeletePath(path)
	if !msg.Succeeded() {
		t.Fatalf("DeletePath failed: %v", msg.Err)
	}

	if events := bus.RecentEvents(1); len(events) != 0 {
		t.Fatalf("DeletePath echoed its returned result through EventBus: %v", events)
	}
}

func TestViewModel_CreateFile_ValidationPreventsEscape(t *testing.T) {
	dir := t.TempDir()
	app := newTestAppWithFileOps(t, dir)

	// Try to create a file outside workspace root.
	msg := app.CreateFile("/tmp/outside-workspace.txt")
	if msg.Succeeded() {
		t.Fatal("expected failure for path outside workspace root")
	}
}

func TestViewModel_CreateFile_ValidationRequiresAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	app := newTestAppWithFileOps(t, dir)

	// Try to create with relative path.
	msg := app.CreateFile("relative/path.txt")
	if msg.Succeeded() {
		t.Fatal("expected failure for relative path")
	}
}

func TestViewModel_RenamePath_SourceNotFound(t *testing.T) {
	dir := t.TempDir()
	app := newTestAppWithFileOps(t, dir)

	msg := app.RenamePath(filepath.Join(dir, "nonexistent"), filepath.Join(dir, "new"))
	if msg.Succeeded() {
		t.Fatal("expected failure for nonexistent source")
	}
}

func TestViewModel_RenamePath_TargetExists(t *testing.T) {
	dir := t.TempDir()
	app := newTestAppWithFileOps(t, dir)

	oldPath := filepath.Join(dir, "old.txt")
	newPath := filepath.Join(dir, "new.txt")
	if err := os.WriteFile(oldPath, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := app.RenamePath(oldPath, newPath)
	if msg.Succeeded() {
		t.Fatal("expected failure when target already exists")
	}
}

func TestViewModel_DeletePath_NotFound(t *testing.T) {
	dir := t.TempDir()
	app := newTestAppWithFileOps(t, dir)

	msg := app.DeletePath(filepath.Join(dir, "nonexistent"))
	if msg.Succeeded() {
		t.Fatal("expected failure for nonexistent path")
	}
}

func TestViewModel_CreateFile_AlreadyExists(t *testing.T) {
	dir := t.TempDir()
	app := newTestAppWithFileOps(t, dir)

	path := filepath.Join(dir, "existing.txt")
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := app.CreateFile(path)
	if msg.Succeeded() {
		t.Fatal("expected failure for already-existing file")
	}
}

func TestViewModel_CreateFolder_Nested(t *testing.T) {
	dir := t.TempDir()
	app := newTestAppWithFileOps(t, dir)

	path := filepath.Join(dir, "a", "b", "c")
	msg := app.CreateFolder(path)
	if !msg.Succeeded() {
		t.Fatalf("CreateFolder nested failed: %v", msg.Err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("nested folder does not exist: %v", err)
	}
}

func TestViewModel_DeletePath_Directory(t *testing.T) {
	dir := t.TempDir()
	app := newTestAppWithFileOps(t, dir)

	// Create a directory with files.
	dirPath := filepath.Join(dir, "delete-dir")
	if err := os.Mkdir(dirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirPath, "file.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := app.DeletePath(dirPath)
	if !msg.Succeeded() {
		t.Fatalf("DeletePath for directory failed: %v", msg.Err)
	}

	if _, err := os.Stat(dirPath); !os.IsNotExist(err) {
		t.Fatal("directory still exists after deletion")
	}
}

func TestViewModel_ApplyWorkspaceEdit_MultipleFiles(t *testing.T) {
	dir := t.TempDir()
	app := newTestAppWithFileOps(t, dir)

	// Create multiple files.
	for _, name := range []string{"a.txt", "b.txt"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Apply edits to all files.
	edits := []workspace.FileEdit{
		{Path: filepath.Join(dir, "a.txt"), OldContent: "old", NewContent: "new-a"},
		{Path: filepath.Join(dir, "b.txt"), OldContent: "old", NewContent: "new-b"},
	}
	msg := app.ApplyWorkspaceEdit(edits)
	if !msg.Succeeded() {
		t.Fatalf("ApplyWorkspaceEdit failed: %v", msg.Err)
	}

	// Verify all files were updated.
	for _, tc := range []struct {
		name    string
		content string
	}{
		{"a.txt", "new-a"},
		{"b.txt", "new-b"},
	} {
		data, err := os.ReadFile(filepath.Join(dir, tc.name))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != tc.content {
			t.Fatalf("%s: got %q, want %q", tc.name, string(data), tc.content)
		}
	}
}

// ── helpers ──

func newTestAppWithFileOps(t *testing.T, dir string) *App {
	t.Helper()
	return newTestAppWithFileOpsAndBus(t, dir, nil)
}

func newTestAppWithFileOpsAndBus(t *testing.T, dir string, bus *workspace.EventBus) *App {
	t.Helper()
	th := theme.DefaultDark()
	wmgr := wm.NewWindowManager()

	ws := &workspace.Workspace{
		RootPath:  dir,
		AgentMgr:  workspace.NewAgentManager(),
		PaneMgr:   workspace.NewPaneManager(),
		StartTime: time.Now(),
		FileOps:   workspace.NewFileOpsService(dir, bus, &testLogger{}),
	}
	if bus != nil {
		ws.SetEventBus(bus)
	}

	app := &App{
		Workspace:          ws,
		AgentView:          NewAgentViewState(ws.AgentMgr),
		FocusStack:         NewFocusStack(),
		ActiveSession:      &workspace.Session{},
		Theme:              th,
		Tree:               NewTreeState(nil, 30),
		OverlayState:       NewOverlayState(),
		LSPPopup:           NewLSPPopupState(),
		EditorState:        NewEditorState(),
		actionRegistry:     ui.DefaultRegistry(),
		windowManager:      wmgr,
		overlayHost:        ui.NewOverlayHost(wmgr),
		subPaneProjections: newSubPaneProjectionRegistry(th),
	}

	app.Win = window.New(th)

	return app
}
