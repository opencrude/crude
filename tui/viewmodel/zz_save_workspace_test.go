package viewmodel

import (
	"testing"

	sessionsettings "crude/tui/screens/sessionsettings"
	ttypes "crude/tui/types"
	"crude/types"
	"crude/workspace"
)

// TestSaveAsWorkspacePromotesAgentSettings verifies the "Save as workspace"
// action writes the current agent settings into the workspace overrides so
// future agents inherit them.
func TestSaveAsWorkspacePromotesAgentSettings(t *testing.T) {
	app := newTestApp(t)
	app.Logger = noopLogger{}
	wdb, err := workspace.OpenWorkspaceDB(t.TempDir())
	if err != nil {
		t.Fatalf("OpenWorkspaceDB: %v", err)
	}
	t.Cleanup(func() { _ = wdb.Close() })
	app.Workspace.WorkspaceDB = wdb
	app.Workspace.SetAppDefaults(ttypes.AppSettings{
		ActiveProvider: "openrouter",
		Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
			"default": {Provider: "openrouter", Model: "old-model"},
		}},
	})

	// Build a session settings screen with a new model for the main agent.
	screen := sessionsettings.New(app.Theme, types.Settings{
		ProviderID: "openai",
		Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
			"default": {Provider: "openai", Model: "gpt-5.6", ThinkingEffort: "high"},
		}},
	}, types.Settings{}, nil)

	_, cmd := app.Update(sessionsettings.SaveAsWorkspaceMsg{
		Target:   screen,
		Settings: screen.ToSettings(),
	})
	if cmd != nil {
		t.Fatalf("unexpected cmd: %v", cmd)
	}

	// The workspace overrides must now carry the promoted agent settings.
	overrides := app.LoadWorkspaceOverrides()
	if len(overrides.Agents) == 0 {
		t.Fatal("workspace overrides have no agents after save-as-workspace")
	}
	effective := app.LoadAppSettings()
	got := effective.Agents.Categories["default"]
	if got.Provider != "openai" || got.Model != "gpt-5.6" {
		t.Errorf("default agent after save-as-workspace = %+v, want openai/gpt-5.6", got)
	}
}
