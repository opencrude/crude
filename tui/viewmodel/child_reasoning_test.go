package viewmodel

import (
	"strings"
	"testing"

	"crude/types"
	"crude/types/reasoning"
)

func TestChildRawReasoningStaysInItsPaneWhileParentHasCompactThinking(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 100, 40
	app.AddOrchestrator("parent")
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "parent", Type: "turn_started", Data: types.TurnIdentity{
		Generation: 1, ProviderID: "gateway", AccountID: "parent-account", ModelID: "parent-model",
	}})
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "parent", Type: "thinking", Data: reasoning.Delta{
		Text: "Parent summary", Kind: reasoning.Summary,
	}})
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "child", Type: "sub_agent_started", Data: map[string]string{
		"agent_id": "child", "agent_name": "Researcher", "category": "researcher", "parent_id": "parent", "model": "child-model",
	}})
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "child", Type: "turn_started", Data: types.TurnIdentity{
		Generation: 1, ProviderID: "gateway", AccountID: "child-account", ModelID: "child-model",
	}})
	trace := strings.Repeat("Raw child investigation across the source.\n", 30)
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "child", Type: "thinking", Data: reasoning.Delta{
		Text: trace, Kind: reasoning.InferredRaw,
	}})

	child := app.AgentMgr.Get("child")
	parent := app.AgentMgr.Get("parent")
	if child == nil || child.State.ThinkingPaneID == "" {
		t.Fatal("child raw trace did not promote into its own thinking pane")
	}
	if pane := app.PaneMgr.Get(child.State.ThinkingPaneID); pane == nil || !strings.Contains(pane.Messages.Messages()[0].Content, "Raw child investigation") {
		t.Fatal("child thinking pane lost the raw body")
	}
	if parent.State.ThinkingPaneID != "" || !strings.Contains(app.AgentActivity("parent").Thinking, "Parent summary") {
		t.Fatal("child raw trace changed the parent's compact reasoning")
	}
	app.switchToAgentView("child")
	app.paintShellFrame()
	if line := shellBufferRow(app, app.chatRect.Y+app.chatRect.H-1); strings.Contains(line, "Raw child investigation") || strings.Contains(line, "Parent summary") {
		t.Fatalf("child raw trace or parent summary leaked into child strip: %q", line)
	}
	app.switchToAgentView("parent")
	app.paintShellFrame()
	if line := shellBufferRow(app, app.chatRect.Y+app.chatRect.H-1); !strings.Contains(line, "Parent summary") || strings.Contains(line, "Raw child investigation") {
		t.Fatalf("parent strip lost its own reasoning: %q", line)
	}
}

func TestChildMeasuredRawReasoningPromotesAfterUsageArrives(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 100, 40
	app.AddOrchestrator("parent")
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "child", Type: "sub_agent_started", Data: map[string]string{
		"agent_id": "child", "agent_name": "Researcher", "parent_id": "parent", "model": "child-model",
	}})
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "child", Type: "turn_started", Data: types.TurnIdentity{
		Generation: 1, ProviderID: "gateway", AccountID: "child-account", ModelID: "child-model",
	}})
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "child", Type: "thinking", Data: reasoning.Delta{
		Text: strings.Repeat("unlabelled child trace.\n", 25), Kind: reasoning.Provisional,
	}})
	child := app.AgentMgr.Get("child")
	if child == nil || child.State.ThinkingPaneID != "" {
		t.Fatal("unclassified trace was promoted before provider evidence")
	}
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "child", Type: "reasoning_classified", Data: reasoning.InferredRaw})
	if child.State.ThinkingPaneID == "" {
		t.Fatal("measured raw trace was left in the status strip")
	}
	if pane := app.PaneMgr.Get(child.State.ThinkingPaneID); pane == nil || !strings.Contains(pane.Messages.Messages()[0].Content, "unlabelled child trace") {
		t.Fatal("measured child trace was lost during promotion")
	}
}
