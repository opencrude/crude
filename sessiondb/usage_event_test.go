package sessiondb

import (
	"path/filepath"
	"testing"
)

func TestUsageEventAndPersistencePreserveFullGeneration(t *testing.T) {
	generation := uint64(1)<<63 + 17
	r, ok := UsageRecordFromEvent(map[string]any{
		"request_id": "request", "turn_generation": generation,
		"reported_at": int64(123), "prompt": 100, "completion": 10,
		"context_prompt": 100, "request_kind": "compaction",
	}, "session", "agent", "", "model")
	if !ok || r.TurnGeneration != generation || r.RequestKind != "compaction" {
		t.Fatalf("generation lost: %+v", r)
	}
	db, err := OpenAndMigrate(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := UpsertUsageRecord(db, r); err != nil {
		t.Fatal(err)
	}
	records, err := ListUsageRecords(db)
	if err != nil || len(records) != 1 || records[0].TurnGeneration != generation || records[0].RequestKind != "compaction" {
		t.Fatalf("generation changed at SQLite boundary: %+v err=%v", records, err)
	}
}

func TestUsageEventRejectsMissingRequestIdentity(t *testing.T) {
	if _, ok := UsageRecordFromEvent(map[string]any{"prompt": 100}, "session", "agent", "", "model"); ok {
		t.Fatal("decoder invented a different identity for each consumer")
	}
}
