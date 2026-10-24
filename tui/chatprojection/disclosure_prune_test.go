package chatprojection_test

import (
	"testing"
	"time"

	"crude/tui/chatprojection"
	"crude/tui/theme"
	"crude/types"
)

func TestRenderer_PrunesExpandedWhenMessagesRemoved(t *testing.T) {
	r := chatprojection.NewRenderer(theme.DefaultDark())
	r.SetChatAreaBounds(0, 30)

	messages := []types.ChatMessage{
		{InternalID: "m1", Role: types.RoleTool, ToolName: "execute_command", ToolStatus: "success", Content: "out", Time: time.Now()},
		{InternalID: "m2", Role: types.RoleThinking, Content: "reasoning", Time: time.Now()},
	}
	r.SyncMessages(messages, 1, 0)

	// Expand both.
	r.ToggleDisclosure("m1")
	r.ToggleDisclosure("m2")
	if !r.DisclosureExpanded("m1") || !r.DisclosureExpanded("m2") {
		t.Fatal("both messages should be expanded before removal")
	}

	// Remove m1 by syncing a reduced set. The renderer must prune m1's entry.
	r.SyncMessages([]types.ChatMessage{
		{InternalID: "m2", Role: types.RoleThinking, Content: "reasoning", Time: time.Now()},
	}, 2, 0)

	if r.DisclosureExpanded("m1") {
		t.Fatal("expanded entry for removed message m1 must be pruned")
	}
	if !r.DisclosureExpanded("m2") {
		t.Fatal("m2's expansion must survive the sync")
	}
}
