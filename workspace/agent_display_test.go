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

func TestAgentTabViewsNeverExposeRawIDAsDisplayTitle(t *testing.T) {
	manager := NewAgentManager()
	node := manager.AddOrchestrator("2026_orch_1", NewAgentState("2026_orch_1", nil, nil))
	node.Category = "default"

	views := (&Workspace{AgentMgr: manager}).AgentTabViews()
	if len(views) != 1 {
		t.Fatalf("expected 1 view, got %d", len(views))
	}
	if views[0].DisplayTitle != "Main Agent" {
		t.Fatalf("DisplayTitle=%q want Main Agent", views[0].DisplayTitle)
	}
	if views[0].ID != "2026_orch_1" {
		t.Fatalf("ID should remain internal key, got %q", views[0].ID)
	}
}
