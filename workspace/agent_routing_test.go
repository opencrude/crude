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

func TestResolveAgentBlockReplacesStaleBufferedResponse(t *testing.T) {
	mgr := NewAgentManager()
	block := make(chan string, 1)
	block <- "stale"
	mgr.AddOrchestrator("root", nil, nil, nil, block, &AgentState{})
	ws := &Workspace{AgentMgr: mgr}

	if !ws.ResolveConfirmation("root", "allow") {
		t.Fatal("latest response was rejected")
	}
	if got := <-block; got != "allow" {
		t.Fatalf("buffered response = %q, want latest answer", got)
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

func TestCloseAgentPersistsRecoverableRegistryRow(t *testing.T) {
	wdb := openTestWorkspaceDB(t)
	sdb := openTestSessionDB(t, "sess-close-1")
	mgr := NewAgentManager()
	cancelCh := make(chan struct{}, 1)
	mgr.AddOrchestrator("agent", nil, nil, cancelCh, nil, &AgentState{})
	sess := &Session{
		SessionID: "sess-close-1",
		AgentID:   "agent",
		DB:        sdb,
	}
	ws := &Workspace{
		RootPath:        "/tmp/project",
		AgentMgr:        mgr,
		WorkspaceDB:     wdb,
		Sessions:        []*Session{sess},
		SessionsByID:    map[string]*Session{"sess-close-1": sess},
		SessionsByAgent: map[string]*Session{"agent": sess},
		Logger:          nopLogger{},
	}

	// Intentionally never registered — CloseAgent must Register + CloseSession.
	ws.CloseAgent("agent")

	metas, err := wdb.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(metas) != 1 || metas[0].ID != "sess-close-1" {
		t.Fatalf("registry after close: %+v", metas)
	}
	var closedAt int64
	if err := wdb.DB.QueryRow(
		`SELECT closed_at FROM workspace_sessions WHERE session_id = ?`, "sess-close-1",
	).Scan(&closedAt); err != nil {
		t.Fatalf("closed_at read: %v", err)
	}
	if closedAt == 0 {
		t.Fatal("closed_at was not stamped")
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
		"context_prompt": float64(37),
		"estimated":      true,
	})
	if !ok {
		t.Fatal("RecordAgentUsage() rejected valid usage")
	}
	if got.Prompt != 10 || got.Completion != 4 || got.Cached != 2 ||
		got.Cost != 0.25 || got.ContextUsed != 37 || !got.Estimated {
		t.Fatalf("unexpected usage snapshot: %#v", got)
	}
	if second.TokenPrompt != 99 {
		t.Fatalf("background usage mutated active unrelated session: %d", second.TokenPrompt)
	}
}
