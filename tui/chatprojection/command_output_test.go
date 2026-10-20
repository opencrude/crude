package chatprojection

import (
	"strings"
	"testing"

	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func TestCommandOutputReadableAcrossAllToolEntryPoints(t *testing.T) {
	for _, tool := range []string{"execute_command", "async_execute_command", "check_async_command", "read_async_output", "exec_command", "write_stdin"} {
		t.Run(tool, func(t *testing.T) {
			// Literal wire fixture: producer and expectation do not share a builder.
			content := `{"command":"go test ./...","cwd":"/project","exit_code":7,"output_descriptor":{"handle":"build","status":"failed","total_bytes":123,"shown_ranges":[{"start_byte":0,"end_byte":123}]},"retrieval":"MODEL ONLY: never paint this","output":"\u001b[31mFAIL\u001b[0m\r\n    expected true\n\tactual false\n/home/u/a.go:2: evidence\n/home/u/b.go:3: evidence\n"}`
			msg := types.ChatMessage{Role: types.RoleTool, ToolName: tool, ToolStatus: types.ToolStatusSuccess, Content: content}
			for _, disclosure := range []DisclosureState{DisclosureCollapsed, DisclosureExpanded} {
				rows := renderToolMessageLines(msg, theme.DefaultDark(), 116, 116, 120, 0, 30, "done", disclosure)
				text := strings.Join(rows.lines, "\n")
				for _, want := range []string{"Command failed", "exit 7", "handle build", "FAIL", "    expected true", "        actual false", "/home/u/a.go:2: evidence", "/home/u/b.go:3: evidence"} {
					if !strings.Contains(text, want) {
						t.Fatalf("disclosure %v lost %q:\n%s", disclosure, want, text)
					}
				}
				for _, absent := range []string{"output_descriptor", "MODEL ONLY", "[31m", `\r\n`, "wall_time_seconds"} {
					if strings.Contains(text, absent) {
						t.Fatalf("transport clutter %q leaked:\n%s", absent, text)
					}
				}
				for _, row := range rows.native[1:] {
					for _, span := range row.Spans {
						if strings.Contains(span.Text, "expected true") && span.Role != ui.ColorFg {
							t.Fatal("command evidence is dim or painted red")
						}
					}
				}
			}
		})
	}
}

func TestCommandDoesNotConfuseOmittedOutputWithEmptyCapture(t *testing.T) {
	for _, tc := range []struct{ tool, content, want string }{
		{"async_execute_command", `{"output":"","output_descriptor":{"handle":"job","status":"running","total_bytes":120}}`, "output retained"},
		{"check_async_command", `{"output":"","output_descriptor":{"handle":"job","status":"completed","total_bytes":120}}`, "status only"},
		{"read_async_output", `{"output":"","output_descriptor":{"handle":"job","status":"completed","total_bytes":120},"page":{"since_byte":120,"next_byte":120}}`, "no bytes in this output range"},
		{"exec_command", `{"output":"\u001b[2K","output_descriptor":{"status":"completed","total_bytes":4}}`, "control sequences only"},
	} {
		msg := types.ChatMessage{Role: types.RoleTool, ToolName: tc.tool, ToolStatus: types.ToolStatusSuccess, Content: tc.content}
		rows := renderToolMessageLines(msg, theme.DefaultDark(), 100, 100, 104, 0, 20, "done", DisclosureCollapsed)
		if text := strings.Join(rows.lines, "\n"); !strings.Contains(text, tc.want) {
			t.Fatalf("%s misreported empty result: %s", tc.tool, text)
		}
	}
}

func TestCancelledCommandKeepsPartialOutputWithoutCallingItFailure(t *testing.T) {
	msg := types.ChatMessage{Role: types.RoleTool, ToolName: "exec_command", ToolCommand: "long build", ToolStatus: types.ToolStatusSuccess,
		Content: `{"cancelled":true,"output":"partial stderr evidence","error":"interrupted","output_descriptor":{"handle":"job","status":"killed","total_bytes":23}}`}
	rows := renderToolMessageLines(msg, theme.DefaultDark(), 100, 100, 104, 0, 20, "done", DisclosureCollapsed)
	text := strings.Join(rows.lines, "\n")
	if !strings.Contains(text, "Command stopped") || !strings.Contains(text, "partial stderr evidence") || strings.Contains(text, "Command failed") || strings.Contains(text, "×") {
		t.Fatalf("cancelled command evidence was lost or falsely failed: %s", text)
	}
	if activityEligible(&msg) {
		t.Fatal("cancelled command disappeared into a success group")
	}
}

func TestMultilineCommandKeepsExactReadableInputInDisclosure(t *testing.T) {
	msg := types.ChatMessage{Role: types.RoleTool, ToolName: "execute_command", ToolStatus: types.ToolStatusSuccess,
		Content: `{"command":"python3 - <<'PY'\n    print('audit')\nPY","output":"audit","output_descriptor":{"status":"completed","total_bytes":5}}`}
	rows := renderToolMessageLines(msg, theme.DefaultDark(), 100, 100, 104, 0, 20, "done", DisclosureExpanded)
	text := strings.Join(rows.lines, "\n")
	if !strings.Contains(text, "Command:") || !strings.Contains(text, "    print('audit')") || !strings.Contains(text, "│   PY") {
		t.Fatalf("multiline command input was lost: %s", text)
	}
	if strings.Contains(rows.native[0].PlainText(), "\n") {
		t.Fatal("literal newline entered a single title row")
	}
}

func TestCommandPreviewShowsHeadTailAndDisclosureRestoresMiddle(t *testing.T) {
	var content strings.Builder
	for _, line := range []string{"first", "setup", "middle-1", "middle-2", "middle-3", "middle-4", "middle-5", "middle-6", "result", "last"} {
		content.WriteString(line + "\n")
	}
	msg := types.ChatMessage{Role: types.RoleTool, ToolName: "execute_command", ToolStatus: types.ToolStatusSuccess, Content: content.String()}
	collapsed := renderToolMessageLines(msg, theme.DefaultDark(), 80, 80, 84, 0, 30, "done", DisclosureCollapsed)
	text := strings.Join(collapsed.lines, "\n")
	for _, want := range []string{"first", "last", "more lines"} {
		if !strings.Contains(text, want) {
			t.Fatalf("preview lost %q: %s", want, text)
		}
	}
	if strings.Contains(text, "middle-3") {
		t.Fatal("preview is not bounded")
	}
	expanded := renderToolMessageLines(msg, theme.DefaultDark(), 80, 80, 84, 0, 30, "done", DisclosureExpanded)
	if !strings.Contains(strings.Join(expanded.lines, "\n"), "middle-3") {
		t.Fatal("disclosure lost retained output")
	}
}

func TestCommandOutputBinaryEmptyAndLiteralJSON(t *testing.T) {
	for _, tc := range []struct{ content, want string }{
		{`{"output_encoding":"base64","output":"/wBB","output_descriptor":{"status":"completed","total_bytes":3}}`, "ff 00 41"},
		{`{"output":"","exit_code":0,"output_descriptor":{"status":"completed","total_bytes":0}}`, "no output captured"},
		{`{"output":"","session_id":4,"output_descriptor":{"status":"running","total_bytes":0}}`, "no output captured yet"},
		{`{"output":"{\"answer\":42}","exit_code":0,"output_descriptor":{"status":"completed","total_bytes":13}}`, `{"answer":42}`},
		{`{"output":"dGV4dA==","output_encoding":"base64","exit_code":0,"output_descriptor":{"status":"completed","total_bytes":4}}`, "text"},
	} {
		msg := types.ChatMessage{Role: types.RoleTool, ToolName: "exec_command", ToolStatus: types.ToolStatusSuccess, Content: tc.content}
		rows := renderToolMessageLines(msg, theme.DefaultDark(), 80, 80, 84, 0, 20, "done", DisclosureCollapsed)
		if text := strings.Join(rows.lines, "\n"); !strings.Contains(text, tc.want) {
			t.Fatalf("missing %q: %s", tc.want, text)
		}
	}
}
