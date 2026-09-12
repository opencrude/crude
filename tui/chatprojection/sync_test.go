package chatprojection

import (
	"strings"
	"testing"
	"time"

	"crude/tui/theme"
	"crude/tui/types"
)

func TestRendererSyncMessagesPreservesUnchangedCaches(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	now := time.Unix(1, 0)
	messages := []types.ChatMessage{{InternalID: "domain-1", Role: types.RoleAssistant, Content: "hello", Time: now}}
	r.SyncMessages(messages, 1)
	r.sync.Messages[0].CachedLines = []string{"rendered"}
	r.sync.Messages[0].StreamCompletedLines = []string{"streamed"}

	r.SyncMessages(messages, 2)

	if got := r.sync.Messages[0].CachedLines; len(got) != 1 || got[0] != "rendered" {
		t.Fatalf("unchanged cache was discarded: %#v", got)
	}
	if got := r.sync.Messages[0].StreamCompletedLines; len(got) != 1 || got[0] != "streamed" {
		t.Fatalf("unchanged streaming cache was discarded: %#v", got)
	}
}

func TestRendererSyncMessagesInvalidatesChangedItem(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	now := time.Unix(1, 0)
	r.SyncMessages([]types.ChatMessage{{InternalID: "domain-1", Role: types.RoleAssistant, Content: "old", Time: now}}, 1)
	original := r.sync.Messages[0]
	original.CachedLines = []string{"rendered"}

	r.SyncMessages([]types.ChatMessage{{InternalID: "domain-1", Role: types.RoleAssistant, Content: "new", Time: now}}, 2)

	if r.sync.Messages[0] != original {
		t.Fatal("stable message ID did not preserve retained item identity")
	}
	if r.sync.Messages[0].Content != "new" || r.sync.Messages[0].CachedLines != nil {
		t.Fatalf("changed item was not updated and invalidated: %#v", r.sync.Messages[0])
	}
}

func TestRendererSyncMessagesClearsConsumedBTWStatus(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SetSize(80, 20)
	r.SetChatAreaBounds(0, 12)
	message := types.ChatMessage{InternalID: "btw", Role: types.RoleUser, Content: "use edit tool", PendingBtw: true}
	r.SyncMessages([]types.ChatMessage{message}, 1)
	before, _, _ := r.ExportMessageRows(80)
	if len(before) != 1 || !strings.Contains(strings.Join(before[0].Lines, "\n"), "Waiting to send") {
		t.Fatalf("pending row = %#v", before)
	}
	message.PendingBtw = false
	r.SyncMessages([]types.ChatMessage{message}, 2)
	after, _, _ := r.ExportMessageRows(80)
	if len(after) != 1 || strings.Contains(strings.Join(after[0].Lines, "\n"), "Waiting to send") {
		t.Fatalf("consumed BTW status remained painted: %#v", after)
	}
}

func TestRendererSyncMessagesRemovesMissingItems(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SyncMessages([]types.ChatMessage{
		{InternalID: "one", Role: types.RoleUser},
		{InternalID: "two", Role: types.RoleAssistant},
	}, 1)

	r.SyncMessages([]types.ChatMessage{{InternalID: "two", Role: types.RoleAssistant}}, 2)

	if len(r.sync.Messages) != 1 || r.sync.Messages[0].InternalID != "two" {
		t.Fatalf("unexpected retained messages: %#v", r.sync.Messages)
	}
	if r.sync.MsgByID["one"] != nil {
		t.Fatal("removed message remained indexed")
	}
}

func TestRendererSyncMessagesAppendsWithoutRebuildingExistingItem(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	first := types.ChatMessage{InternalID: "one", Role: types.RoleAssistant, Content: "first"}
	r.SyncMessages([]types.ChatMessage{first}, 1)
	original := r.sync.Messages[0]
	original.CachedLines = []string{"cached"}

	r.SyncMessages([]types.ChatMessage{
		first,
		{InternalID: "two", Role: types.RoleAssistant, Content: "second"},
	}, 2)

	if r.sync.Messages[0] != original || len(r.sync.Messages[0].CachedLines) != 1 {
		t.Fatal("append discarded the retained first item")
	}
	if r.vl.ItemCount() != 2 {
		t.Fatalf("virtual item count = %d, want 2", r.vl.ItemCount())
	}
}
