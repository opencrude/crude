package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"crude/agent"
	"crude/collaboration"
	"crude/coreprotocol"
	"crude/document"
	"crude/types/control"
	"crude/workspace"
	"crude/worktree"
)

func TestControlDomainExactCheckoutAndNativeClaims(t *testing.T) {
	a := controlExecutionFixture(t)
	ctx := context.Background()
	store := a.ws.Collaboration
	parent, err := store.RegisterAgent(ctx, collaboration.AgentRegistration{ID: "domain-parent", Name: "Parent", SessionID: "domain", OwnerInstance: "domain-test"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.RegisterAgent(ctx, collaboration.AgentRegistration{ID: "domain-child", Name: "Child", ParentID: parent.ID, SessionID: "domain", OwnerInstance: "domain-test"})
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
	childPath := filepath.Join(c.Root, "live.txt")
	if err := os.WriteFile(childPath, []byte("child checkout\n"), 0644); err != nil {
		t.Fatal(err)
	}
	claimed := []string{childPath, filepath.Join(c.Root, "new.txt"), filepath.Join(c.Root, "renamed.txt")}
	if err := a.ws.FileOwnership.PreClaim(agent.AgentID(child.ID), child.Name, claimed); err != nil {
		t.Fatal(err)
	}
	call := func(command, execution string, input any) control.Result {
		t.Helper()
		raw, _ := json.Marshal(input)
		work, publish, ok := a.prepareControlDomain(control.Request{ID: command, CommandID: command, Caller: control.Caller{Kind: control.AgentCaller, AgentID: child.ID, RunID: run.ID}, Target: control.Target{ExecutionID: execution}, Input: raw}, coreprotocol.Owner{ClientID: "agent-domain", Epoch: 1})
		if !ok {
			t.Fatal("missing route", command)
		}
		out := work(ctx)
		if publish != nil {
			out = publish(out)
		}
		return out
	}
	read := call("files.read", c.ID, coreprotocol.FileReadRequest{Path: "live.txt"})
	if read.Fault != nil {
		t.Fatal(read.Fault)
	}
	var chunk coreprotocol.FileChunk
	if err := json.Unmarshal(read.Output, &chunk); err != nil || string(chunk.Data) != "child checkout\n" {
		t.Fatal("read logical bytes", string(chunk.Data), err)
	}
	for _, input := range []coreprotocol.FileReadRequest{{Path: filepath.Join(a.ws.RootPath, "live.txt")}, {Path: ".git"}} {
		if out := call("files.read", c.ID, input); out.Fault == nil || out.Fault.Code != control.Denied {
			t.Fatal("path escape accepted", out)
		}
	}
	if out := call("files.read", "", coreprotocol.FileReadRequest{Path: "live.txt"}); out.Fault == nil {
		t.Fatal("cleared execution accepted")
	}
	opened := call("document.open", c.ID, coreprotocol.DocumentOpenRequest{Path: "live.txt"})
	if opened.Fault != nil {
		t.Fatal(opened.Fault)
	}
	var snap document.Snapshot
	if err := json.Unmarshal(opened.Output, &snap); err != nil || snap.Path != childPath {
		t.Fatal("wrong document", snap, err)
	}
	edited := call("document.edit", c.ID, coreprotocol.DocumentEditRequest{ID: snap.ID, Revision: snap.Revision, Group: "child-edit", Edits: []document.Edit{{Start: 0, End: 5, Text: "CHILD"}}})
	if edited.Fault != nil {
		t.Fatal(edited.Fault)
	}
	accepted, err := a.ws.LiveDocuments.Snapshot(snap.ID)
	if err != nil || string(accepted.Text) != "CHILD checkout\n" {
		t.Fatal("child edit missing", accepted, err)
	}
	logical, err := a.ws.LiveDocuments.Open(document.Owner{ClientID: "human", Epoch: 1}, "live.txt")
	if err != nil {
		t.Fatal(err)
	}
	if out := call("document.edit", c.ID, coreprotocol.DocumentEditRequest{ID: logical.ID, Revision: logical.Revision, Edits: []document.Edit{{Start: 0, End: 3, Text: "BAD"}}}); out.Fault == nil {
		t.Fatal("logical document ID escape")
	}
	for _, step := range []struct {
		command string
		input   coreprotocol.FilePathRequest
	}{
		{"files.create", coreprotocol.FilePathRequest{Path: "new.txt"}},
		{"files.rename", coreprotocol.FilePathRequest{Path: "new.txt", NewPath: "renamed.txt"}},
		{"files.delete", coreprotocol.FilePathRequest{Path: "renamed.txt"}},
	} {
		if out := call(step.command, c.ID, step.input); out.Fault != nil {
			t.Fatal(step.command, out.Fault)
		}
	}
	if out := call("files.create", c.ID, coreprotocol.FilePathRequest{Path: "unclaimed.txt"}); out.Fault == nil || out.Fault.Code != control.Denied {
		t.Fatal("unclaimed child write", out)
	}
	if _, err := os.Stat(filepath.Join(c.Root, "unclaimed.txt")); !os.IsNotExist(err) {
		t.Fatal("denied effect exists", err)
	}
	for _, name := range []string{"new.txt", "renamed.txt"} {
		if _, err := os.Stat(filepath.Join(a.ws.RootPath, name)); !os.IsNotExist(err) {
			t.Fatal("logical file changed", name, err)
		}
	}
	bytes, err := os.ReadFile(filepath.Join(a.ws.RootPath, "live.txt"))
	if err != nil || string(bytes) != "one\ntwo\nthree\n" {
		t.Fatal("logical checkout changed", string(bytes), err)
	}
	unchanged, err := a.ws.LiveDocuments.Snapshot(logical.ID)
	if err != nil || string(unchanged.Text) != "one\ntwo\nthree\n" {
		t.Fatal("logical accepted text changed", unchanged, err)
	}
	git := call("git.snapshot", c.ID, coreprotocol.GitSnapshotRequest{})
	if git.Fault != nil {
		t.Fatal(git.Fault)
	}
	var gitSnapshot coreprotocol.GitSnapshotResult
	if err := json.Unmarshal(git.Output, &gitSnapshot); err != nil {
		t.Fatal(err)
	}
	// Reviewed stage owns the isolated index, never the logical checkout index.
	review := call("git.review", c.ID, coreprotocol.GitCompareRequest{Change: workspace.GitChange{Path: "live.txt", Area: workspace.GitAreaWorking}})
	if review.Fault != nil {
		t.Fatal(review.Fault)
	}
	var compared coreprotocol.GitCompareResult
	if err := json.Unmarshal(review.Output, &compared); err != nil {
		t.Fatal(err)
	}
	if compared.Comparison.RepositoryRoot != c.Root {
		t.Fatal("Git review used logical root", compared)
	}
	if out := call("git.stage_reviewed", c.ID, coreprotocol.GitStageReviewedRequest(compared)); out.Fault != nil {
		t.Fatal(out.Fault)
	}
	logicalGit, err := a.ws.Git.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range logicalGit.Changes {
		if change.Path == "live.txt" {
			t.Fatal("logical index changed", change)
		}
	}
}

func TestControlDomainRootMayUseExternallyCreatedPinnedExecution(t *testing.T) {
	a := controlExecutionFixture(t)
	ctx := context.Background()
	parent, err := a.ws.Collaboration.RegisterAgent(ctx, collaboration.AgentRegistration{ID: "domain-root", Name: "Root", SessionID: "domain", OwnerInstance: "domain-test"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := a.ws.Collaboration.StartRun(ctx, collaboration.StartRunRequest{AgentID: parent.ID, RuntimeEpoch: parent.RuntimeEpoch, Origin: collaboration.OriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	external := worktree.Owner{AgentID: "client:external", RunID: "epoch:1"}
	c, err := a.ws.Executions.Create(ctx, worktree.Request{Owner: external, Mode: worktree.Isolated})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.ws.Executions.Pin(c.ID, worktree.Owner{AgentID: parent.ID, RunID: run.ID}); err != nil {
		t.Fatal(err)
	}
	in := control.Request{ID: "root-create", CommandID: "files.create", Caller: control.Caller{Kind: control.AgentCaller, AgentID: parent.ID, RunID: run.ID}, Target: control.Target{ExecutionID: c.ID}, Input: json.RawMessage(`{"path":"root-created.txt"}`)}
	work, _, _ := a.prepareControlDomain(in, coreprotocol.Owner{ClientID: "agent-root", Epoch: 1})
	if out := work(ctx); out.Fault != nil {
		t.Fatal(out.Fault)
	}
	if _, err := os.Stat(filepath.Join(c.Root, "root-created.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(a.ws.RootPath, "root-created.txt")); !os.IsNotExist(err) {
		t.Fatal("root created logical file", err)
	}
	claims, err := a.ws.Collaboration.Claims(ctx, parent.ID, true)
	if err != nil || len(claims) != 1 || claims[0].Path != filepath.Join(c.Root, "root-created.txt") || claims[0].RequestID != in.ID {
		t.Fatal("missing native claim", claims, err)
	}
}
