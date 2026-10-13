package viewmodel

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/types"
	"crude/types/reasoning"
	"crude/workspace"
)

func thinkingMessageCount(state *workspace.AgentState) int {
	n := 0
	for _, message := range state.Messages.Messages() {
		if message.Role == workspace.MessageRoleThinking {
			n++
		}
	}
	return n
}

// A human Stop while a long raw reasoning stream is promoted to a pane must
// stop the turn, and reasoning deltas still queued behind the stop must neither
// create a new thinking message/pane per delta nor reactivate the turn.
func TestStopWithPromotedRawThinkingKeepsOneThoughtAndNoNewPanes(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 100, 40
	app.AddOrchestrator("agent")
	agent := app.ws.AgentMgr().Get("agent").State
	app.paintShellFrame()
	app.handleAgentEvent(types.AgentEventMsg{Type: "turn_started", AgentID: "agent", Data: types.TurnIdentity{Generation: 1, ModelID: "deepseek-v4.1-flash"}})
	for i := 0; i < 40; i++ {
		app.handleAgentEvent(types.AgentEventMsg{Type: "thinking", AgentID: "agent", Data: reasoning.Delta{Text: strings.Repeat("reasoning word ", 12), Kind: reasoning.Raw}})
	}
	paneID := app.thinkingPaneID(agent)
	if paneID == "" {
		t.Fatal("fixture did not promote the raw thinking pane")
	}
	if got := thinkingMessageCount(agent); got != 1 {
		t.Fatalf("thinking messages before stop = %d", got)
	}
	sequence := app.thinkingPaneSequence

	_, _ = app.dispatchChatKey(tea.KeyPressMsg{Code: tea.KeyEsc}, new([]tea.Cmd))
	if !agent.Cancelling || agent.IsActive() {
		t.Fatalf("Esc did not begin cancellation: cancelling=%v active=%v", agent.Cancelling, agent.IsActive())
	}
	if app.thinkingPaneID(agent) != "" {
		t.Fatal("stop left the thinking pane promoted")
	}
	stopped := thinkingMessageCount(agent)
	for i := 0; i < 50; i++ {
		app.handleAgentEvent(types.AgentEventMsg{Type: "thinking", AgentID: "agent", Data: reasoning.Delta{Text: "late", Kind: reasoning.Raw}})
		app.paintShellFrame()
		if id := app.thinkingPaneID(agent); id != "" {
			t.Fatalf("late delta %d promoted pane %q after stop", i, id)
		}
	}
	if got := thinkingMessageCount(agent); got != stopped {
		t.Fatalf("late deltas created thinking messages: %d -> %d", stopped, got)
	}
	if app.thinkingPaneSequence != sequence {
		t.Fatalf("late deltas allocated %d new pane ids", app.thinkingPaneSequence-sequence)
	}
	if agent.IsActive() || len(app.thinkingPresentations) != 0 {
		t.Fatalf("late deltas reactivated the stopped turn: active=%v panes=%d", agent.IsActive(), len(app.thinkingPresentations))
	}
}
