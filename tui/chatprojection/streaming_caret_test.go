package chatprojection_test

import (
	"strings"
	"testing"
	"time"

	"crude/tui/chatprojection"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/types"
)

func streamingRows(t *testing.T, content string, streaming bool) []string {
	t.Helper()
	r := chatprojection.NewRenderer(theme.DefaultDark())
	r.SetSize(80, 40)
	r.SetChatAreaBounds(0, 40)
	r.SetStreaming(streaming)
	r.SetActiveTyping(true)
	r.AppendMessage(types.ChatMessage{InternalID: "assistant", Role: types.RoleAssistant, Content: content, Time: time.Now()})
	rows, _, _ := r.ExportMessageRows(80)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want one assistant item", len(rows))
	}
	out := make([]string, len(rows[0].Native))
	for i, line := range rows[0].Native {
		out[i] = line.PlainText()
	}
	return out
}

// The typing caret must sit right after the last streamed character, the way
// a caret follows typed text. Completed paragraphs are padded to the block
// width and an open code fence ends with its Copy row; the caret used to be
// painted after that padding or chrome, at the far edge of the transcript.
func TestStreamingCaretFollowsTheLastStreamedText(t *testing.T) {
	for _, tc := range []struct {
		name, content, before string
	}{
		{"completed paragraph", "The actor stalled for 8.2s.\n\nI will check the history prune and set it aside.\n", "set it aside."},
		{"paragraph break", "Para one.\n\n", "Para one."},
		{"open code fence", "```go\nfunc main() {\n", "func main() {"},
		{"plain tail", "still typing", "still typing"},
		{"trailing space token", "ends with space ", "ends with space "},
		{"right to left", "\u0633\u0644\u0627\u0645 \u062f\u0646\u06cc\u0627.\n", "\u0633\u0644\u0627\u0645 \u062f\u0646\u06cc\u0627."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			live := streamingRows(t, tc.content, true)
			settled := streamingRows(t, tc.content, false)
			if len(live) != len(settled) {
				t.Fatalf("the caret changed the row count: live %d, settled %d\n%q", len(live), len(settled), live)
			}
			caretRow := -1
			for i, row := range live {
				if strings.Contains(row, "\u2588") {
					if caretRow >= 0 {
						t.Fatalf("more than one caret: %q", live)
					}
					caretRow = i
				}
			}
			if caretRow < 0 {
				t.Fatalf("no caret: %q", live)
			}
			row := live[caretRow]
			if !strings.Contains(row, tc.before+"\u2588") {
				t.Fatalf("caret is not right after %q: %q", tc.before, row)
			}
			// The caret takes a padding cell: it never widens a padded row.
			if limit := max(rendering.VisibleWidth(settled[caretRow]), rendering.VisibleWidth(tc.before)+1); rendering.VisibleWidth(row) > limit {
				t.Fatalf("caret widened the row to %d (limit %d): %q", rendering.VisibleWidth(row), limit, row)
			}
			for i := range live {
				if i != caretRow && live[i] != settled[i] {
					t.Fatalf("row %d changed: live %q, settled %q", i, live[i], settled[i])
				}
			}
		})
	}
}

// Live reasoning ends with line breaks between thoughts. The caret follows the
// last thought instead of sitting on an empty row below it.
func TestStreamingCaretFollowsTheLastThought(t *testing.T) {
	r := chatprojection.NewRenderer(theme.DefaultDark())
	r.SetSize(80, 40)
	r.SetChatAreaBounds(0, 40)
	r.SetStreaming(true)
	r.SetActiveTyping(true)
	r.AppendMessage(types.ChatMessage{InternalID: "thinking", Role: types.RoleThinking, ThinkingActive: true,
		Content: "Considering the options.\n\nSecond thought ends.\n", Time: time.Now()})
	rows, _, _ := r.ExportMessageRows(80)
	var lines []string
	for _, row := range rows {
		for _, line := range row.Native {
			lines = append(lines, line.PlainText())
		}
	}
	last := lines[len(lines)-1]
	if !strings.HasSuffix(last, "Second thought ends.\u2588") {
		t.Fatalf("caret is not after the last thought: %q", lines)
	}
}
