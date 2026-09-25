package chatprojection

import (
	"strings"
	"testing"

	"crude/tui/theme"
	"crude/tui/types"
)

func TestNativePatchPaintsBothFilesAndPartialFailure(t *testing.T) {
	body := "```diff\n--- a/first.go\n+++ b/first.go\n@@ -1 +1 @@\n-old\n+new\n--- a/second.go\n+++ b/second.go\n@@ -0,0 +1 @@\n+second\n```\n"
	for _, width := range []int{60, 160} {
		for _, failed := range []bool{false, true} {
			msg := types.ChatMessage{Role: types.RoleTool, ToolName: "apply_patch", ToolArgs: "first.go, second.go", ToolStatus: "success", ToolPreviewPhase: "done", Content: "Success. Updated the following files:\nM first.go\nA second.go\n" + body}
			if failed {
				msg.ToolStatus = "error"
				msg.ToolPreviewPhase = "error"
				msg.Content = "Error: later hunk failed\nEarlier operations committed and remain undoable:\n" + body
			}
			rows := renderToolMessageLines(msg, theme.DefaultDark(), width, width, width, 0, 40, msg.ToolPreviewPhase, DisclosureExpanded)
			painted := strings.Join(rows.lines, "\n")
			for _, want := range []string{"first.go", "second.go", "new", "second"} {
				if !strings.Contains(painted, want) {
					t.Fatalf("missing %q at width %d: %s", want, width, painted)
				}
			}
			if strings.Contains(painted, "```") {
				t.Fatalf("diff fences leaked: %s", painted)
			}
			if failed && (!strings.Contains(painted, "later hunk failed") || !strings.Contains(painted, "committed")) {
				t.Fatalf("partial mutation hidden: %s", painted)
			}
		}
	}
}
