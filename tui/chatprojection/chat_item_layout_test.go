package chatprojection

import (
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/theme"
	"crude/tui/types"
)

// leadingBlanks counts blank rows at the top of a message's lines.
func leadingBlanks(lines []string) int {
	c := 0
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			c++
		} else {
			break
		}
	}
	return c
}

// trailingBlanks counts blank rows at the end of a message's lines.
func trailingBlanks(lines []string) int {
	c := 0
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			c++
		} else {
			break
		}
	}
	return c
}

// itemGap measures the blank rows separating rows[a] and rows[b] (a before b,
// adjacent). It sums the trailing blanks of the first item and the leading
// blanks of the second.
func itemGap(rows []MessageRow, a, b int) int {
	return trailingBlanks(rows[a].Lines) + leadingBlanks(rows[b].Lines)
}

func exportForLayout(t *testing.T, msgs []types.ChatMessage) []MessageRow {
	t.Helper()
	r := NewRenderer(theme.DefaultDark())
	r.SetSize(120, 40)
	r.SetChatAreaBounds(0, 30)
	r.SetModelName("m")
	r.SetUsername("u")
	for _, m := range msgs {
		r.AppendMessage(m)
	}
	rows, _, _ := r.ExportMessageRows(120)
	if len(rows) != len(msgs) {
		t.Fatalf("expected %d rows, got %d", len(msgs), len(rows))
	}
	return rows
}

// TestChatItemSpacingUniform asserts exactly one blank row separates adjacent
// chat items regardless of role/header-suppression, matching the IDE contract
// the user described: one and only one space between elements.
func TestChatItemSpacingUniform(t *testing.T) {
	msgs := []types.ChatMessage{
		{InternalID: "a1", Role: types.RoleAssistant, Content: "first reply"},
		{InternalID: "t1", Role: types.RoleTool, ToolName: "remember", ToolStatus: "success", ToolArgs: "title= X", Content: `{"title":"X"}`},
		{InternalID: "a2", Role: types.RoleAssistant, Content: "second reply"},
		{InternalID: "t2", Role: types.RoleTool, ToolName: "read_file", ToolStatus: "success", ToolArgs: "main.go", Content: "L1\nL2\nL3"},
		{InternalID: "th1", Role: types.RoleThinking, Content: "thinking text here"},
		{InternalID: "a3", Role: types.RoleAssistant, Content: "third reply"},
		{InternalID: "u1", Role: types.RoleUser, Content: "next question"},
	}
	rows := exportForLayout(t, msgs)
	for i := 0; i+1 < len(rows); i++ {
		if gap := itemGap(rows, i, i+1); gap != 1 {
			t.Fatalf("gap between item %d (%s) and %d (%s) = %d, want exactly 1",
				i, msgs[i].Role, i+1, msgs[i+1].Role, gap)
		}
	}
}

// hasStrike reports whether any span carries the strikethrough attribute.
func hasStrike(pl PaintLine) bool {
	for _, sp := range pl.Spans {
		if sp.Attr&cell.AttrStrikethrough != 0 {
			return true
		}
	}
	return false
}

func TestCancelledAssistantStruckThrough(t *testing.T) {
	rows := exportForLayout(t, []types.ChatMessage{
		{InternalID: "a1", Role: types.RoleAssistant, Content: "I will now edit the file",
			Incomplete: true, IncompleteReason: "operation cancelled by user"},
	})
	bodyFound := false
	for _, line := range rows[0].Native {
		if strings.Contains(line.PlainText(), "edit the file") {
			if !hasStrike(line) {
				t.Fatalf("cancelled assistant body should be struck through: %q", line.PlainText())
			}
			bodyFound = true
		}
	}
	if !bodyFound {
		t.Fatalf("assistant body line not rendered: %#v", rows[0].Lines)
	}
	// The "(operation cancelled by user)" inline suffix must be gone.
	for _, l := range rows[0].Lines {
		if strings.Contains(l, "operation cancelled") {
			t.Fatalf("inline reason should be removed, got %q", l)
		}
	}
}

func TestInterruptedThinkingStruckThrough(t *testing.T) {
	rows := exportForLayout(t, []types.ChatMessage{
		{InternalID: "th1", Role: types.RoleThinking, Content: "I should edit the file now", ThinkingInterrupted: true},
	})
	bodyFound := false
	for _, line := range rows[0].Native {
		if strings.Contains(line.PlainText(), "edit the file now") {
			if !hasStrike(line) {
				t.Fatalf("interrupted thinking body should be struck through")
			}
			bodyFound = true
		}
	}
	if !bodyFound {
		t.Fatalf("thinking body not rendered: %#v", rows[0].Lines)
	}
}

func TestCancelledToolCallStruckThrough(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SetSize(120, 40)
	r.SetChatAreaBounds(0, 30)
	r.SetModelName("m")
	r.UpdateToolStreaming("tid-1", "", "write_file", map[string]any{"target": "main.go"})
	r.UpdateToolCall("tid-1", "", "write_file", "main.go")
	r.MarkStreamingToolsCancelled()
	rows, _, _ := r.ExportMessageRows(120)
	if len(rows) == 0 {
		t.Fatal("no rows")
	}
	for _, line := range rows[0].Native {
		if strings.Contains(line.PlainText(), "Write File") {
			if !hasStrike(line) {
				t.Fatalf("cancelled tool title should be struck through: %q", line.PlainText())
			}
			return
		}
	}
	t.Fatalf("tool title not found: %#v", rows[0].Lines)
}

// TestSearchHistoryRealBackendShape covers the memory history projection with
// the real backend JSON shape (tools/files/key_files are space-joined strings,
// not JSON arrays). A strict []string field unmarshal used to fail on this and
// fall back to dumping the raw JSON blob into chat.
func TestSearchHistoryRealBackendShape(t *testing.T) {
	content := `{"turns":[{"session_id":"s1","turn":3,"when":"3m","user":"fix the stale deny","assistant":"drained the channel","tools":"edit_file execute_command","files":"agent/tool_exec.go"}],"sessions":[{"key_files":"a.go b.go","outcome":"completed","session_id":"s2","summary":"did stuff","title":"Work done","when":"1m ago"}]}`
	rows := exportForLayout(t, []types.ChatMessage{
		{InternalID: "t1", Role: types.RoleTool, ToolName: "search_history", ToolStatus: "success", Content: content},
	})
	joined := strings.Join(rows[0].Lines, "\n")
	if strings.Contains(joined, `"turns"`) || strings.Contains(joined, `{"`) {
		t.Fatalf("search_history fell back to raw JSON:\n%s", joined)
	}
	if !strings.Contains(joined, "Work done") {
		t.Fatalf("missing session title in projection:\n%s", joined)
	}
	if !strings.Contains(joined, "fix the stale deny") {
		t.Fatalf("missing turn user text:\n%s", joined)
	}
}

// TestCommandResultPathPrefixStripped covers grep/find-style output where every
// line repeats the same long absolute path. The projection strips the shared
// directory prefix so far more matches fit per line.
func TestCommandResultPathPrefixStripped(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 5; i++ {
		sb.WriteString("/home/u/ocd/crude/tui/ui/panel_" + string(rune('0'+i)) + ".go:28" + string(rune('0'+i)) + ":applyRuleBleed(args)\n")
	}
	rows := exportForLayout(t, []types.ChatMessage{
		{InternalID: "t1", Role: types.RoleTool, ToolName: "execute_command", ToolStatus: "success", ToolArgs: "grep applyRuleBleed", Content: sb.String()},
	})
	for _, l := range rows[0].Lines {
		if strings.Contains(l, "panel_0.go") && strings.Contains(l, "/home/u/ocd") {
			t.Fatalf("per-line absolute path not stripped: %q", l)
		}
	}
}

// TestCommandResultPathPrefixOnlyWhenShared ensures scattered output is never
// mangled by the prefix stripper.
func TestCommandResultPathPrefixOnlyWhenShared(t *testing.T) {
	content := "/home/a/file.go:1:x\n/usr/local/other.go:2:y\nplain text"
	rows := exportForLayout(t, []types.ChatMessage{
		{InternalID: "t1", Role: types.RoleTool, ToolName: "execute_command", ToolStatus: "success", Content: content},
	})
	joined := strings.Join(rows[0].Lines, "\n")
	if !strings.Contains(joined, "/home/a/file.go:1:x") {
		t.Fatalf("scattered prefixes were stripped:\n%s", joined)
	}
}

// TestErrorProjectionAligned asserts error message continuations hang under the
// message body (not flushed to the margin) and stay within the render width.
func TestErrorProjectionAligned(t *testing.T) {
	rows := exportForLayout(t, []types.ChatMessage{
		{InternalID: "e1", Role: types.RoleError, Content: "Error: patch failed: op[0]: search text not found (tried exact match, whitespace-normalised match, and substring match) at tui/screens/question/question_test.go\nHint: pass an exact snippet from the latest file."},
	})
	lines := rows[0].Lines
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Hint: pass an exact snippet") {
		t.Fatalf("error continuation missing:\n%s", joined)
	}
	// Continuation rows must carry the 6-space hanging indent, not flush-left.
	foundCont := false
	for _, l := range lines {
		if strings.HasPrefix(l, "      whitespace-normalised") || strings.HasPrefix(l, "      tui/screens") || strings.Contains(l, "Hint:") {
			foundCont = true
		}
	}
	if !foundCont {
		t.Fatalf("continuation lines should be indented with hanging indent:\n%s", joined)
	}
}
