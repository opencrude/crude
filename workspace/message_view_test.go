package workspace

import (
	"reflect"
	"testing"
	"time"

	"crude/provider"
	"crude/sessiondb"
	types "crude/types"
)

func TestConvertMessagesForDisplayUsesCanonicalSnapshotWithoutReconstruction(t *testing.T) {
	want := types.ConversationMessage{
		InternalID: "canonical", Role: "tool", Content: "result", Time: time.Now().Round(0),
		ToolName: "agent_start", ToolArgs: "description: inspect", ToolID: "call",
		ToolStatus: "success", ToolProgressPhase: "done", ToolProgressLabel: "child",
		ToolPreviewPhase: "done", ToolEstTokens: 20, ToolLinesAdded: 2,
		ToolDuration: time.Second, SubPaneID: "child-1", PermTag: "always",
	}
	views := ConvertMessagesForDisplay([]sessiondb.MessageWithMeta{{
		Meta: sessiondb.DisplayMeta{SnapshotVersion: sessiondb.ConversationSnapshotVersion, Snapshots: []types.ConversationMessage{want}},
	}})
	want.ToolArgs = "inspect"
	want.ToolInput = "description: inspect"
	if len(views) != 1 || !reflect.DeepEqual(types.ConversationMessage(views[0]), want) {
		t.Fatalf("canonical conversion\n got: %#v\nwant: %#v", views, want)
	}
}

func TestCanonicalEmptySnapshotSuppressesProviderOnlyAssistantEnvelope(t *testing.T) {
	views := ConvertMessagesForDisplay([]sessiondb.MessageWithMeta{{
		Msg:  provider.ChatMessage{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "call"}}},
		Meta: sessiondb.DisplayMeta{SnapshotVersion: sessiondb.ConversationSnapshotVersion},
	}})
	if len(views) != 0 {
		t.Fatalf("provider-only assistant envelope became a visible hydration row: %#v", views)
	}
}

func TestConvertMessagesForDisplayExcludesContextOnlyWithoutSnapshots(t *testing.T) {
	for _, test := range []struct {
		name       string
		meta       sessiondb.DisplayMeta
		visibility provider.MessageVisibility
	}{
		{name: "memory metadata", meta: sessiondb.DisplayMeta{ContextOnly: true, DisplayRole: "hidden_runtime_context"}},
		{name: "context metadata", meta: sessiondb.DisplayMeta{ContextOnly: true, DisplayRole: "user"}},
		{name: "visibility only", visibility: provider.VisibilityContextOnly},
	} {
		t.Run(test.name, func(t *testing.T) {
			message := provider.NewTextMessage("user", "runtime context must not become display geometry")
			message.Visibility = test.visibility
			if views := ConvertMessagesForDisplay([]sessiondb.MessageWithMeta{{Msg: message, Meta: test.meta}}); len(views) != 0 {
				t.Fatalf("context-only message became visible rows: %+v", views)
			}
		})
	}
}

func TestConvertMessagesForDisplayKeepsExplicitContextBoundarySnapshots(t *testing.T) {
	for _, phase := range []string{"history_restored", "compaction_completed"} {
		t.Run(phase, func(t *testing.T) {
			message := provider.NewTextMessage("user", "hidden provider context")
			message.Visibility = provider.VisibilityContextOnly
			boundary := types.ConversationMessage{InternalID: phase, Role: "status", LifecyclePhase: phase, LifecycleOperationID: "operation", Time: time.Unix(1000, 0)}
			views := ConvertMessagesForDisplay([]sessiondb.MessageWithMeta{{Msg: message, Meta: sessiondb.DisplayMeta{
				ContextOnly: true, DisplayRole: "hidden_wire_context", SnapshotVersion: sessiondb.ConversationSnapshotVersion,
				Snapshots: []types.ConversationMessage{boundary},
			}}})
			if len(views) != 1 || !reflect.DeepEqual(views[0], boundary) {
				t.Fatalf("explicit context boundary was lost: %+v", views)
			}
		})
	}
}

// An automatic compaction must reach the chat projection with the same facts
// live and after a resume. The projection picks the quiet boundary form from the
// trigger alone, so a resumed record that lost the trigger, the stage label or
// the token result would paint a different row for the same operation.
func TestAutomaticCompactionBoundaryFieldsMatchLiveAndResumed(t *testing.T) {
	state := NewAgentState("agent", nil, nil)
	data := map[string]any{
		"request_id": "compact-auto", "trigger": "automatic", "role": "root",
		"stage": 2, "completed_stages": 1, "stage_count": 4, "stage_label": "Distilling work",
		"before_tokens": 420000, "context_window": 500000,
	}
	state.ApplyEvent(AgentEvent{Type: "context_compaction_started", Data: data})
	state.ApplyEvent(AgentEvent{Type: "context_compaction_stage", Data: data})
	messages := state.Messages.Messages()
	if len(messages) != 1 {
		t.Fatalf("live compaction rows = %+v", messages)
	}
	live := messages[0]
	resumed := messageFromView(compactionRecordView(sessiondb.CompactionRecord{
		OperationID: "compact-auto", SessionID: "session", AgentID: "agent", Trigger: "automatic", Role: "root",
		State: sessiondb.CompactionExtracting, StageName: "work", StageIndex: 2,
		CompletedStages: 1, StageCount: 4, BeforeTokens: 420000, ContextWindow: 500000,
	}))
	if live.LifecyclePhase != "compaction_extracting" || live.LifecyclePhase != resumed.LifecyclePhase {
		t.Fatalf("phase live=%q resumed=%q", live.LifecyclePhase, resumed.LifecyclePhase)
	}
	if live.LifecycleTrigger != "automatic" || live.LifecycleTrigger != resumed.LifecycleTrigger {
		t.Fatalf("trigger live=%q resumed=%q", live.LifecycleTrigger, resumed.LifecycleTrigger)
	}
	if live.LifecycleStageName != "Distilling work" || live.LifecycleStageName != resumed.LifecycleStageName {
		t.Fatalf("stage live=%q resumed=%q", live.LifecycleStageName, resumed.LifecycleStageName)
	}
	if live.LifecycleBeforeTokens != resumed.LifecycleBeforeTokens ||
		live.LifecycleCompleted != resumed.LifecycleCompleted ||
		live.Content != resumed.Content || live.Role != resumed.Role {
		t.Fatalf("live=%+v resumed=%+v", live, resumed)
	}
}
