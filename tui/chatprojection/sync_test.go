package chatprojection

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"crude/agent/provider"
	"crude/tui/theme"
	"crude/types/command"
	"crude/types/conversation"
)

func TestRendererSyncMessagesPreservesUnchangedCaches(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	now := time.Unix(1, 0)
	messages := []conversation.ChatMessage{{InternalID: "domain-1", Role: conversation.RoleAssistant, Content: "hello", Time: now}}
	r.SyncMessages(messages, 1, 0)
	r.sync.Messages[0].CachedLines = []string{"rendered"}

	r.SyncMessages(messages, 2, 0)

	if got := r.sync.Messages[0].CachedLines; len(got) != 1 || got[0] != "rendered" {
		t.Fatalf("unchanged cache was discarded: %#v", got)
	}

}

func TestRendererSyncMessagesAcceptsResetWithReusedRevision(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SetSize(80, 20)
	r.SetChatAreaBounds(0, 12)
	r.SyncMessages([]conversation.ChatMessage{{InternalID: "same", Role: conversation.RoleAssistant, Content: "old source"}}, 1, 1)
	_, _, _ = r.ExportMessageRows(80)
	r.SyncMessages([]conversation.ChatMessage{{InternalID: "same", Role: conversation.RoleAssistant, Content: "replacement source"}}, 1, 2)
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
		change func(*conversation.ChatMessage)
	}{
		{"origin", func(m *conversation.ChatMessage) { m.Origin = provider.OriginPeer }},
		{"collaboration", func(m *conversation.ChatMessage) {
			m.Collaboration = &provider.CollaborationAttribution{ArtifactID: "report", ArtifactRevision: 2}
		}},
		{"account", func(m *conversation.ChatMessage) { m.AccountID = "other-account" }},
		{"history paths", func(m *conversation.ChatMessage) { m.HistoryPathCount = 2 }},
		{"history target", func(m *conversation.ChatMessage) { m.HistoryTargetSnapshot = "target" }},
		{"history safety", func(m *conversation.ChatMessage) { m.HistorySafetySnapshot = "safety" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewMessageSync()
			message := conversation.ChatMessage{InternalID: "same", Role: conversation.RoleUser, Content: "unchanged text"}
			s.SyncMessages([]conversation.ChatMessage{message}, 1, 1)
			s.Messages[0].CachedLines = []string{"old painted facts"}
			tc.change(&message)
			result, changed := s.SyncMessages([]conversation.ChatMessage{message}, 2, 1)
			if !changed || len(result.ChangedIDs) != 1 || s.Messages[0].CachedLines != nil {
				t.Fatalf("metadata update retained stale caches: %+v", result)
			}
		})
	}
}

func TestRendererSyncMessagesInvalidatesChangedItem(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	now := time.Unix(1, 0)
	r.SyncMessages([]conversation.ChatMessage{{InternalID: "domain-1", Role: conversation.RoleAssistant, Content: "old", Time: now}}, 1, 0)
	original := r.sync.Messages[0]
	original.CachedLines = []string{"rendered"}

	r.SyncMessages([]conversation.ChatMessage{{InternalID: "domain-1", Role: conversation.RoleAssistant, Content: "new", Time: now}}, 2, 0)

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
	message := conversation.ChatMessage{InternalID: "btw", Role: conversation.RoleUser, Content: "use edit tool", PendingSteering: true}
	r.SyncMessages([]conversation.ChatMessage{message}, 1, 0)
	before, _, _ := r.ExportMessageRows(80)
	if len(before) != 1 || !strings.Contains(strings.Join(before[0].Lines, "\n"), "Waiting to send") {
		t.Fatalf("pending row = %#v", before)
	}
	message.PendingSteering = false
	r.SyncMessages([]conversation.ChatMessage{message}, 2, 0)
	after, _, _ := r.ExportMessageRows(80)
	if len(after) != 1 || strings.Contains(strings.Join(after[0].Lines, "\n"), "Waiting to send") {
		t.Fatalf("consumed human steering status remained painted: %#v", after)
	}
}

func TestUserHeaderSurvivesBackgroundOutcomeAndSteeringAdmission(t *testing.T) {
	const width = 80
	newRenderer := func() *Renderer {
		r := NewRenderer(theme.DefaultDark())
		r.SetSize(width, 24)
		r.SetChatAreaBounds(0, 20)
		r.SetUsername("tester")
		return r
	}
	stamp := time.Unix(1780000000, 0)
	before := []conversation.ChatMessage{
		{InternalID: "first", Role: conversation.RoleUser, Content: "Verify the change", Time: stamp},
		{InternalID: "reply", Role: conversation.RoleAssistant, Content: "I am checking it", ModelID: "test-model", Time: stamp},
		{InternalID: "job", Role: conversation.RoleUser, Origin: provider.OriginPeer, Collaboration: &provider.CollaborationAttribution{
			Kind: "command_outcome", Command: &command.Outcome{Handle: "checks", Status: "failed", ExitCode: 1, ExitKnown: true},
		}},
		{InternalID: "read", Role: conversation.RoleTool, ToolName: "read_file", ToolStatus: "success"},
	}
	for _, origin := range []provider.MessageOrigin{provider.OriginHuman, provider.OriginHumanSteering} {
		t.Run(string(origin), func(t *testing.T) {
			r := newRenderer()
			message := conversation.ChatMessage{InternalID: "follow", Role: conversation.RoleUser, Origin: origin,
				Content: "Check the download and the attachment", Time: stamp.Add(time.Second), PendingSteering: origin == provider.OriginHumanSteering}
			r.SyncMessages(before, 1, 1)
			r.ExportMessageRows(width)
			order := []string{"first", "reply", "job", "read", "follow"}
			if !r.SyncChanges([]conversation.ChatMessage{message}, nil, order, 2, 1) {
				t.Fatal("live message not installed")
			}
			assertPrompt := func(r *Renderer, waiting bool) []PaintLine {
				t.Helper()
				rows, _, _ := r.ExportMessageRows(width)
				for _, row := range rows {
					if row.ID != "follow" {
						continue
					}
					text := strings.Join(row.Lines, "\n")
					if len(row.Native) < 3 || !row.Native[1].HeaderRule || !strings.HasPrefix(row.Lines[1], "tester ") ||
						!strings.Contains(text, message.Content) || strings.Contains(text, "Waiting to send") != waiting {
						t.Fatalf("human prompt lost its own header or acknowledgement: %q", text)
					}
					return row.Native
				}
				t.Fatal("human prompt not painted")
				return nil
			}
			assertPrompt(r, message.PendingSteering)
			if message.PendingSteering {
				message.PendingSteering = false
				if !r.SyncChanges([]conversation.ChatMessage{message}, nil, nil, 3, 1) {
					t.Fatal("steering acknowledgement not installed")
				}
			}
			live := assertPrompt(r, false)
			if origin == provider.OriginHumanSteering {
				message.Collaboration = &provider.CollaborationAttribution{EventID: "original-event", Kind: "human_steering", SenderID: "human", SenderName: "tester", RunID: "original-run"}
			}
			cold := newRenderer()
			cold.SyncMessages(append(slices.Clone(before), message), 1, 1)
			if revived := assertPrompt(cold, false); !reflect.DeepEqual(live, revived) {
				t.Fatalf("revived prompt ink or layout differs from live: %#v / %#v", live, revived)
			}
		})
	}
}

func TestRendererSyncMessagesRemovesMissingItems(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SyncMessages([]conversation.ChatMessage{
		{InternalID: "one", Role: conversation.RoleUser},
		{InternalID: "two", Role: conversation.RoleAssistant},
	}, 1, 0)

	r.SyncMessages([]conversation.ChatMessage{{InternalID: "two", Role: conversation.RoleAssistant}}, 2, 0)

	if len(r.sync.Messages) != 1 || r.sync.Messages[0].InternalID != "two" {
		t.Fatalf("unexpected retained messages: %#v", r.sync.Messages)
	}
	if r.sync.MsgByID["one"] != nil {
		t.Fatal("removed message remained indexed")
	}
}

func TestRendererSyncMessagesAppendsWithoutRebuildingExistingItem(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	first := conversation.ChatMessage{InternalID: "one", Role: conversation.RoleAssistant, Content: "first"}
	r.SyncMessages([]conversation.ChatMessage{first}, 1, 0)
	original := r.sync.Messages[0]
	original.CachedLines = []string{"cached"}

	r.SyncMessages([]conversation.ChatMessage{
		first,
		{InternalID: "two", Role: conversation.RoleAssistant, Content: "second"},
	}, 2, 0)

	if r.sync.Messages[0] != original || len(r.sync.Messages[0].CachedLines) != 1 {
		t.Fatal("append discarded the retained first item")
	}
	if r.vl.ItemCount() != 2 {
		t.Fatalf("virtual item count = %d, want 2", r.vl.ItemCount())
	}
}
