package chatprojection

import (
	"encoding/json"
	"strings"
	"testing"

	"crude/tui/theme"
	"crude/types"
)

func TestRenderSearchHistoryStructured(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{
		"turns": []map[string]any{
			{"session_id": "s1", "turn": 3, "when": "2d ago", "user": "fix the bug", "assistant": "found it", "tools": []string{"grep_files"}, "files": []string{"main.go"}},
		},
		"sessions": []map[string]any{
			{"session_id": "s2", "title": "Refactor", "summary": "Moved settings.", "key_files": []string{"settings.go"}, "when": "1w ago"},
		},
	})
	msg := types.ChatMessage{
		ToolName:   "search_history",
		ToolStatus: "success",
		Content:    string(payload),
	}
	ml := renderToolMessageLines(msg, theme.DefaultDark(), 80, 90, 100, 0, 12, "done", DisclosureExpanded)

	plain := append([]string(nil), ml.lines...)
	joined := strings.Join(plain, "\n")
	if strings.Contains(joined, `"turns"`) || strings.Contains(joined, "{") {
		t.Fatalf("search_history result is raw JSON:\n%s", joined)
	}
	if !strings.Contains(joined, "Refactor") || !strings.Contains(joined, "Moved settings.") {
		t.Fatalf("search_history result missing structured content:\n%s", joined)
	}
	if !strings.Contains(joined, "fix the bug") {
		t.Fatalf("search_history result missing turn content:\n%s", joined)
	}
}
