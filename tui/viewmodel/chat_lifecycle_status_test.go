package viewmodel

import (
	"strings"
	"testing"

	"crude/tui/types"
	"crude/types/reasoning"
	"crude/workspace"
)

func TestSubmitProjectsLifecycleIntoChatNotBottomStatus(t *testing.T) {
	app := newFullTestApp(t)
	app.Logger = stubLogger{}
	agentID := addBackendTestOrchestrator(t, app)

	_ = app.submitInput(agentID, "inspect lifecycle", nil)
	messages := app.AgentMgr.Get(agentID).State.Messages.Messages()
	if len(messages) != 2 || messages[1].Role != types.RoleStatus || messages[1].LifecyclePhase != "preparing" {
		t.Fatalf("submitted conversation = %#v", messages)
	}
	if bottom := testStatusText(app); strings.Contains(bottom, "Getting next turn ready") || strings.Contains(bottom, "uploading context") {
		t.Fatalf("per-agent lifecycle leaked into bottom status: %q", bottom)
	}
	if working, _ := app.backgroundAgentCounts(); working != 0 {
		t.Fatalf("selected agent was duplicated as background work: %d", working)
	}
}

func TestPostCompactionThinkingCoalescesAndPaintsAfterBoundary(t *testing.T) {
	app := newFullTestApp(t)
	app.Logger = stubLogger{}
	agentID := addBackendTestOrchestrator(t, app)
	base := map[string]any{
		"request_id": "compact-1", "trigger": "manual", "role": "root",
		"provider_id": "provider", "model_id": "aeron/model",
		"stage": 4, "completed_stages": 4, "stage_count": 4,
		"before_tokens": 155955, "after_tokens": 10476,
	}
	_ = app.handleAgentDomainEvent(workspace.AgentEvent{Type: "context_compaction_started", AgentID: agentID, Data: base})
	_ = app.handleAgentDomainEvent(workspace.AgentEvent{Type: "context_compacted", AgentID: agentID, Data: base})
	app.appendAgentMessage(agentID, types.ChatMessage{Role: types.RoleUser, Content: "continue after compact"})
	_ = app.handleAgentDomainEvent(workspace.AgentEvent{Type: "thinking", AgentID: agentID, Data: reasoning.Delta{Text: "first "}})
	_ = app.handleAgentDomainEvent(workspace.AgentEvent{Type: "thinking", AgentID: agentID, Data: reasoning.Delta{Text: "second"}})

	messages := app.AgentMgr.Get(agentID).State.Messages.Messages()
	statusAt, userAt, thinkingAt, thinkingCount := -1, -1, -1, 0
	for index, message := range messages {
		switch {
		case message.LifecycleOperationID == "compact-1":
			statusAt = index
		case message.Content == "continue after compact":
			userAt = index
		case message.Role == types.RoleThinking:
			thinkingAt = index
			thinkingCount++
			if message.Content != "first second" {
				t.Fatalf("thinking content=%q", message.Content)
			}
		}
	}
	if thinkingCount != 1 || statusAt < 0 || userAt <= statusAt || thinkingAt <= userAt {
		t.Fatalf("post-compaction order status=%d user=%d thinking=%d count=%d messages=%+v", statusAt, userAt, thinkingAt, thinkingCount, messages)
	}
	app.syncAgentMessages(agentID, true)
	renderer := app.activeMessageList()
	renderer.SetSize(100, 20)
	renderer.SetChatAreaBounds(0, 20)
	rows := renderer.ExportMessageRowsSnapshot(100).Rows
	statusRow, thinkingRow := -1, -1
	for index, row := range rows {
		if row.ContextBoundary {
			statusRow = index
		}
		if strings.Contains(strings.Join(row.Lines, "\n"), "first second") {
			thinkingRow = index
		}
	}
	if statusRow < 0 || thinkingRow <= statusRow {
		t.Fatalf("paint projection order status=%d thinking=%d", statusRow, thinkingRow)
	}
	if rows[statusRow].ContextBoundaryStart != 1 || len(rows[thinkingRow].Lines) < 2 || strings.TrimSpace(rows[thinkingRow].Lines[0]) != "" {
		t.Fatalf("mandatory inter-object spacing missing: boundary=%+v thinking=%+v", rows[statusRow], rows[thinkingRow])
	}
}
