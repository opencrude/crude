package viewmodel

import (
	"crude/shellport"
	"crude/workspace"
	"testing"
)

func TestAgentTabViewsNeverExposeRawIDAsDisplayTitle(t *testing.T) {
	manager := workspace.NewAgentManager()
	app := &App{ws: shellport.Local(&workspace.Workspace{AgentMgr: manager}), AgentView: NewAgentViewState(manager)}
	node := manager.AddOrchestrator("2026_orch_1", workspace.NewAgentState("2026_orch_1", nil, nil))
	node.Category = "default"

	views := app.AgentTabViews()
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
