package agent

import (
	"strings"
	"testing"
)

// The bare "maximum concurrent sub-agents (4) reached" left the parent model
// guessing which children held the slots and what to do next.
func TestRegisterAtLimitNamesRunningChildrenAndRemedy(t *testing.T) {
	m := NewSubAgentManager(1)
	if err := m.Register(&SubAgentHandle{Spec: SubAgentSpec{ID: "agent-a", Name: "Nova sub Ada"}}); err != nil {
		t.Fatal(err)
	}
	err := m.Register(&SubAgentHandle{Spec: SubAgentSpec{ID: "agent-b", Name: "Orion sub Ada"}})
	if err == nil {
		t.Fatal("second registration exceeded the limit")
	}
	for _, want := range []string{"Nova sub Ada (agent-a)", "agent_wait", "agent_control", "Nothing was launched"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("limit error %q lacks %q", err, want)
		}
	}
}
