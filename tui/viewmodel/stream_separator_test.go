package viewmodel

import (
	"fmt"
	"strings"
	"testing"
	"time"

	ctypes "crude/types"
	"crude/types/reasoning"
	"crude/workspace"
)

// Replays the event shape inclusionai/ling-3.1 streams over the Responses
// API: an answer, then parallel reads with a late "\n" delta of the closed
// answer item between them. The painted chat must show one grouped activity
// row right after the answer, with no blank gaps and no split reads.
func TestSeparatorDeltasKeepParallelReadsGrouped(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 40
	app.AddOrchestrator("ling")
	state := app.ws.AgentMgr().Get("ling").State
	app.switchToAgentView(state.ID)
	event := func(typ string, data any) {
		app.handleAgentEvent(ctypes.AgentEventMsg{AgentID: state.ID, Type: typ, Data: data})
		app.paintShellFrame()
	}
	event("thinking", reasoning.Delta{Text: "Check the lint rules first.", Kind: reasoning.Raw})
	event("stream_token", "Let me check the lint rules.")
	event("stream_token", "\n")
	ids := []string{"eslint", "design", "main"}
	for i, id := range ids {
		if i > 0 {
			event("stream_token", "\n")
		}
		event("tool_call_streaming", map[string]any{"tool_id": id, "internal_id": id, "name": "read_file", "path": "site/" + id + ".js"})
	}
	for _, id := range ids {
		event("tool_call", map[string]any{"tool_id": id, "internal_id": id, "request_id": "q1", "name": "read_file", "args": fmt.Sprintf(`{"path":"site/%s.js"}`, id)})
	}
	for _, id := range ids {
		event("tool_result", map[string]any{"tool_id": id, "internal_id": id, "name": "read_file", "result": "export default {}"})
	}
	for i := range 3 {
		state.Messages.AdvanceToolLifecycles(time.Now().Add(time.Duration(i+1) * time.Second))
	}
	app.syncAgentMessages(state.ID, false)
	app.paintShellFrame()

	painted := strings.Split(bufferPlain(app.mainSurf.Buffer()), "\n")
	answer, group := -1, -1
	for y, line := range painted {
		if strings.Contains(line, "Let me check the lint rules.") {
			answer = y
		}
		if strings.Contains(line, "Read 3 files") {
			group = y
		}
		// Leaves of the group carry tree branches; a standalone row does not.
		if strings.Contains(line, "Read site/") && !strings.Contains(line, "├─") && !strings.Contains(line, "└─") {
			t.Fatalf("a read left its group:\n%s", strings.Join(painted, "\n"))
		}
	}
	if answer < 0 || group < 0 {
		t.Fatalf("missing answer or grouped reads:\n%s", strings.Join(painted, "\n"))
	}
	if group != answer+2 {
		t.Fatalf("answer at row %d, reads at row %d: want exactly one blank row between them:\n%s", answer, group, strings.Join(painted, "\n"))
	}
	for _, message := range state.Messages.Messages() {
		if message.Role == workspace.MessageRoleAssistant && strings.TrimSpace(message.Content) == "" {
			t.Fatalf("separator delta opened a blank reply: %+v", message)
		}
	}
}

// A thinking pane overflows (and gains its own scrollbar) while it streams,
// then disappears when the tools start. The transcript must keep its width
// and wrapping through all of it.
func TestThinkingPaneKeepsTranscriptWidth(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 36
	app.AddOrchestrator("thinker")
	state := app.ws.AgentMgr().Get("thinker").State
	app.switchToAgentView(state.ID)
	for i := range 20 {
		state.Messages.AppendMessage(workspace.Message{Role: ctypes.RoleAssistant,
			Content: fmt.Sprintf("Earlier reply %d is long enough to wrap close to the right edge of the chat column when the width changes by a column or two.", i)})
	}
	app.syncAgentMessages(state.ID, false)
	app.paintShellFrame()
	renderer := app.subPaneProjections.GetExisting(state.ID).renderer
	transcript := func() string {
		rows, _, _ := renderer.ExportMessageRows(app.chatReflowInnerW)
		var lines []string
		for _, row := range rows {
			if strings.HasPrefix(row.ID, "msg-") && row.SubPaneID == "" && len(row.Lines) > 1 && strings.Contains(row.Lines[1], "Earlier reply") {
				lines = append(lines, row.Lines...)
			}
		}
		return strings.Join(lines, "\n")
	}
	want, width := transcript(), renderer.ScrollbarWidth()
	overflowed := false
	for round := range 2 {
		for line := range 30 {
			app.handleAgentEvent(ctypes.AgentEventMsg{AgentID: state.ID, Type: "thinking",
				Data: reasoning.Delta{Text: fmt.Sprintf("Thinking line %d of round %d.\n", line, round), Kind: reasoning.Raw}})
			app.paintShellFrame()
			overflowed = overflowed || renderer.PaneScrollbarWidth() > 0
			if got := renderer.ScrollbarWidth(); got != width {
				t.Fatalf("round %d line %d: transcript scrollbar reserve changed %d -> %d", round, line, width, got)
			}
			if got := transcript(); got != want {
				t.Fatalf("round %d line %d: thinking pane reflowed the transcript", round, line)
			}
		}
		id := fmt.Sprintf("read-%d", round)
		app.handleAgentEvent(ctypes.AgentEventMsg{AgentID: state.ID, Type: "tool_call", Data: map[string]any{
			"tool_id": id, "internal_id": id, "request_id": id, "name": "read_file", "args": `{"path":"a.go"}`}})
		app.paintShellFrame()
		if got := transcript(); got != want {
			t.Fatalf("round %d: closing the thinking pane reflowed the transcript", round)
		}
	}
	if !overflowed {
		t.Fatal("fixture never overflowed the thinking pane")
	}
}
