package sessiondb

import (
	"path/filepath"
	"reflect"
	"testing"

	"crude/provider"
)

func TestAppendMessageWithMetaPreservesExistingRows(t *testing.T) {
	db, err := OpenAndMigrate(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	first := provider.NewTextMessage("user", "original")
	if _, err := ReplaceMessagesWithMeta(db, "s", []provider.ChatMessage{first}, []int{1}, []DisplayMeta{{DisplayContent: "original"}}); err != nil {
		t.Fatal(err)
	}
	before, err := LoadMessagesWithMeta(db)
	if err != nil {
		t.Fatal(err)
	}
	event := provider.NewTextMessage("user", "<workspace_history_event/>")
	if err := AppendMessageWithMeta(db, "s", event, 2, DisplayMeta{ContextOnly: true}); err != nil {
		t.Fatal(err)
	}
	after, err := LoadMessagesWithMeta(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 2 || after[0].CreatedAt != before[0].CreatedAt || after[0].Msg.Content.String() != "original" {
		t.Fatalf("messages before=%+v after=%+v", before, after)
	}
}

func TestMessagePersistenceRetainsCollaborationProvenance(t *testing.T) {
	db, err := OpenAndMigrate(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	message := provider.NewTextMessage("user", "steer the exact run")
	message.InternalID = "history-1"
	message.Origin = provider.OriginHumanSteering
	message.Collaboration = &provider.CollaborationAttribution{
		EventID: "event-1", ThreadID: "thread-1", SenderID: "human", SenderName: "Human",
		RecipientIDs: []string{"agent-1"}, RecipientNames: []string{"Lily"},
		Kind: "human_steering", RequestID: "request-1", RequestRevision: 2,
		RunID: "run-1", HistoryMessageID: "history-1",
	}
	if _, err := ReplaceMessagesWithMeta(db, "s", []provider.ChatMessage{message}, []int{1}, []DisplayMeta{{DisplayContent: "steer the exact run"}}); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadMessagesWithMeta(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].Msg.InternalID != message.InternalID ||
		loaded[0].Msg.EffectiveOrigin() != provider.OriginHumanSteering ||
		!reflect.DeepEqual(loaded[0].Msg.Collaboration, message.Collaboration) {
		t.Fatalf("persisted provenance = %+v", loaded)
	}
}
