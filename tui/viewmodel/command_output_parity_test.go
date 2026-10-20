package viewmodel

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/provider"
	"crude/sessiondb"
	"crude/tui/chatprojection"
	"crude/tui/theme"
	ctypes "crude/types"
	"crude/workspace"
)

// Live tool events and SQLite display snapshots must paint the same literal
// command evidence, including parallel leaves, at desktop and constrained widths.
func TestCommandOutputLiveEventsAndSQLiteHydration(t *testing.T) {
	const result = `{"command":"go test ./...","exit_code":7,"cwd":"/project","output_descriptor":{"handle":"build","status":"failed","total_bytes":80},"retrieval":"MODEL-ONLY","output":"\u001b[31mFAIL\u001b[0m\r\n    retained stderr\n\tactual false\n{\"answer\":42}\n"}`
	for _, tool := range []string{"execute_command", "async_execute_command", "check_async_command", "read_async_output", "exec_command", "write_stdin", "multi_tool_use.parallel"} {
		t.Run(tool, func(t *testing.T) {
			args, content := `{"command":"go test ./..."}`, result
			switch tool {
			case "exec_command":
				args = `{"cmd":"go test ./..."}`
			case "write_stdin":
				args = `{"session_id":4}`
			case "check_async_command", "read_async_output":
				args = `{"handle":"build"}`
			case "multi_tool_use.parallel":
				args = `{"tool_uses":[{"recipient_name":"functions.execute_command","parameters":{"command":"go test ./..."}}]}`
				body, err := json.Marshal(map[string]any{"results": []any{map[string]any{"recipient_name": "execute_command", "result": result, "error": true}}})
				if err != nil {
					t.Fatal(err)
				}
				content = string(body)
			}
			state := &workspace.AgentState{ID: "owner", Messages: workspace.NewConversation()}
			state.ApplyEvent(workspace.AgentEvent{Type: "tool_call", Data: map[string]any{"name": tool, "tool_id": "call", "internal_id": "call", "args": args}})
			state.ApplyEvent(workspace.AgentEvent{Type: "tool_result", Data: map[string]any{"name": tool, "tool_id": "call", "internal_id": "call", "result": content, "error": "true"}})
			for i := 0; i < 8; i++ {
				state.Messages.AdvanceToolLifecycles(time.Now().Add(time.Duration(i+1) * time.Second))
			}
			messages := state.Messages.Messages()
			messages[0].ToolDuration = 0
			db, err := sessiondb.OpenAndMigrate(filepath.Join(t.TempDir(), "session.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if err := sessiondb.Create(db, sessiondb.SessionMeta{ID: "audit", ProjectPath: "/project", ProjectName: "project", CreatedAt: time.Now().UnixNano()}); err != nil {
				t.Fatal(err)
			}
			record := sessiondb.MessageWithMeta{Msg: provider.ChatMessage{Role: "tool", ToolCallID: "call", Content: provider.TextContent(content), ToolError: true}, Meta: sessiondb.DisplayMeta{SnapshotVersion: sessiondb.ConversationSnapshotVersion, Snapshots: messages}}
			if err := sessiondb.AppendMessageWithMeta(db, "audit", record.Msg, 1, record.Meta); err != nil {
				t.Fatal(err)
			}
			loaded, err := sessiondb.LoadMessagesWithMeta(db)
			if err != nil {
				t.Fatal(err)
			}
			hydrated := ChatMessagesFromViews(workspace.ConvertMessagesForDisplay(loaded))
			live, _ := chatMessagesFromConversation(state.Messages)
			live[0].ToolDuration = 0
			for _, width := range []int{60, 160} {
				newRenderer := func(msg []ctypes.ChatMessage) *chatprojection.Renderer {
					r := chatprojection.NewRenderer(theme.DefaultDark())
					r.SetSize(width, 30)
					r.SetChatAreaBounds(0, 24)
					r.SyncMessages(msg, 1)
					return r
				}
				left, right := newRenderer(live), newRenderer(hydrated)
				for _, expanded := range []bool{false, true} {
					if expanded {
						id := "call"
						if tool == "multi_tool_use.parallel" {
							id += ":0"
						}
						left.ToggleDisclosure(id)
						right.ToggleDisclosure(id)
					}
					l, _, _ := left.ExportMessageRows(width)
					r, _, _ := right.ExportMessageRows(width)
					if len(l) != 1 || len(r) != 1 {
						t.Fatalf("lost command row: live=%d hydrated=%d", len(l), len(r))
					}
					assertSameRows(t, "command evidence", l[0], r[0])
					text := strings.Join(l[0].Lines, "\n")
					for _, want := range []string{"FAIL", "    retained stderr", "actual false", `{"answer":42}`} {
						if !strings.Contains(text, want) {
							t.Fatalf("width %d missing %q: %s", width, want, text)
						}
					}
					if strings.Contains(text, "MODEL-ONLY") || strings.Contains(text, "[31m") || strings.Contains(text, "output_descriptor") {
						t.Fatalf("unreadable command transport: %s", text)
					}
				}
			}
		})
	}
}
