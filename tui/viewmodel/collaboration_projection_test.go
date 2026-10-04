package viewmodel

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"crude/provider"
	"crude/sessiondb"
	"crude/tui/chatprojection"
	"crude/tui/theme"
	"crude/types"
	"crude/workspace"
)

func TestCollaborationMessageLiveSQLiteHydrationParity(t *testing.T) {
	for _, themeCase := range []struct {
		name string
		th   *theme.Theme
	}{{"dark", theme.DefaultDark()}, {"light", theme.DefaultLight()}} {
		for _, width := range []int{48, 80, 160} {
			t.Run(fmt.Sprintf("%s/%d", themeCase.name, width), func(t *testing.T) {
				attribution := &provider.CollaborationAttribution{
					EventID: "event-review", ThreadID: "thread-review",
					SenderID: "child", SenderName: "Asha sub Rebecca",
					RecipientIDs: []string{"root"}, RecipientNames: []string{"Rebecca"},
					Kind: "work_request", RequestID: "request-review", RequestRevision: 2,
				}
				canonical := types.ConversationMessage{
					InternalID: "event-review", Role: types.RoleUser, Origin: provider.OriginPeer,
					Content:       "Review the parser ownership and verify the exact persisted evidence.",
					Collaboration: attribution, Time: time.Unix(123, 0),
				}
				wire := provider.NewTextMessage("user", "<workspace_event>wire-only context</workspace_event>")
				wire.InternalID = canonical.InternalID
				wire.Origin = provider.OriginPeer
				wire.DisplayContent = canonical.Content
				wire.Collaboration = attribution

				database := sessiondb.OpenAndMigrateAt(t.TempDir())
				defer database.Close()
				if err := sessiondb.CreateSessionRow(database, "session"); err != nil {
					t.Fatal(err)
				}
				if err := sessiondb.AppendMessageWithMeta(database, "session", wire, 1, sessiondb.DisplayMeta{
					SnapshotVersion: sessiondb.ConversationSnapshotVersion,
					Snapshots:       []types.ConversationMessage{canonical},
				}); err != nil {
					t.Fatal(err)
				}
				stored, err := sessiondb.LoadMessagesWithMeta(database)
				if err != nil {
					t.Fatal(err)
				}
				live := ChatMessagesFromViews([]workspace.ChatMessageView{canonical})
				hydrated := ChatMessagesFromViews(workspace.ConvertMessagesForDisplay(stored))
				render := func(messages []types.ChatMessage) []chatprojection.MessageRow {
					renderer := chatprojection.NewRenderer(themeCase.th)
					renderer.SetSize(width, 40)
					renderer.SetChatAreaBounds(0, 30)
					renderer.SyncMessages(messages, 1)
					return renderer.ExportMessageRowsSnapshot(width).Rows
				}
				left, right := render(live), render(hydrated)
				if len(left) != 1 || len(right) != 1 {
					t.Fatalf("rows live=%d hydrated=%d", len(left), len(right))
				}
				assertSameRows(t, "collaboration", left[0], right[0])
				text := strings.Join(left[0].Lines, "\n")
				plain := strings.Join(strings.Fields(strings.ReplaceAll(text, "│", " ")), " ")
				for _, expected := range []string{
					"Asha sub Rebecca", "Rebecca", "Work request",
					"Review the parser ownership", "persisted evidence",
				} {
					if !strings.Contains(plain, expected) {
						t.Fatalf("missing %q:\n%s", expected, text)
					}
				}
				if strings.Contains(text, `{"`) || strings.Contains(text, "<workspace_event>") {
					t.Fatalf("wire envelope leaked into projection:\n%s", text)
				}
				if hydrated[0].Origin != provider.OriginPeer ||
					hydrated[0].Collaboration == nil ||
					hydrated[0].Collaboration.RequestRevision != 2 {
					t.Fatalf("hydrated attribution = %+v", hydrated[0])
				}
			})
		}
	}
}

func TestRunningAgentBroadcastRendersAsRareBroadcastInsteadOfRecipientDump(t *testing.T) {
	message := types.ConversationMessage{
		InternalID: "event-broadcast", Role: types.RoleUser, Origin: provider.OriginPeer,
		Content: "Stop before publishing stale generated files.",
		Collaboration: &provider.CollaborationAttribution{
			EventID: "event-broadcast", ThreadID: "event-broadcast",
			SenderID: "asha", SenderName: "Asha",
			RecipientIDs:   []string{"b", "c", "d"},
			RecipientNames: []string{"Blake", "Casey", "Drew"},
			Kind:           "peer_note", Broadcast: true,
		},
	}
	renderer := chatprojection.NewRenderer(theme.DefaultDark())
	renderer.SetSize(80, 24)
	renderer.SetChatAreaBounds(0, 20)
	renderer.SyncMessages(ChatMessagesFromViews([]workspace.ChatMessageView{message}), 1)
	rows := renderer.ExportMessageRowsSnapshot(80).Rows
	if len(rows) != 1 {
		t.Fatalf("rows=%d", len(rows))
	}
	text := strings.Join(rows[0].Lines, "\n")
	for _, want := range []string{"Asha → 3 running agents · Broadcast", "Stop before publishing"} {
		if !strings.Contains(text, want) {
			t.Fatalf("broadcast projection missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Blake, Casey, Drew") {
		t.Fatalf("broadcast header dumped the full recipient roster:\n%s", text)
	}
}
