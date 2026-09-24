package sessiondb

import (
	"encoding/json"
	"testing"
	"time"

	"crude/provider"
)

func TestCommitContextCompactionArchivesPreviousGenerationAtomically(t *testing.T) {
	db := OpenAndMigrateAt(t.TempDir())
	t.Cleanup(func() { _ = db.Close() })
	if err := CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	old := provider.NewTextMessage("user", "exact old active context")
	if err := SaveContextState(db, "session", "root", ContextState{
		AgentID: "root", Generation: 3, Reason: "turn_complete", Messages: []provider.ChatMessage{old},
	}); err != nil {
		t.Fatal(err)
	}
	started := time.Now().Add(-time.Second)
	if err := SaveCompactionRecord(db, CompactionRecord{
		OperationID: "compact-1", SessionID: "session", AgentID: "root", Trigger: "manual", Role: "root",
		State: CompactionExtracting, AccountID: "work", StageName: "state", StageIndex: 3, CompletedStages: 2,
		StartedAt: started, UpdatedAt: started,
	}); err != nil {
		t.Fatal(err)
	}
	checkpoint := provider.NewTextMessage("user", "<context_checkpoint/>")
	if err := CommitContextCompaction(db, "session", "root", ContextState{
		AgentID: "root", Generation: 4, Reason: "manual_compaction", Messages: []provider.ChatMessage{checkpoint},
	}, CompactionRecord{
		OperationID: "compact-1", SessionID: "session", AgentID: "root", Trigger: "manual", Role: "root",
		State: CompactionCompleted, StageIndex: 4, CompletedStages: 4, BeforeTokens: 155955, AfterTokens: 10476,
	}); err != nil {
		t.Fatal(err)
	}
	state, ok, err := LoadContextState(db, "session", "root")
	if err != nil || !ok || state.Generation != 4 || len(state.Messages) != 1 || state.Messages[0].ContentString() != checkpoint.ContentString() {
		t.Fatalf("active context=%+v ok=%v err=%v", state, ok, err)
	}
	var archived []byte
	if err := db.QueryRow(`SELECT messages_json FROM agent_context_generations WHERE session_id = ? AND agent_id = ? AND generation = 3`, "session", "root").Scan(&archived); err != nil {
		t.Fatal(err)
	}
	var oldMessages []provider.ChatMessage
	if err := json.Unmarshal(archived, &oldMessages); err != nil || len(oldMessages) != 1 || oldMessages[0].ContentString() != old.ContentString() {
		t.Fatalf("archived generation=%+v err=%v", oldMessages, err)
	}
	records, err := ListCompactionRecords(db, "session", "root")
	if err != nil || len(records) != 1 || records[0].State != CompactionCompleted || records[0].BeforeTokens != 155955 || records[0].AfterTokens != 10476 || records[0].AccountID != "work" {
		t.Fatalf("records=%+v err=%v", records, err)
	}
}

func TestInterruptRunningCompactionsPreservesTerminalRecords(t *testing.T) {
	db := OpenAndMigrateAt(t.TempDir())
	t.Cleanup(func() { _ = db.Close() })
	if err := CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	for _, record := range []CompactionRecord{
		{OperationID: "running", SessionID: "session", AgentID: "root", Trigger: "automatic", Role: "root", State: CompactionRetrying},
		{OperationID: "done", SessionID: "session", AgentID: "root", Trigger: "manual", Role: "root", State: CompactionCompleted},
	} {
		if err := SaveCompactionRecord(db, record); err != nil {
			t.Fatal(err)
		}
	}
	if err := InterruptRunningCompactions(db, "session"); err != nil {
		t.Fatal(err)
	}
	records, err := ListCompactionRecords(db, "session", "root")
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, record := range records {
		states[record.OperationID] = record.State
	}
	if states["running"] != CompactionInterrupted || states["done"] != CompactionCompleted {
		t.Fatalf("states=%v", states)
	}
}

func TestInterruptedCompactionKeepsExactPersistedSourceContext(t *testing.T) {
	db := OpenAndMigrateAt(t.TempDir())
	t.Cleanup(func() { _ = db.Close() })
	if err := CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	source := []provider.ChatMessage{
		provider.NewTextMessage("user", "current request"),
		provider.NewTextMessage("assistant", "in-flight evidence before compact"),
	}
	if err := BeginContextCompaction(db, "session", "root", ContextState{
		AgentID: "root", Generation: 7, Reason: "compaction_source", Messages: source,
	}, CompactionRecord{
		OperationID: "compact-crash", SessionID: "session", AgentID: "root",
		Trigger: "automatic", Role: "root", State: CompactionExtracting,
	}); err != nil {
		t.Fatal(err)
	}
	if err := InterruptRunningCompactions(db, "session"); err != nil {
		t.Fatal(err)
	}
	state, ok, err := LoadContextState(db, "session", "root")
	if err != nil || !ok || state.Generation != 7 || len(state.Messages) != len(source) || state.Messages[1].ContentString() != source[1].ContentString() {
		t.Fatalf("restored source=%+v ok=%v err=%v", state, ok, err)
	}
	records, err := ListCompactionRecords(db, "session", "root")
	if err != nil || len(records) != 1 || records[0].State != CompactionInterrupted {
		t.Fatalf("records=%+v err=%v", records, err)
	}
}
