package chatprojection

import (
	"strings"
	"testing"

	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/types"
)

func TestStructuredPreviewIsUsefulAndFullDisclosureRetainsLongUnicodeText(t *testing.T) {
	body := strings.Repeat("Retained evidence 👩‍💻 中文 é with complete findings. ", 40) + "final-evidence-token"
	msg := types.ChatMessage{Role: types.RoleTool, ToolName: "recall_memory", ToolStatus: "success", ToolPreviewPhase: "done",
		Content: `{"results":[{"id":"m1","revision":7,"title":"Evidence","content":"` + body + `"}]}`}
	for _, width := range []int{12, 32, 80, 160} {
		compact := renderToolMessageLines(msg, theme.DefaultDark(), width, width, width, 0, 24, "done", DisclosureCollapsed)
		full := renderToolMessageLines(msg, theme.DefaultDark(), width, width, width, 0, 24, "done", DisclosureExpanded)
		text := strings.Join(compact.lines, "\n")
		if !strings.Contains(text, "Retained") || strings.Contains(text, "…") || !strings.Contains(strings.ReplaceAll(text, "\n│   ", " "), "Expand for full text") {
			t.Fatalf("compact evidence is useless at %d: %s", width, text)
		}
		if !strings.Contains(strings.Join(full.lines, ""), "final-evidence-token") && width >= 32 {
			t.Fatalf("expanded evidence was clipped at %d", width)
		}
		for _, rows := range [][]PaintLine{compact.native, full.native} {
			for _, row := range rows {
				if row.VisibleWidth() > width {
					t.Fatalf("native row overflows %d: %q (%d)", width, row.PlainText(), rendering.VisibleWidth(row.PlainText()))
				}
			}
		}
	}
}

func TestStructuredCancelledAndMalformedEvidenceIsReadableWithoutSuccess(t *testing.T) {
	for _, name := range []string{"agent_send", "board_read", "remember", "capability_search", "mcp__docs__write"} {
		msg := types.ChatMessage{Role: types.RoleTool, ToolName: name, ToolStatus: types.ToolStatusCancelled, Content: `{"message":"Interrupted before receiving exact result"}`}
		full := renderToolMessageLines(msg, theme.DefaultDark(), 80, 80, 80, 0, 20, "done", DisclosureExpanded)
		text := strings.Join(full.lines, "\n")
		if !strings.Contains(text, "Interrupted before") || strings.Contains(text, `{"`) || strings.Contains(text, "●") {
			t.Fatalf("stopped result lost honest evidence: %s", text)
		}
		msg.ToolStatus, msg.Content = types.ToolStatusError, `{"private":"unfinished`
		full = renderToolMessageLines(msg, theme.DefaultDark(), 80, 80, 80, 0, 20, "error", DisclosureExpanded)
		text = strings.Join(full.lines, "\n")
		if strings.Contains(text, "private") || !strings.Contains(text, "invalid JSON") {
			t.Fatalf("malformed transport envelope leaked: %s", text)
		}
	}
}
