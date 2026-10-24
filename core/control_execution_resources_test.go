package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"crude/agent"
	"crude/collaboration"
	"crude/coreprotocol"
	"crude/document"
	"crude/types/control"
	"crude/workspace"
	"crude/worktree"
)

// Integration and its inverse must publish the accepted document journal without
// reentering the document mutation lock or waiting for a shell's applied ACK.
func TestControlExecutionLiveDocumentTopicApplyAndUndo(t *testing.T) {
	a := controlExecutionFixture(t)
	r, err := New(func(string) (*workspace.Workspace, error) { return a.ws, nil }, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	c := controlWireClient(t, r, "execution-topic")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var attached coreprotocol.AttachResult
	if err := c.Call(ctx, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: a.ws.RootPath}, &attached); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, attached.WorkspaceID, coreprotocol.ControlClientRegister, &coreprotocol.ControlClientGrant{Effects: []control.Effect{control.Read, control.Write, control.Execute}}, nil); err != nil {
		t.Fatal(err)
	}
	var snapshot document.Snapshot
	if err := c.Call(ctx, attached.WorkspaceID, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: "live.txt"}, &snapshot); err != nil {
		t.Fatal(err)
	}
	var edited document.Receipt
	if err := c.Call(ctx, attached.WorkspaceID, coreprotocol.DocumentEdit, &coreprotocol.DocumentEditRequest{ID: snapshot.ID, Revision: snapshot.Revision, Edits: []document.Edit{{Start: 0, End: 3, Text: "ONE"}}}, &edited); err != nil {
		t.Fatal(err)
	}
	type publication struct {
		header coreprotocol.TopicHeader
		update coreprotocol.DocumentUpdate
		ack    chan struct{}
	}
	updates := make(chan publication, 4)
	sub := c.Subscribe(ctx, attached.WorkspaceID, coreprotocol.TopicDocument, snapshot.ID, func(h coreprotocol.TopicHeader, body []byte) error {
		var update coreprotocol.DocumentUpdate
		if err := coreprotocol.Decode(body, &update); err != nil {
			return err
		}
		p := publication{h, update, make(chan struct{})}
		select {
		case updates <- p:
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case <-p.ack:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	defer sub.Close()
	next := func() publication {
		t.Helper()
		select {
		case p := <-updates:
			return p
		case <-ctx.Done():
			t.Fatal("document topic stalled", ctx.Err())
			return publication{}
		}
	}
	initial := next()
	if !initial.header.Reset || initial.update.Snapshot == nil || string(initial.update.Snapshot.Text) != "ONE\ntwo\nthree\n" || !initial.update.Snapshot.Dirty {
		t.Fatalf("missing dirty initial document: %+v", initial)
	}
	close(initial.ack)
	sequence := 0
	call := func(command, resource string, input any) control.Result {
		t.Helper()
		sequence++
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		request := control.Request{ID: fmt.Sprintf("execution-topic-%d", sequence), CommandID: command, Target: control.Target{WorkspaceID: attached.WorkspaceID, ResourceID: resource}, Input: raw}
		payload, err := coreprotocol.Marshal(coreprotocol.Ptr(request))
		if err != nil {
			t.Fatal(err)
		}
		response, err := c.CallRequest(ctx, coreprotocol.Request{ID: request.ID, Owner: c.Owner(), WorkspaceID: attached.WorkspaceID, Method: coreprotocol.ControlExecute, Payload: payload})
		if err != nil || response.Error != nil {
			t.Fatalf("%s did not complete: %v %v", command, err, response.Error)
		}
		var result control.Result
		if err := coreprotocol.Decode(response.Payload, &result); err != nil {
			t.Fatal(err)
		}
		if result.Fault != nil {
			t.Fatal(command, result.Fault)
		}
		return result
	}
	var execution worktree.Context
	if err := json.Unmarshal(call("execution.create", "", map[string]any{"mode": "worktree"}).Output, &execution); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(execution.Root, "live.txt"), []byte("ONE\nTWO\nthree\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var plan worktree.PlanDelta
	if err := json.Unmarshal(call("execution.review", execution.ID, map[string]any{"id": execution.ID}).Output, &plan); err != nil {
		t.Fatal(err)
	}
	applied := call("execution.apply", execution.ID, map[string]any{"plan": plan})
	delta := next()
	if delta.header.Reset || delta.header.Seq != initial.header.Seq+1 || delta.update.Base != edited.Revision || delta.update.State.Revision != edited.Revision+1 || len(delta.update.Changes) != 1 || !delta.update.State.Dirty {
		t.Fatalf("integration did not publish one contiguous delta: %+v", delta)
	}
	text, _, err := document.Apply(initial.update.Snapshot.Text, delta.update.Changes[0].Edits)
	if err != nil || string(text) != "ONE\nTWO\nthree\n" {
		t.Fatalf("integration recipient bytes=%q err=%v", text, err)
	}
	// Re-observe a lost reply while the recipient still holds the publication.
	if duplicate := call("execution.apply", execution.ID, map[string]any{"plan": plan}); string(duplicate.Output) != string(applied.Output) {
		t.Fatal("lost reply changed integration receipt")
	}
	close(delta.ack)
	var receipt worktree.Receipt
	if err := json.Unmarshal(applied.Output, &receipt); err != nil {
		t.Fatal(err)
	}
	call("execution.undo", receipt.OperationID, map[string]any{"operation_id": receipt.OperationID})
	undone := next()
	if undone.header.Reset || undone.header.Seq != delta.header.Seq+1 || undone.update.Base != delta.update.State.Revision || undone.update.State.Revision != delta.update.State.Revision+1 || len(undone.update.Changes) != 1 {
		t.Fatalf("undo did not publish exactly one next revision: %+v", undone)
	}
	text, _, err = document.Apply(text, undone.update.Changes[0].Edits)
	if err != nil || string(text) != "ONE\ntwo\nthree\n" {
		t.Fatalf("undo recipient bytes=%q err=%v", text, err)
	}
	close(undone.ack)
	if data, err := os.ReadFile(filepath.Join(a.ws.RootPath, "live.txt")); err != nil || string(data) != "one\ntwo\nthree\n" {
		t.Fatalf("integration forced accepted dirty text to disk: %q %v", data, err)
	}
	// Closing an applied topic cannot strand a notifier or the next document read.
	sub.Close()
	var current document.Snapshot
	if err := c.Call(ctx, attached.WorkspaceID, coreprotocol.DocumentRead, coreprotocol.DocumentReadRequest{ID: snapshot.ID}, &current); err != nil {
		t.Fatal(err)
	}
	if string(current.Text) != "ONE\ntwo\nthree\n" || current.Revision != undone.update.State.Revision {
		t.Fatalf("wrong canonical state after topic close: %+v", current)
	}
}

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
