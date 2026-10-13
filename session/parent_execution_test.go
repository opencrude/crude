package session

import (
	"crude/agent"
	"crude/types/control"
	"crude/worktree"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestChildPreparationBindsParentCheckoutAndOwnControlTarget(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if b, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("git %v %s", e, b)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(root, "foo"), []byte("base\ntwo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "base")
	service, err := worktree.Open(root, t.TempDir(), worktree.Options{})
	if err != nil {
		t.Fatal(err)
	}
	a, err := service.Create(t.Context(), worktree.Request{Owner: worktree.Owner{AgentID: "parent", RunID: "parent-run"}, Mode: worktree.Isolated})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.Root, "foo"), []byte("PARENT\ntwo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s := &Session{sessionID: "logical-session"}
	s.SetWorkDir(root)
	s.SetExecutionService(&launchExecutionFixture{Service: service})
	r := &SubAgentRunner{session: s}
	target := &control.Target{WorkspaceID: "workspace", SessionID: "logical-session", ExecutionID: a.ID, Shell: &control.ShellOwner{ClientID: "shell", Epoch: 7}}
	for _, mode := range []worktree.Mode{worktree.Shared, worktree.Isolated} {
		spec := agent.SubAgentSpec{ID: "child", Isolation: string(mode), ControlTarget: target, FileOwnership: []string{"foo"}}
		if err := r.prepareChildExecution(t.Context(), &spec, "child-run-"+string(mode)); err != nil {
			t.Fatal(err)
		}
		if spec.ControlTarget == target || spec.ControlTarget.ExecutionID != spec.ExecutionID || spec.ControlTarget.SessionID != target.SessionID || *spec.ControlTarget.Shell != *target.Shell || target.ExecutionID != a.ID {
			t.Fatalf("target binding %+v", spec.ControlTarget)
		}
		if mode == worktree.Shared && spec.ExecutionRoot != a.Root {
			t.Fatal("shared root not A")
		}
		if spec.FileOwnership[0] != filepath.Join(spec.ExecutionRoot, "foo") {
			t.Fatal("claims not bound to child root")
		}
		cmd := exec.Command("git", "-C", spec.ExecutionRoot, "rev-parse", "--show-toplevel")
		b, e := cmd.Output()
		if e != nil || !sameDirectory(strings.TrimSpace(string(b)), spec.ExecutionRoot) {
			t.Fatalf("actual root %q %v", b, e)
		}
		b, e = os.ReadFile(filepath.Join(spec.ExecutionRoot, "foo"))
		if e != nil || string(b) != "PARENT\ntwo\n" {
			t.Fatalf("read %q %v", b, e)
		}
	}
	if s.WorkDir() != root {
		t.Fatal("logical session globally rebound")
	}
	bad := agent.SubAgentSpec{ID: "bad", Isolation: "shared", ControlTarget: &control.Target{ExecutionID: "unknown"}}
	if err := r.prepareChildExecution(t.Context(), &bad, "bad-run"); err == nil {
		t.Fatal("unknown source launched")
	}
}

// sameDirectory compares directory identity, not spelling: Git reports
// forward-slash paths on Windows.
func sameDirectory(a, b string) bool {
	left, err := os.Stat(a)
	if err != nil {
		return false
	}
	right, err := os.Stat(b)
	return err == nil && os.SameFile(left, right)
}
