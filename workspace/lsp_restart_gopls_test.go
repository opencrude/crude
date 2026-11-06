package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/types"
)

// The editor's own path against a real server. A search asked while the
// server is still starting, or right after a restart, waits for the server and
// answers on the first try. It must not fail with "needs a synchronized
// language server". Skipped where gopls is not installed.
func TestEditorNavigationWaitsForRealServerAfterStartAndRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real language server")
	}
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls is not installed")
	}
	root := t.TempDir()
	write := func(name, text string) string {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	write("go.mod", "module probe\n\ngo 1.22\n")
	write("lib.go", "package probe\n\nfunc Answer() int { return 42 }\n")
	main := write("main.go", "package probe\n\nvar x int = Answer() + 1\n")
	m := NewLSPManager(nil)
	t.Cleanup(m.Close)
	probeSources(t, m, root)
	m.SetWorkspaceRoot(root)
	trustAnalysis(t, m, root)
	m.SetSettings(types.DefaultLSPSettings())
	m.StartForWorkspace(root)
	editorSource(t, m, main, "package probe\n\nvar x int = Answer() + 1\n")

	// One request, no retry: waiting is the manager's job.
	once := func(label string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
		defer cancel()
		refs, err := m.RequestNavigation(ctx, main, types.LSPNavigationReferences, 2, 12, true)
		if err != nil || len(refs.Targets) != 2 {
			t.Fatalf("%s: references = %+v err=%v health=%+v", label, refs.Targets, err, m.ServerHealthSnapshots())
		}
	}
	once("first start")
	for _, label := range []string{"after restart", "after second restart"} {
		health := m.ServerHealthSnapshots()
		if len(health) == 0 || !m.RestartServer(health[0].ID) {
			t.Fatalf("%s: restart refused: %+v", label, health)
		}
		once(label)
	}
}

// Exercise an actual quick fix, not only a fake edit-shaped server answer.
// The server must offer an edit that reaches the normal review and file owner.
func TestRealServerQuickFixWorksAfterRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real language server")
	}
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls is not installed")
	}
	f := newCodeActionFixture(t, "package probe\n\nimport \"fmt\"\n\nvar answer = 42\n")
	root := filepath.Dir(f.path)
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module probe\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := f.workspace.LSPMgr
	m.SetRuntime(defaultLSPRuntime{})
	m.StartForWorkspace(root)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	if err := m.awaitSynchronized(ctx, f.path); err != nil {
		t.Fatal(err)
	}
	health := m.ServerHealthSnapshots()
	if len(health) == 0 || !m.RestartServer(health[0].ID) {
		t.Fatal("could not restart the real server")
	}
	set, err := f.workspace.RequestLSPCodeActions(ctx, f.owner, f.source, 22, 22)
	if err != nil {
		t.Fatal(err)
	}
	index := -1
	for _, row := range set.Items {
		t.Logf("action %q review=%t resolve=%t reason=%s", row.Title, row.CanReview, row.NeedsResolve, row.UnsupportedReason)
		if row.Kind == "source.organizeImports" && row.CanReview {
			index = row.Index
		}
	}
	if index < 0 {
		t.Fatalf("no usable organize-imports action: %+v", set.Items)
	}
	preview, err := f.workspace.PrepareLSPCodeAction(ctx, f.owner, set.ID, set.Digest, index)
	if err != nil {
		t.Fatal(err)
	}
	result := f.workspace.LiveDocuments.ApplyReviewedEdit(ctx, f.owner, preview.ID, preview.Digest,
		f.workspace.LocalHistory, NewFileOpsService(root, nil, nil))
	if result.State != "committed" || result.SafetySnapshotID == "" {
		t.Fatalf("quick fix did not commit with recovery: %+v", result)
	}
	disk, err := os.ReadFile(f.path)
	if err != nil || string(disk) != "package probe\n\nvar answer = 42\n" {
		t.Fatalf("quick fix did not remove the unused import: %q, %v", disk, err)
	}
}

// A real gopls code action can be command-based. Crude must execute it once,
// capture the server's create-file edit, review it and apply it transactionally.
func TestRealServerAddTestCommandReachesReviewedCreate(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real language server")
	}
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls is not installed")
	}
	text := "package probe\n\nimport \"context\"\n\n// AsyncHandler runs each call in its own goroutine.\nfunc AsyncHandler(h func(context.Context)) func(context.Context) {\n\treturn func(ctx context.Context) { go h(ctx) }\n}\n"
	f := newCodeActionFixture(t, text)
	root := filepath.Dir(f.path)
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module probe\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := f.workspace.LSPMgr
	m.SetRuntime(defaultLSPRuntime{})
	m.StartForWorkspace(root)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	if err := m.awaitSynchronized(ctx, f.path); err != nil {
		t.Fatal(err)
	}
	line := strings.Count(text[:strings.Index(text, "func A")], "\n") + 1
	offset := strings.Index(text, "func A") + len("func A")
	set, err := f.workspace.RequestLSPCodeActions(ctx, f.owner, f.source, offset, offset)
	if err != nil {
		t.Fatal(err)
	}
	index := -1
	for _, row := range set.Items {
		t.Logf("action %q review=%t resolve=%t reason=%s", row.Title, row.CanReview, row.NeedsResolve, row.UnsupportedReason)
		if strings.HasPrefix(row.Title, "Add test for AsyncHandler") && row.CanReview {
			index = row.Index
		}
	}
	if index < 0 {
		t.Fatalf("gopls did not offer the command action at line %d: %+v", line+1, set.Items)
	}
	preview, err := f.workspace.PrepareLSPCodeAction(ctx, f.owner, set.ID, set.Digest, index)
	if err != nil {
		t.Fatal(err)
	}
	if preview.ID == "" || len(preview.Files) != 1 || !preview.Files[0].Created ||
		!strings.HasSuffix(preview.Files[0].Path, "_test.go") {
		t.Fatalf("add-test action was not a reviewed create: %+v", preview)
	}
	result := f.workspace.LiveDocuments.ApplyReviewedEdit(ctx, f.owner, preview.ID, preview.Digest,
		f.workspace.LocalHistory, NewFileOpsService(root, nil, nil))
	if result.State != "committed" || result.Error != "" {
		t.Fatalf("add-test action did not commit: %+v", result)
	}
	if data, err := os.ReadFile(preview.Files[0].Path); err != nil || !strings.Contains(string(data), "func TestAsyncHandler") {
		t.Fatalf("generated test file = %q, %v", data, err)
	}
}
