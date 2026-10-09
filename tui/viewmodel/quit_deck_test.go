package viewmodel

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/workspace"
)

// Ctrl+Q closes the top-most closable surface. A blocking decision deck cannot
// be closed, so the chord must reach the next rung (minimizing the tab, which
// keeps the decision pending) instead of being silently swallowed.
func TestCtrlQWithBlockingDeckMinimizesTab(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 30
	app.AddOrchestrator("root")
	session := &workspace.Session{SessionID: "root-session"}
	app.ActiveSession = session
	wsOf(app).SessionsByAgent = map[string]*workspace.Session{"root": session}
	app.switchToAgentView("root")
	app.paintShellFrame()
	app.enqueueAgentConfirmation(agentPromptRequest{kind: agentPromptConfirmation, agentID: "root", blockID: "block", question: "Allow write?"})
	projection := app.activeSessionDeck()
	if projection == nil || !projection.deck.IsOpen() {
		t.Fatal("blocking confirmation did not open the session deck")
	}
	app.dispatchKeyPress(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	if !app.AgentView.IsHidden("root") {
		t.Fatal("Ctrl+Q with a blocking deck open did nothing")
	}
	if len(app.agentPrompts) != 1 {
		t.Fatalf("Ctrl+Q must not resolve the pending decision: %d prompts", len(app.agentPrompts))
	}
}
