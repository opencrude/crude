package workspace

import "testing"

func TestAgentTabViewsExposeDomainStateWithoutTUIObjects(t *testing.T) {
	manager := NewAgentManager()
	manager.AddOrchestrator("root", nil, nil, nil, nil, NewAgentState("root", nil, nil))
	manager.AddSubAgent("child", "root", "Research", "model", "researcher", NewAgentState("child", nil, nil))
	manager.SetActive("child")

	views := (&Workspace{AgentMgr: manager}).AgentTabViews()
	if len(views) != 2 {
		t.Fatalf("expected two tab views, got %d", len(views))
	}
	if views[0].Kind != "orchestrator" || views[1].Kind != "sub-agent" {
		t.Fatalf("unexpected tab kinds: %#v", views)
	}
	if !views[1].Active || views[1].DisplayTitle != "Research" {
		t.Fatalf("active child view lost domain tab data: %#v", views[1])
	}
}

func TestStatusViewUsesWorkspaceUsageSnapshot(t *testing.T) {
	manager := NewAgentManager()
	state := NewAgentState("root", nil, nil)
	state.Status = "running"
	state.Thinking = true
	manager.AddOrchestrator("root", nil, nil, nil, nil, state)
	ws := &Workspace{
		AgentMgr:  manager,
		ModelName: "model",
		Username:  "user",
		SessionsByAgent: map[string]*Session{
			"root": {TokenPrompt: 3, TokenCompletion: 4},
		},
	}

	view := ws.StatusView()
	if view.CurrentAgentID != "root" || view.ActiveAgents != 1 {
		t.Fatalf("unexpected status view: %#v", view)
	}
	if view.ModelName != "model" || view.Username != "user" {
		t.Fatalf("status metadata missing: %#v", view)
	}
}
