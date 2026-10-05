package sessiondb

import (
	"testing"

	"crude/provider"
)

func TestContextAndCollaborationAdmissionsCommitAtomically(t *testing.T) {
	db := OpenAndMigrateAt(t.TempDir())
	defer db.Close()
	if err := CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	state := ContextState{
		AgentID: "agent", Generation: 2, Reason: "collaboration_admission",
		Messages: []provider.ChatMessage{provider.NewTextMessage("user", "peer data")},
	}
	admission := CollaborationAdmission{
		DeliveryID: "delivery", EventID: "event", SessionID: "session",
		AgentID: "agent", RuntimeEpoch: 3, DeliveryVersion: 2,
		ContextCommitID: "commit", RunID: "run",
	}
	if err := SaveContextStateWithAdmissions(db, "session", "agent", "agent", state, []CollaborationAdmission{admission}); err != nil {
		t.Fatal(err)
	}
	loaded, ok, err := LoadContextState(db, "session", "agent")
	if err != nil || !ok || len(loaded.Messages) != 1 || loaded.Messages[0].ContentString() != "peer data" {
		t.Fatalf("context = %+v, %v, %v", loaded, ok, err)
	}
	pending, err := PendingCollaborationAdmissions(db, "agent")
	if err != nil || len(pending) != 1 || pending[0].ContextCommitID != "commit" {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	if err := MarkCollaborationAdmissionPublished(db, "delivery", "commit"); err != nil {
		t.Fatal(err)
	}
	pending, err = PendingCollaborationAdmissions(db, "agent")
	if err != nil || len(pending) != 0 {
		t.Fatalf("published admission remained pending = %+v, %v", pending, err)
	}
}

func TestCollaborationAdmissionConflictRollsBackContext(t *testing.T) {
	db := OpenAndMigrateAt(t.TempDir())
	defer db.Close()
	if err := CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	first := ContextState{
		AgentID: "agent", Generation: 1, Reason: "first",
		Messages: []provider.ChatMessage{provider.NewTextMessage("user", "first")},
	}
	admission := CollaborationAdmission{
		DeliveryID: "delivery", EventID: "event", SessionID: "session",
		AgentID: "agent", RuntimeEpoch: 1, DeliveryVersion: 2, ContextCommitID: "commit",
	}
	if err := SaveContextStateWithAdmissions(db, "session", "agent", "agent", first, []CollaborationAdmission{admission}); err != nil {
		t.Fatal(err)
	}
	second := ContextState{
		AgentID: "agent", Generation: 2, Reason: "second",
		Messages: []provider.ChatMessage{provider.NewTextMessage("user", "must roll back")},
	}
	changed := admission
	changed.EventID = "different"
	if err := SaveContextStateWithAdmissions(db, "session", "agent", "agent", second, []CollaborationAdmission{changed}); err == nil {
		t.Fatal("changed admission identity succeeded")
	}
	loaded, ok, err := LoadContextState(db, "session", "agent")
	if err != nil || !ok || loaded.Generation != 1 || loaded.Messages[0].ContentString() != "first" {
		t.Fatalf("context transaction did not roll back = %+v, %v, %v", loaded, ok, err)
	}
	if err := MarkCollaborationAdmissionPublished(db, "missing", "missing"); err == nil {
		t.Fatal("missing admission was marked published")
	}
}
