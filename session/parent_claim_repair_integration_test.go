package session

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"crude/agent"
	"crude/collaboration"
	"crude/tools"
)

func TestParentClaimRepairToolProjectsOnlyCommittedNarrowScope(t *testing.T) {
	s, store, parent, child := collaborationContractFixture(t)
	workDir := t.TempDir()
	s.SetWorkDir(workDir)
	broad := filepath.Join(workDir, "src")
	narrow := filepath.Join(broad, "owned.go")
	sibling := filepath.Join(broad, "sibling.go")
	run, err := store.StartRun(t.Context(), collaboration.StartRunRequest{
		AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, RequestID: "assignment-tool-call", Origin: collaboration.OriginPeer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AcquireClaims(t.Context(), collaboration.AcquireClaimsRequest{
		AgentID: child.ID, RunID: run.ID, RequestID: run.RequestID, Paths: []string{broad},
	}); err != nil {
		t.Fatal(err)
	}
	finished, err := store.FinishRun(t.Context(), collaboration.FinishRunRequest{
		RunID: run.ID, AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch,
		ExpectedVersion: run.Version, State: collaboration.RunInterrupted, Outcome: "guidance blocked before effects",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), tools.ExecutionIdentityKey{}, tools.ExecutionIdentity{ToolCallID: "repair-call"})
	toolResult, handled, err := s.executeCollaborationTool(ctx, parent.ID, "agent_update_claims", map[string]any{
		"agent_id": child.ID, "runtime_epoch": int(child.RuntimeEpoch),
		"terminal_run_id": run.ID, "expected_run_version": int(finished.Version),
		"expected_versions": map[string]any{}, "paths": []any{narrow},
	})
	if err != nil || !handled {
		t.Fatalf("agent_update_claims: handled=%v result=%+v err=%v", handled, toolResult, err)
	}
	var repaired collaboration.UpdateClaimsResult
	if err := json.Unmarshal([]byte(toolResult.Content), &repaired); err != nil {
		t.Fatalf("decode repair result %q: %v", toolResult.Content, err)
	}
	if len(repaired.Claims) != 1 || repaired.Claims[0].Path != filepath.Clean(narrow) || repaired.Audit.ID == "" {
		t.Fatalf("repair result = %+v", repaired)
	}
	projection := agent.NewFileOwnership()
	if err := projection.PreClaim(agent.AgentID(child.ID), child.Name, []string{broad}); err != nil {
		t.Fatal(err)
	}
	if err := projection.ReplaceClaims(agent.AgentID(child.ID), child.Name, []string{repaired.Claims[0].Path}); err != nil {
		t.Fatal(err)
	}
	if err := projection.CheckOrClaim("other", "Other", sibling, true); err != nil {
		t.Fatalf("old broad scope survived repair projection: %v", err)
	}
	if err := projection.CheckOrClaim("other", "Other", narrow, true); err == nil {
		t.Fatal("committed narrow scope was not enforced")
	}
	if !ownershipScopeAllows([]string{repaired.Claims[0].Path}, narrow) || ownershipScopeAllows([]string{repaired.Claims[0].Path}, sibling) {
		t.Fatal("resumed tool guard did not enforce the committed narrow scope")
	}
	active, err := store.Claims(t.Context(), child.ID, true)
	if err != nil || len(active) != 1 || active[0].Path != filepath.Clean(narrow) || active[0].RunID != "" {
		t.Fatalf("durable repaired claims = %+v err=%v", active, err)
	}
	if _, err := store.StartRun(t.Context(), collaboration.StartRunRequest{
		AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, RequestID: run.RequestID, Origin: collaboration.OriginPeer,
	}); err != nil {
		t.Fatalf("explicit Continue could not create a fresh run identity: %v", err)
	}
}

func TestActiveActorCanNarrowOwnClaimsWithoutEndingRun(t *testing.T) {
	s, store, parent, _ := collaborationContractFixture(t)
	workDir := t.TempDir()
	s.SetWorkDir(workDir)
	broad := filepath.Join(workDir, "src")
	narrow := filepath.Join(broad, "owned.go")
	run, err := store.StartRun(t.Context(), collaboration.StartRunRequest{
		AgentID: parent.ID, RuntimeEpoch: parent.RuntimeEpoch, RequestID: "parent-active-task", Origin: collaboration.OriginHuman,
	})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := store.AcquireClaims(t.Context(), collaboration.AcquireClaimsRequest{
		AgentID: parent.ID, RunID: run.ID, RequestID: run.RequestID, Paths: []string{broad},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FileOwnership().ReplaceClaims(agent.AgentID(parent.ID), parent.Name, []string{broad}); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), tools.ExecutionIdentityKey{}, tools.ExecutionIdentity{AgentID: parent.ID, ToolCallID: "narrow-call"})
	result, handled, err := s.executeClaimsTool(ctx, parent.ID, "agent_narrow_claims", map[string]any{
		"runtime_epoch": int(parent.RuntimeEpoch), "run_id": run.ID, "expected_run_version": int(run.Version),
		"expected_versions": map[string]any{claims[0].ID: int(claims[0].Version)}, "paths": []any{narrow},
	})
	if err != nil || !handled {
		t.Fatalf("narrow claims: handled=%v result=%+v err=%v", handled, result, err)
	}
	if got := s.FileOwnership().WriteClaimsByAgent(agent.AgentID(parent.ID)); len(got) != 1 || got[0] != filepath.Clean(narrow) {
		t.Fatalf("local projection = %v", got)
	}
	active, err := store.Claims(t.Context(), parent.ID, true)
	if err != nil || len(active) != 1 || active[0].Path != filepath.Clean(narrow) || active[0].RunID != run.ID {
		t.Fatalf("durable claims = %+v err=%v", active, err)
	}
	actor, err := store.Agent(t.Context(), parent.ID)
	if err != nil || actor.CurrentRunID != run.ID {
		t.Fatalf("active run changed: %+v err=%v", actor, err)
	}
}
