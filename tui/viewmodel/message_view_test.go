package viewmodel

import (
	"testing"
	"time"

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
