package workspace

import "testing"

func TestResolveAgentBlockRoutesThroughOwningOrchestrator(t *testing.T) {
	mgr := NewAgentManager()
	firstBlock := make(chan string, 2)
	secondBlock := make(chan string, 1)
	mgr.AddOrchestrator("first", nil, nil, nil, firstBlock, &AgentState{})
	mgr.AddSubAgent("child", "first", "child", "model", "category", &AgentState{})
	mgr.AddOrchestrator("second", nil, nil, nil, secondBlock, &AgentState{})
	ws := &Workspace{AgentMgr: mgr}

	if !ws.ResolveConfirmation("child", "always") {
		t.Fatal("ResolveConfirmation() did not route the sub-agent response")
	}
	if got := <-firstBlock; got != "always" {
		t.Fatalf("first orchestrator response = %q, want always", got)
	}

	answers := map[string]string{"1": "Go", "2": "SQLite"}
	questions := map[string]string{"1": "Language?", "2": "Database?"}
	if !ws.ResolveQuestion("child", answers, questions, false) {
		t.Fatal("ResolveQuestion() did not route the sub-agent response")
	}
	const want = "Q1: Language?\nA1: Go\n\nQ2: Database?\nA2: SQLite"
	if got := <-firstBlock; got != want {
		t.Fatalf("question response = %q, want %q", got, want)
	}
	select {
	case got := <-secondBlock:
		t.Fatalf("response was sent to unrelated orchestrator: %q", got)
	default:
	}
}

func TestCloseAgentCancelsBeforeRemovingSessionOwnership(t *testing.T) {
	mgr := NewAgentManager()
	cancelCh := make(chan struct{}, 1)
	mgr.AddOrchestrator("agent", nil, nil, cancelCh, nil, &AgentState{})
	sess := &Session{AgentID: "agent"}
	ws := &Workspace{
		AgentMgr:        mgr,
		Sessions:        []*Session{sess},
		SessionsByAgent: map[string]*Session{"agent": sess},
	}

	ws.CloseAgent("agent")

	select {
	case <-cancelCh:
	default:
		t.Fatal("CloseAgent() did not cancel the orchestrator")
	}
	if _, ok := ws.SessionsByAgent["agent"]; ok {
		t.Fatal("CloseAgent() retained stale session ownership")
	}
	if sess.AgentID != "" {
		t.Fatalf("session AgentID = %q, want empty", sess.AgentID)
	}
}

func TestRecordAgentUsageUpdatesOwningSession(t *testing.T) {
	mgr := NewAgentManager()
	mgr.AddOrchestrator("first", nil, nil, nil, nil, &AgentState{})
	mgr.AddOrchestrator("second", nil, nil, nil, nil, &AgentState{})
	first := &Session{AgentID: "first"}
	second := &Session{AgentID: "second", TokenPrompt: 99}
	ws := &Workspace{
		AgentMgr: mgr,
		SessionsByAgent: map[string]*Session{
			"first":  first,
			"second": second,
		},
		ActiveSession: second,
	}

	got, ok := ws.RecordAgentUsage("first", map[string]any{
		"prompt":         float64(10),
		"completion":     float64(4),
		"prompt_cached":  float64(2),
		"cost":           0.25,
		"context_window": float64(100),
		"context_used":   float64(14),
		"estimated":      true,
	})
	if !ok {
		t.Fatal("RecordAgentUsage() rejected valid usage")
	}
	if got.Prompt != 10 || got.Completion != 4 || got.Cached != 2 || got.Cost != 0.25 || got.ContextUsed != 14 || !got.Estimated {
		t.Fatalf("unexpected usage snapshot: %#v", got)
	}
	if second.TokenPrompt != 99 {
		t.Fatalf("background usage mutated active unrelated session: %d", second.TokenPrompt)
	}
}
