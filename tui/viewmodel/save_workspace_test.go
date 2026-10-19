package viewmodel

import (
	"encoding/json"
	"testing"

	"crude/coreprotocol"
	sessionsettings "crude/tui/screens/sessionsettings"
	"crude/tui/ui"
	"crude/types"
	"crude/types/control"
	"crude/workspace"
)

// TestSaveAsWorkspacePromotesAgentSettings verifies the "Save as workspace"
// action writes the current agent settings into the workspace overrides so
// future agents inherit them.
func TestSaveAsWorkspacePromotesAgentSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	app := newTestApp(t)
	wsOf(app).Logger = noopLogger{}
	wdb, err := workspace.OpenWorkspaceDB(t.TempDir())
	if err != nil {
		t.Fatalf("OpenWorkspaceDB: %v", err)
	}
	t.Cleanup(func() { _ = wdb.Close() })
	wsOf(app).WorkspaceDB = wdb
	wsOf(app).SetAppDefaults(types.AppSettings{

		Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
			"default": {Provider: "openrouter", Model: "old-model"},
		}},
	})
	if err := wsOf(app).SaveWorkspaceSettings(types.WorkspaceOverrides{Editor: json.RawMessage(`{"tab_size":8}`), LSP: json.RawMessage(`{"enabled":false}`)}); err != nil {
		t.Fatal(err)
	}

	// Build a session settings screen with a new model for the main agent.
	screen := sessionsettings.New(app.Theme, types.Settings{
		ProviderID: "openai",
		Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
			"default": {Provider: "openai", Model: "gpt-5.6", ThinkingEffort: "high"},
		}},
	}, types.Settings{}, map[string]types.CategorySettings{"default": {Model: "gpt-5.6", ThinkingEffort: "high"}})
	owner := &workspace.Session{SessionID: "promotion-owner"}
	app.sessionProjections = newSessionProjectionRegistry(app.Theme)
	app.sessionProjections.items[owner] = &sessionProjection{settings: screen}

	_, cmd := app.Update(sessionsettings.SaveAsWorkspaceMsg{
		Target:   screen,
		Settings: screen.ToSettings(),
	})
	runAppCommand(t, app, cmd)

	// The workspace overrides must now carry the promoted agent settings.
	overrides := app.ws.LoadWorkspaceOverrides()
	if len(overrides.Agents) == 0 {
		t.Fatal("workspace overrides have no agents after save-as-workspace")
	}
	effective := app.ws.LoadAppSettings()
	got := effective.Agents.Categories["default"]
	if got.Model != "gpt-5.6" {
		t.Errorf("default agent after save-as-workspace = %+v, want openai/gpt-5.6", got)
	}
	if string(overrides.Editor) != `{"tab_size":8}` || string(overrides.LSP) != `{"enabled":false}` {
		t.Fatal("promotion erased neighboring workspace settings")
	}
}

func TestSaveAsWorkspaceIncludesExplicitMemoryAndLimitOnly(t *testing.T) {
	limit := 2
	enabled := false
	code := false
	stored := types.WorkspaceOverrides{Agents: json.RawMessage(`{"agent_profiles":{"kept":{"model":"custom"}}}`), Editor: json.RawMessage(`{"tab_size":8}`)}
	settings := types.Settings{SessionMultiAgent: &enabled, SessionMaxSubAgents: &limit, SessionCodeIntelligence: &code, SessionOverrides: map[string]types.CategorySettings{"memory": {Model: "memory-only"}}}
	if err := types.ApplySettingsEdits(&stored, promotedSessionEdits(settings)); err != nil {
		t.Fatal(err)
	}
	effective := types.MergeAppWorkspace(types.AppSettings{}, stored)
	if effective.Agents.Enabled || effective.Agents.MaxConcurrent != 2 || effective.Memory.MemoryCategory.Model != "memory-only" || effective.Agents.AgentProfiles["kept"].Model != "custom" || len(stored.LSP) != 0 || string(stored.Editor) != `{"tab_size":8}` {
		t.Fatal("promotion copied session-only state or erased neighbors")
	}
}

func TestConnectedSessionPromotionKeepsStagedChoicesUntilSaved(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	one := newRemoteJourneyShell(t, runtime, t.TempDir())
	remoteJourneyCreate(t, one)
	app := one.app
	app.openSessionSettings()
	screen := app.sessionSettings()
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "session-settings.role.open", Payload: "default"})
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "form.changed"})
	// Drive the same retained control as the native role editor.
	in, d, node := invokeDeckControl(t, one, "session-settings/form/thinking-effort", ui.ControlOperation{Verb: "select", ItemID: "high"})
	result := requestShellForTest(t, runtime, []remoteJourneyShell{one}, one, "shell.control", in, func(e *coreprotocol.ControlEnvelope) {
		e.Descriptor = d
		e.Descriptor.ID = "shell.control"
		e.Request.Target.ResourceID = in.ComponentID
		e.Request.Target.SessionID = node.Target.SessionID
		e.Request.Preconditions = control.Preconditions{Revision: &node.Revision, OpenEpoch: &node.OpenEpoch}
	})
	if result.State != control.Applied {
		t.Fatal(result)
	}
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "session-settings.role.apply"})
	_, cmd := app.Update(sessionsettings.SaveAsWorkspaceMsg{Target: screen, Settings: screen.ToSettings()})
	runAppCommand(t, app, cmd)
	if screen.ToSettings().SessionOverrides["default"].ThinkingEffort != "high" {
		t.Fatal("promotion consumed the source session draft")
	}
	_, cmd = app.Update(sessionsettings.SaveMsg{Target: screen, Settings: screen.ToSettings()})
	runAppCommand(t, app, cmd)
	app.openSessionSettings()
	if app.sessionSettings().ToSettings().SessionOverrides["default"].ThinkingEffort != "high" {
		t.Fatal("session Save after promotion lost explicit choice")
	}
}
