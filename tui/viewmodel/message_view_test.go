package viewmodel

import (
	"testing"
	"time"

	"crude/provider"
	ttypes "crude/tui/types"
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
	if msg.CachedLines != nil || msg.StreamCompletedLines != nil || msg.SessionPickerSessions != nil {
		t.Fatal("workspace hydration populated frontend presentation state")
	}
}

func TestAppendActiveMessageUpdatesConversationAndProjection(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator(
		"agent",
		make(chan provider.UserInput, 1),
		make(chan provider.UserInput, 1),
		make(chan struct{}, 1),
		make(chan string, 1),
	)

	app.appendActiveMessage(ttypes.ChatMessage{Role: ttypes.RoleAssistant, Content: "hello", Time: time.Unix(1, 0)})

	node := app.AgentMgr.Get("agent")
	if node == nil || node.State == nil {
		t.Fatal("agent state was not created")
	}
	domain := node.State.Messages.Messages()
	if len(domain) != 1 || domain[0].Content != "hello" {
		t.Fatalf("unexpected conversation state: %#v", domain)
	}
	pane := app.PaneMgr.Get("agent")
	if pane == nil {
		t.Fatal("pane was not created")
	}
	proj := app.subPaneProjections.GetExisting("agent")
	if proj == nil || len(proj.msgList.Messages()) != 1 || proj.msgList.Messages()[0].Content != "hello" {
		t.Fatalf("projection did not synchronize: %#v", proj)
	}
}
