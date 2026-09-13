package workspace

import (
	"errors"
	"testing"

	"crude/session"
	ttypes "crude/types"
)

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
	cfg := &session.Config{
		ActiveProvider: "openai",
		Providers: map[string]session.ProviderConfig{
			"openai": {APIKey: "test", ProviderURL: "http://127.0.0.1", APIStandard: "openai-chat", Model: "model"},
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
