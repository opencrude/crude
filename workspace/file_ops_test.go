package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── FileOpsService tests ──

func TestFileOpsService_CreateFile(t *testing.T) {
	dir := t.TempDir()
	bus := NewEventBus()
	svc := NewFileOpsService(dir, bus, testLogger{})

	// Create a file.
	path := filepath.Join(dir, "new.txt")
	msg := svc.CreateFile(path)
	if !msg.Succeeded() {
		t.Fatalf("CreateFile failed: %v", msg.Err)
	}
	if msg.Op != FileOpCreate {
		t.Fatalf("expected FileOpCreate, got %v", msg.Op)
	}

	// Verify file exists.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file does not exist after CreateFile: %v", err)
	}
}

func TestFileOpsService_CreateFile_AlreadyExists(t *testing.T) {
	dir := t.TempDir()
	bus := NewEventBus()
	svc := NewFileOpsService(dir, bus, testLogger{})

	// Create a file first.
	path := filepath.Join(dir, "existing.txt")
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Try to create again.
	msg := svc.CreateFile(path)
	if msg.Succeeded() {
		t.Fatal("expected failure for already-existing file")
	}
	if msg.Detail != "already exists" {
		t.Fatalf("expected detail 'already exists', got %q", msg.Detail)
	}
}

func TestFileOpsService_CreateFile_OutsideRoot(t *testing.T) {
	dir := t.TempDir()
	bus := NewEventBus()
	svc := NewFileOpsService(dir, bus, testLogger{})

	// Try to create a file outside workspace root.
	msg := svc.CreateFile("/tmp/outside-workspace.txt")
	if msg.Succeeded() {
		t.Fatal("expected failure for path outside workspace root")
	}
}

func TestFileOpsService_CreateFile_RelativePath(t *testing.T) {
	dir := t.TempDir()
	bus := NewEventBus()
	svc := NewFileOpsService(dir, bus, testLogger{})

	// Try to create with relative path.
	msg := svc.CreateFile("relative/path.txt")
	if msg.Succeeded() {
		t.Fatal("expected failure for relative path")
	}
}

func TestFileOpsService_CreateFile_EmptyPath(t *testing.T) {
	dir := t.TempDir()
	bus := NewEventBus()
	svc := NewFileOpsService(dir, bus, testLogger{})

	msg := svc.CreateFile("")
	if msg.Succeeded() {
		t.Fatal("expected failure for empty path")
	}
}

func TestFileOpsService_CreateFile_PublishesEvent(t *testing.T) {
	dir := t.TempDir()
	bus := NewEventBus()
	svc := NewFileOpsService(dir, bus, testLogger{})

	path := filepath.Join(dir, "event.txt")
	svc.CreateFile(path)

	// Check that an event was published.
	events := bus.RecentEvents(1)
	if len(events) == 0 {
		t.Fatal("no event published for CreateFile")
	}
}

func TestFileOpsService_CreateFolder(t *testing.T) {
	dir := t.TempDir()
	bus := NewEventBus()
	svc := NewFileOpsService(dir, bus, testLogger{})

	// Create a folder.
	path := filepath.Join(dir, "subdir")
	msg := svc.CreateFolder(path)
	if !msg.Succeeded() {
		t.Fatalf("CreateFolder failed: %v", msg.Err)
	}
	if msg.Op != FileOpCreateFolder {
		t.Fatalf("expected FileOpCreateFolder, got %v", msg.Op)
	}
	if !msg.IsDir {
		t.Fatal("expected IsDir=true for folder creation")
	}

	// Verify folder exists.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("folder does not exist after CreateFolder: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("created path is not a directory")
	}
}

func TestFileOpsService_CreateFolder_Nested(t *testing.T) {
	dir := t.TempDir()
	bus := NewEventBus()
	svc := NewFileOpsService(dir, bus, testLogger{})

	// Create nested folders.
	path := filepath.Join(dir, "a", "b", "c")
	msg := svc.CreateFolder(path)
	if !msg.Succeeded() {
		t.Fatalf("CreateFolder nested failed: %v", msg.Err)
	}

	// Verify nested folder exists.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("nested folder does not exist: %v", err)
	}
}

func TestFileOpsService_CreateFolder_AlreadyExists(t *testing.T) {
	dir := t.TempDir()
	bus := NewEventBus()
	svc := NewFileOpsService(dir, bus, testLogger{})

	path := filepath.Join(dir, "existing")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}

	msg := svc.CreateFolder(path)
	if msg.Succeeded() {
		t.Fatal("expected failure for already-existing folder")
	}
}

func TestFileOpsService_RenamePath(t *testing.T) {
	dir := t.TempDir()
	bus := NewEventBus()
	svc := NewFileOpsService(dir, bus, testLogger{})

	// Create a file to rename.
	oldPath := filepath.Join(dir, "old.txt")
	if err := os.WriteFile(oldPath, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	newPath := filepath.Join(dir, "new.txt")
	msg := svc.RenamePath(oldPath, newPath)
	if !msg.Succeeded() {
		t.Fatalf("RenamePath failed: %v", msg.Err)
	}
	if msg.Op != FileOpRename {
		t.Fatalf("expected FileOpRename, got %v", msg.Op)
	}
	if msg.OldPath != oldPath {
		t.Fatalf("OldPath mismatch: got %q", msg.OldPath)
	}
	if msg.NewPath != newPath {
		t.Fatalf("NewPath mismatch: got %q", msg.NewPath)
	}

	// Verify old path no longer exists.
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatal("old path still exists after rename")
	}
	// Verify new path exists.
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("new path does not exist after rename: %v", err)
	}
}

func TestFileOpsService_RenamePath_SourceNotFound(t *testing.T) {
	dir := t.TempDir()
	bus := NewEventBus()
	svc := NewFileOpsService(dir, bus, testLogger{})

	msg := svc.RenamePath(filepath.Join(dir, "nonexistent"), filepath.Join(dir, "new"))
	if msg.Succeeded() {
		t.Fatal("expected failure for nonexistent source")
	}
}

func TestFileOpsService_RenamePath_TargetExists(t *testing.T) {
	dir := t.TempDir()
	bus := NewEventBus()
	svc := NewFileOpsService(dir, bus, testLogger{})

	oldPath := filepath.Join(dir, "old.txt")
	newPath := filepath.Join(dir, "new.txt")
	if err := os.WriteFile(oldPath, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := svc.RenamePath(oldPath, newPath)
	if msg.Succeeded() {
		t.Fatal("expected failure when target already exists")
	}
}

func TestFileOpsService_RenamePath_Directory(t *testing.T) {
	dir := t.TempDir()
	bus := NewEventBus()
	svc := NewFileOpsService(dir, bus, testLogger{})

	oldDir := filepath.Join(dir, "olddir")
	newDir := filepath.Join(dir, "newdir")
	if err := os.Mkdir(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}

	msg := svc.RenamePath(oldDir, newDir)
	if !msg.Succeeded() {
		t.Fatalf("RenamePath for directory failed: %v", msg.Err)
	}
	if !msg.IsDir {
		t.Fatal("expected IsDir=true for directory rename")
	}
}

func TestFileOpsService_DeletePath_File(t *testing.T) {
	dir := t.TempDir()
	bus := NewEventBus()
	svc := NewFileOpsService(dir, bus, testLogger{})

	path := filepath.Join(dir, "delete-me.txt")
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := svc.DeletePath(path)
	if !msg.Succeeded() {
		t.Fatalf("DeletePath failed: %v", msg.Err)
	}
	if msg.Op != FileOpDelete {
		t.Fatalf("expected FileOpDelete, got %v", msg.Op)
	}
	if msg.IsDir {
		t.Fatal("expected IsDir=false for file deletion")
	}

	// Verify file no longer exists.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file still exists after deletion")
	}
}

func TestFileOpsService_DeletePath_Directory(t *testing.T) {
	dir := t.TempDir()
	bus := NewEventBus()
	svc := NewFileOpsService(dir, bus, testLogger{})

	// Create a directory with files.
	dirPath := filepath.Join(dir, "delete-dir")
	if err := os.Mkdir(dirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirPath, "file.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := svc.DeletePath(dirPath)
	if !msg.Succeeded() {
		t.Fatalf("DeletePath for directory failed: %v", msg.Err)
	}
	if !msg.IsDir {
		t.Fatal("expected IsDir=true for directory deletion")
	}

	// Verify directory no longer exists.
	if _, err := os.Stat(dirPath); !os.IsNotExist(err) {
		t.Fatal("directory still exists after deletion")
	}
}

func TestFileOpsService_DeletePath_NotFound(t *testing.T) {
	dir := t.TempDir()
	bus := NewEventBus()
	svc := NewFileOpsService(dir, bus, testLogger{})

	msg := svc.DeletePath(filepath.Join(dir, "nonexistent"))
	if msg.Succeeded() {
		t.Fatal("expected failure for nonexistent path")
	}
}

func TestFileOpsService_ApplyWorkspaceEdit(t *testing.T) {
	dir := t.TempDir()
	bus := NewEventBus()
	svc := NewFileOpsService(dir, bus, testLogger{})

	// Create a file to edit.
	path := filepath.Join(dir, "edit.txt")
	if err := os.WriteFile(path, []byte("old content"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Apply edit.
	edits := []FileEdit{
		{Path: path, OldContent: "old content", NewContent: "new content"},
	}
	msg := svc.ApplyWorkspaceEdit(edits)
	if !msg.Succeeded() {
		t.Fatalf("ApplyWorkspaceEdit failed: %v", msg.Err)
	}
	if msg.Op != FileOpApplyEdit {
		t.Fatalf("expected FileOpApplyEdit, got %v", msg.Op)
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

func TestFileOpsService_ApplyWorkspaceEdit_ContentMismatch(t *testing.T) {
	dir := t.TempDir()
	bus := NewEventBus()
	svc := NewFileOpsService(dir, bus, testLogger{})

	path := filepath.Join(dir, "mismatch.txt")
	if err := os.WriteFile(path, []byte("actual content"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Apply edit with wrong old content.
	edits := []FileEdit{
		{Path: path, OldContent: "expected content", NewContent: "new content"},
	}
	msg := svc.ApplyWorkspaceEdit(edits)
	if msg.Succeeded() {
		t.Fatal("expected failure for content mismatch")
	}
	if !strings.Contains(msg.Error(), "content mismatch") {
		t.Fatalf("expected 'content mismatch' error, got: %v", msg.Err)
	}
}

func TestFileOpsService_ApplyWorkspaceEdit_MultipleFiles(t *testing.T) {
	dir := t.TempDir()
	bus := NewEventBus()
	svc := NewFileOpsService(dir, bus, testLogger{})

	// Create multiple files.
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Apply edits to all files.
	edits := []FileEdit{
		{Path: filepath.Join(dir, "a.txt"), OldContent: "old", NewContent: "new-a"},
		{Path: filepath.Join(dir, "b.txt"), OldContent: "old", NewContent: "new-b"},
		{Path: filepath.Join(dir, "c.txt"), OldContent: "old", NewContent: "new-c"},
	}
	msg := svc.ApplyWorkspaceEdit(edits)
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
		{"c.txt", "new-c"},
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

func TestFileOpsService_ApplyWorkspaceEdit_EmptyEdits(t *testing.T) {
	dir := t.TempDir()
	bus := NewEventBus()
	svc := NewFileOpsService(dir, bus, testLogger{})

	msg := svc.ApplyWorkspaceEdit(nil)
	if msg.Succeeded() {
		t.Fatal("expected failure for empty edits")
	}
}

// ── Workspace integration tests ──

func TestWorkspace_CreateFile(t *testing.T) {
	dir := t.TempDir()
	ws := testWorkspace(t, dir)
	bus := NewEventBus()
	ws.SetEventBus(bus)

	path := filepath.Join(dir, "ws-file.txt")
	msg := ws.CreateFile(path)
	if !msg.Succeeded() {
		t.Fatalf("Workspace.CreateFile failed: %v", msg.Err)
	}

	// Verify file exists.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file does not exist: %v", err)
	}
}

func TestWorkspace_CreateFolder(t *testing.T) {
	dir := t.TempDir()
	ws := testWorkspace(t, dir)
	bus := NewEventBus()
	ws.SetEventBus(bus)

	path := filepath.Join(dir, "ws-folder")
	msg := ws.CreateFolder(path)
	if !msg.Succeeded() {
		t.Fatalf("Workspace.CreateFolder failed: %v", msg.Err)
	}

	// Verify folder exists.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("folder does not exist: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("created path is not a directory")
	}
}

func TestWorkspace_RenamePath(t *testing.T) {
	dir := t.TempDir()
	ws := testWorkspace(t, dir)
	bus := NewEventBus()
	ws.SetEventBus(bus)

	oldPath := filepath.Join(dir, "old.txt")
	newPath := filepath.Join(dir, "new.txt")
	if err := os.WriteFile(oldPath, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := ws.RenamePath(oldPath, newPath)
	if !msg.Succeeded() {
		t.Fatalf("Workspace.RenamePath failed: %v", msg.Err)
	}

	// Verify rename.
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatal("old path still exists")
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("new path does not exist: %v", err)
	}
}

func TestWorkspace_DeletePath(t *testing.T) {
	dir := t.TempDir()
	ws := testWorkspace(t, dir)
	bus := NewEventBus()
	ws.SetEventBus(bus)

	path := filepath.Join(dir, "delete.txt")
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := ws.DeletePath(path)
	if !msg.Succeeded() {
		t.Fatalf("Workspace.DeletePath failed: %v", msg.Err)
	}

	// Verify deletion.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file still exists after deletion")
	}
}

func TestWorkspace_ApplyWorkspaceEdit(t *testing.T) {
	dir := t.TempDir()
	ws := testWorkspace(t, dir)
	bus := NewEventBus()
	ws.SetEventBus(bus)

	path := filepath.Join(dir, "edit.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	edits := []FileEdit{
		{Path: path, OldContent: "old", NewContent: "new"},
	}
	msg := ws.ApplyWorkspaceEdit(edits)
	if !msg.Succeeded() {
		t.Fatalf("Workspace.ApplyWorkspaceEdit failed: %v", msg.Err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Fatalf("content mismatch: got %q", string(data))
	}
}

func TestWorkspace_FileOps_PublishesThroughEventBus(t *testing.T) {
	dir := t.TempDir()
	ws := testWorkspace(t, dir)
	bus := NewEventBus()
	ws.SetEventBus(bus)

	path := filepath.Join(dir, "event-test.txt")
	ws.CreateFile(path)

	// Verify event was published.
	events := bus.RecentEvents(1)
	if len(events) == 0 {
		t.Fatal("no event published through EventBus")
	}
}

// ── helpers ──

func testWorkspace(t *testing.T, dir string) *Workspace {
	t.Helper()
	ws := &Workspace{
		RootPath: dir,
		FileOps:  NewFileOpsService(dir, nil, testLogger{}),
		Logger:   testLogger{},
	}
	return ws
}
