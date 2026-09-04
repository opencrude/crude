package workspace

import (
	"path/filepath"
	"testing"

	"crude/session"
	ttypes "crude/types"
)

func TestWorkspaceSwitchRebindsBackendToolRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	rootA := t.TempDir()
	rootB := t.TempDir()
	cfg := workspaceToolRootConfig()
	ws := NewWorkspace(rootA, "root-model", "tester", ttypes.Settings{}, &testLogger{})
	t.Cleanup(ws.Close)
	ws.SetBackendConfig(cfg, home)

	first, err := ws.CreateSession(cfg, home)
	if err != nil {
		t.Fatal(err)
	}
	if got := first.backend.WorkDir(); got != rootA {
		t.Fatalf("first backend workDir = %q, want %q", got, rootA)
	}

	if err := ws.SwitchProject(rootB); err != nil {
		t.Fatal(err)
	}
	second, err := ws.CreateSession(cfg, home)
	if err != nil {
		t.Fatal(err)
	}
	if got := second.backend.WorkDir(); got != rootB {
		t.Fatalf("switched backend workDir = %q, want %q", got, rootB)
	}
	if second.SessionDir != filepath.Join(ws.WorkspaceDir, "sessions", second.SessionID) {
		t.Fatalf("switched session dir = %q, workspace=%q", second.SessionDir, ws.WorkspaceDir)
	}
}

func workspaceToolRootConfig() *session.Config {
	agents := session.DefaultAgentsConfig()
	return &session.Config{
		ActiveProvider: "openai",
		Providers: map[string]session.ProviderConfig{
			"openai": {
				APIKey: "test", ProviderURL: "http://127.0.0.1", APIStandard: "openai", Model: "root-model",
			},
		},
		Agents: agents,
	}
}
