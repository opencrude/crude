package viewmodel

import (
	"time"

	"strings"
	"testing"

	"crude/tui/chatprojection"
	types "crude/types"
	"crude/types/reasoning"
	"crude/workspace"
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
	if app.thinkingPaneID(main) == "" || app.thinkingPaneID(child) == "" {
		t.Fatalf("overflow did not promote both owners: main=%q child=%q", app.thinkingPaneID(main), app.thinkingPaneID(child))
	}
	for _, tc := range []struct{ id, paneID string }{{"main", app.thinkingPaneID(main)}, {"child", app.thinkingPaneID(child)}} {
		pane := app.Get(tc.paneID)
		if pane == nil {
			t.Fatalf("%s pane missing", tc.id)
		}
		thought, active := pane.Messages.ActiveThinking()
		if !active || !strings.Contains(thought.Content, "a line of reasoning") {
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
	owner := workspace.NewSession("thinking-owner", t.TempDir(), nil, nil)
	app.SessionsByAgent = map[string]*workspace.Session{"owner": owner}
	app.ActiveSession = owner
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "owner", Type: "turn_started", Data: types.TurnIdentity{Generation: 1, ModelID: "model"}})
	long := strings.Repeat("a line of reasoning about the task\n", 20)

	app.handleAgentEvent(types.AgentEventMsg{AgentID: "owner", Type: "thinking", Data: reasoning.Delta{Text: long, Kind: reasoning.Summary}})
	state := app.AgentMgr.Get("owner").State
	if app.thinkingPaneID(state) == "" {
		t.Fatal("overflowing block did not promote")
	}
	if !app.thinkingOverflowLatched() {
		t.Fatal("promotion did not latch the session")
	}
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "owner", Type: "done", Data: map[string]any{"reason": "completed"}})

	// Latched: a later short block still opens in a pane.
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "owner", Type: "turn_started", Data: types.TurnIdentity{Generation: 2, ModelID: "model"}})
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "owner", Type: "thinking", Data: reasoning.Delta{Text: "brief", Kind: reasoning.Summary}})
	if app.thinkingPaneID(state) == "" {
		t.Fatal("session latch did not keep later blocks in a pane")
	}
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "owner", Type: "done", Data: map[string]any{"reason": "completed"}})

	// A settings change clears the latch, so the same short block stays inline.
	app.clearThinkingOverflowLatch(app.ActiveSession)
	if app.thinkingOverflowLatched() {
		t.Fatal("settings change did not clear the promotion latch")
	}
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "owner", Type: "turn_started", Data: types.TurnIdentity{Generation: 3, ModelID: "model"}})
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "owner", Type: "thinking", Data: reasoning.Delta{Text: "brief", Kind: reasoning.Summary}})
	if app.thinkingPaneID(state) != "" {
		t.Fatal("cleared latch still promoted a short block")
	}
}

// TestThinkingOverflowRuleIsKindIndependent pins the UI-level requirement: the
// rule reads only the current block's wrapped rows and the render width, so a
// provider's reasoning field name or declared kind cannot influence promotion.
func TestThinkingOverflowRuleIsKindIndependent(t *testing.T) {
	width := 70
	long := strings.Repeat("a line of reasoning\n", 8)
	if !chatprojection.ThinkingOverflowsInline(long, 0, time.Now(), width) {
		t.Fatal("a long reasoning block did not promote")
	}
	// The same accumulated text stays inline while a short current block is the
	// one the reader sees, which is how a condensed gateway keeps its strip: each
	// header is its own semantic block, so Start points at the latest one.
	content := strings.Repeat("**Earlier header**\n\n", 40) + "**Clarifying burst and rate**"
	if chatprojection.ThinkingOverflowsInline(content, len(content)-len("**Clarifying burst and rate**"), time.Now(), width) {
		t.Fatal("a condensed gateway header promoted")
	}
	// A short trace that accumulated slowly must also stay, because it never
	// arrived as a burst.
	if chatprojection.ThinkingOverflowsInline(strings.Repeat("a line of reasoning\n", 8), 0, time.Now().Add(-60*time.Second), width) {
		t.Fatal("a slow long block promoted on length alone")
	}
	if !chatprojection.ThinkingOverflowsInline(strings.Repeat("word ", 40), 0, time.Now(), 20) {
		t.Fatal("a narrow chat did not promote an overflowing block")
	}
	if chatprojection.ThinkingOverflowsInline("short thought", 0, time.Now(), 200) {
		t.Fatal("a wide chat promoted a short block")
	}
}
