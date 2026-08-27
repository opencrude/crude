package chatprojection

import (
	"fmt"
	"strings"
	"testing"

	"crude/tui/theme"
	"crude/tui/types"
)

func TestCommandPreviewPreservesIndentationAndTail(t *testing.T) {
	var lines []string
	for i := 0; i < 20; i++ {
		text := fmt.Sprintf("line-%02d", i)
		if i == 1 {
			text = "    indented detail"
		}
		lines = append(lines, text)
	}
	msg := types.ChatMessage{
		Role: "tool", ToolName: "execute_command", ToolStatus: "success",
		ToolPreviewPhase: "done", Content: strings.Join(lines, "\n"),
	}
	ml := renderToolMessageLines(msg, theme.DefaultDark(), 50, 60, 60, 0, 9, "done", DisclosureCollapsed)
	plain := strings.Join(ml.lines, "\n")
	for _, want := range []string{"line-00", "    indented detail", "more lines", "line-19"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("preview missing %q:\n%s", want, plain)
		}
	}
}

func TestToolPreviewDoesNotSynthesizeNarrowWidth(t *testing.T) {
	msg := types.ChatMessage{
		Role: "tool", ToolName: "execute_command", ToolStatus: "success",
		ToolPreviewPhase: "done", Content: "abcdefghijk",
	}
	ml := renderToolMessageLines(msg, theme.DefaultDark(), 3, 3, 3, 0, 6, "done", DisclosureCollapsed)
	for _, line := range ml.native[1:] {
		if line.VisibleWidth() > 5 { // 4-column tool gutter + one content cell.
			t.Fatalf("narrow tool row width = %d, row=%q", line.VisibleWidth(), line.PlainText())
		}
	}
}

func TestDisclosurePolicyUsesOnlyMeaningfulFamilies(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SetSize(80, 20)
	r.SetChatAreaBounds(0, 12)
	version := uint64(1)
	for _, tool := range []string{"read_file", "grep_files", "web_search", "web_fetch", "recall_memory"} {
		msg := types.ChatMessage{InternalID: tool, Role: types.RoleTool, ToolName: tool, ToolStatus: "success", Content: "body"}
		r.SyncMessages([]types.ChatMessage{msg}, version)
		version++
		rows, _, _ := r.ExportMessageRows(80)
		if len(rows) != 1 {
			t.Fatalf("%s rows = %+v", tool, rows)
		}
		if rows[0].Toggle {
			t.Fatalf("%s must not advertise expansion", tool)
		}
	}
	for _, tool := range []string{"execute_command", "edit_file", "write_file", "update_plan"} {
		msg := types.ChatMessage{InternalID: tool, Role: types.RoleTool, ToolName: tool, ToolStatus: "success", Content: "body"}
		r.SyncMessages([]types.ChatMessage{msg}, version)
		version++
		rows, _, _ := r.ExportMessageRows(80)
		if len(rows) != 1 {
			t.Fatalf("%s rows = %+v", tool, rows)
		}
		if !rows[0].Toggle {
			t.Fatalf("%s should advertise expansion", tool)
		}
	}
}

func TestToolContextShowsFullCommandAndGrepPattern(t *testing.T) {
	command := "sed -i 's/very-long-old-expression/very-long-new-expression/' tui/ui/list.go"
	cmd := types.ChatMessage{
		Role: "tool", ToolName: "execute_command", ToolStatus: "success",
		ToolPreviewPhase: "done", ToolCommand: command, Content: "(command completed with no output)",
	}
	ml := renderToolMessageLines(cmd, theme.DefaultDark(), 35, 40, 40, 0, 12, "done", DisclosureCollapsed)
	commandRows := strings.Join(ml.lines, "\n")
	if !strings.Contains(commandRows, "very-long-old-expression") || !strings.Contains(commandRows, "ery-long-new-expression") || !strings.Contains(commandRows, "t.go") {
		t.Fatalf("full command missing: %#v", ml.lines)
	}
	grep := types.ChatMessage{
		Role: "tool", ToolName: "grep_files", ToolStatus: "success", ToolPreviewPhase: "done",
		ToolInput: "func (f *FilterField) type FilterField", ToolScope: "tui/ui", Content: "1 match\ntui/ui/filter.go:1: match",
	}
	ml = renderToolMessageLines(grep, theme.DefaultDark(), 35, 40, 40, 0, 12, "done", DisclosureCollapsed)
	plain := strings.Join(ml.lines, "\n")
	if !strings.Contains(plain, "func (f *FilterField)") || !strings.Contains(plain, "type ") || !strings.Contains(plain, "FilterField in tui/ui") {
		t.Fatalf("grep context missing:\n%s", plain)
	}
}
