package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"crude/collaboration"
	"crude/coreprotocol"
	"crude/session"
	"crude/sessiondb"
	"crude/types/control"
	"crude/worktree"
)

func TestWorkspaceJanitorReconcilesTerminalPinsAndCleansAnotherParentsWork(t *testing.T) {
	a := controlExecutionFixture(t)
	ctx := t.Context()
	store := a.ws.Collaboration
	parent, err := store.RegisterAgent(ctx, collaboration.AgentRegistration{ID: "neglectful-parent", Name: "Parent", SessionID: "old-session", OwnerInstance: "parent-instance"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.RegisterAgent(ctx, collaboration.AgentRegistration{ID: "old-child", Name: "Child", SessionID: "old-child", ParentID: parent.ID, OwnerInstance: "child-instance"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.StartRun(ctx, collaboration.StartRunRequest{AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, Origin: collaboration.OriginPeer})
	if err != nil {
		t.Fatal(err)
	}
	c, err := a.ws.Executions.Create(ctx, worktree.Request{Owner: worktree.Owner{AgentID: child.ID, RunID: run.ID}, Mode: worktree.Isolated})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.Root, "leftover.txt"), []byte("failed attempt evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	janitor, err := store.RegisterAgent(ctx, collaboration.AgentRegistration{ID: "janitor", Name: "Janitor", SessionID: "another-session", OwnerInstance: "janitor-instance"})
	if err != nil {
		t.Fatal(err)
	}
	call := func(command string, input any) control.Result {
		t.Helper()
		raw, _ := json.Marshal(input)
		fn, _, ok := a.prepareControlDomain(control.Request{ID: command, CommandID: command, Caller: control.Caller{Kind: control.AgentCaller, AgentID: janitor.ID, RunID: "checked-by-gateway"}, Target: control.Target{ResourceID: c.ID}, Input: raw}, coreprotocol.Owner{ClientID: "fixture", Epoch: 1})
		if !ok {
			t.Fatal("missing route", command)
		}
		return fn(ctx)
	}
	live := call("execution.reconcile", map[string]any{"id": c.ID})
	if live.Fault != nil {
		t.Fatal(live.Fault)
	}
	var reconciled executionReconciliation
	if err := json.Unmarshal(live.Output, &reconciled); err != nil || len(reconciled.Released) != 0 || len(reconciled.Retained) != 1 {
		t.Fatal("live pin released", live, err)
	}
	if _, err := store.FinishRun(ctx, collaboration.FinishRunRequest{RunID: run.ID, AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, ExpectedVersion: run.Version, State: collaboration.RunFailed, Outcome: "failed"}); err != nil {
		t.Fatal(err)
	}
	settled := call("execution.reconcile", map[string]any{"id": c.ID})
	if settled.Fault != nil {
		t.Fatal(settled.Fault)
	}
	if err := json.Unmarshal(settled.Output, &reconciled); err != nil || len(reconciled.Released) != 1 || len(reconciled.Retained) != 0 {
		t.Fatal("stale pin not reconciled", settled, err)
	}
	kept := call("execution.keep", map[string]any{"id": c.ID, "keep": true})
	if kept.Fault != nil {
		t.Fatal(kept.Fault)
	}
	if got := call("execution.cleanup", map[string]any{"id": c.ID}); got.Fault == nil {
		t.Fatal("Keep ignored")
	}
	if got := call("execution.keep", map[string]any{"id": c.ID, "keep": false}); got.Fault != nil {
		t.Fatal(got.Fault)
	}
	reviewed := call("execution.review", map[string]any{"id": c.ID, "discard": true})
	if reviewed.Fault != nil {
		t.Fatal(reviewed.Fault)
	}
	var plan worktree.PlanDelta
	if err := json.Unmarshal(reviewed.Output, &plan); err != nil || plan.DiscardDigest == "" {
		t.Fatal("missing discard review", err)
	}
	if got := call("execution.cleanup", map[string]any{"id": c.ID, "discard": true, "digest": plan.DiscardDigest}); got.Fault != nil {
		t.Fatal(got.Fault)
	}
	if _, err := os.Stat(c.Root); !os.IsNotExist(err) {
		t.Fatal("settled garbage worktree remains", err)
	}
	retained, err := a.ws.Executions.Inspect(c.ID)
	if err != nil || !retained.Removed || string(retained.ArchivedResult["leftover.txt"].Data) != "failed attempt evidence" {
		t.Fatal("janitorial cleanup lost evidence", err)
	}
}

func TestJanitorProtectsColdForeignSessionReferences(t *testing.T) {
	a := controlExecutionFixture(t)
	c, err := a.ws.Executions.Create(t.Context(), worktree.Request{Owner: worktree.Owner{AgentID: "client:fixture", RunID: "epoch:1"}, Mode: worktree.Isolated})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.ws.Executions.Unpin(c.ID, c.Owner); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cold-root.db", filepath.Join("cold-parent", "subagents", "cold-child.db")} {
		path := filepath.Join(a.ws.WorkspaceDir, "sessions", name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		db, err := sessiondb.OpenAndMigrate(path)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(session.ExecutionSelection{ID: c.ID, Revision: 1})
		if err := sessiondb.SaveRuntimeState(t.Context(), db, "execution_selection", raw); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		if err := a.executionRetentionCheck()(t.Context(), c.Owner, c.ID, c.Root); err == nil {
			t.Fatal("cold foreign selected execution was cleanable", name)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
}
