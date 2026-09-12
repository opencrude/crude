package workspace

import "testing"

func TestAgentTabViewsExposeDomainStateWithoutTUIObjects(t *testing.T) {
	manager := NewAgentManager()
	root := manager.AddOrchestrator("root", NewAgentState("root", nil, nil))
	root.Category = "default"
	manager.AddSubAgent("child", "root", "Research", "model", "researcher", NewAgentState("child", nil, nil))
	manager.SetActive("child")

	views := (&Workspace{AgentMgr: manager}).AgentTabViews()
	if len(views) != 2 {
		t.Fatalf("expected two tab views, got %d", len(views))
	}
	if views[0].Kind != "orchestrator" || views[1].Kind != "sub-agent" {
		t.Fatalf("unexpected tab kinds: %#v", views)
	}
	if views[0].DisplayTitle != "Main Agent" {
		t.Fatalf("orchestrator display title=%q want Main Agent", views[0].DisplayTitle)
	}
	if !views[1].Active || views[1].DisplayTitle != "Research" {
		t.Fatalf("active child view lost domain tab data: %#v", views[1])
	}
}
