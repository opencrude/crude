package capability

import (
	"encoding/json"
	"testing"
)

func TestProjectToolDecisionsCloneAndRegistration(t *testing.T) {
	raw := json.RawMessage(` { "bad" : [1, 2] } `)
	clone := Clone(Settings{ProjectToolDecisions: raw})
	if string(clone.ProjectToolDecisions) != string(raw) {
		t.Fatalf("raw decision changed: %q", clone.ProjectToolDecisions)
	}
	clone.ProjectToolDecisions[1] = 'x'
	if raw[1] == 'x' {
		t.Fatal("clone aliases decision bytes")
	}
	if _, err := ApplyRegistration(Settings{}, RegistrationChange{Kind: KindTool}, Settings{}); err == nil {
		t.Fatal("tool registration was accepted as MCP")
	}
}
