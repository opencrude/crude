package app

import (
	"os"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/history"
	"crude/provider"
	"crude/tui"
	"crude/workspace"
)

// This extension runs the same goal-only live-model acceptance through the
// production Bubble Tea shell and message pump. It never writes credentials to
// its isolated home. Use scripts/tuishot.sh sequentially with a compiled app test
// binary and CRUDE_VISUAL_CAPABILITIES=1; normal test runs start no terminal.
func startLiveCapabilityVisual(t *testing.T, ws *workspace.Workspace, agentID string, sender *liveCapabilitySender) *tui.TUI {
	t.Helper()
	if os.Getenv("CRUDE_VISUAL_CAPABILITIES") != "1" {
		return nil
	}
	terminal, err := tui.Start(ws, providerPresets(), history.New(), &appLogger{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		terminal.Stop()
		terminal.Wait()
	})
	sender.forward = terminal
	ws.SetEventSender(sender)
	// Let the production frontend create its pane, renderer and shared
	// conversation. Pre-registering a headless state skips that initialization
	// and produces a blank transcript even though the backend is working.
	terminal.AddOrchestrator(agentID)
	// Keep chat visible. Settings is a separate explicit inspection, not a
	// startup action and not the session-only /settings deck.
	time.Sleep(5 * time.Second)
	return terminal
}

func submitLiveCapabilityInput(t *testing.T, ws *workspace.Workspace, agentID, text string, terminal *tui.TUI) {
	t.Helper()
	if terminal == nil {
		if !ws.SubmitAgentInput(agentID, provider.PlainInput(text)) {
			t.Fatal("live capability task was not admitted")
		}
		return
	}
	// Use the actual composer paste/send route: it publishes the user row and
	// updates the same transcript that renders agent and tool events.
	terminal.Program.Send(tea.PasteMsg{Content: text})
	terminal.Program.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
}
