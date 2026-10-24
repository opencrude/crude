package core

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/localhistory"
	"crude/provider"
	"crude/types/control"
	"crude/workspace"
	"crude/worktree"
)

func TestControlHistoryLinkedDiscardInverse(t *testing.T) {
	a := controlExecutionFixture(t)
	ctx := context.Background()
	c, err := a.ws.Executions.Create(ctx, worktree.Request{Owner: worktree.Owner{AgentID: "external", RunID: "review"}, Mode: worktree.Isolated})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := workspace.CloseExecutionHistory(a.ws); err != nil {
			t.Error(err)
		}
	})
	path := filepath.Join(c.Root, "disk.txt")
	wanted := []byte("reviewed dirty bytes\n")
	if err := os.WriteFile(path, wanted, 0644); err != nil {
		t.Fatal(err)
	}
	logical, err := os.ReadFile(filepath.Join(a.ws.RootPath, "disk.txt"))
	if err != nil {
		t.Fatal(err)
	}
	index := func() []byte {
		t.Helper()
		out, err := exec.Command("git", "-C", c.Root, "ls-files", "--stage").Output()
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	beforeIndex := index()
	call := func(id, command string, input any) control.Result {
		t.Helper()
		raw, _ := json.Marshal(input)
		work, publish, finish, ok := a.prepareControlHistory(control.Request{ID: id, CommandID: command, Target: control.Target{ExecutionID: c.ID}, Input: raw}, coreprotocol.Owner{ClientID: "external", Epoch: 1})
		if finish != nil {
			defer finish()
		}
		if !ok {
			t.Fatal("route absent")
		}
		out := work(ctx)
		if publish != nil {
			out = publish(out)
		}
		if out.Fault != nil {
			t.Fatal(command, out.Fault)
		}
		return out
	}
	previewResult := call("preview", "git.discard_preview", coreprotocol.GitCompareRequest{Change: workspace.GitChange{Path: "disk.txt", Area: workspace.GitAreaWorking}})
	var preview workspace.GitDiscardPreview
	if err := json.Unmarshal(previewResult.Output, &preview); err != nil {
		t.Fatal(err)
	}
	out := call("discard", "git.discard_reviewed", coreprotocol.DiscardGitReviewedRequest{Preview: preview})
	var discarded coreprotocol.DiscardGitReviewedResult
	if err := json.Unmarshal(out.Output, &discarded); err != nil || discarded.SafetyID == "" {
		t.Fatal(out, err)
	}
	if got, _ := os.ReadFile(path); bytes.Equal(got, wanted) {
		t.Fatal("discard did not change checkout")
	}
	store, root, err := a.ws.ExecutionHistory(c.ID)
	if err != nil || root != c.Root {
		t.Fatal(err)
	}
	manifest, err := store.LoadManifest(discarded.SafetyID)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := manifest.Entries[".git"]; found {
		t.Fatal("Git administration captured")
	}
	planResult := call("plan", "history.plan", coreprotocol.HistoryPlanRequest{SnapshotID: discarded.SafetyID, Options: localhistory.SnapshotOptions{DisableGitIgnore: true, IncludePaths: []string{"disk.txt"}}})
	var plan localhistory.RestorePlan
	if err := json.Unmarshal(planResult.Output, &plan); err != nil {
		t.Fatal(err)
	}
	call("inverse", "history.apply", coreprotocol.HistoryApplyRequest{PlanID: plan.ID, Choices: []localhistory.RestoreChoice{{Path: "disk.txt"}}})
	if got, _ := os.ReadFile(path); !bytes.Equal(got, wanted) {
		t.Fatalf("inverse bytes %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(a.ws.RootPath, "disk.txt")); !bytes.Equal(got, logical) {
		t.Fatal("logical project changed")
	}
	if !bytes.Equal(beforeIndex, index()) {
		t.Fatal("index changed")
	}
	// A reviewed preimage cannot overwrite newer bytes, nor follow a symlink.
	if err := os.WriteFile(path, []byte("newer than preview\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.DiscardGitReviewedAt(ctx, workspace.NewGitService(c.Root), store, a.ws.LiveDocuments, preview); err == nil {
		t.Fatal("stale preview overwrote newer bytes")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(a.ws.RootPath, "disk.txt"), path); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.DiscardGitReviewedAt(ctx, workspace.NewGitService(c.Root), store, a.ws.LiveDocuments, preview); err == nil {
		t.Fatal("symlink target accepted")
	}
	if got, _ := os.ReadFile(filepath.Join(a.ws.RootPath, "disk.txt")); !bytes.Equal(got, logical) {
		t.Fatal("symlink mutated logical bytes")
	}
}

func TestHistoryLeaseAdmissionPreservesDraftAndPendingIntent(t *testing.T) {
	a := controlExecutionFixture(t)
	ctx := context.Background()
	var backendSessionID string
	for _, info := range a.ws.ListSessions() {
		backendSessionID = info.ID
		break
	}
	backend := a.ws.BackendSession(backendSessionID)
	if backend == nil {
		t.Fatal("backend absent")
	}
	orchestrator, err := backend.NewOrchestrator(nil)
	if err != nil {
		t.Fatal(err)
	}
	release, err := backend.BeginHistoryRestoreLease(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := backend.ValidateHistoryAdmission(); err == nil {
		t.Fatal("exclusion not published")
	}
	draft := provider.PlainInput("unsubmitted exact draft")
	if _, err := backend.PrepareInputAdmission(orchestrator.ID, draft, 7); err == nil {
		t.Fatal("new input admitted during restore")
	}
	if draft.Text != "unsubmitted exact draft" {
		t.Fatal("draft consumed")
	}
	release()
	intent, err := backend.PrepareInputAdmission(orchestrator.ID, draft, 7)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.BeginHistoryRestoreLease(ctx); err == nil {
		t.Fatal("pending input crossed restore")
	}
	if intent.InputID == "" {
		t.Fatal("pending input lost")
	}
}

// A semantic history mutation keeps its session admission lease until the
// actor has written the immutable receipt, and a worker panic still settles
// exactly one uncertain receipt and one release. A history owner without its
// store is a real owner defect, not a test-only hook.
func TestControlHistoryLeaseOutlivesWorkerPanicUntilReceipt(t *testing.T) {
	a := controlExecutionFixture(t)
	ctx := t.Context()
	journal, err := openReceipts(filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.close() })
	a.ctx, a.jobs, a.receipts = ctx, make(chan actorJobResult, 1), journal
	var backend interface{ ValidateHistoryAdmission() error }
	for _, info := range a.ws.ListSessions() {
		backend = a.ws.BackendSession(info.ID)
	}
	if backend == nil || a.ws.LocalHistory == nil {
		t.Fatal("fixture lacks a backend session or Local History")
	}
	path := filepath.Join(a.ws.RootPath, "disk.txt")
	snapshot, err := a.ws.LocalHistory.Snapshot(ctx, localhistory.SnapshotOptions{DisableGitIgnore: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("later bytes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := a.ws.LocalHistory.PlanRestorePaths(ctx, snapshot.ID, localhistory.SnapshotOptions{DisableGitIgnore: true}, []string{"disk.txt"})
	if err != nil {
		t.Fatal(err)
	}
	a.ws.HistoryService = &workspace.LocalHistoryService{}
	raw, _ := json.Marshal(coreprotocol.HistoryApplyRequest{PlanID: plan.ID, Choices: []localhistory.RestoreChoice{{Path: "disk.txt"}}})
	in := control.Request{ID: "panicking-apply", CommandID: "history.apply", Input: raw}
	owner := coreprotocol.Owner{ClientID: "external", Epoch: 1}
	command := &actorControlCommand{request: &in, result: make(chan control.Result, 1), ctx: ctx}
	if !a.startControlDomainJob(command, owner, control.Descriptor{ID: "history.apply", Effect: control.Write}) {
		t.Fatal("history.apply was not admitted")
	}
	var job actorJobResult
	select {
	case job = <-a.jobs:
	case <-time.After(5 * time.Second):
		t.Fatal("panicking worker never completed its job")
	}
	if !a.historyBusy || backend.ValidateHistoryAdmission() == nil {
		t.Fatal("history admission was released before the receipt was written")
	}
	a.finishJob(job)
	result := <-command.result
	if result.Fault == nil || !strings.Contains(result.Fault.Message, "panicked") || result.State == control.Committed {
		t.Fatalf("worker panic was not an uncertain outcome: %+v", result)
	}
	receipt, err := journal.lookup(ctx, owner.ClientID, in.ID)
	if err != nil || receipt.State != "completed" {
		t.Fatalf("panicked operation has no settled receipt: %+v %v", receipt, err)
	}
	if a.historyBusy || backend.ValidateHistoryAdmission() != nil {
		t.Fatal("history admission leaked after receipt completion")
	}
	job.finish()
	release, err := a.ws.BeginHistoryRestoreLease(ctx)
	if err != nil {
		t.Fatalf("lease unusable after settlement: %v", err)
	}
	release()
}
