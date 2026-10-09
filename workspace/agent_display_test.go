package workspace

import "testing"

func TestAgentDisplayLabelFallbackOrder(t *testing.T) {
	if got := AgentDisplayLabel(nil); got != "" {
		t.Fatalf("nil node: got %q", got)
	}
	if got := AgentDisplayLabel(&AgentNode{}); got != "" {
		t.Fatalf("empty node should omit label, got %q", got)
	}
	if got := AgentDisplayLabel(&AgentNode{ID: "sess_orch_1"}); got != "" {
		t.Fatalf("must never return raw id, got %q", got)
	}
	if got := AgentDisplayLabel(&AgentNode{Category: "default"}); got != "Main Agent" {
		t.Fatalf("category fallback: got %q", got)
	}
	if got := AgentDisplayLabel(&AgentNode{Label: "Research", Category: "default"}); got != "Research" {
		t.Fatalf("label wins over category: got %q", got)
	}
	if got := AgentDisplayLabel(&AgentNode{Category: "researcher"}); got != "Researcher" {
		t.Fatalf("researcher: got %q", got)
	}
}
