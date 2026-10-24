package chatprojection

import (
	"strings"
	"testing"
	"time"

	"crude/provider"
	"crude/tui/theme"
	"crude/types"
)

func TestRendererSyncMessagesPreservesUnchangedCaches(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	now := time.Unix(1, 0)
	messages := []types.ChatMessage{{InternalID: "domain-1", Role: types.RoleAssistant, Content: "hello", Time: now}}
	r.SyncMessages(messages, 1, 0)
	r.sync.Messages[0].CachedLines = []string{"rendered"}
	r.sync.Messages[0].StreamCompletedLines = []string{"streamed"}

	r.SyncMessages(messages, 2, 0)

	if got := r.sync.Messages[0].CachedLines; len(got) != 1 || got[0] != "rendered" {
		t.Fatalf("unchanged cache was discarded: %#v", got)
	}
	if got := r.sync.Messages[0].StreamCompletedLines; len(got) != 1 || got[0] != "streamed" {
		t.Fatalf("unchanged streaming cache was discarded: %#v", got)
	}
}

func TestRendererSyncMessagesAcceptsResetWithReusedRevision(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SetSize(80, 20)
	r.SetChatAreaBounds(0, 12)
	r.SyncMessages([]types.ChatMessage{{InternalID: "same", Role: types.RoleAssistant, Content: "old source"}}, 1, 1)
	_, _, _ = r.ExportMessageRows(80)
	r.SyncMessages([]types.ChatMessage{{InternalID: "same", Role: types.RoleAssistant, Content: "replacement source"}}, 1, 2)
	rows, _, _ := r.ExportMessageRows(80)
	if len(rows) != 1 || !strings.Contains(strings.Join(rows[0].Lines, "\n"), "replacement source") {
		t.Fatalf("reset reused stale rendered rows: %+v", rows)
	}
	r.SyncMessages(nil, 0, 3)
	rows, _, _ = r.ExportMessageRows(80)
	if len(rows) != 0 {
		t.Fatal("empty replacement source retained old rows")
	}
}

func TestMessageSyncMetadataOnlyChangesInvalidateRetainedRows(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*types.ChatMessage)
	}{
		{"origin", func(m *types.ChatMessage) { m.Origin = provider.OriginPeer }},
		{"collaboration", func(m *types.ChatMessage) {
			m.Collaboration = &provider.CollaborationAttribution{ArtifactID: "report", ArtifactRevision: 2}
		}},
		{"account", func(m *types.ChatMessage) { m.AccountID = "other-account" }},
		{"history paths", func(m *types.ChatMessage) { m.HistoryPathCount = 2 }},
		{"history target", func(m *types.ChatMessage) { m.HistoryTargetSnapshot = "target" }},
		{"history safety", func(m *types.ChatMessage) { m.HistorySafetySnapshot = "safety" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewMessageSync()
			message := types.ChatMessage{InternalID: "same", Role: types.RoleUser, Content: "unchanged text"}
			s.SyncMessages([]types.ChatMessage{message}, 1, 1)
			s.Messages[0].CachedLines = []string{"old painted facts"}
			tc.change(&message)
			result, changed := s.SyncMessages([]types.ChatMessage{message}, 2, 1)
			if !changed || len(result.ChangedIDs) != 1 || s.Messages[0].CachedLines != nil {
				t.Fatalf("metadata update retained stale caches: %+v", result)
			}
		})
	}
}

func TestRendererSyncMessagesInvalidatesChangedItem(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	now := time.Unix(1, 0)
	r.SyncMessages([]types.ChatMessage{{InternalID: "domain-1", Role: types.RoleAssistant, Content: "old", Time: now}}, 1, 0)
	original := r.sync.Messages[0]
	original.CachedLines = []string{"rendered"}

	r.SyncMessages([]types.ChatMessage{{InternalID: "domain-1", Role: types.RoleAssistant, Content: "new", Time: now}}, 2, 0)

	if r.sync.Messages[0] != original {
		t.Fatal("stable message ID did not preserve retained item identity")
	}
	if r.sync.Messages[0].Content != "new" || r.sync.Messages[0].CachedLines != nil {
		t.Fatalf("changed item was not updated and invalidated: %#v", r.sync.Messages[0])
	}
}

func TestRendererSyncMessagesClearsAdmittedSteeringStatus(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SetSize(80, 20)
	r.SetChatAreaBounds(0, 12)
	message := types.ChatMessage{InternalID: "btw", Role: types.RoleUser, Content: "use edit tool", PendingSteering: true}
	r.SyncMessages([]types.ChatMessage{message}, 1, 0)
	before, _, _ := r.ExportMessageRows(80)
	if len(before) != 1 || !strings.Contains(strings.Join(before[0].Lines, "\n"), "Waiting to send") {
		t.Fatalf("pending row = %#v", before)
	}
	message.PendingSteering = false
	r.SyncMessages([]types.ChatMessage{message}, 2, 0)
	after, _, _ := r.ExportMessageRows(80)
	if len(after) != 1 || strings.Contains(strings.Join(after[0].Lines, "\n"), "Waiting to send") {
		t.Fatalf("consumed human steering status remained painted: %#v", after)
	}
}

func TestRendererSyncMessagesRemovesMissingItems(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SyncMessages([]types.ChatMessage{
		{InternalID: "one", Role: types.RoleUser},
		{InternalID: "two", Role: types.RoleAssistant},
	}, 1, 0)

	r.SyncMessages([]types.ChatMessage{{InternalID: "two", Role: types.RoleAssistant}}, 2, 0)

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
	r.SyncMessages([]types.ChatMessage{first}, 1, 0)
	original := r.sync.Messages[0]
	original.CachedLines = []string{"cached"}

	r.SyncMessages([]types.ChatMessage{
		first,
		{InternalID: "two", Role: types.RoleAssistant, Content: "second"},
	}, 2, 0)

	if r.sync.Messages[0] != original || len(r.sync.Messages[0].CachedLines) != 1 {
		t.Fatal("append discarded the retained first item")
	}
	if r.vl.ItemCount() != 2 {
		t.Fatalf("virtual item count = %d, want 2", r.vl.ItemCount())
	}
}
