package workspace

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"crude/provider"
	"crude/session"
	"crude/sessiondb"
	"crude/types"
)

func TestHydrateCompletedSubAgentsRestoresReadOnlyReviewTree(t *testing.T) {
	setTestHome(t)
	root := t.TempDir()
	sessionsRoot := filepath.Join(t.TempDir(), "sessions")
	parentDBPath := filepath.Join(t.TempDir(), "parent.db")
	parentDB, err := sessiondb.OpenAndMigrate(parentDBPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.Create(parentDB, sessiondb.SessionMeta{
		ID: "parent", ProjectPath: root, ProjectName: "project", Model: "main-model", Provider: "openai", AccountID: "test-account", CreatedAt: time.Now().UnixNano(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.InsertSubAgentResult(parentDB, sessiondb.SubAgentResultRow{
		ParentSessionID: "parent", ChildSessionID: "child", AgentID: "child",
		AgentName: "full-child-review-name", AgentCategory: "researcher", Status: "completed",
		PromptTokens: 100, CompletionTokens: 25, DurationMs: 1500, CreatedAt: time.Now().UnixNano(),
	}); err != nil {
		t.Fatal(err)
	}
	for _, record := range []sessiondb.UsageRecord{
		{RequestID: "root/1/1", SessionID: "parent", AgentID: "root", PromptTokens: 10, CompletionTokens: 4, CachedTokens: 2, CacheReadKnown: true, CostUSD: 0.01, CostKnown: true, CreatedAt: 1, UpdatedAt: 1},
		{RequestID: "child/1/1", SessionID: "parent", AgentID: "child", ParentAgentID: "root", PromptTokens: 100, CompletionTokens: 25, CostUSD: 0.05, CostKnown: true, CreatedAt: 2, UpdatedAt: 2},
	} {
		if err := sessiondb.UpsertUsageRecord(parentDB, record); err != nil {
			t.Fatal(err)
		}
	}
	if err := parentDB.Close(); err != nil {
		t.Fatal(err)
	}

	childDir := filepath.Join(sessionsRoot, "parent", "subagents")
	if err := os.MkdirAll(childDir, 0o700); err != nil {
		t.Fatal(err)
	}
	childDB, err := sessiondb.OpenAndMigrate(filepath.Join(childDir, "child.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.Create(childDB, sessiondb.SessionMeta{
		ID: "child", ProjectPath: root, ProjectName: "project", Model: "child-model",
		ParentSessionID: sql.NullString{String: "parent", Valid: true}, CreatedAt: time.Now().UnixNano(),
	}); err != nil {
		t.Fatal(err)
	}
	messages := []provider.ChatMessage{
		provider.NewTextMessage("user", "# Assignment\n- Description: preserve me"),
		provider.NewTextMessage("assistant", "Completed report."),
	}
	if _, err := sessiondb.ReplaceMessagesWithMeta(childDB, "child", messages, []int{1, 1}, []sessiondb.DisplayMeta{{DisplayRole: "assignment"}, {DisplayRole: "assistant"}}); err != nil {
		t.Fatal(err)
	}
	if err := childDB.Close(); err != nil {
		t.Fatal(err)
	}

	agents := session.DefaultAgentsConfig()
	cfg := &session.Config{ActiveProvider: "openai", Accounts: map[string]provider.Account{"test-account": {ID: "test-account", ProviderID: "openai", Label: "Test", APIKey: "test"}}, Agents: agents}
	cfg.Providers = map[string]session.ProviderConfig{
		"openai": {Name: "OpenAI", ProviderURL: "http://127.0.0.1", APIStandard: "openai-chat"},
	}
	profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	main := profile.Categories[session.CategoryDefault]
	main.AccountID = "test-account"
	main.Model = "main-model"
	profile.Categories[session.CategoryDefault] = main
	cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile

	backend, err := session.ResumeSessionWithDir(parentDBPath, cfg, root, sessionsRoot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(backend.Close)
	frontend := NewSession("parent", backend.SessionDir(), backend.DB(), nopLogger{})
	frontend.attachBackend(backend)
	manager := NewAgentManager()
	panes := NewPaneManager()
	ws := &Workspace{
		RootPath: root, AgentMgr: manager, PaneMgr: panes, ActiveSession: frontend,
		SessionsByAgent: map[string]*Session{"root": frontend}, Logger: nopLogger{},
	}
	manager.AddOrchestrator("root", NewAgentState("root", NewConversation(), nopLogger{}))
	ws.Hydrate()
	if usage, ok := ws.UsageSummary("root", types.UsageSession); !ok || usage.InputTotal.Value != 110 || usage.Output.Value != 29 || usage.CacheRead.Value != 2 {
		t.Fatalf("hydrated request usage = %#v ok=%v", usage, ok)
	}

	ids := ws.HydrateCompletedSubAgents("root")
	if len(ids) != 1 || ids[0] != "child" {
		t.Fatalf("hydrated IDs = %v", ids)
	}
	child := manager.Get("child")
	pane := panes.Get("child")
	if child == nil || pane == nil || !child.Hidden || !child.IsCompleted() || child.Label != "full-child-review-name" || child.Model != "child-model" {
		t.Fatalf("hydrated child = %#v pane=%#v", child, pane)
	}
	views := pane.Messages.Messages()
	if len(views) != 2 || views[0].Role != MessageRoleAssignment || views[0].Content != "# Assignment\n- Description: preserve me" {
		t.Fatalf("hydrated child transcript = %+v", views)
	}
}
