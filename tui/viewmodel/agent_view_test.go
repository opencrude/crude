package viewmodel

import (
	"reflect"
	"slices"
	"testing"

	"crude/shellport"
	ttypes "crude/types"
	"crude/workspace"
)

func TestAgentViewIndependentShellNavigationPreservesSharedLifecycle(t *testing.T) {
	manager := workspace.NewAgentManager()
	root := manager.AddOrchestrator("root", &workspace.AgentState{Running: true, Status: "running"})
	manager.AddSubAgent("parent", "root", "Parent", "model", "crafter", &workspace.AgentState{Running: true})
	review := manager.AddSubAgent("review", "parent", "Review", "model", "crafter", &workspace.AgentState{Completed: true, Status: "completed"})
	sibling := manager.AddSubAgent("sibling", "parent", "Sibling", "model", "crafter", &workspace.AgentState{Completed: true})
	owner := &workspace.Workspace{AgentMgr: manager, Status: workspace.NewStatusCenter(nil, nil)}
	shared := shellport.Local(owner)
	// Navigation-only holders: no running Apps, event sinks or shared UI resources.
	left := &App{ws: shared, AgentView: NewAgentViewState(manager)}
	right := &App{ws: shared, AgentView: NewAgentViewState(manager)}
	before := make(map[string]workspace.AgentState)
	for _, node := range manager.AllNodes() {
		before[node.ID] = *node.State
	}
	owner.Status.SetAgent(ttypes.AgentStatusSnapshot{Ref: ttypes.ResourceRef{ID: "review"}, Phase: ttypes.AgentCompleted})
	left.AgentView.SetActive("root")
	right.AgentView.SetActive("review")
	left.MinimizeAgent("sibling")
	left.MinimizeAgent("review")
	left.MinimizeAgent("root")
	if left.AgentView.ActiveID() != "" || left.AgentView.VisibleCount() != 0 {
		t.Fatal("hidden subtree remained selected or visible in left shell")
	}
	if right.AgentView.ActiveID() != "review" || right.AgentView.VisibleCount() != 4 || right.AgentView.IsHidden("review") {
		t.Fatal("left shell changed right selection or visibility")
	}
	left.RestoreAgent("review")
	if got := left.AgentView.TabOrder(); !slices.Equal(got, []string{"root", "parent", "review"}) {
		t.Fatalf("revealed wrong path: %v", got)
	}
	if left.AgentView.ActiveNode() != review || !left.AgentView.IsHidden(sibling.ID) {
		t.Fatal("restore lost exact selection or independent sibling choice")
	}
	right.MinimizeAgent("parent")
	if right.AgentView.ActiveID() != "" || !right.AgentView.IsHidden("review") || left.AgentView.IsHidden("review") {
		t.Fatal("right subtree hide crossed shells or retained hidden selection")
	}
	if views := left.AgentTabViews(); len(views) != 3 || !views[2].Active || views[2].DisplayTitle != "Review" {
		t.Fatalf("left rendered tab projection = %+v", views)
	}
	if views := right.AgentTabViews(); len(views) != 1 || views[0].ID != root.ID || views[0].Active {
		t.Fatalf("right rendered tab projection = %+v", views)
	}
	left.syncAgentStatusInventory()
	if status, _ := owner.Status.Agent("review"); status.Hidden {
		t.Fatal("shell visibility leaked into shared StatusCenter")
	}
	for _, node := range manager.AllNodes() {
		if !reflect.DeepEqual(*node.State, before[node.ID]) {
			t.Fatalf("navigation mutated %s lifecycle", node.ID)
		}
	}
}

func TestAgentViewReplacementDoesNotInheritSelectionOrVisibility(t *testing.T) {
	manager := workspace.NewAgentManager()
	manager.AddOrchestrator("root", &workspace.AgentState{})
	view := NewAgentViewState(manager)
	view.SetActive("root")
	manager.AddOrchestrator("root", &workspace.AgentState{})
	if view.ActiveID() != "" {
		t.Fatal("replacement inherited prior exact-node selection")
	}
	view.SetHidden("root", true)
	manager.AddOrchestrator("root", &workspace.AgentState{})
	if view.IsHidden("root") || view.VisibleCount() != 1 {
		t.Fatal("replacement inherited prior exact-node visibility")
	}
	view.SetActive("root")
	manager.Remove("root")
	if view.ActiveNode() != nil || view.ActiveID() != "" || view.VisibleCount() != 0 {
		t.Fatal("removed inventory retained a selected or visible node")
	}
}
