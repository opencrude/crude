package widgets

import (
	"testing"
	"time"

	"crude/tui/theme"
	"crude/tui/types"
)

func TestMessageListSyncMessagesPreservesUnchangedCaches(t *testing.T) {
	ml := NewMessageList(theme.DefaultDark())
	now := time.Unix(1, 0)
	messages := []types.ChatMessage{{InternalID: "domain-1", Role: types.RoleAssistant, Content: "hello", Time: now}}
	ml.SyncMessages(messages, 1)
	ml.messages[0].CachedLines = []string{"rendered"}
	ml.messages[0].StreamCompletedLines = []string{"streamed"}

	ml.SyncMessages(messages, 2)

	if got := ml.messages[0].CachedLines; len(got) != 1 || got[0] != "rendered" {
		t.Fatalf("unchanged cache was discarded: %#v", got)
	}
	if got := ml.messages[0].StreamCompletedLines; len(got) != 1 || got[0] != "streamed" {
		t.Fatalf("unchanged streaming cache was discarded: %#v", got)
	}
}

func TestMessageListSyncMessagesInvalidatesChangedItem(t *testing.T) {
	ml := NewMessageList(theme.DefaultDark())
	now := time.Unix(1, 0)
	ml.SyncMessages([]types.ChatMessage{{InternalID: "domain-1", Role: types.RoleAssistant, Content: "old", Time: now}}, 1)
	original := ml.messages[0]
	original.CachedLines = []string{"rendered"}

	ml.SyncMessages([]types.ChatMessage{{InternalID: "domain-1", Role: types.RoleAssistant, Content: "new", Time: now}}, 2)

	if ml.messages[0] != original {
		t.Fatal("stable message ID did not preserve retained item identity")
	}
	if ml.messages[0].Content != "new" || ml.messages[0].CachedLines != nil {
		t.Fatalf("changed item was not updated and invalidated: %#v", ml.messages[0])
	}
}

func TestMessageListSyncMessagesRemovesMissingItems(t *testing.T) {
	ml := NewMessageList(theme.DefaultDark())
	ml.SyncMessages([]types.ChatMessage{
		{InternalID: "one", Role: types.RoleUser},
		{InternalID: "two", Role: types.RoleAssistant},
	}, 1)

	ml.SyncMessages([]types.ChatMessage{{InternalID: "two", Role: types.RoleAssistant}}, 2)

	if len(ml.messages) != 1 || ml.messages[0].InternalID != "two" {
		t.Fatalf("unexpected retained messages: %#v", ml.messages)
	}
	if ml.msgByID["one"] != nil {
		t.Fatal("removed message remained indexed")
	}
}

func TestMessageListSyncMessagesAppendsWithoutRebuildingExistingItem(t *testing.T) {
	ml := NewMessageList(theme.DefaultDark())
	first := types.ChatMessage{InternalID: "one", Role: types.RoleAssistant, Content: "first"}
	ml.SyncMessages([]types.ChatMessage{first}, 1)
	original := ml.messages[0]
	original.CachedLines = []string{"cached"}

	ml.SyncMessages([]types.ChatMessage{
		first,
		{InternalID: "two", Role: types.RoleAssistant, Content: "second"},
	}, 2)

	if ml.messages[0] != original || len(ml.messages[0].CachedLines) != 1 {
		t.Fatal("append discarded the retained first item")
	}
	if ml.vl.ItemCount() != 2 {
		t.Fatalf("virtual item count = %d, want 2", ml.vl.ItemCount())
	}
}
