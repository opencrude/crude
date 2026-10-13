package core

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

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
		work, publish, ok := a.prepareControlHistory(control.Request{ID: id, CommandID: command, Target: control.Target{ExecutionID: c.ID}, Input: raw}, coreprotocol.Owner{ClientID: "external", Epoch: 1})
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
