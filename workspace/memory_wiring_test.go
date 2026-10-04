package workspace

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"crude/memory"
	"crude/provider"
	"crude/session"
	"crude/sessiondb"
	ttypes "crude/types"
)

func TestCurrentWorkspaceReopensWithSessionsMemoryAndCapabilities(t *testing.T) {
	setTestHome(t)
	project, home := t.TempDir(), t.TempDir()
	wdb, err := OpenWorkspaceDB(project)
	if err != nil {
		t.Fatal(err)
	}
	const id = "existing-session"
	if err := wdb.RegisterSession(id, project, filepath.Base(project), "existing-model", "openai"); err != nil {
		t.Fatal(err)
	}
	if _, err := wdb.Memory.Remember(context.Background(), memory.Record{ID: "existing-memory", Title: "Workspace knowledge", Content: "Retain this knowledge.", SourceSessionID: id}); err != nil {
		t.Fatal(err)
	}
	if err := wdb.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := SessionDir(project, id); err != nil {
		t.Fatal(err)
	}
	path, err := SessionDBPath(project, id)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sessiondb.OpenAndMigrate(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.Create(db, sessiondb.SessionMeta{ID: id, ProjectPath: project, ProjectName: filepath.Base(project), CreatedAt: 100,
		Provider: "openai", Model: "existing-model", AccountID: "work"}); err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.SaveAgentSettingOverrides(db, id, map[string]sessiondb.AgentSettingOverride{"default": {AccountID: "work", Model: "existing-model"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	ws := NewWorkspace(project, "existing-model", "user", ttypes.Settings{}, nopLogger{})
	t.Cleanup(ws.Close)
	if !ws.MemoryAvailable() {
		t.Fatalf("existing workspace lost its database handle: %v", ws.workspaceDBErr)
	}
	if listed := ws.ListSessions(); len(listed) != 1 || listed[0].ID != id {
		t.Fatalf("existing sessions disappeared from launcher: %+v", listed)
	}
	ctx := context.Background()
	if records, err := ws.SearchMemories(ctx, memory.ListQuery{}); err != nil || len(records) != 1 || records[0].ID != "existing-memory" || records[0].Content != "Retain this knowledge." {
		t.Fatalf("existing memory browser unavailable: %+v, %v", records, err)
	}
	if _, err := ws.CapabilitySnapshot(ctx); err != nil {
		t.Fatalf("capability settings unavailable after recovery: %v", err)
	}
	if _, err := ws.BoardSnapshot(ctx, "", "", 0, 0); err != nil {
		t.Fatalf("board unavailable after recovery: %v", err)
	}
	cfg := &session.Config{ActiveProvider: "openai", Agents: session.DefaultAgentsConfig(),
		Accounts:  map[string]provider.Account{"work": {ID: "work", ProviderID: "openai", APIKey: "test"}},
		Providers: map[string]session.ProviderConfig{"openai": {Name: "OpenAI", ProviderURL: "http://127.0.0.1:1", APIStandard: "openai"}},
	}
	frontend, err := ws.ResumeSession(id, cfg, home)
	if err != nil {
		t.Fatalf("existing session cannot resume: %v", err)
	}
	if frontend.backend.MemoryStore() != ws.WorkspaceDB.Memory || frontend.backend.SettingsSnapshot().AccountID != "work" {
		t.Fatal("resume lost workspace memory or existing account pin")
	}
}

func TestCreateSessionRecoversWorkspaceMemoryAfterInitialDBFailure(t *testing.T) {
	setTestHome(t)
	project := t.TempDir()
	ws := NewWorkspace(project, "model", "user", ttypes.Settings{}, nopLogger{})
	if ws.registryCostWriter != nil {
		ws.registryCostWriter.Close()
		ws.registryCostWriter = nil
	}
	if ws.Status != nil {
		ws.Status.Close()
	}
	ws.Status = NewStatusCenter(nil, nopLogger{})
	if ws.WorkspaceDB != nil {
		_ = ws.WorkspaceDB.Close()
	}
	ws.WorkspaceDB = nil
	ws.workspaceDBErr = errors.New("simulated workspace open failure")

	agents := session.DefaultAgentsConfig()
	profile := agents.Profiles[agents.ActiveProfile]
	main := profile.Categories[session.CategoryDefault]
	main.AccountID, main.Model = "test-account", "model"
	profile.Categories[session.CategoryDefault] = main
	agents.Profiles[agents.ActiveProfile] = profile
	cfg := &session.Config{
		ActiveProvider: "openai",
		Accounts:       map[string]provider.Account{"test-account": {ID: "test-account", ProviderID: "openai", Label: "Test", APIKey: "test"}},
		Providers: map[string]session.ProviderConfig{
			"openai": {Name: "OpenAI", ProviderURL: "http://127.0.0.1", APIStandard: "openai-chat"},
		},
		Agents: agents,
	}
	frontend, err := ws.CreateSession(cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ws.Close)
	if ws.WorkspaceDB == nil || ws.WorkspaceDB.Memory == nil || !ws.MemoryAvailable() {
		t.Fatal("workspace memory was not recovered before session creation")
	}
	if frontend == nil || frontend.backend == nil || frontend.backend.MemoryStore() != ws.WorkspaceDB.Memory {
		t.Fatal("recovered workspace memory was not wired into backend session")
	}
	var registered int
	if err := ws.WorkspaceDB.DB.QueryRow(`SELECT count(*) FROM workspace_sessions WHERE session_id = ?`, frontend.SessionID).Scan(&registered); err != nil || registered != 1 {
		t.Fatalf("recovered session registry count=%d err=%v", registered, err)
	}
}

func TestNewSessionFromLauncherReturnsVisibleFailure(t *testing.T) {
	ws := &Workspace{Logger: nopLogger{}}
	result := ws.NewSessionFromLauncher()
	if result == nil || result.Error == "" {
		t.Fatalf("launcher result=%+v want visible failure", result)
	}
}
