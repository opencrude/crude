package worktree

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitTest(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, b)
	}
	return string(b)
}
func put(t *testing.T, root, path string, data []byte) {
	t.Helper()
	p := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0644); err != nil {
		t.Fatal(err)
	}
}
func fixture(t *testing.T) (string, string, *Service) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "repo with spaces")
	store := filepath.Join(base, "artifacts")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	gitTest(t, root, "init")
	gitTest(t, root, "config", "user.email", "test@example.invalid")
	gitTest(t, root, "config", "user.name", "Test")
	put(t, root, "a.txt", []byte("committed\n"))
	put(t, root, "deleted", []byte("old"))
	put(t, root, "binary", []byte{0, 1, 2})
	put(t, root, ".gitignore", []byte("secret\n"))
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-m", "initial")
	s, err := Open(root, store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return root, store, s
}

var testOwner = Owner{AgentID: "exact-agent", RunID: "exact-run"}

func TestFaithfulBaselineAndDelta(t *testing.T) {
	root, store, s := fixture(t)
	ctx := context.Background()
	put(t, root, "a.txt", []byte("staged human\n"))
	gitTest(t, root, "add", "a.txt")
	put(t, root, "a.txt", []byte("unstaged human\n"))
	put(t, root, "binary", []byte{0, 9, 8})
	put(t, root, "new human", []byte("untracked"))
	put(t, root, "secret", []byte("credential"))
	if err := os.Remove(filepath.Join(root, "deleted")); err != nil {
		t.Fatal(err)
	}
	before := gitTest(t, root, "status", "--porcelain=v1")
	docs := map[string]Document{"a.txt": {Revision: 7, Exists: true, Data: []byte("accepted unsaved\n")}}
	c, err := s.Create(ctx, Request{Owner: testOwner, Mode: Isolated, Documents: docs})
	if err != nil {
		t.Fatal(err)
	}
	if !c.Ready || c.Owner != testOwner || c.Root == root {
		t.Fatalf("bad context: %+v", c)
	}
	fi, err := os.Lstat(filepath.Join(c.Root, ".git"))
	if err != nil || !fi.Mode().IsRegular() {
		t.Fatal("not a linked worktree Git pointer")
	}
	for p, want := range map[string][]byte{"a.txt": []byte("accepted unsaved\n"), "binary": {0, 9, 8}, "new human": []byte("untracked")} {
		got, err := os.ReadFile(filepath.Join(c.Root, p))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s: %q %v", p, got, err)
		}
	}
	for _, p := range []string{"deleted", "secret"} {
		if _, err := os.Stat(filepath.Join(c.Root, p)); !os.IsNotExist(err) {
			t.Fatalf("unexpected %s: %v", p, err)
		}
	}
	if before != gitTest(t, root, "status", "--porcelain=v1") {
		t.Fatal("source index/dirty bytes changed")
	}
	p, err := s.Review(ctx, c.ID)
	if err != nil || len(p.Changes) != 0 {
		t.Fatalf("inherited changes in delta: %+v %v", p, err)
	}
	put(t, c.Root, "a.txt", []byte("child result\n"))
	put(t, c.Root, "child new", []byte{0, 3, 4})
	p, err = s.Review(ctx, c.ID)
	if err != nil || len(p.Changes) != 2 {
		t.Fatalf("child delta: %+v %v", p, err)
	}
	if err = s.ValidateDestination(ctx, p, docs); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Apply(ctx, c.ID, docs); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "a.txt"))
	if string(got) != "unstaged human\n" {
		t.Fatal("plan-only Apply mutated destination")
	}
	newDocs := map[string]Document{"a.txt": {Revision: 8, Exists: true, Data: []byte("human newer\n")}}
	if err = s.ValidateDestination(ctx, p, newDocs); err == nil {
		t.Fatal("new accepted document was overwritten")
	}
	put(t, root, "child new", []byte("new human collision"))
	if err = s.ValidateDestination(ctx, p, docs); err == nil {
		t.Fatal("new disk file collision accepted")
	}
	put(t, c.Root, "a.txt", []byte("child newer\n"))
	if err = s.ValidateDestination(ctx, p, docs); err == nil || !strings.Contains(err.Error(), "source changed") {
		t.Fatalf("stale source: %v", err)
	}
	// Returned contexts cannot mutate the authoritative baseline.
	c.Baseline["a.txt"] = File{}
	saved, err := s.Inspect(c.ID)
	if err != nil || saved.Baseline["a.txt"].Hash == "" {
		t.Fatal("baseline aliased")
	}
	if err = s.Cleanup(ctx, c.ID, testOwner); err == nil {
		t.Fatal("active context removed")
	}
	if err = s.Unpin(c.ID, testOwner); err != nil {
		t.Fatal(err)
	}
	if err = s.Cleanup(ctx, c.ID, testOwner); err == nil {
		t.Fatal("dirty context removed")
	}
	// Passive reopen works without a Git executable and preserves crash facts.
	t.Setenv("PATH", t.TempDir())
	reopened, err := Open(root, store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	r, err := reopened.Inspect(c.ID)
	if err != nil || r.Removed || len(r.Pins) != 0 || r.Baseline["a.txt"].DocumentRevision != 7 {
		t.Fatalf("bad reopen: %+v %v", r, err)
	}
}

func TestCleanupOwnershipPinsAndKeep(t *testing.T) {
	_, _, s := fixture(t)
	ctx := context.Background()
	c, err := s.Create(ctx, Request{Owner: testOwner, Mode: Isolated})
	if err != nil {
		t.Fatal(err)
	}
	foreign := Owner{AgentID: testOwner.AgentID, RunID: "replacement-run"}
	if err = s.Unpin(c.ID, foreign); err == nil {
		t.Fatal("foreign unpin succeeded")
	}
	if err = s.Cleanup(ctx, c.ID, foreign); err == nil {
		t.Fatal("foreign cleanup succeeded")
	}
	if err = s.Unpin(c.ID, testOwner); err != nil {
		t.Fatal(err)
	}
	if err = s.Cleanup(ctx, c.ID, testOwner); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(c.Root); !os.IsNotExist(err) {
		t.Fatalf("checkout remains: %v", err)
	}
	saved, err := s.Inspect(c.ID)
	if err != nil || !saved.Removed {
		t.Fatal("removal not persisted")
	}
	c, err = s.Create(ctx, Request{Owner: testOwner, Mode: Isolated})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Keep(c.ID, testOwner); err != nil {
		t.Fatal(err)
	}
	if err = s.Unpin(c.ID, testOwner); err != nil {
		t.Fatal(err)
	}
	if err = s.Cleanup(ctx, c.ID, testOwner); err == nil {
		t.Fatal("kept artifact removed")
	}
}

func TestUnsupportedAndBudgets(t *testing.T) {
	root, store, s := fixture(t)
	ctx := context.Background()
	var err error
	if _, err = s.Create(ctx, Request{Mode: Isolated}); err == nil {
		t.Fatal("missing owner accepted")
	}
	if _, err = Open(root, filepath.Join(root, ".crude"), Options{}); err == nil {
		t.Fatal("in-project store accepted")
	}
	if _, err = s.Create(ctx, Request{Owner: testOwner, Mode: Isolated, IncludeIgnored: []string{".git/config"}}); err == nil {
		t.Fatal("Git admin copied")
	}
	if err = os.Symlink("a.txt", filepath.Join(root, "link")); err != nil {
		t.Skip(err)
	}
	if _, err = s.Create(ctx, Request{Owner: testOwner, Mode: Isolated}); err == nil {
		t.Fatal("symlink accepted")
	}
	if err = os.Remove(filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	limited, err := Open(root, store, Options{MaxFileBytes: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = limited.Create(ctx, Request{Owner: testOwner, Mode: Isolated}); err == nil {
		t.Fatal("budget ignored")
	}
	gitTest(t, root, "config", "filter.untrusted.clean", "touch should-not-run")
	if _, err = s.Create(ctx, Request{Owner: testOwner, Mode: Isolated}); err == nil {
		t.Fatal("configured filter accepted")
	}
}

func TestModeResolutionAndIgnoredOptIn(t *testing.T) {
	root, _, s := fixture(t)
	ctx := context.Background()
	put(t, root, "secret", []byte("explicit inclusion"))
	c, err := s.Create(ctx, Request{Owner: testOwner, Mode: Auto, Independent: true, IncludeIgnored: []string{"secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if c.Mode != Isolated {
		t.Fatal("independent auto did not isolate")
	}
	b, err := os.ReadFile(filepath.Join(c.Root, "secret"))
	if err != nil || string(b) != "explicit inclusion" {
		t.Fatal("opt-in ignored file missing")
	}
	c, err = s.Create(ctx, Request{Owner: testOwner, Mode: Auto})
	if err != nil || c.Mode != Shared || c.Root != root {
		t.Fatalf("coupled auto: %+v %v", c, err)
	}
	base := t.TempDir()
	project := filepath.Join(base, "plain")
	if err = os.Mkdir(project, 0755); err != nil {
		t.Fatal(err)
	}
	plain, err := Open(project, filepath.Join(base, "store"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	c, err = plain.Create(ctx, Request{Owner: testOwner, Mode: Auto, Independent: true})
	if err != nil || c.Mode != Shared {
		t.Fatalf("non-Git auto: %+v %v", c, err)
	}
	if _, err = plain.Create(ctx, Request{Owner: testOwner, Mode: Isolated}); err == nil {
		t.Fatal("non-Git isolated accepted")
	}
}
