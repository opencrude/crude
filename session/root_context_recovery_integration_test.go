package session

import (
	"path/filepath"
	"testing"
	"time"

	"crude/provider"
	"crude/sessiondb"
	recoverytypes "crude/types/recovery"
)

func TestRecoveryStatusPublicationPreservesNewerRootContext(t *testing.T) {
	db, err := sessiondb.OpenAndMigrate(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := sessiondb.CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	original := provider.NewTextMessage("user", "original accepted input")
	original.InternalID = "original"
	initial, err := sessiondb.PublishContext(db, sessiondb.ContextPublication{SessionID: "session", AgentID: "session", Mutation: sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{Generation: 1, Reason: "initial", Messages: []provider.ChatMessage{original}}}})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := sessiondb.RecoveryCheckpoint{
		Status:       recoverytypes.Status{ID: "incident", AgentID: "root", Generation: 1, StartedAt: time.Now()},
		PendingInput: provider.UserInput{InternalID: original.InternalID, Text: original.ContentString()}, InputAdmitted: true,
		Archive: []provider.ChatMessage{original}, ActiveContext: []provider.ChatMessage{original},
		ContextOwnerID: "session", ContextRevision: initial.Revision, ContextGeneration: initial.Generation, ContextReason: initial.Reason,
	}
	if err := sessiondb.SaveRecoveryCheckpoint(db, "session", checkpoint); err != nil {
		t.Fatal(err)
	}
	checkpoint, _, err = sessiondb.LoadRecoveryCheckpoint(db, "session", "root")
	if err != nil {
		t.Fatal(err)
	}
	state := newRecoveryRunState(checkpoint.PendingInput, &checkpoint)
	newer := provider.NewTextMessage("user", "newer accepted fact")
	newer.InternalID = "newer"
	current, err := sessiondb.PublishContext(db, sessiondb.ContextPublication{SessionID: "session", AgentID: "session", ExpectedRevision: checkpoint.ContextRevision, Mutation: sessiondb.ContextMutation{Append: []sessiondb.ContextMessageDelta{{Message: newer, CreatedAt: time.Now().UnixNano(), AgentID: "root"}}}})
	if err != nil {
		t.Fatal(err)
	}
	state.setContext("session", current.Revision, current.Generation, current.Reason)
	status := checkpoint.Status
	status.State = recoverytypes.NeedsAttention
	status.Summary = "still blocked"
	if err := state.publish(db, "session", status); err != nil {
		t.Fatal(err)
	}
	got, found, err := sessiondb.LoadContextState(db, "session", "session")
	if err != nil || !found || len(got.Messages) != 2 || got.Messages[1].InternalID != newer.InternalID {
		t.Fatalf("status-only publication regressed current context: %+v found=%v err=%v", got, found, err)
	}
}

func TestRecoveryContinuationPublishesCanonicalSnapshotOnce(t *testing.T) {
	db, err := sessiondb.OpenAndMigrate(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := sessiondb.CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	input := provider.NewTextMessage("user", "accepted input")
	input.InternalID = "input"
	initial, err := sessiondb.PublishContext(db, sessiondb.ContextPublication{SessionID: "session", AgentID: "session", Mutation: sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{Generation: 1, Reason: "initial", Messages: []provider.ChatMessage{input}}}})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := sessiondb.RecoveryCheckpoint{Status: recoverytypes.Status{ID: "incident", AgentID: "root", Generation: 1, StartedAt: time.Now()}, PendingInput: provider.UserInput{InternalID: input.InternalID, Text: input.ContentString()}, InputAdmitted: true, Archive: []provider.ChatMessage{input}, ActiveContext: []provider.ChatMessage{input}, ContextOwnerID: "session", ContextRevision: initial.Revision, ContextGeneration: initial.Generation, ContextReason: initial.Reason}
	if err := sessiondb.SaveRecoveryCheckpoint(db, "session", checkpoint); err != nil {
		t.Fatal(err)
	}
	checkpoint, _, err = sessiondb.LoadRecoveryCheckpoint(db, "session", "root")
	if err != nil {
		t.Fatal(err)
	}
	state := newRecoveryRunState(checkpoint.PendingInput, &checkpoint)
	state.appendContinuation("interrupted")
	status := checkpoint.Status
	status.State = recoverytypes.NeedsAttention
	if err := state.publish(db, "session", status); err != nil {
		t.Fatal(err)
	}
	first, found, err := sessiondb.LoadContextState(db, "session", "session")
	if err != nil || !found || len(first.Messages) != 2 || first.Messages[1].EffectiveOrigin() != provider.OriginContinuation {
		t.Fatalf("continuation not published: %+v found=%v err=%v", first, found, err)
	}
	if err := state.publish(db, "session", status); err != nil {
		t.Fatal(err)
	}
	second, _, err := sessiondb.LoadContextState(db, "session", "session")
	if err != nil || len(second.Messages) != 2 {
		t.Fatalf("status retry duplicated canonical continuation: %+v err=%v", second, err)
	}
}
