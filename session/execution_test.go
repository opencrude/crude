package session

import (
	"crude/agent"
	"crude/safety"
	"crude/worktree"
	"path/filepath"
	"testing"
)

func TestExecutionAutoRequiresServiceAndExplicitSharedKeepsRoot(t *testing.T) {
	s := &Session{}
	s.SetWorkDir(t.TempDir())
	r := &SubAgentRunner{session: s}
	for _, mode := range []string{"", string(worktree.Auto), string(worktree.Isolated), "invalid"} {
		spec := agent.SubAgentSpec{Isolation: mode}
		if err := r.prepareChildExecution(t.Context(), &spec, "run"); err == nil {
			t.Fatalf("mode %q silently shared", mode)
		}
	}
	spec := agent.SubAgentSpec{Isolation: string(worktree.Shared), FileOwnership: []string{"owned.go"}}
	if err := r.prepareChildExecution(t.Context(), &spec, "run"); err != nil {
		t.Fatal(err)
	}
	if spec.ExecutionRoot != s.WorkDir() || len(spec.FileOwnership) != 1 || spec.FileOwnership[0] != filepath.Join(s.WorkDir(), "owned.go") {
		t.Fatalf("explicit shared %+v", spec)
	}
}

func TestExecutionDefaultClassifiesSelectedIndependentWriters(t *testing.T) {
	base := agent.SubAgentSpec{ParentID: "parent", Category: agent.CatCrafter, Policy: agent.ComposePolicy(safety.DefaultPolicy(), "no-delete", agent.CatCrafter), Model: "fixture"}
	if !childIndependentWriter(base) {
		t.Fatal("delegated writer not isolated by auto")
	}
	base.AllowedTools = []string{"read_file", "execute_command", "publish_report"}
	if childIndependentWriter(base) {
		t.Fatal("read/command-only assignment incorrectly classified as file writer")
	}
	base.AllowedTools = nil
	base.ParentID = ""
	if childIndependentWriter(base) {
		t.Fatal("standalone writer incorrectly classified as parallel child")
	}
}

func TestPatchAlwaysGrantsUseExactExecutionRoot(t *testing.T) {
	s, err := NewWithSessionDir("patch-root-grants", testConfig(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	logical, isolated := t.TempDir(), t.TempDir()
	s.SetWorkDir(logical)
	const patch = "*** Begin Patch\n*** Add File: scoped.txt\n+value\n*** End Patch"
	s.grantPatchAlwaysAtRoot(isolated, patch)
	if !s.patchAlwaysAllowedAtRoot(isolated, patch) {
		t.Fatal("exact isolated grant absent")
	}
	if s.patchAlwaysAllowedAtRoot(logical, patch) {
		t.Fatal("isolated grant widened to logical checkout")
	}
}
