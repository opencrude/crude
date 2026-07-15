package workspace

import (
	"testing"

	"crude/tui/input"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/widgets"
)

func TestActivateSessionByAgentSwitchesSessionSettingsOwner(t *testing.T) {
	th := theme.DefaultDark()
	one := &Session{
		SessionID: "one", AgentID: "agent-one",
		Settings: widgets.NewSessionSettings(th, types.Settings{Model: "model-one"}),
	}
	two := &Session{
		SessionID: "two", AgentID: "agent-two",
		Settings: widgets.NewSessionSettings(th, types.Settings{Model: "model-two"}),
	}
	ws := &Workspace{
		Sessions: []*Session{one, two},
		Input:    input.NewModel(th),
	}

	ws.ActivateSessionByAgent("agent-one")
	if ws.ActiveSession != one || ws.SessionSettings != one.Settings {
		t.Fatal("agent-one did not activate its own session settings")
	}
	ws.ActivateSessionByAgent("agent-two")
	if ws.ActiveSession != two || ws.SessionSettings != two.Settings {
		t.Fatal("agent-two did not activate its own session settings")
	}
}
