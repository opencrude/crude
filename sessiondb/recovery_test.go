package sessiondb

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/provider"
	recoverytypes "crude/types/recovery"
)

func TestRecoveryCheckpointRoundTripAndStartupInterruption(t *testing.T) {
	db, err := OpenAndMigrate(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	started := time.Unix(1700000000, 123)
	updated := started.Add(time.Second)
	input := provider.UserInput{
		InternalID: "msg-1", Text: "finish the exact task", DisplayText: "finish",
		ReferenceIDs: []string{"ref-1"}, Origin: provider.OriginHuman,
		ContextParts: []string{"context"}, ActivePaths: []string{"main.go"},
	}
	archive := []provider.ChatMessage{
		provider.NewTextMessage("user", "finish the exact task"),
		provider.NewTextMessage("assistant", "accepted partial"),
	}
	active := append([]provider.ChatMessage(nil), archive...)
	checkpoint := RecoveryCheckpoint{
		Status: recoverytypes.Status{
			ID: "recovery-1", AgentID: "root", Generation: 7,
			StartedAt: started, UpdatedAt: updated,
		},
		PendingInput: input, InputAdmitted: true, Archive: archive, ActiveContext: active,
		ContextOwnerID: "session", ContextGeneration: 3, ContextReason: "run_checkpoint",
		OwnerData: []byte(`{"kind":"root"}`),
	}
	if err := SaveRecoveryCheckpoint(db, "session", checkpoint); err != nil {
		t.Fatal(err)
	}
	if records, err := LoadRecoveryRecords(db, "session"); err != nil || len(records) != 0 {
		t.Fatalf("internal pending envelope leaked into recovery transcript: %+v, %v", records, err)
	}

	checkpoint.Status.State = recoverytypes.Recovering
	checkpoint.Status.Attempt = 2
	checkpoint.Status.RetryAt = updated.Add(5 * time.Minute)
	checkpoint.Status.Summary = "Provider connection is recovering"
	checkpoint.Status.Detail = strings.Repeat("full diagnostic ", 40)
	checkpoint.Status.Action = "Stop to cancel"
	checkpoint.Status.UpdatedAt = updated.Add(time.Second)
	if err := SaveRecoveryCheckpoint(db, "session", checkpoint); err != nil {
		t.Fatal(err)
	}
	loaded, ok, err := LoadRecoveryCheckpoint(db, "session", "root")
	if err != nil || !ok {
		t.Fatalf("load checkpoint: ok=%v err=%v", ok, err)
	}
	if loaded.Status != checkpoint.Status || loaded.PendingInput.Text != input.Text ||
		len(loaded.Archive) != 2 || loaded.Archive[1].ContentString() != "accepted partial" ||
		len(loaded.ActiveContext) != 2 || !loaded.InputAdmitted ||
		loaded.ContextGeneration != 3 || string(loaded.OwnerData) != `{"kind":"root"}` {
		t.Fatalf("checkpoint mismatch:\n got=%+v\nwant=%+v", loaded, checkpoint)
	}
	state, found, err := LoadContextState(db, "session", "session")
	if err != nil || !found || state.Generation != 3 || len(state.Messages) != 2 {
		t.Fatalf("transactional active context = %+v found=%v err=%v", state, found, err)
	}

	if err := InterruptActiveRecoveries(db, "session"); err != nil {
		t.Fatal(err)
	}
	records, err := LoadRecoveryRecords(db, "session")
	if err != nil || len(records) != 1 {
		t.Fatalf("records=%+v err=%v", records, err)
	}
	if records[0].State != recoverytypes.Interrupted || !records[0].RetryAt.IsZero() ||
		records[0].Detail != checkpoint.Status.Detail ||
		records[0].Action != "Continue to resume this unfinished run" {
		t.Fatalf("interrupted status lost truth: %+v", records[0])
	}
}

func TestRecoveryCheckpointTransactionRollsBackStatusWithContext(t *testing.T) {
	db, err := OpenAndMigrate(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TRIGGER reject_recovery_context
		BEFORE INSERT ON agent_context_state
		BEGIN SELECT RAISE(ABORT, 'blocked context'); END
	`); err != nil {
		t.Fatal(err)
	}
	err = SaveRecoveryCheckpoint(db, "session", RecoveryCheckpoint{
		Status: recoverytypes.Status{
			ID: "recovery-rollback", AgentID: "root", Generation: 1,
			State: recoverytypes.NeedsAttention, StartedAt: time.Now(), UpdatedAt: time.Now(),
		},
		PendingInput:   provider.PlainInput("pending"),
		Archive:        []provider.ChatMessage{provider.NewTextMessage("user", "pending")},
		ActiveContext:  []provider.ChatMessage{provider.NewTextMessage("user", "pending")},
		ContextOwnerID: "session", ContextGeneration: 1,
	})
	if err == nil {
		t.Fatal("checkpoint unexpectedly committed without its active context")
	}
	var count int
	if queryErr := db.QueryRow(`SELECT COUNT(*) FROM agent_recoveries WHERE incident_id='recovery-rollback'`).Scan(&count); queryErr != nil {
		t.Fatal(queryErr)
	}
	if count != 0 {
		t.Fatal("recovery status committed without its transactional context")
	}
}
