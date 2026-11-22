package chatprojection

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/types/conversation"
	"crude/types/recovery"
)

func TestRedundantStopNoticesAreNeutralToRowsHeadersAndGrouping(t *testing.T) {
	for _, notice := range []conversation.ChatMessage{
		{InternalID: "stop", Role: conversation.RoleStatus, LifecyclePhase: "stopped", LifecycleOperationID: "stop"},
		{InternalID: "incident", Role: conversation.RoleStatus, Recovery: &recovery.Status{ID: "incident", State: recovery.Cancelled, Detail: "retained cancelled incident"}},
	} {
		for _, content := range [][]conversation.ChatMessage{
			{{InternalID: "a", Role: conversation.RoleAssistant, Content: "Accepted partial findings", TurnGeneration: 1}, {InternalID: "b", Role: conversation.RoleAssistant, Content: "More accepted findings", TurnGeneration: 1}},
			{{InternalID: "a", Role: conversation.RoleTool, ToolName: "read_file", ToolArgs: "a.go", ToolStatus: conversation.ToolStatusSuccess}, {InternalID: "b", Role: conversation.RoleTool, ToolName: "read_file", ToolArgs: "b.go", ToolStatus: conversation.ToolStatusSuccess}},
		} {
			baseline := newActivityRenderer()
			baseline.SyncMessages(content, 1, 0)
			want := baseline.ExportMessageRowsSnapshot(100)
			r := newActivityRenderer()
			r.SyncMessages([]conversation.ChatMessage{content[0], notice, content[1]}, 1, 0)
			got := r.ExportMessageRowsSnapshot(100)
			if got.ContentHeight != want.ContentHeight || !reflect.DeepEqual(got.Rows, want.Rows) {
				t.Fatalf("notice %s changed visible adjacency/spacing: got %+v want %+v", notice.InternalID, got, want)
			}
			if len(r.sync.Messages) != 3 || r.sync.MsgByID[notice.InternalID] == nil {
				t.Fatal("presentation filtering removed canonical evidence")
			}
		}
	}
}

func TestStopFilteringKeepsRecoveryEvidenceAndExactActions(t *testing.T) {
	for _, state := range []recovery.State{recovery.Recovering, recovery.NeedsAttention, recovery.Interrupted, recovery.Recovered, recovery.Cancelled} {
		for _, cooldown := range []bool{false, true} {
			status := recovery.Status{ID: "incident", AgentID: "owner", Generation: 7, State: state, Detail: "Full diagnostic evidence", Action: "Repair the exact owning account"}
			if cooldown {
				status.RetryAt = time.UnixMilli(30000)
			}
			r := newActivityRenderer()
			// A stale generic phase cannot hide a real actionable/resumed incident.
			r.SyncMessages([]conversation.ChatMessage{{InternalID: status.ID, Role: conversation.RoleStatus, LifecyclePhase: "stopped", Recovery: &status}}, 1, 0)
			r.ToggleDisclosure(status.ID)
			got := r.ExportMessageRowsSnapshot(100)
			if state == recovery.Cancelled {
				if len(got.Rows) != 0 || got.ContentHeight != 0 {
					t.Fatal("cancelled generic incident retained rows/actions")
				}
				continue
			}
			if len(got.Rows) != 1 || !strings.Contains(strings.Join(got.Rows[0].Lines, "\n"), status.Detail) || !strings.Contains(strings.Join(got.Rows[0].Lines, "\n"), status.Action) {
				t.Fatalf("state=%s lost full recovery evidence: %+v", state, got.Rows)
			}
			actions := 0
			for _, line := range got.Rows[0].Native {
				for _, span := range line.Spans {
					if span.Action != nil {
						actions++
						want := ChatRecoveryRetryPayload{AgentID: "owner", IncidentID: "incident", Generation: 7}
						if span.Action.ID != ChatRecoveryRetryActionID || span.Action.Payload != want {
							t.Fatalf("recovery action lost exact ownership: %+v", span.Action)
						}
					}
				}
			}
			wantAction := state == recovery.NeedsAttention || state == recovery.Interrupted || state == recovery.Recovering && cooldown
			if (actions == 1) != wantAction || actions > 1 {
				t.Fatalf("state=%s cooldown=%v actions=%d", state, cooldown, actions)
			}
		}
	}
}

func TestStopFilteringDoesNotHideCancelledToolsErrorsOrContextBoundaries(t *testing.T) {
	for _, message := range []conversation.ChatMessage{
		{InternalID: "cancelled-tool", Role: conversation.RoleTool, ToolName: "read_file", ToolStatus: conversation.ToolStatusCancelled, Content: "Retained partial output"},
		{InternalID: "error", Role: conversation.RoleError, Content: "Actual failure"},
		{InternalID: "failed", Role: conversation.RoleStatus, LifecyclePhase: "failed", Content: "Failure reason"},
		{InternalID: "compaction", Role: conversation.RoleStatus, LifecycleOperationID: "compact", LifecyclePhase: "compaction_cancelled"},
		{InternalID: "history", Role: conversation.RoleStatus, LifecycleOperationID: "history", LifecyclePhase: "history_restored"},
	} {
		r := newActivityRenderer()
		r.SyncMessages([]conversation.ChatMessage{message}, 1, 0)
		got := r.ExportMessageRowsSnapshot(100)
		if len(got.Rows) != 1 || got.ContentHeight == 0 || got.Rows[0].ID != message.InternalID {
			t.Fatalf("stop filter erased non-generic evidence: %+v", message)
		}
	}
}
