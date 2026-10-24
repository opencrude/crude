package core

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/document"
	"crude/fileio"
	"crude/localhistory"
)

func TestConnectedHistoryRestoreSafetyInverseAndReceipt(t *testing.T) {
	_, client, root, ws := bulkRuntimeClient(t)
	ctx := t.Context()
	history := (&coreclient.WorkspaceView{Client: client, ID: ws}).History()
	path := filepath.Join(root, "restored.txt")
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	check := func(want string) {
		t.Helper()
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("restored bytes=%q want=%q err=%v", got, want, err)
		}
	}
	write("before restore\n")
	snapshot, err := history.Capture(ctx, localhistory.SnapshotOptions{Kind: "manual", Label: "restore target"})
	if err != nil {
		t.Fatal(err)
	}
	write("later exact bytes\n")
	plan, err := history.PlanRestorePaths(ctx, snapshot.ID, localhistory.SnapshotOptions{}, []string{"restored.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := history.ApplyRestoreChoices(ctx, plan.ID, nil); err == nil {
		t.Fatal("empty interactive selection restored every path")
	}
	check("later exact bytes\n")
	input := coreprotocol.HistoryApplyRequest{PlanID: plan.ID, Choices: []localhistory.RestoreChoice{{Path: "restored.txt"}}}
	payload := mustTestPayload(&input)
	request := coreprotocol.Request{ID: "history-original-apply", WorkspaceID: ws, Method: coreprotocol.HistoryApply, Payload: payload}
	response, err := client.CallRequest(ctx, request)
	if err != nil || response.Error != nil {
		t.Fatalf("connected UI restore did not apply: transport=%v result=%+v", err, response.Error)
	}
	var restored localhistory.RestoreResult
	if err := coreprotocol.Decode(response.Payload, &restored); err != nil || restored.SafetySnapshotID == "" || restored.AfterSnapshotID == "" {
		t.Fatalf("restore lacks durable safety/after identity: %+v %v", restored, err)
	}
	check("before restore\n")
	// A duplicate original receipt must not replace bytes written after success.
	write("newer independent bytes\n")
	duplicate, err := client.CallRequest(ctx, request)
	if err != nil || duplicate.Error != nil || !bytes.Equal(duplicate.Payload, response.Payload) {
		t.Fatalf("original restore receipt was not retained: %v %+v", err, duplicate.Error)
	}
	check("newer independent bytes\n")
	input.Choices[0].Path = "not-in-preview.txt"
	request.Payload = mustTestPayload(&input)
	changed, err := client.CallRequest(ctx, request)
	if err == nil && changed.Error == nil {
		t.Fatal("changed restore preimage reused original receipt")
	}
	check("newer independent bytes\n")
	// The saved safety snapshot is the exact pre-effect content, not the target.
	write("before restore\n")
	inverse, err := history.PlanRestorePaths(ctx, restored.SafetySnapshotID, localhistory.SnapshotOptions{}, []string{"restored.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := history.ApplyRestoreChoices(ctx, inverse.ID, []localhistory.RestoreChoice{{Path: "restored.txt"}}); err != nil {
		t.Fatalf("connected safety inverse failed: %v", err)
	}
	check("later exact bytes\n")
	stale, err := history.PlanRestorePaths(ctx, snapshot.ID, localhistory.SnapshotOptions{}, []string{"restored.txt"})
	if err != nil {
		t.Fatal(err)
	}
	write("write after immutable preview\n")
	if _, err := history.ApplyRestoreChoices(ctx, stale.ID, []localhistory.RestoreChoice{{Path: "restored.txt"}}); err == nil {
		t.Fatal("connected stale preview overwrote later disk work")
	}
	check("write after immutable preview\n")
	// Failure must release admission rather than poisoning later valid restore.
	valid, err := history.PlanRestorePaths(context.Background(), snapshot.ID, localhistory.SnapshotOptions{}, []string{"restored.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := history.ApplyRestoreChoices(ctx, valid.ID, []localhistory.RestoreChoice{{Path: "restored.txt"}}); err != nil {
		t.Fatalf("restore refusal retained an admission lease: %v", err)
	}
	check("before restore\n")
}

func TestConnectedHistoryRestoreRefusesDirtyDocument(t *testing.T) {
	_, client, root, ws := bulkRuntimeClient(t)
	ctx := t.Context()
	history := (&coreclient.WorkspaceView{Client: client, ID: ws}).History()
	path := filepath.Join(root, "dirty.txt")
	if err := os.WriteFile(path, []byte("target\n"), 0600); err != nil {
		t.Fatal(err)
	}
	target, err := history.Capture(ctx, localhistory.SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("disk\n"), 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := history.PlanRestorePaths(ctx, target.ID, localhistory.SnapshotOptions{}, []string{"dirty.txt"})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot document.Snapshot
	if err := client.Call(ctx, ws, coreprotocol.DocumentOpen, &coreprotocol.DocumentOpenRequest{Path: path}, &snapshot); err != nil {
		t.Fatal(err)
	}
	var receipt document.Receipt
	if err := client.Call(ctx, ws, coreprotocol.DocumentEdit, &coreprotocol.DocumentEditRequest{ID: snapshot.ID, Revision: snapshot.Revision, Group: "private", Edits: []document.Edit{{Start: 0, End: 0, Text: "unsaved "}}}, &receipt); err != nil {
		t.Fatal(err)
	}
	if _, err := history.ApplyRestoreChoices(ctx, plan.ID, []localhistory.RestoreChoice{{Path: "dirty.txt"}}); err == nil {
		t.Fatal("restore discarded retained unsaved document bytes")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "disk\n" {
		t.Fatalf("refused restore changed disk: %q %v", data, err)
	}
	if err := client.Call(ctx, ws, coreprotocol.DocumentRead, &coreprotocol.DocumentReadRequest{ID: snapshot.ID}, &snapshot); err != nil || string(snapshot.Text) != "unsaved disk\n" {
		t.Fatalf("refused restore changed document: %q %v", snapshot.Text, err)
	}
}

// Native user Undo of an accepted agent operation may reach outside the
// project; semantic execution confinement must not refuse it, and the history
// owner's own preimage checks must still refuse substituted paths.
func TestConnectedHistoryRestoreReversesExternalOperation(t *testing.T) {
	r, client, _, ws := bulkRuntimeClient(t)
	ctx := t.Context()
	store := r.Workspaces()[0].LocalHistory
	history := (&coreclient.WorkspaceView{Client: client, ID: ws}).History()
	external := t.TempDir()
	run := func(message string, mutate func(path string) error) (string, localhistory.RestorePlan) {
		t.Helper()
		path := filepath.Join(external, message)
		if err := os.WriteFile(path, []byte("accepted external bytes\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		before, err := store.Snapshot(ctx, localhistory.SnapshotOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.BeginMessageScope("bulk-session", message, before.ID, time.Now()); err != nil {
			t.Fatal(err)
		}
		state := func(text string) *localhistory.OperationState {
			return &localhistory.OperationState{Type: localhistory.EntryFile, Mode: 0o644, Data: []byte(text)}
		}
		operation := localhistory.AgentOperation{ID: message + "-write", SessionID: "bulk-session", MessageID: message, AgentID: "agent", Source: "write_file", Paths: []localhistory.OperationPath{{Path: path, Before: state("accepted external bytes\n"), After: state("agent bytes\n")}}}
		if err := store.RecordAgentOperation(ctx, operation); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("agent bytes\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := store.CompleteAgentOperation(operation.ID, true); err != nil {
			t.Fatal(err)
		}
		after, err := store.Snapshot(ctx, localhistory.SnapshotOptions{ParentID: before.ID})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CloseMessageScope("bulk-session", message, after.ID, nil, nil, time.Now()); err != nil {
			t.Fatal(err)
		}
		plan, err := history.PlanAgentRestore(ctx, "bulk-session", message, "agent")
		if err != nil || len(plan.Changes) != 1 || !filepath.IsAbs(filepath.FromSlash(plan.Changes[0].Path)) || plan.Changes[0].UnsafeReason != "" {
			t.Fatalf("external operation plan=%+v err=%v", plan.Changes, err)
		}
		if mutate != nil {
			if err := mutate(path); err != nil {
				t.Fatal(err)
			}
		}
		return path, plan
	}
	path, plan := run("external-undo", nil)
	// This fixture session has no transcript owner, so publication fails after
	// the disk effect. The client must still receive the known committed result.
	restored, err := history.ApplyRestoreChoices(ctx, plan.ID, []localhistory.RestoreChoice{{Path: plan.Changes[0].Path}})
	if !fileio.WriteCommitted(err) || restored.SafetySnapshotID == "" {
		t.Fatalf("connected Undo lost or refused an accepted external operation: %+v %v", restored, err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "accepted external bytes\n" {
		t.Fatalf("external bytes=%q err=%v", data, err)
	}
	// A symlink substituted after the immutable preview is not that preimage.
	decoy := filepath.Join(t.TempDir(), "decoy")
	if err := os.WriteFile(decoy, []byte("unrelated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path, plan = run("external-substituted", func(path string) error {
		if err := os.Remove(path); err != nil {
			return err
		}
		return os.Symlink(decoy, path)
	})
	if _, err := history.ApplyRestoreChoices(ctx, plan.ID, []localhistory.RestoreChoice{{Path: plan.Changes[0].Path}}); err == nil {
		t.Fatal("restore followed a symlink substituted after preview")
	}
	if data, err := os.ReadFile(decoy); err != nil || string(data) != "unrelated\n" {
		t.Fatalf("symlink target changed: %q %v", data, err)
	}
	if target, err := os.Readlink(path); err != nil || target != decoy {
		t.Fatalf("substituted path changed: %q %v", target, err)
	}
}
