package chatprojection

import (
	"strings"
	"testing"

	"crude/tui/theme"
	"crude/types"
)

func TestSettledResultsDefaultToTitleOnlyAndRetainExpandedEvidence(t *testing.T) {
	for _, name := range []string{"read_file", "agent_send", "mcp__test__read"} {
		t.Run(name, func(t *testing.T) {
			r := newActivityRenderer()
			message := types.ChatMessage{InternalID: "result", Role: types.RoleTool, ToolName: name,
				ToolStatus: types.ToolStatusError, ToolPreviewPhase: types.ToolPreviewError,
				Content: "Error: cannot complete the operation\nRetained diagnostic evidence"}
			mode := r.defaultDisclosure(&message)
			compact := renderToolMessageLines(message, theme.DefaultDark(), 160, 160, 160, 0, 24, "done", mode)
			if len(compact.lines) != 1 {
				t.Fatalf("settled result is not title-only:\n%s", strings.Join(compact.lines, "\n"))
			}
			expanded := renderToolMessageLines(message, theme.DefaultDark(), 160, 160, 160, 0, 24, "done", DisclosureExpanded)
			if !strings.Contains(strings.Join(expanded.lines, "\n"), "Retained diagnostic evidence") {
				t.Fatalf("expanded result lost evidence: %v", expanded.lines)
			}
		})
	}
}

func TestLongOperandCannotHideClosedReasonOrDuplicateExpandedDiagnostic(t *testing.T) {
	msg := types.ChatMessage{Role: types.RoleTool, ToolName: "edit_file", ToolArgs: "/project/" + strings.Repeat("long-directory/", 20) + "file.go",
		ToolStatus: types.ToolStatusError, Content: "Error: search text not found\nExact search evidence"}
	for _, width := range []int{80, 160} {
		closed := renderToolMessageLines(msg, theme.DefaultDark(), width, width, width, 0, 24, "done", DisclosureCollapsed)
		plain := strings.Join(closed.lines, "\n")
		if len(closed.lines) > 2 || !strings.Contains(plain, "search text not found") || !strings.Contains(plain, "▸") {
			t.Fatalf("long operand hid the reason or control: %s", plain)
		}
		open := renderToolMessageLines(msg, theme.DefaultDark(), width, width, width, 0, 24, "done", DisclosureExpanded)
		plain = strings.Join(open.lines, "\n")
		if strings.Count(plain, "search text not found") != 1 || !strings.Contains(plain, "Exact search evidence") {
			t.Fatalf("expanded error lost or duplicated evidence: %s", plain)
		}
	}
}
