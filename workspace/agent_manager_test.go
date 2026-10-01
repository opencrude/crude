package workspace

import (
	"slices"
	"testing"
)

func TestAgentManagerPreservesMultipleOrchestrators(t *testing.T) {
	mgr := NewAgentManager()
	first := mgr.AddOrchestrator("first", &AgentState{})
	mgr.AddSubAgent("first-child", first.ID, "child", "model", "category", &AgentState{})
	second := mgr.AddOrchestrator("second", &AgentState{})

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
	mgr.AddOrchestrator("first", &AgentState{})
	mgr.AddSubAgent("first-child", "first", "child", "model", "category", &AgentState{})
	mgr.AddOrchestrator("second", &AgentState{})
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

func TestAgentManagerVisibilityPreservesIndependentChildChoicesAndForestOrder(t *testing.T) {
	mgr := NewAgentManager()
	mgr.AddOrchestrator("first", &AgentState{})
	mgr.AddSubAgent("running", "first", "running", "", "", &AgentState{})
	mgr.AddSubAgent("archived", "first", "archived", "", "", &AgentState{})
	mgr.AddSubAgent("grandchild", "running", "grandchild", "", "", &AgentState{})
	mgr.AddOrchestrator("second", &AgentState{})
	mgr.AddSubAgent("second-child", "second", "second child", "", "", &AgentState{})
	mgr.SetHidden("archived", true)
	assertOrder := func(want ...string) {
		t.Helper()
		if got := mgr.TabOrder(); !slices.Equal(got, want) {
			t.Fatalf("TabOrder=%v, want %v", got, want)
		}
		if got := mgr.VisibleCount(); got != len(want) {
			t.Fatalf("VisibleCount=%d, want %d", got, len(want))
		}
	}
	assertOrder("first", "running", "grandchild", "second", "second-child")
	mgr.SetActive("grandchild")
	mgr.SetHidden("first", true)
	if mgr.ActiveID() != "" || !mgr.IsHidden("grandchild") || !mgr.IsHidden("archived") {
		t.Fatal("minimizing the parent must hide all descendants and clear a hidden active child")
	}
	assertOrder("second", "second-child")
	mgr.SetHidden("first", false)
	if !mgr.IsHidden("archived") || mgr.Get("archived").Hidden != true {
		t.Fatal("restoring the parent changed its archived child's own visibility")
	}
	assertOrder("first", "running", "grandchild", "second", "second-child")
	mgr.SetHidden("running", true)
	assertOrder("first", "second", "second-child")
	mgr.SetHidden("running", false)
	assertOrder("first", "running", "grandchild", "second", "second-child")
}
