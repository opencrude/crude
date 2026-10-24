package viewmodel

import (
	"strings"
	"testing"
	"time"

	"crude/tui/chatprojection"
	"crude/tui/theme"
	"crude/workspace"
)

func TestCanonicalBatchCancellationPaintsStaticRetainedEvidence(t *testing.T) {
	state := workspace.NewAgentState("agent", nil, nil)
	state.SubmitInput("read and build")
	event := func(typ string, data any) {
		state.ApplyEvent(workspace.AgentEvent{Type: typ, Data: data})
	}
	input := `{"tool_uses":[{"recipient_name":"read_file","parameters":{"path":"a.go"}},{"recipient_name":"execute_command","parameters":{"command":"build"}}]}`
	event("tool_call", map[string]any{"tool_id": "batch", "internal_id": "batch", "name": "multi_tool_use.parallel", "args": input})
	event("tool_call_streaming", map[string]any{
		"tool_id": "batch", "internal_id": "batch", "name": "multi_tool_use.parallel",
		"args": input, "content": `{"results":[{"recipient_name":"read_file","result":"retained contents"},{"recipient_name":"execute_command","phase":"running","result":"partial build output"}]}`,
	})
	r := chatprojection.NewRenderer(theme.DefaultDark())
	r.SetSize(100, 25)
	r.SetChatAreaBounds(0, 20)
	parentID := ""
	sync := func() {
		views, version := chatMessagesFromConversation(state.Messages)
		for _, view := range views {
			if view.ToolID == "batch" {
				parentID = view.InternalID
			}
		}
		r.SyncMessages(views, version, 0)
	}
	paint := func() string {
		rows, _, _ := r.ExportMessageRows(100)
		var plain strings.Builder
		for _, row := range rows {
			for _, line := range row.Lines {
				plain.WriteString(line + "\n")
			}
		}
		return plain.String()
	}
	sync()
	if live := paint(); !strings.Contains(live, "running") && !strings.Contains(live, "Running") {
		t.Fatalf("real canonical batch did not begin active:\n%s", live)
	}
	state.BeginCancellation()
	event("done", map[string]any{"reason": "cancelled"})
	state.Messages.AdvanceToolLifecycles(time.Now().Add(time.Second))
	sync()
	if parentID == "" {
		t.Fatal("canonical batch lost its tool identity")
	}
	r.ToggleDisclosure(parentID + ":1")
	var first string
	for index, frame := range []int{0, 401, 801} {
		r.SetSpinFrame(frame)
		plain := paint()
		if strings.Contains(plain, "running") || strings.Contains(plain, "Running") {
			t.Fatalf("stopped canonical batch still paints running:\n%s", plain)
		}
		if index == 0 {
			first = plain
		} else if plain != first {
			t.Fatal("stopped batch blinks across activity phases")
		}
	}
	if !strings.Contains(first, "partial build output") || !strings.Contains(first, "Stopped") {
		t.Fatalf("stopped build lost readable partial evidence:\n%s", first)
	}
}
