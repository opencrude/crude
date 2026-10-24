package chatprojection

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"crude/tui/cell"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func TestActivityExpansionKeepsTreeAndFullOperands(t *testing.T) {
	r := newActivityRenderer()
	messages := []types.ChatMessage{
		{InternalID: "read", Role: "tool", ToolName: "read_file", ToolArgs: "tui/chatprojection/very_descriptive_file_name.go", ToolStatus: "success", Content: "package fixture"},
		{InternalID: "search", Role: "tool", ToolName: "grep_files", ToolInput: "the complete expression", ToolScope: "tui/chatprojection", ToolStatus: "calling", ToolPreviewPhase: "streaming"},
	}
	r.SyncMessages(messages, 1, 0)
	r.ToggleDisclosure("activity:read")
	rows, _, _ := r.ExportMessageRows(100)
	if len(rows) != 1 || rows[0].ID != "activity:read" {
		t.Fatalf("expansion changed tree into separate transcript rows: %+v", rows)
	}
	text := strings.Join(rows[0].Lines, "\n")
	for _, want := range []string{"very_descriptive_file_name.go", "the complete expression", "tui/chatprojection", "├─", "└─"} {
		if !strings.Contains(text, want) {
			t.Fatalf("tree lost %q: %s", want, text)
		}
	}
	found := false
	for _, line := range rows[0].Native {
		for _, span := range line.Spans {
			if span.Role == ui.ColorToolTitle && strings.ContainsAny(span.Text, "●○") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("active branch lost its semantic activity dot")
	}
}

func TestNativeCommandOutputIsReadableAndNotAllRed(t *testing.T) {
	msg := types.ChatMessage{Role: "tool", ToolName: "exec_command", ToolCommand: "go test ./...", ToolStatus: "error", ToolPreviewPhase: "error", Content: `{"wall_time_seconds":0.2,"exit_code":1,"output":"FAIL package/example\n    expected true\nactual false"}`}
	ml := renderToolMessageLines(msg, theme.DefaultDark(), 80, 95, 100, 0, 25, "error", DisclosureExpanded)
	text := strings.Join(ml.lines, "\n")
	if strings.Contains(text, "wall_time_seconds") || strings.Contains(text, `\n`) || !strings.Contains(text, "expected true") {
		t.Fatalf("native envelope painted as output: %s", text)
	}
	for _, line := range ml.native[1:] {
		for _, span := range line.Spans {
			if strings.Contains(span.Text, "expected true") && span.Role == ui.ColorError {
				t.Fatal("entire command output painted as an error")
			}
		}
	}
}

func TestPatchFailureColorsOnlyActionAndIndicator(t *testing.T) {
	msg := types.ChatMessage{Role: types.RoleTool, ToolName: "apply_patch", ToolArgs: "source.go", ToolStatus: types.ToolStatusError, ToolPreviewPhase: types.ToolPreviewError, Content: "Error: one hunk failed\n```diff\n--- a/source.go\n+++ b/source.go\n@@ -1 +1 @@\n-old\n+new\n```"}
	lines := renderToolMessageLines(msg, theme.DefaultDark(), 80, 90, 100, 0, 30, types.ToolPreviewError, DisclosureExpanded)
	if !strings.Contains(lines.lines[0], "×") || lines.native[0].Spans[2].Role != ui.ColorError {
		t.Fatal("failure action/indicator missing")
	}
	for _, line := range lines.native[1:] {
		for _, span := range line.Spans {
			if span.Role == ui.ColorError {
				t.Fatalf("failure recolored non-title content: %q", span.Text)
			}
		}
	}
}

func TestWriteDefaultsExpandedAndCanCollapse(t *testing.T) {
	r := newActivityRenderer()
	msg := types.ChatMessage{InternalID: "write", Role: "tool", ToolName: "write_file", ToolArgs: "new.go", ToolStatus: "success", ToolPreviewPhase: "done", Content: "Written 2 lines\n+package fixture\n+var X = true"}
	r.SyncMessages([]types.ChatMessage{msg}, 1, 0)
	if r.disclosureState(r.sync.MsgByID["write"]) != DisclosureExpanded {
		t.Fatal("file write hidden by default")
	}
	r.ToggleDisclosure("write")
	if r.disclosureState(r.sync.MsgByID["write"]) != DisclosureCollapsed {
		t.Fatal("default-open write cannot be collapsed")
	}
}

func TestToolLabelColorsSurviveWrapping(t *testing.T) {
	for _, tc := range []struct {
		name, status, phase string
		title, dot          ui.SemanticColor
	}{
		{"completed", types.ToolStatusSuccess, types.ToolPreviewDone, ui.ColorToolTitle, ui.ColorToolTitle},
		{"completed-stale-phase", types.ToolStatusSuccess, types.ToolPreviewError, ui.ColorToolTitle, ui.ColorToolTitle},
		{"preparing", types.ToolStatusCalling, types.ToolPreviewPreparing, ui.ColorToolTitle, ui.ColorToolTitle},
		{"running", types.ToolStatusCalling, types.ToolPreviewStreaming, ui.ColorToolTitle, ui.ColorToolTitle},
		{"failed", types.ToolStatusError, types.ToolPreviewError, ui.ColorError, ui.ColorError},
		{"stopped", types.ToolStatusCancelled, types.ToolPreviewDone, ui.ColorToolTitle, ui.ColorMuted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := types.ChatMessage{Role: types.RoleTool, ToolName: "execute_command", ToolArgs: "rg --line-number complete search expression in a long directory", ToolStatus: tc.status, ToolPreviewPhase: tc.phase, ToolDuration: time.Second}
			for _, frame := range []int{0, 400} {
				header := buildToolTitleLine(msg, tc.phase, frame)
				if header.Spans[2].Role != tc.title {
					t.Fatalf("title lost stable semantic color: %+v", header.Spans)
				}
				lines := buildToolTitleLines(msg, tc.phase, frame, 30, DisclosureCollapsed)
				if len(lines) < 2 {
					t.Fatal("fixture does not exercise wrapped continuation rows")
				}
				if lines[0].Spans[1].Role != tc.dot {
					t.Fatalf("outcome/activity dot lost its color: %+v", lines[0].Spans[1])
				}
				if (tc.status == types.ToolStatusError) != strings.Contains(lines[0].Spans[1].Text, "×") {
					t.Fatalf("failure does not have a unique times indicator: %+v", lines[0])
				}
				var operand strings.Builder
				for _, line := range lines {
					for _, span := range line.Spans {
						if span.Role == ui.ColorFg || span.FGSet {
							t.Fatalf("unclassified/bright text span: %+v", span)
						}
						if span.Role == ui.ColorToolOperand {
							operand.WriteString(span.Text)
							operand.WriteByte(' ')
						}
					}
				}
				if !strings.Contains(strings.Join(strings.Fields(operand.String()), " "), msg.ToolArgs) {
					t.Fatalf("operand lost its semantic color through wrapping: %q", operand.String())
				}
			}
		})
	}
}

func TestSemanticWrappingPreservesOperandAndMetadata(t *testing.T) {
	path := "Reading/\u754c\u9762/very_long_read_file_2s.go"
	line := PaintLine{Spans: []RowSpan{{Text: "Reading", Role: ui.ColorToolTitle}, {Text: " " + path, Role: ui.ColorToolOperand, Attr: cell.AttrItalic}, {Text: " 2s", Role: ui.ColorMuted}}}
	for _, width := range []int{12, 24, 120} {
		var operand, meta strings.Builder
		for _, row := range wrapPaintLine(line, width) {
			if row.VisibleWidth() > width {
				t.Fatalf("wrapped row exceeds %d columns: %q", width, row.PlainText())
			}
			for _, span := range row.Spans {
				switch span.Role {
				case ui.ColorToolOperand:
					operand.WriteString(span.Text)
					if span.Attr != cell.AttrItalic {
						t.Fatal("wrap discarded operand attributes")
					}
				case ui.ColorMuted:
					meta.WriteString(span.Text)
				}
			}
		}
		if strings.TrimSpace(operand.String()) != path || strings.TrimSpace(meta.String()) != "2s" {
			t.Fatalf("width %d confused operand/title/metadata: %q / %q", width, operand.String(), meta.String())
		}
	}
}

func TestAmbientFailureDoesNotRecolorRunningTool(t *testing.T) {
	msg := types.ChatMessage{Role: types.RoleTool, ToolName: "execute_command", ToolArgs: "go test ./...", ToolStatus: types.ToolStatusCalling, ToolPreviewPhase: types.ToolPreviewStreaming}
	line := buildToolTitleLine(msg, types.ToolPreviewError, 0)
	if strings.Contains(line.PlainText(), "×") || line.Spans[2].Role == ui.ColorError {
		t.Fatalf("ambient request failure recolored an active tool: %+v", line)
	}
}

func BenchmarkDenseActivityPresentedFrame(b *testing.B) {
	r := newActivityRenderer()
	messages := make([]types.ChatMessage, 4001)
	for i := range 4000 {
		messages[i] = types.ChatMessage{InternalID: fmt.Sprint(i), Role: "tool", ToolName: "read_file", ToolArgs: "src/file.go", ToolStatus: "success", ToolPreviewPhase: "done", Content: strings.Repeat("source line\n", 40)}
	}
	messages[4000] = types.ChatMessage{InternalID: "active", Role: "tool", ToolName: "exec_command", ToolCommand: "go test ./...", ToolStatus: "calling", ToolPreviewPhase: "streaming"}
	r.SyncMessages(messages, 1, 0)
	r.ExportMessageRows(100)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.SetSpinFrame(i * 400)
		r.ExportMessageRows(100)
	}
}
