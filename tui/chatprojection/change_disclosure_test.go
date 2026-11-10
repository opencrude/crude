package chatprojection

import (
	"fmt"
	"strings"
	"testing"

	"crude/types"
)

func TestChangesOpenByDefaultLiveAndHydrated(t *testing.T) {
	for _, name := range []string{"write_file", "edit_file", "apply_patch", "update_plan", "get_plan"} {
		for _, width := range []int{40, 100} {
			t.Run(fmt.Sprintf("%s/%d", name, width), func(t *testing.T) {
				content := "```diff\n--- a/file.go\n+++ b/file.go\n@@ -1 +1 @@\n-old_marker()\n+visible_marker()\n```"
				if name == "update_plan" || name == "get_plan" {
					content = `{"id":"plan-1","revision":1,"objective":"Ship changes","status":"active","steps":[{"id":"first","description":"visible_marker()","status":"in_progress"},{"id":"last","description":"last_step_marker","status":"pending"}]}`
				}
				msg := types.ChatMessage{InternalID: "change", Role: types.RoleTool, ToolName: name, ToolArgs: "file.go", ToolStatus: types.ToolStatusSuccess, ToolPreviewPhase: types.ToolPreviewDone, Content: content}
				live := newActivityRenderer()
				live.AppendMessage(types.ChatMessage{InternalID: msg.InternalID, Role: msg.Role, ToolName: name, ToolStatus: types.ToolStatusCalling})
				live.SyncMessages([]types.ChatMessage{msg}, 2, 0)
				cold := newActivityRenderer()
				cold.SyncMessages([]types.ChatMessage{msg}, 2, 0)
				liveRows, _, _ := live.ExportMessageRows(width)
				coldRows, _, _ := cold.ExportMessageRows(width)
				plain := strings.Join(liveRows[0].Lines, "\n")
				if plain != strings.Join(coldRows[0].Lines, "\n") {
					t.Fatal("live and hydrated changes have different defaults")
				}
				if !strings.Contains(plain, "visible_marker()") || !strings.Contains(plain, "▾") {
					t.Fatalf("change starts closed:\n%s", plain)
				}
				if (name == "update_plan" || name == "get_plan") && !strings.Contains(plain, "last_step_marker") {
					t.Fatalf("default plan lost a step:\n%s", plain)
				}
				pane, _ := cold.BuildSubPaneToolLines([]*types.ChatMessage{&msg}, width, 0)
				var paneText strings.Builder
				for _, line := range pane {
					paneText.WriteString(line.PlainText())
					paneText.WriteByte('\n')
				}
				if !strings.Contains(paneText.String(), "visible_marker()") {
					t.Fatalf("compact agent pane hid the change:\n%s", paneText.String())
				}
				live.ToggleDisclosure(msg.InternalID)
				live.SyncMessages([]types.ChatMessage{msg}, 3, 0)
				closed, _, _ := live.ExportMessageRows(width)
				plain = strings.Join(closed[0].Lines, "\n")
				if strings.Contains(plain, "visible_marker()") || !strings.Contains(plain, "▸") {
					t.Fatalf("explicit collapse did not survive synchronization:\n%s", plain)
				}
				live.ToggleDisclosure(msg.InternalID)
				open, _, _ := live.ExportMessageRows(width)
				if strings.Join(open[0].Lines, "\n") != strings.Join(coldRows[0].Lines, "\n") {
					t.Fatal("reopening changed retained evidence")
				}
			})
		}
	}
}
