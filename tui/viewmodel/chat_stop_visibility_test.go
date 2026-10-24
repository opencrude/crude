package viewmodel

import (
	"reflect"
	"testing"

	"crude/provider"
	"crude/sessiondb"
	"crude/tui/chatprojection"
	"crude/tui/theme"
	"crude/types"
	"crude/workspace"
)

func TestLiveFinishAndHydratedStopPreserveContentWithoutTranscriptNotice(t *testing.T) {
	conversation := workspace.NewConversation()
	conversation.AppendMessage(workspace.Message{InternalID: "partial", Role: types.RoleAssistant, Content: "Accepted partial investigation"})
	conversation.Finish("", "cancelled", true, "stop")
	live, version := chatMessagesFromConversation(conversation)
	if len(live) != 2 || live[1].LifecyclePhase != "stopped" || live[1].LifecycleOperationID != "stop" {
		t.Fatalf("real Finish path did not retain its canonical boundary: %+v", live)
	}
	// This is the persisted snapshot shape authored by canonicalizeDisplayMeta
	// for OriginTurnStopped, passed through the real resume display converter.
	views := workspace.ConvertMessagesForDisplay([]sessiondb.MessageWithMeta{{
		Msg: provider.ChatMessage{Role: "user", Origin: provider.OriginTurnStopped},
		Meta: sessiondb.DisplayMeta{SnapshotVersion: sessiondb.ConversationSnapshotVersion, Snapshots: []types.ConversationMessage{{
			InternalID: "stop", Role: types.RoleStatus, LifecyclePhase: "stopped", LifecycleOperationID: "stop", Time: live[1].Time,
		}}},
	}})
	hydrated := append(append([]types.ChatMessage(nil), live[:1]...), ChatMessagesFromViews(views)...)
	var first chatprojection.MessageRowsSnapshot
	for i, messages := range [][]types.ChatMessage{live, hydrated} {
		r := chatprojection.NewRenderer(theme.DefaultDark())
		r.SetSize(100, 30)
		r.SetChatAreaBounds(0, 30)
		r.SyncMessages(messages, version, 0)
		got := r.ExportMessageRowsSnapshot(100)
		if len(got.Rows) != 1 || got.Rows[0].ID != "partial" || got.ContentHeight != len(got.Rows[0].Lines) {
			t.Fatalf("stop left a row or spacer after accepted content: %+v", got)
		}
		for _, frame := range []int{400, 800, 9000} {
			r.SetSpinFrame(frame)
			later := r.ExportMessageRowsSnapshot(100)
			if !reflect.DeepEqual(got.Rows, later.Rows) || got.ContentHeight != later.ContentHeight {
				t.Fatal("accepted partial content animated after stop")
			}
		}
		if i == 0 {
			first = got
		} else if !reflect.DeepEqual(first.Rows, got.Rows) || first.ContentHeight != got.ContentHeight {
			t.Fatal("live and hydrated stop projections differ")
		}
	}
}
