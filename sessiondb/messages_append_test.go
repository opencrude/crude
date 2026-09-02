package sessiondb

import (
	"path/filepath"
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
