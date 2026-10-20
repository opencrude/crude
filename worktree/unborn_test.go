package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A freshly initialized repository has no commit to branch from. Delegated
// work must still launch: shared and auto run in the checkout, and an explicit
// worktree request explains what it needs.
func TestRepositoryWithoutCommitsSharesTheCheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(root, "main.py"), []byte("print(1)\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service, err := Open(root, filepath.Join(t.TempDir(), "store"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []Mode{Auto, Shared} {
		created, err := service.Create(t.Context(), Request{Owner: Owner{AgentID: "child-" + string(mode), RunID: "run-" + string(mode)}, Mode: mode, Independent: true})
		if err != nil {
			t.Fatalf("%s isolation failed without commits: %v", mode, err)
		}
		if created.Mode != Shared || created.BaseCommit != "" {
			t.Fatalf("%s isolation = %+v, want shared without a base commit", mode, created)
		}
		if created.ID != "" || len(created.Baseline) != 0 || len(service.List()) != 0 {
			t.Fatal("shared execution allocated a snapshot", created.ID)
		}
	}
	if _, err := service.Create(t.Context(), Request{Owner: Owner{AgentID: "isolated", RunID: "run-isolated"}, Mode: Isolated}); err == nil || !strings.Contains(err.Error(), "at least one commit") {
		t.Fatalf("explicit worktree without commits: %v", err)
	}
}
