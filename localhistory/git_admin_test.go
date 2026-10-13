package localhistory

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinkedWorktreeHistoryHardExcludesGitAdministration(t *testing.T) {
	root := t.TempDir()
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if b, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("git %v %s", e, b)
		}
	}
	git(root, "init", "-q")
	writeTestFile(t, root, "foo", []byte("base\n"), 0600)
	git(root, "add", ".")
	git(root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "base")
	linked := filepath.Join(t.TempDir(), "checkout")
	git(root, "worktree", "add", "--detach", linked)
	// Relocate the common Git directory inside the linked checkout with a non-.git name.
	// Git's explicit commondir resolves it; user includes must not override it.
	common := filepath.Join(linked, "administration")
	if err := os.Rename(filepath.Join(root, ".git"), common); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, ".git", []byte("gitdir: "+common+"\n"), 0600)
	pointer := filepath.Join(common, "worktrees", filepath.Base(linked))
	writeTestFile(t, linked, ".git", []byte("gitdir: "+pointer+"\n"), 0600)
	writeTestFile(t, pointer, "gitdir", []byte(filepath.Join(linked, ".git")+"\n"), 0600)
	writeTestFile(t, linked, ".crudehistoryignore", []byte("!.git\n!administration/**\n"), 0600)
	store, err := Open(t.TempDir(), linked)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	opts := SnapshotOptions{DisableGitIgnore: true, IncludePaths: []string{".git", "administration"}}
	before, err := store.Snapshot(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	assertClean := func(m Manifest) {
		t.Helper()
		for path := range m.Entries {
			if path == ".git" || strings.HasPrefix(path, ".git/") || path == "administration" || strings.HasPrefix(path, "administration/") {
				t.Fatalf("Git data in history: %s", path)
			}
		}
	}
	assertClean(before)
	ignored, err := store.ShouldIgnore("administration/config", false, opts)
	if err != nil || !ignored {
		t.Fatalf("admin ignore %v %v", ignored, err)
	}
	pointerBefore := readTestFile(t, linked, ".git")
	configBefore := readTestFile(t, common, "config")
	writeTestFile(t, linked, "foo", []byte("changed\n"), 0600)
	plan, err := store.PlanRestore(t.Context(), before.ID, opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range plan.Changes {
		if change.Path != "foo" {
			t.Fatalf("unexpected restore path %s", change.Path)
		}
	}
	result, err := store.ApplyRestore(t.Context(), plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{result.SafetySnapshotID, result.AfterSnapshotID} {
		m, e := store.LoadManifest(id)
		if e != nil {
			t.Fatal(e)
		}
		assertClean(m)
	}
	if string(readTestFile(t, linked, "foo")) != "base\n" {
		t.Fatal("user file not restored")
	}
	if string(readTestFile(t, linked, ".git")) != string(pointerBefore) || string(readTestFile(t, common, "config")) != string(configBefore) {
		t.Fatal("Git metadata changed by restore")
	}
	git(linked, "status", "--porcelain=v1")
}
