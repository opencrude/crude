package workspace

import "testing"

func TestResolveAgentBlockRejectsMissingBackendOwner(t *testing.T) {
	mgr := NewAgentManager()
	mgr.AddOrchestrator("first", &AgentState{})
	mgr.AddSubAgent("child", "first", "child", "model", "category", &AgentState{})
	ws := &Workspace{AgentMgr: mgr}

	if ws.ResolveConfirmation("child", "block-1", "always") {
		t.Fatal("response was accepted without an owning backend session")
	}
}

func TestResolveAgentBlockRejectsMissingRequestID(t *testing.T) {
	mgr := NewAgentManager()
	mgr.AddOrchestrator("root", &AgentState{})
	ws := &Workspace{AgentMgr: mgr}

	if ws.ResolveConfirmation("root", "", "allow") {
		t.Fatal("response without request ID was accepted")
	}
}

func TestCloseAgentRemovesSessionOwnership(t *testing.T) {
	mgr := NewAgentManager()
	mgr.AddOrchestrator("agent", &AgentState{})
	sess := &Session{AgentID: "agent"}
	ws := &Workspace{
		AgentMgr:        mgr,
		Sessions:        []*Session{sess},
		SessionsByAgent: map[string]*Session{"agent": sess},
	}

	ws.CloseAgent("agent")

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
	mgr.AddOrchestrator("agent", &AgentState{})
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
	mgr.AddOrchestrator("first", &AgentState{})
	mgr.AddOrchestrator("second", &AgentState{})
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
	got, ok = ws.RecordAgentUsage("first", map[string]any{
		"prompt": float64(20), "completion": float64(6), "prompt_cached": float64(3),
		"cost": float64(0.30), "context_window": float64(100), "context_prompt": float64(50),
	})
	if !ok || got.Prompt != 30 || got.Completion != 10 || got.Cached != 5 || got.Cost != 0.55 || got.ContextUsed != 50 {
		t.Fatalf("usage did not accumulate across calls: %#v ok=%v", got, ok)
	}
	mgr.AddSubAgent("child", "first", "child", "model", "researcher", &AgentState{})
	if _, ok := ws.RecordAgentUsage("child", map[string]any{
		"prompt": float64(7), "completion": float64(2), "context_window": float64(80), "context_prompt": float64(60),
	}); !ok {
		t.Fatal("child usage was not routed to its root session")
	}
	rootUsage, _ := ws.AgentUsage("first")
	childUsage, _ := ws.AgentUsage("child")
	if rootUsage.Prompt != 37 || rootUsage.Completion != 12 || rootUsage.ContextUsed != 50 || childUsage.ContextUsed != 60 {
		t.Fatalf("session totals/per-agent contexts diverged: root=%#v child=%#v", rootUsage, childUsage)
	}
	if second.TokenPrompt != 99 {
		t.Fatalf("background usage mutated active unrelated session: %d", second.TokenPrompt)
	}
}
