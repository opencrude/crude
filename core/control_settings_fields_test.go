package core

import (
	"context"
	"crude/coreprotocol"
	"crude/provider"
	"crude/session"
	"crude/types"
	"crude/types/control"
	"crude/workspace"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestControlSessionExactRoleModelPersists(t *testing.T) {
	setCoreTestHome(t, t.TempDir())
	ws := workspace.NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, coreTestLogger{})
	t.Cleanup(ws.Close)
	cfg := &session.Config{Accounts: map[string]provider.Account{"one": {ID: "one", ProviderID: "openai", APIKey: "retained-secret", Connection: provider.Template{Name: "test", AuthType: provider.AuthBearer, APIStandard: "openai", ProviderURL: "https://example.com"}}}, Agents: session.AgentsConfig{ActiveProfile: "default", Profiles: map[string]session.ProfileConfig{"default": {Categories: map[string]session.CategoryConfig{"default": {AccountID: "one", Model: "initial"}, "crafter": {AccountID: "one", Model: "crafter-old"}}}}}}
	owner, err := ws.CreateSession(cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	backend := ws.BackendSession(owner.SessionID)
	a := &actor{ws: ws}
	snap := backend.SettingsSnapshots()
	raw, _ := json.Marshal(map[string]any{"scope": "session", "path": "value.Agents.Categories.crafter.Model", "value": "crafter-new", "session_revision": snap.Revision})
	in := control.Request{ID: "role-model", CommandID: "settings.set", Target: control.Target{SessionID: owner.SessionID}, Input: raw}
	run, apply, _ := a.prepareControlDomain(in, coreprotocol.Owner{ClientID: "test", Epoch: 1})
	out := run(context.Background())
	if out.Fault != nil {
		t.Fatal(out.Fault)
	}
	apply(out)
	next := backend.SettingsSnapshots()
	if next.Effective.Agents.Categories["crafter"].Model != "crafter-new" || next.Effective.Agents.Categories["default"].Model != "initial" || next.Overrides["crafter"].AccountID != "one" {
		t.Fatal("wrong role or lost account", next)
	}
	if strings.Contains(string(out.Output), "retained-secret") {
		t.Fatal("secret leaked")
	}
	run, _, _ = a.prepareControlDomain(in, coreprotocol.Owner{ClientID: "test", Epoch: 1})
	if got := run(context.Background()); got.Fault == nil || got.Fault.Code != control.Stale {
		t.Fatal("stale session accepted")
	}
}

func TestControlFieldsProgressiveRedactedExact(t *testing.T) {
	ws := workspace.NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, coreTestLogger{})
	t.Cleanup(ws.Close)
	app := types.AppSettings{}
	types.ApplyAppDefaults(&app)
	app.Agents.Categories = types.DefaultAgentRoleCategories()
	app.Agents.AgentProfiles = map[string]types.AgentProfileSettings{}
	for i := 0; i < 25; i++ {
		app.Agents.AgentProfiles[fmt.Sprintf("profile-%02d", i)] = types.AgentProfileSettings{Model: "model", Instructions: "lower-priority overlay"}
	}
	ws.SetAppDefaults(app)
	a := &actor{ws: ws}
	cursor := ""
	seen := map[string]controlSettingsField{}
	for pages := 0; pages < 30; pages++ {
		raw, _ := json.Marshal(controlSettingsFieldsInput{Scope: "workspace", Limit: 128, Cursor: cursor})
		run, _, ok := a.prepareControlDomain(control.Request{ID: "fields", CommandID: "settings.fields", Input: raw}, coreprotocol.Owner{ClientID: "test", Epoch: 1})
		if !ok {
			t.Fatal("not routed")
		}
		out := run(context.Background())
		if out.Fault != nil {
			t.Fatal(out.Fault)
		}
		if len(out.Output) > control.MaxValueBytes {
			t.Fatal("unbounded page")
		}
		var page controlSettingsFieldsPage
		if err := json.Unmarshal(out.Output, &page); err != nil {
			t.Fatal(err)
		}
		for _, field := range page.Fields {
			if _, exists := seen[field.Path]; exists {
				t.Fatal("duplicate cursor field")
			}
			seen[field.Path] = field
		}
		cursor = page.Cursor
		if cursor == "" {
			break
		}
	}
	if cursor != "" || len(seen) <= 128 {
		t.Fatal("inventory did not progress", len(seen))
	}
	for _, path := range []string{"editor.word_wrap", "lsp.languages.go.enabled", "agents.Categories.default.Model", "agents.agent_profiles.profile-01.instructions"} {
		f, ok := seen[path]
		if !ok || f.HumanOnly || f.ReadOnly || !f.Inherited {
			t.Fatalf("ordinary field missing or blocked %s: %#v", path, f)
		}
	}
	if f := seen["network.proxy_url"]; !f.HumanOnly || !f.ReadOnly || len(f.Value) != 0 {
		t.Fatal("trust/secret projection unsafe", f)
	}
	for _, field := range seen {
		if strings.Contains(field.Path, "TerminalShells") {
			t.Fatal("transient metadata exported")
		}
	}
}

func TestControlWorkspaceFalseResetSQLiteAndMap(t *testing.T) {
	setCoreTestHome(t, t.TempDir())
	ws := workspace.NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, coreTestLogger{})
	t.Cleanup(ws.Close)
	app := types.AppSettings{}
	types.ApplyAppDefaults(&app)
	app.Editor.WordWrap = true
	ws.SetAppDefaults(app)
	a := &actor{ws: ws}
	mutate := func(cmd, path string, value any) {
		t.Helper()
		input := map[string]any{"scope": "workspace", "path": path, "expected_revision": entityRevision(ws.LoadWorkspaceOverrides())}
		if cmd == "settings.set" {
			input["value"] = value
		}
		raw, _ := json.Marshal(input)
		run, apply, _ := a.prepareControlDomain(control.Request{ID: cmd + path, CommandID: cmd, Input: raw}, coreprotocol.Owner{ClientID: "test", Epoch: 1})
		out := run(context.Background())
		if out.Fault != nil {
			t.Fatal(out.Fault)
		}
		apply(out)
	}
	mutate("settings.set", "editor.tab_size", 8)
	mutate("settings.set", "editor.word_wrap", false)
	mutate("settings.set", "lsp.languages.go.enabled", false)
	stored := ws.LoadWorkspaceOverrides()
	if !strings.Contains(string(stored.Editor), `"word_wrap":false`) || !strings.Contains(string(stored.LSP), `"enabled":false`) {
		t.Fatal("false not persisted", stored)
	}
	if ws.LoadAppSettings().LSP.Languages["go"].Enabled || !ws.AppDefaults().LSP.Languages["go"].Enabled {
		t.Fatal("map mutation changed application owner")
	}
	mutate("settings.reset", "editor.word_wrap", nil)
	mutate("settings.reset", "lsp.languages.go.enabled", nil)
	if !ws.LoadAppSettings().Editor.WordWrap || ws.LoadAppSettings().Editor.TabSize != 8 || !ws.LoadAppSettings().LSP.Languages["go"].Enabled {
		t.Fatal("reset lost inheritance/neighbors")
	}
}

func TestControlFieldEnumsAndMapAssignment(t *testing.T) {
	app := types.AppSettings{}
	types.ApplyAppDefaults(&app)
	for path, raw := range map[string]string{"editor.syntax_theme": `"bogus"`, "editor.render_whitespace": `"bogus"`, "terminal.placement": `"bogus"`, "theme.preset": `"bogus"`, "ui.context_help": `"bogus"`, "theme.pad_left": `-1`} {
		if err := controlSetField(&app, path, json.RawMessage(raw)); err != nil {
			t.Fatal(err)
		}
		if err := controlValidateField(app, path); err == nil {
			t.Fatal("invalid enum/bound accepted", path)
		}
	}
	if err := controlSetField(&app, "lsp.languages.go.Enabled", json.RawMessage(`false`)); err == nil {
		t.Fatal("case alias accepted")
	}
	if ControlSettingsAuthority("agents.Categories.default.Model") || !ControlSettingsAuthority("agents.Categories.default.AllowedTools") || !ControlSettingsAuthority("agents") {
		t.Fatal("ordinary/policy classification drift")
	}
}
