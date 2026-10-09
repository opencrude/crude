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

func TestConvertMessagesForDisplayUsesAgentIDForChildLink(t *testing.T) {
	message := provider.NewTextMessage("tool", `{"agent_id":"child-1","session_id":"session-1","status":"running"}`)
	message.ToolCallID = "call"
	views := ConvertMessagesForDisplay([]sessiondb.MessageWithMeta{{
		Msg:  message,
		Meta: sessiondb.DisplayMeta{DisplayRole: "tool", ToolName: "agent_start", ToolStatus: "success"},
	}})
	if len(views) != 1 || views[0].SubPaneID != "child-1" {
		t.Fatalf("sub-agent link = %#v", views)
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

func TestConvertMessagesForDisplayRetainsToolResultBody(t *testing.T) {
	message := provider.NewTextMessage("tool", "=== RUN TestOne\n--- PASS: TestOne")
	message.ToolCallID = "call-1"
	views := ConvertMessagesForDisplay([]sessiondb.MessageWithMeta{{
		Msg: message,
		Meta: sessiondb.DisplayMeta{
			DisplayRole: "tool", ToolName: "execute_command", ToolArgs: `{"command":"go test ./..."}`,
			ToolStatus: "success",
		},
	}})
	if len(views) != 1 {
		t.Fatalf("hydrated views = %+v", views)
	}
	if views[0].Content != "=== RUN TestOne\n--- PASS: TestOne" {
		t.Fatalf("tool result body lost during hydration: %q", views[0].Content)
	}
	if views[0].ToolName != "execute_command" || views[0].ToolPreviewPhase != "done" {
		t.Fatalf("tool presentation metadata = %+v", views[0])
	}
	if views[0].ToolCommand != "go test ./..." || views[0].ToolArgs != "go test ./..." {
		t.Fatalf("hydrated command metadata = %+v", views[0])
	}
}

func TestConvertMessagesForDisplayHydratesThinkingDuration(t *testing.T) {
	thinking := provider.NewTextMessage("assistant", "answer")
	thinking.ReasoningContent = "reasoning body"
	views := ConvertMessagesForDisplay([]sessiondb.MessageWithMeta{
		{Msg: thinking, Meta: sessiondb.DisplayMeta{DisplayRole: "thinking", DurationMs: 5000}},
	})
	if len(views) < 1 || views[0].Role != "thinking" {
		t.Fatalf("hydrated views = %+v", views)
	}
	if views[0].ThinkingDuration != 5*time.Second {
		t.Fatalf("thinking duration = %v", views[0].ThinkingDuration)
	}
}

func TestConvertMessagesForDisplayPreservesLegacyMetaAttribution(t *testing.T) {
	message := provider.NewTextMessage("assistant", "answer")
	views := ConvertMessagesForDisplay([]sessiondb.MessageWithMeta{{
		Msg: message,
		Meta: sessiondb.DisplayMeta{
			DisplayRole: "assistant", TurnGeneration: 7,
			ProviderID: "ollama-cloud", ModelID: "kimi-k3",
		},
	}})
	if len(views) != 1 || views[0].TurnGeneration != 7 ||
		views[0].ProviderID != "ollama-cloud" || views[0].ModelID != "kimi-k3" {
		t.Fatalf("hydrated attribution = %#v", views)
	}
}

func TestConvertMessagesForDisplayUsesOriginalReferenceCaption(t *testing.T) {
	message := provider.NewMultiPartMessage("user", []provider.ContentPart{
		{Type: "text", Text: "what is this?"},
		{Type: "image_url", ImageURL: &provider.ImageURL{URL: "data:image/png;base64,eA=="}},
	})
	views := ConvertMessagesForDisplay([]sessiondb.MessageWithMeta{{
		Msg: message,
		Meta: sessiondb.DisplayMeta{
			DisplayRole:     "user",
			DisplayContent:  "[Pasted IMG1] what is this?",
			ReferenceLabels: []string{"[Pasted IMG1]"},
		},
	}})
	if len(views) != 1 || views[0].Content != "[Pasted IMG1] what is this?" {
		t.Fatalf("hydrated views = %+v", views)
	}
	if len(views[0].ReferenceLabels) != 1 || views[0].ReferenceLabels[0] != "[Pasted IMG1]" {
		t.Fatalf("hydrated reference labels = %v", views[0].ReferenceLabels)
	}
}

func TestConvertMessagesForDisplayLegacyMultipartUsesPromptPartOnly(t *testing.T) {
	message := provider.NewMultiPartMessage("user", []provider.ContentPart{
		{Type: "text", Text: "[Pasted Text1] summarize"},
		{Type: "text", Text: "private pasted payload"},
	})
	views := ConvertMessagesForDisplay([]sessiondb.MessageWithMeta{{
		Msg:  message,
		Meta: sessiondb.DisplayMeta{DisplayRole: "user"},
	}})
	if len(views) != 1 || views[0].Content != "[Pasted Text1] summarize" {
		t.Fatalf("legacy hydrated views = %+v", views)
	}
}
