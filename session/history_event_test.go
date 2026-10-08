package session

import (
	"path/filepath"
	"testing"
	"time"

	"crude/provider"
	"crude/sessiondb"
)

func TestWorkspaceHistoryEventAppendsWithoutMutatingPrefix(t *testing.T) {
	original := provider.NewTextMessage("user", "original")
	original.InternalID = "original-input"
	s, err := NewWithSessionDir("history-event", testConfig(), t.TempDir(), filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	when := time.Now()
	meta := canonicalizeDisplayMeta(original, sessiondb.DisplayMeta{DisplayRole: "user"}, "", 0, when)
	if _, err := s.publishRootContext(sessiondb.ContextPublication{Mutation: sessiondb.ContextMutation{Append: []sessiondb.ContextMessageDelta{contextDelta(original, meta, 1, when, "root")}}}); err != nil {
		t.Fatal(err)
	}
	s.msgs, s.msgTurns, s.displayMetas, s.createdAts = []provider.ChatMessage{original}, []int{1}, []sessiondb.DisplayMeta{meta}, []int64{when.UnixNano()}
	s.turnNumber = 1
	if err := s.AppendWorkspaceHistoryEvent("<workspace_history_event>restored</workspace_history_event>", "Restored one file"); err != nil {
		t.Fatal(err)
	}
	if len(s.msgs) != 2 || len(s.activeContext) != 2 || s.msgs[0].Content.String() != "original" || s.activeContext[0].Content.String() != "original" {
		t.Fatalf("archive=%+v context=%+v", s.msgs, s.activeContext)
	}
	if s.displayMetas[1].ContextOnly || len(s.displayMetas[1].Snapshots) != 1 {
		t.Fatalf("meta=%+v", s.displayMetas)
	}
	rows, err := sessiondb.LoadMessagesWithMeta(s.db)
	if err != nil || len(rows) != 2 || len(rows[1].Meta.Snapshots) != 1 || rows[1].Meta.Snapshots[0].Content != "Restored one file" {
		t.Fatalf("restored boundary was not durably visible: rows=%+v error=%v", rows, err)
	}
}
