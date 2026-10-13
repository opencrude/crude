package viewmodel

import (
	"reflect"
	"testing"
	"time"

	ttypes "crude/types"
	"crude/workspace"
)

func TestChatMessagesFromViewsProjectsDataWithoutPresentationState(t *testing.T) {
	now := time.Unix(123, 456)
	views := []workspace.ChatMessageView{{
		Role:              "tool",
		Content:           "read_file file.go",
		Time:              now,
		ToolName:          "read_file",
		ToolArgs:          "file.go",
		ToolID:            "call-1",
		ToolStatus:        "success",
		ToolProgressLabel: "Read file.go",
		ToolPreviewPhase:  "done",
		ToolEstTokens:     42,
		ToolLinesAdded:    3,
		ToolLinesRemoved:  1,
		ToolDuration:      2 * time.Second,
		SubPaneID:         "pane-1",
		PermTag:           "allowed",
	}}

	got := ChatMessagesFromViews(views)
	if len(got) != 1 {
		t.Fatalf("len(messages) = %d, want 1", len(got))
	}
	msg := got[0]
	if msg.Role != views[0].Role || msg.Content != views[0].Content || msg.Time != now {
		t.Fatalf("basic projection mismatch: %#v", msg)
	}
	if msg.ToolName != "read_file" || msg.ToolLivedAdded != 3 || msg.ToolLivedRemoved != 1 || msg.PermTag != "allowed" {
		t.Fatalf("tool projection mismatch: %#v", msg)
	}
	if msg.CachedLines != nil || msg.StreamCompletedLines != nil {
		t.Fatal("workspace hydration populated frontend presentation state")
	}
}

func TestAppendActiveMessageUpdatesConversationAndProjection(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator(
		"agent")

	app.appendActiveMessage(ttypes.ChatMessage{Role: ttypes.RoleAssistant, Content: "hello", Time: time.Unix(1, 0)})

	node := app.ws.AgentMgr().Get("agent")
	if node == nil || node.State == nil {
		t.Fatal("agent state was not created")
	}
	domain := node.State.Messages.Messages()
	if len(domain) != 1 || domain[0].Content != "hello" {
		t.Fatalf("unexpected conversation state: %#v", domain)
	}
	pane := app.ws.PaneMgr().Get("agent")
	if pane == nil {
		t.Fatal("pane was not created")
	}
	proj := app.subPaneProjections.GetExisting("agent")
	if proj == nil || len(proj.renderer.Messages()) != 1 || proj.renderer.Messages()[0].Content != "hello" {
		t.Fatalf("projection did not synchronize: %#v", proj)
	}
}

func TestCanonicalMessageFrontendRoundTrip(t *testing.T) {
	want := workspace.ChatMessageView{
		InternalID: "canonical", Role: "tool", Content: "result", ReferenceLabels: []string{"@x"}, Time: time.Now().Round(0),
		ToolName: "execute_command", ToolArgs: "command: go test", ToolCommand: "go test ./...",
		ToolInput: "needle", ToolScope: "src", ToolID: "call", ToolStatus: "success", ToolIndex: 2,
		ToolProgressPhase: "done", ToolProgressMessage: "ok", ToolProgressLabel: "tests",
		ToolPreviewPhase: "done", ToolEstTokens: 5, ToolLinesAdded: 2, ToolLinesRemoved: 1,
		ToolStartTime: time.Now().Add(-time.Second).Round(0), ToolDuration: time.Second,
		ThinkingInterrupted: true, ThinkingDuration: time.Second,
		LifecyclePhase: "done", LifecycleProgress: 100, LifecycleProvider: "ollama",
		LifecycleUploadedBytes: 3, LifecycleTotalBytes: 4,
		Incomplete: true, IncompleteReason: "partial", SubPaneID: "child", PermTag: "always",
	}
	front := ChatMessagesFromViews([]workspace.ChatMessageView{want})[0]
	got := workspace.ChatMessageView(workspaceMessageFromChat(front))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("frontend canonical round-trip\n got: %#v\nwant: %#v", got, want)
	}
}

func TestAccountAttributionSurvivesFrontendRoundTrip(t *testing.T) {
	canonical := workspace.ChatMessageView{Role: "assistant", Content: "answer", ProviderID: "chatgpt", AccountID: "work", ModelID: "gpt-5", TurnGeneration: 7}
	messages := ChatMessagesFromViews([]workspace.ChatMessageView{canonical})
	if len(messages) != 1 || messages[0].AccountID != "work" {
		t.Fatalf("account lost in frontend projection: %+v", messages)
	}
	back := workspaceMessageFromChat(messages[0])
	if back.AccountID != "work" || back.ProviderID != "chatgpt" || back.TurnGeneration != 7 {
		t.Fatalf("account lost in canonical conversion: %+v", back)
	}
}
