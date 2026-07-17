package workspace

import "testing"

func TestAgentManagerPreservesMultipleOrchestrators(t *testing.T) {
	mgr := NewAgentManager()
	first := mgr.AddOrchestrator("first", nil, nil, nil, nil, &AgentState{})
	mgr.AddSubAgent("first-child", first.ID, "child", "model", "category", &AgentState{})
	second := mgr.AddOrchestrator("second", nil, nil, nil, nil, &AgentState{})

	if got := mgr.Count(); got != 3 {
		t.Fatalf("Count() = %d, want 3", got)
	}
	if mgr.Get(first.ID) == nil || mgr.Get("first-child") == nil || mgr.Get(second.ID) == nil {
		t.Fatal("adding a second orchestrator orphaned an existing agent tree")
	}
	if got := len(mgr.Orchestrators()); got != 2 {
		t.Fatalf("len(Orchestrators()) = %d, want 2", got)
	}
	if got := mgr.OrchestratorFor("first-child"); got != first {
		t.Fatalf("OrchestratorFor(first-child) = %v, want first", got)
	}
}

func TestAgentManagerRemoveTreePreservesOtherOrchestrators(t *testing.T) {
	mgr := NewAgentManager()
	mgr.AddOrchestrator("first", nil, nil, nil, nil, &AgentState{})
	mgr.AddSubAgent("first-child", "first", "child", "model", "category", &AgentState{})
	mgr.AddOrchestrator("second", nil, nil, nil, nil, &AgentState{})
	mgr.SetActive("first-child")

	mgr.Remove("first")

	if mgr.Get("first") != nil || mgr.Get("first-child") != nil {
		t.Fatal("removing an orchestrator did not remove its complete tree")
	}
	if mgr.Get("second") == nil || mgr.Count() != 1 {
		t.Fatal("removing one orchestrator removed or hid another orchestrator")
	}
	if got := mgr.ActiveID(); got != "" {
		t.Fatalf("ActiveID() = %q after removing active descendant, want empty", got)
	}
}
