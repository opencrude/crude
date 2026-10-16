package chatprojection

import (
	"testing"

	"crude/types"
)

// A started sub-agent stays minimized; its transcript row offers to open it.
func TestStartedAgentRowOffersOpen(t *testing.T) {
	msg := types.ChatMessage{ToolName: "agent_start", ToolStatus: types.ToolStatusSuccess,
		Content: `{"name":"Nova sub Ada","agent_id":"agent-7","status":"running","category":"researcher"}`}
	line := buildToolTitleLine(msg, "", 0)
	var found *ChatAgentOpenPayload
	for _, span := range line.Spans {
		if span.Action != nil && span.Action.ID == ChatAgentOpenActionID {
			payload := span.Action.Payload.(ChatAgentOpenPayload)
			found = &payload
		}
	}
	if found == nil || found.AgentID != "agent-7" {
		t.Fatalf("open chip = %+v in %+v", found, line.Spans)
	}
	msg.ToolStatus = types.ToolStatusError
	for _, span := range buildToolTitleLine(msg, "", 0).Spans {
		if span.Action != nil && span.Action.ID == ChatAgentOpenActionID {
			t.Fatal("a failed launch offered to open an agent")
		}
	}
}
