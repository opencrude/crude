package workspace

import (
	"testing"

	"crude/types"
)

func TestActivateSessionByAgentSwitchesSessionSettingsOwner(t *testing.T) {
	one := &Session{
		SessionID: "one", AgentID: "agent-one",
		Settings: NewSessionSettingsState(types.Settings{Model: "model-one"}, types.Settings{}, nil),
	}
	two := &Session{
		SessionID: "two", AgentID: "agent-two",
		Settings: NewSessionSettingsState(types.Settings{Model: "model-two"}, types.Settings{}, nil),
	}
	ws := &Workspace{
		Sessions: []*Session{one, two},
	}

	ws.ActivateSessionByAgent("agent-one")
	if ws.ActiveSession != one || ws.ActiveSession.Settings != one.Settings {
		t.Fatal("agent-one did not activate its own session settings")
	}
	ws.ActivateSessionByAgent("agent-two")
	if ws.ActiveSession != two || ws.ActiveSession.Settings != two.Settings {
		t.Fatal("agent-two did not activate its own session settings")
	}
}
