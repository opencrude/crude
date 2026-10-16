package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/agent"
	"crude/collaboration"
	"crude/safety"
	"crude/tools"
	"crude/worktree"
)

func TestManagedChildNativeWritesCannotEscapeCheckout(t *testing.T) {
	for _, scoped := range []bool{false, true} {
		t.Run(map[bool]string{false: "unscoped", true: "claimed-outside"}[scoped], func(t *testing.T) {
			s, err := NewWithSessionDir("boundary", testConfig(), t.TempDir(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			logical, checkout := t.TempDir(), t.TempDir()
			s.SetWorkDir(logical)
			store := attachDelegationTestCollaboration(t, s)
			if _, err := s.registerCollaborator(t.Context(), "parent", "Parent", s.sessionID, "", CategoryDefault, s.db); err != nil {
				t.Fatal(err)
			}
			child, err := s.registerCollaborator(t.Context(), "child", "Child", "child", "parent", CategoryCrafter, s.db)
			if err != nil {
				t.Fatal(err)
			}
			run, err := store.StartRun(t.Context(), collaboration.StartRunRequest{AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, RequestID: "assignment", Origin: collaboration.OriginPeer})
			if err != nil {
				t.Fatal(err)
			}
			r := NewSubAgentRunner(s, 1)
			spec := agent.SubAgentSpec{ID: "child", Name: "Child", Isolation: string(worktree.Isolated), ExecutionRoot: checkout, Policy: safety.DefaultPolicy()}
			if scoped {
				spec.FileOwnership = []string{logical, checkout}
				if err := r.fileOwnership.PreClaim(spec.ID, spec.Name, spec.FileOwnership); err != nil {
					t.Fatal(err)
				}
			}
			ctx := context.WithValue(t.Context(), tools.ExecutionIdentityKey{}, tools.ExecutionIdentity{AgentID: child.ID, RunID: run.ID, ToolCallID: "effect"})
			fn := r.buildProxiedToolFn(spec, nil, nil)
			outside := filepath.Join(logical, "victim")
			inside := filepath.Join(checkout, "source")
			if err := os.WriteFile(outside, []byte("original\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(inside, []byte("original\n"), 0600); err != nil {
				t.Fatal(err)
			}
			for _, test := range []struct {
				name   string
				params map[string]any
			}{
				{"write_file", map[string]any{"path": outside, "content": "escaped\n"}},
				{"edit_file", map[string]any{"path": outside, "old_string": "original", "new_string": "escaped"}},
				{"delete_file", map[string]any{"path": outside}},
				{"copy_file", map[string]any{"source": inside, "destination": outside}},
				{"move_file", map[string]any{"source": inside, "destination": outside}},
				{"move_file", map[string]any{"source": outside, "destination": filepath.Join(checkout, "moved")}},
				{"apply_patch", map[string]any{"input": "*** Begin Patch\n*** Add File: neighbor\n+safe\n*** Update File: " + outside + "\n@@\n-original\n+escaped\n*** End Patch"}},
				{"write_file", map[string]any{"path": filepath.Join("..", filepath.Base(logical), "victim"), "content": "escaped\n"}},
			} {
				if _, err := fn(ctx, test.name, test.params); err == nil || !strings.Contains(err.Error(), "execution checkout") {
					t.Fatalf("%s escape not rejected by checkout boundary: %v", test.name, err)
				}
				if data, err := os.ReadFile(outside); err != nil || string(data) != "original\n" {
					t.Fatalf("shared file changed: %q %v", data, err)
				}
				if data, err := os.ReadFile(inside); err != nil || string(data) != "original\n" {
					t.Fatalf("move source changed: %q %v", data, err)
				}
			}
			if _, err := os.Stat(filepath.Join(checkout, "neighbor")); !os.IsNotExist(err) {
				t.Fatalf("multi-target patch partially ran: %v", err)
			}
			if !scoped {
				if claims, err := store.Claims(t.Context(), child.ID, true); err != nil || len(claims) != 0 {
					t.Fatalf("rejected effects leaked claims: %+v %v", claims, err)
				}
			}
			link := filepath.Join(checkout, "escape")
			if err := os.Symlink(logical, link); err == nil {
				if _, err := fn(ctx, "write_file", map[string]any{"path": filepath.Join(link, "victim"), "content": "escaped"}); err == nil {
					t.Fatal("symlink parent bypassed checkout boundary")
				}
				if data, err := os.ReadFile(outside); err != nil || string(data) != "original\n" {
					t.Fatalf("symlink escape changed shared file: %q %v", data, err)
				}
			}
			if _, err := fn(ctx, "write_file", map[string]any{"path": "allowed", "content": "isolated\n"}); err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(filepath.Join(checkout, "allowed")); err != nil || string(data) != "isolated\n" {
				t.Fatalf("checkout write failed: %q %v", data, err)
			}
			if _, err := fn(ctx, "write_file", map[string]any{"path": filepath.Join(r.childTempDir(spec.ID), "scratch"), "content": "scratch"}); err != nil {
				t.Fatalf("private scratch rejected: %v", err)
			}
			sharedSpec := spec
			sharedSpec.Isolation, sharedSpec.ExecutionRoot, sharedSpec.FileOwnership = string(worktree.Shared), logical, nil
			external := filepath.Join(t.TempDir(), "explicit-shared")
			if _, err := r.buildProxiedToolFn(sharedSpec, nil, nil)(ctx, "write_file", map[string]any{"path": external, "content": "shared"}); err != nil {
				t.Fatalf("explicit shared execution lost external-path authority: %v", err)
			}
		})
	}
}

func TestManagedClaimUpdatesUseTargetCheckoutAndRejectSharedPaths(t *testing.T) {
	s, store, parent, child := collaborationContractFixture(t)
	logical, checkout := t.TempDir(), t.TempDir()
	s.SetWorkDir(logical)
	run, err := store.StartRun(t.Context(), collaboration.StartRunRequest{AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, RequestID: "assignment", Origin: collaboration.OriginPeer})
	if err != nil {
		t.Fatal(err)
	}
	s.SetExecutionService(&claimExecutionFixture{contexts: []worktree.Context{{ID: "execution", Owner: worktree.Owner{AgentID: child.ID, RunID: run.ID}, LogicalRoot: logical, Root: checkout, Mode: worktree.Isolated, Ready: true}}})
	ctx := context.WithValue(t.Context(), tools.ExecutionIdentityKey{}, tools.ExecutionIdentity{AgentID: parent.ID, ToolCallID: "repair"})
	for _, interrupted := range []bool{false, true} {
		if interrupted {
			if _, err := store.FinishRun(t.Context(), collaboration.FinishRunRequest{RunID: run.ID, AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, ExpectedVersion: run.Version, State: collaboration.RunInterrupted, Outcome: "interrupted"}); err != nil {
				t.Fatal(err)
			}
		}
		params := map[string]any{"agent_id": child.ID, "run_id": run.ID, "paths": []string{"owned"}}
		if _, _, err := s.executeClaimsTool(ctx, parent.ID, "agent_update_claims", params); err != nil {
			t.Fatal(err)
		}
		claims, err := store.Claims(t.Context(), child.ID, true)
		if err != nil || len(claims) != 1 || claims[0].Path != filepath.Join(checkout, "owned") {
			t.Fatalf("claims bound to wrong checkout: %+v %v", claims, err)
		}
		params["paths"] = []string{filepath.Join(logical, "victim")}
		if _, _, err := s.executeClaimsTool(ctx, parent.ID, "agent_update_claims", params); err == nil || !strings.Contains(err.Error(), "execution checkout") {
			t.Fatalf("shared claim accepted: %v", err)
		}
		if !s.FileOwnership().WriteScopeAllows(agent.AgentID(child.ID), filepath.Join(checkout, "owned")) || s.FileOwnership().WriteScopeAllows(agent.AgentID(child.ID), filepath.Join(logical, "victim")) {
			t.Fatal("failed repair changed ownership projection")
		}
		remaining, err := store.Claims(t.Context(), child.ID, true)
		if err != nil || len(remaining) != 1 || remaining[0] != claims[0] {
			t.Fatalf("failed repair changed durable claims: %+v %v", remaining, err)
		}
	}
}

type claimExecutionFixture struct {
	ExecutionService
	contexts []worktree.Context
}

func (f *claimExecutionFixture) List() []worktree.Context { return f.contexts }
