package workspace

import (
	"reflect"
	"testing"
	"time"

	"crude/provider"
	"crude/sessiondb"
	types "crude/types"
)

func TestTakeHydratedDataIsOneShot(t *testing.T) {
	sess := &Session{
		hydratedMessages: []ChatMessageView{{Role: "user", Content: "hello"}},
		hydratedRefs:     []sessiondb.Reference{{ID: "ref-1"}},
	}

	if got := sess.TakeHydratedMessages(); len(got) != 1 || got[0].Content != "hello" {
		t.Fatalf("unexpected hydrated messages: %#v", got)
	}
	if got := sess.TakeHydratedMessages(); len(got) != 0 {
		t.Fatalf("hydrated messages were returned twice: %#v", got)
	}
	if got := sess.TakeHydratedRefs(); len(got) != 1 || got[0].ID != "ref-1" {
		t.Fatalf("unexpected hydrated references: %#v", got)
	}
	if got := sess.TakeHydratedRefs(); len(got) != 0 {
		t.Fatalf("hydrated references were returned twice: %#v", got)
	}
}

func TestConvertMessagesForDisplayUsesCanonicalSnapshotWithoutReconstruction(t *testing.T) {
	want := types.ConversationMessage{
		InternalID: "canonical", Role: "tool", Content: "result", Time: time.Now().Round(0),
		ToolName: "run_sub_agent", ToolArgs: "description: inspect", ToolID: "call",
		ToolStatus: "success", ToolProgressPhase: "done", ToolProgressLabel: "child",
		ToolPreviewPhase: "done", ToolEstTokens: 20, ToolLinesAdded: 2,
		ToolDuration: time.Second, SubPaneID: "child-1", PermTag: "always",
	}
	views := ConvertMessagesForDisplay([]sessiondb.MessageWithMeta{{
		Meta: sessiondb.DisplayMeta{SnapshotVersion: sessiondb.ConversationSnapshotVersion, Snapshots: []types.ConversationMessage{want}},
	}})
	if len(views) != 1 || !reflect.DeepEqual(types.ConversationMessage(views[0]), want) {
		t.Fatalf("canonical conversion\n got: %#v\nwant: %#v", views, want)
	}
}

func TestConvertMessagesForDisplayRepairsLegacySubAgentLink(t *testing.T) {
	message := provider.NewTextMessage("tool", "## Sub-Agent Result\n**Session:** child-legacy\n**Status:** OK")
	message.ToolCallID = "call"
	views := ConvertMessagesForDisplay([]sessiondb.MessageWithMeta{{
		Msg:  message,
		Meta: sessiondb.DisplayMeta{DisplayRole: "tool", ToolName: "run_sub_agent", ToolStatus: "success"},
	}})
	if len(views) != 1 || views[0].SubPaneID != "child-legacy" {
		t.Fatalf("legacy sub-agent link = %#v", views)
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
