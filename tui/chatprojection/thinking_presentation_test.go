package chatprojection

import (
	"strings"
	"testing"
	"time"

	"crude/tui/theme"
	"crude/tui/types"
)

func TestThinkingPresentationRestoresWithoutSourceChange(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SetSize(80, 12)
	r.SetChatAreaBounds(0, 12)
	message := types.ChatMessage{InternalID: "thought", Role: types.RoleThinking, Content: "retained evidence", Time: time.Now(), ThinkingActive: true}
	r.SyncMessages([]types.ChatMessage{message}, 1)
	r.SetThinkingPanes(map[string]string{"thought": "local-pane"})
	if r.Messages()[0].SubPaneID != "local-pane" || message.SubPaneID != "" {
		t.Fatal("override did not remain renderer-local")
	}
	r.SetThinkingPanes(nil)
	if got := r.Messages()[0]; got.Role != types.RoleThinking || got.Content != message.Content || got.SubPaneID != "" || r.SourceVersion() != 1 {
		t.Fatalf("same-version removal did not restore source row: %+v", got)
	}
	r.expanded["thought"] = true
	rows := r.ExportMessageRowsSnapshot(80).Rows
	found := false
	for _, row := range rows {
		for _, line := range row.Lines {
			if strings.Contains(line, "retained evidence") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("restored thinking body was not rendered")
	}
}

func TestThinkingPresentationStreamingKeepsLatestSourceForRestore(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	message := types.ChatMessage{InternalID: "thought", Role: types.RoleThinking, Content: "first", ThinkingActive: true}
	r.SyncMessages([]types.ChatMessage{message}, 1)
	r.PresentThinkingPane("thought", "pane")
	message.Content += " and continuation"
	r.SyncMessages([]types.ChatMessage{message}, 2)
	if r.Messages()[0].SubPaneID != "pane" {
		t.Fatal("stream sync lost local override")
	}
	r.RemoveThinkingPane("pane")
	if r.Messages()[0].Content != message.Content || r.SourceVersion() != 2 {
		t.Fatal("removal restored an obsolete stream prefix")
	}
}
