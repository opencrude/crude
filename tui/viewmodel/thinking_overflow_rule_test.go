package viewmodel

import (
	"strings"
	"testing"
	"time"

	"crude/tui/chatprojection"
	types "crude/types"
	"crude/types/reasoning"
)

// TestThinkingOverflowPromotionIsSharedByMainAndSubAgents pins the single
// presentation rule: a block that overflows the inline form promotes, and an
// identical block promotes the same way for the main agent and for a sub-agent.
// There is no per-role classifier.
func TestThinkingOverflowPromotionIsSharedByMainAndSubAgents(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 100, 40
	app.AddOrchestrator("main")
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "child", Type: "sub_agent_started", Data: map[string]string{
		"agent_id": "child", "agent_name": "Researcher", "category": "researcher", "parent_id": "main", "model": "child-model",
	}})
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "main", Type: "turn_started", Data: types.TurnIdentity{
		Generation: 1, ProviderID: "gateway", AccountID: "main-account", ModelID: "main-model"}})
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "child", Type: "turn_started", Data: types.TurnIdentity{
		Generation: 1, ProviderID: "gateway", AccountID: "child-account", ModelID: "child-model"}})
	trace := strings.Repeat("a line of reasoning about the task\n", 20)

	app.handleAgentEvent(types.AgentEventMsg{AgentID: "main", Type: "thinking", Data: reasoning.Delta{Text: trace, Kind: reasoning.Summary}})
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "child", Type: "thinking", Data: reasoning.Delta{Text: trace, Kind: reasoning.Summary}})

	main := app.AgentMgr.Get("main").State
	child := app.AgentMgr.Get("child").State
	if main.ThinkingPaneID == "" || child.ThinkingPaneID == "" {
		t.Fatalf("overflow did not promote both owners: main=%q child=%q", main.ThinkingPaneID, child.ThinkingPaneID)
	}
	for _, tc := range []struct{ id, paneID string }{{"main", main.ThinkingPaneID}, {"child", child.ThinkingPaneID}} {
		pane := app.PaneMgr.Get(tc.paneID)
		if pane == nil || len(pane.Messages.Messages()) == 0 || !strings.Contains(pane.Messages.Messages()[0].Content, "a line of reasoning") {
			t.Fatalf("%s pane lost the promoted body", tc.id)
		}
	}

	// A second identical block for either owner follows the same session latch,
	// and a settings change resets that choice (covered by the dedicated latch
	// test); promotion itself stays decided by the block, not the owner's role.
}

// TestThinkingOverflowLatchClearsOnSettingsChange pins the session latch: after
// one promotion every later block in the session opens in its own pane, and a
// settings change resets that choice so the next block starts inline again.
func TestThinkingOverflowLatchClearsOnSettingsChange(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 100, 40
	app.AddOrchestrator("owner")
	long := strings.Repeat("a line of reasoning about the task\n", 20)

	app.handleAgentEvent(types.AgentEventMsg{AgentID: "owner", Type: "thinking", Data: reasoning.Delta{Text: long, Kind: reasoning.Summary}})
	state := app.AgentMgr.Get("owner").State
	if state.ThinkingPaneID == "" {
		t.Fatal("overflowing block did not promote")
	}
	if !app.thinkingOverflowLatched() {
		t.Fatal("promotion did not latch the session")
	}
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "owner", Type: "done", Data: map[string]any{"reason": "completed"}})

	// Latched: a later short block still opens in a pane.
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "owner", Type: "thinking", Data: reasoning.Delta{Text: "brief", Kind: reasoning.Summary}})
	if state.ThinkingPaneID == "" {
		t.Fatal("session latch did not keep later blocks in a pane")
	}
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "owner", Type: "done", Data: map[string]any{"reason": "completed"}})

	// A settings change clears the latch, so the same short block stays inline.
	app.clearThinkingOverflowLatch(app.ActiveSession)
	if app.thinkingOverflowLatched() {
		t.Fatal("settings change did not clear the promotion latch")
	}
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "owner", Type: "thinking", Data: reasoning.Delta{Text: "brief", Kind: reasoning.Summary}})
	if state.ThinkingPaneID != "" {
		t.Fatal("cleared latch still promoted a short block")
	}
}

// TestThinkingOverflowRuleIsWidthSensitive verifies the same trace promotes only
// when it genuinely overflows at the width the chat renders at.
func TestThinkingOverflowRuleIsWidthSensitive(t *testing.T) {
	trace := strings.Repeat("word ", 40)
	if !chatprojection.ThinkingOverflowsInline(trace, 0, false, time.Now(), 20) {
		t.Fatal("a narrow chat did not promote an overflowing trace")
	}
	if chatprojection.ThinkingOverflowsInline("short thought", 0, false, time.Now(), 200) {
		t.Fatal("a wide chat promoted a short trace")
	}
}
