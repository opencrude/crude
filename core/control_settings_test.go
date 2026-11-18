package core

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"crude/core/protocol"
	"crude/types"
	"crude/types/control"
	"crude/workspace"
)

func TestControlSettingsApplicationCaptureCommitApply(t *testing.T) {
	ws := workspace.NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, coreTestLogger{})
	defer ws.Close()
	before := types.AppSettings{Editor: types.EditorSettings{WordWrap: true, TabSize: 4}}
	ws.SetAppDefaults(before)
	commits := 0
	ws.OnAppPreferencesCommit = func(edits []types.SettingEdit) (types.AppSettings, error) {
		next := ws.AppDefaults()
		if err := types.ApplySettingsEdits(&next, edits); err != nil {
			return next, err
		}
		commits++
		if next.Editor.WordWrap || next.Editor.TabSize != 4 {
			t.Fatal("not a single-field change")
		}
		return next, nil
	}
	a := &actor{ws: ws}
	raw, _ := json.Marshal(map[string]any{"scope": "application", "path": "editor.word_wrap", "value": false})
	in := control.Request{ID: "patch", CommandID: "settings.set", Input: raw}
	run, apply, handled := a.prepareControlDomain(in, coreprotocol.Owner{ClientID: "trusted", Epoch: 1})
	if !handled || commits != 0 {
		t.Fatal("prepare performed persistence")
	}
	got := run(context.Background())
	if got.State != control.Committed || got.OperationID != in.ID || commits != 1 {
		t.Fatalf("not committed: %#v", got)
	}
	if !ws.AppDefaults().Editor.WordWrap {
		t.Fatal("worker installed live projection")
	}
	apply(got)
	if ws.AppDefaults().Editor.WordWrap {
		t.Fatal("actor did not publish committed value")
	}
	apply(got)
	run(context.Background())
	if commits != 1 {
		t.Fatal("replayed mutation")
	}
	again, _, _ := a.prepareControlDomain(in, coreprotocol.Owner{ClientID: "trusted", Epoch: 1})
	if out := again(context.Background()); out.Fault != nil {
		t.Fatal("explicit set required a revision", out.Fault)
	}
}

func TestAeronCanConfigureLSPWithoutGivingOtherAgentsAuthority(t *testing.T) {
	ws := workspace.NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, coreTestLogger{})
	t.Cleanup(ws.Close)
	ws.SessionsByAgent["aeron"] = &workspace.Session{AgentID: "aeron",
		Settings: &workspace.SessionSettingsState{Effective: types.Settings{SessionMainAgent: "aeron"}}}
	ws.SessionsByAgent["main"] = &workspace.Session{AgentID: "main",
		Settings: &workspace.SessionSettingsState{Effective: types.Settings{}}}
	a := &actor{ws: ws}
	command := filepath.Join(t.TempDir(), "language-server.exe")
	for _, agentID := range []string{"main", "aeron"} {
		raw, _ := json.Marshal(map[string]any{"scope": "workspace", "path": "lsp.languages.go.command", "value": command})
		in := control.Request{ID: "lsp-" + agentID, CommandID: "settings.set", Input: raw,
			Caller: control.Caller{Kind: control.AgentCaller, AgentID: agentID}}
		desc, err := a.staticControlDescriptor(in)
		if err != nil || desc.HumanOnly != (agentID != "aeron") {
			t.Fatalf("%s descriptor has the wrong authority: %+v, %v", agentID, desc, err)
		}
		run, apply, handled := a.prepareControlDomain(in, coreprotocol.Owner{ClientID: "trusted", Epoch: 1})
		if !handled {
			t.Fatal("settings mutation was not routed")
		}
		out := run(t.Context())
		if agentID == "main" {
			if out.Fault == nil || out.Fault.Code != control.Denied {
				t.Fatalf("ordinary agent changed the server launch command: %+v", out)
			}
			continue
		}
		if out.State != control.Committed || out.Fault != nil {
			t.Fatalf("Aeron could not configure its installed server: %+v", out)
		}
		if apply != nil {
			apply(out)
		}
		if got := ws.LoadAppSettings().LSP.Languages["go"].Command; got != command {
			t.Fatalf("Aeron's launch command did not reach live settings: %q", got)
		}
	}
	// The exception does not include unrelated authority.
	if a.aeronOwnsSetting(control.Caller{Kind: control.AgentCaller, AgentID: "aeron"}, "workspace", "safety.confirm_at") {
		t.Fatal("language-server setup granted unrelated authority")
	}
}

func TestControlSettingsRejectAuthorityAndInvalidLeaves(t *testing.T) {
	ws := workspace.NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, coreTestLogger{})
	defer ws.Close()
	a := &actor{ws: ws}
	for _, path := range []string{"accounts.one", "network.proxy_url", "agents", "safety.confirm_at", "capabilities", "terminal.environment", "value.APIKey"} {
		raw, _ := json.Marshal(map[string]any{"scope": "workspace", "path": path, "value": false})
		run, _, _ := a.prepareControlDomain(control.Request{ID: "denied", CommandID: "settings.set", Input: raw}, coreprotocol.Owner{ClientID: "trusted", Epoch: 1})
		if out := run(context.Background()); out.Fault == nil || out.Fault.Code != control.Denied {
			t.Fatalf("authority %s accepted: %#v", path, out)
		}
	}
	for _, raw := range []string{`"false"`, `null`, `{"unknown":true}`} {
		value := types.AppSettings{}
		if err := controlSetField(&value, "editor.word_wrap", json.RawMessage(raw)); err == nil {
			t.Fatalf("invalid bool accepted: %s", raw)
		}
	}
	value := types.AppSettings{}
	if err := controlSetField(&value, "editor.WordWrap", json.RawMessage(`false`)); err == nil {
		t.Fatal("case alias accepted")
	}
}

func TestControlSessionRootPinResetPreservesOtherRoleFields(t *testing.T) {
	a := controlExecutionFixture(t)
	var sessionID string
	for _, front := range a.ws.Sessions {
		sessionID = front.SessionID
		break
	}
	backend := a.ws.BackendSession(sessionID)
	initial := backend.SettingsSnapshots()
	value := initial.Effective
	value.SessionMainAgent = "crafter"
	value.SessionOverrides = map[string]types.CategorySettings{
		"default":      {AccountID: "one", Model: "neighbor"},
		"root/crafter": {AccountID: "one", Model: "pinned", ThinkingEffort: "high"},
	}
	if _, err := backend.SaveSettingsSnapshot(value); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"Model", "ThinkingEffort", "account_id"} {
		raw, _ := json.Marshal(map[string]any{"scope": "session", "path": "value." + field})
		run, apply, _ := a.prepareControlDomain(control.Request{ID: "reset-" + field, CommandID: "settings.reset", Target: control.Target{SessionID: sessionID}, Input: raw}, coreprotocol.Owner{ClientID: "trusted", Epoch: 1})
		out := run(context.Background())
		if out.Fault != nil {
			t.Fatal(field, out.Fault)
		}
		if apply != nil {
			apply(out)
		}
		snap := backend.SettingsSnapshots()
		pin := snap.Overrides["root/crafter"]
		if snap.Effective.SessionMainAgent != "crafter" || snap.Overrides["default"].Model != "neighbor" {
			t.Fatal("reset switched root or lost neighboring role")
		}
		if field == "Model" && (pin.Model != "" || pin.AccountID != "one" || pin.ThinkingEffort != "high") {
			t.Fatal("model reset not sparse", pin)
		}
		if field == "ThinkingEffort" && (pin.ThinkingEffort != "" || pin.AccountID != "one") {
			t.Fatal("thinking reset cleared connection", pin)
		}
		if field == "account_id" && pin.AccountID != "" {
			t.Fatal("account reset retained pin", pin)
		}
	}
}

// Agents may turn code intelligence on for their session through settings.set,
// and the change reaches the live session; reset returns it to inheritance.
func TestControlSessionCodeIntelligenceSetAndReset(t *testing.T) {
	a := controlExecutionFixture(t)
	var sessionID string
	for _, front := range a.ws.Sessions {
		sessionID = front.SessionID
		break
	}
	backend := a.ws.BackendSession(sessionID)
	call := func(command string, input map[string]any) control.Result {
		t.Helper()
		input["scope"], input["path"] = "session", "code_intelligence"
		raw, _ := json.Marshal(input)
		run, apply, _ := a.prepareControlDomain(control.Request{ID: command, CommandID: command, Target: control.Target{SessionID: sessionID}, Input: raw}, coreprotocol.Owner{ClientID: "trusted", Epoch: 1})
		out := run(context.Background())
		if apply != nil {
			apply(out)
		}
		return out
	}
	if out := call("settings.set", map[string]any{"value": true}); out.Fault != nil {
		t.Fatal(out.Fault)
	}
	if got := backend.SettingsSnapshots().Effective.SessionCodeIntelligence; got == nil || !*got {
		t.Fatalf("session override after set = %v", got)
	}
	if out := call("settings.reset", map[string]any{}); out.Fault != nil {
		t.Fatal(out.Fault)
	}
	if got := backend.SettingsSnapshots().Effective.SessionCodeIntelligence; got != nil {
		t.Fatalf("session override after reset = %v", *got)
	}
	raw, _ := json.Marshal(map[string]any{"scope": "workspace", "path": "lsp.languages.go.command", "value": "/tmp/evil"})
	run, _, _ := a.prepareControlDomain(control.Request{ID: "server", CommandID: "settings.set", Input: raw}, coreprotocol.Owner{ClientID: "trusted", Epoch: 1})
	if out := run(context.Background()); out.Fault == nil || out.Fault.Code != control.Denied {
		t.Fatalf("server command change accepted: %#v", out)
	}
}

func TestAeronOwnsSettingOnlyAtWorkspaceScope(t *testing.T) {
	ws := workspace.NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, coreTestLogger{})
	t.Cleanup(ws.Close)
	ws.SetAppDefaults(types.AppSettings{Terminal: types.TerminalSettings{Environment: map[string]string{"PATH": "app-value"}}})
	ws.SessionsByAgent["aeron"] = &workspace.Session{AgentID: "aeron",
		Settings: &workspace.SessionSettingsState{Effective: types.Settings{SessionMainAgent: "aeron"}}}
	ws.SessionsByAgent["main"] = &workspace.Session{AgentID: "main",
		Settings: &workspace.SessionSettingsState{Effective: types.Settings{}}}
	a := &actor{ws: ws}
	for _, scope := range []string{"workspace", "application", "session", ""} {
		for _, caller := range []control.Caller{
			{Kind: control.AgentCaller, AgentID: "aeron"},
			{Kind: control.AgentCaller, AgentID: "main"},
			{Kind: control.ExternalCaller, AgentID: "aeron"},
		} {
			for _, path := range []string{"lsp.languages.test.command", "terminal.environment", "terminal.environment.PATH", "terminal.environment_other", "terminal.command", "safety.confirm_at", "lsp.ask_aeron"} {
				eligible := path == "lsp.languages.test.command" || path == "terminal.environment" || path == "terminal.environment.PATH"
				want := eligible && scope == "workspace" && caller.Kind == control.AgentCaller && caller.AgentID == "aeron"
				if got := a.aeronOwnsSetting(caller, scope, path); got != want {
					t.Fatalf("caller=%+v scope=%q path=%q: allowed=%v, want %v", caller, scope, path, got, want)
				}
			}
		}
	}
	for _, scope := range []string{"workspace", "application"} {
		for _, agent := range []string{"main", "aeron"} {
			caller := control.Caller{Kind: control.AgentCaller, AgentID: agent}
			allowed := scope == "workspace" && agent == "aeron"
			for _, path := range []string{"terminal.environment", "terminal.environment.PATH", "lsp.languages.test.command", "lsp.ask_aeron"} {
				writeAllowed := allowed && path != "lsp.ask_aeron"
				value := any("${workspaceFolder}/.venv/bin:${env:PATH}")
				switch path {
				case "terminal.environment":
					value = map[string]string{"PATH": value.(string), "VIRTUAL_ENV": "${workspaceFolder}/.venv"}
				case "lsp.languages.test.command":
					value = filepath.Join(t.TempDir(), "test-server")
				}
				raw, _ := json.Marshal(map[string]any{"scope": scope, "path": path, "value": value})
				request := control.Request{ID: scope + agent + path, CommandID: "settings.set", Caller: caller, Input: raw}
				desc, err := a.staticControlDescriptor(request)
				if err != nil || desc.HumanOnly == writeAllowed {
					t.Fatalf("descriptor scope=%s agent=%s path=%s: %+v, %v", scope, agent, path, desc, err)
				}
				run, apply, handled := a.prepareControlSettings(request, coreprotocol.Owner{ClientID: "test", Epoch: 1})
				if !handled {
					t.Fatal("settings command was not handled")
				}
				out := run(t.Context())
				if !writeAllowed {
					if out.Fault == nil || out.Fault.Code != control.Denied {
						t.Fatalf("human-only write accepted: scope=%s agent=%s path=%s: %+v", scope, agent, path, out)
					}
					continue
				}
				if out.Fault != nil || out.State != control.Committed {
					t.Fatalf("workspace Aeron write failed: %s: %+v", path, out)
				}
				if apply != nil {
					apply(out)
				}
			}
			raw, _ := json.Marshal(map[string]any{"scope": scope, "prefix": "terminal.environment", "limit": 128})
			run, _, _ := a.prepareControlSettingsFields(control.Request{ID: "fields", Caller: caller, Input: raw})
			out := run(t.Context())
			var page controlSettingsFieldsPage
			if out.Fault != nil || json.Unmarshal(out.Output, &page) != nil || len(page.Fields) == 0 {
				t.Fatalf("no environment field metadata: %+v", out)
			}
			for _, field := range page.Fields {
				if field.HumanOnly == allowed || !allowed && len(field.Value) != 0 {
					t.Fatalf("field authority disagrees with writes: %+v", field)
				}
			}
		}
	}
	want := map[string]string{"PATH": "${workspaceFolder}/.venv/bin:${env:PATH}", "VIRTUAL_ENV": "${workspaceFolder}/.venv"}
	if got := ws.TermMgr.Settings().Environment; !reflect.DeepEqual(got, want) {
		t.Fatalf("terminal launch settings did not receive Aeron's environment: %+v", got)
	}
	if got := ws.AppDefaults().Terminal.Environment["PATH"]; got != "app-value" {
		t.Fatalf("workspace environment changed application defaults: %q", got)
	}
}

func TestControlTerminalLoginAllowsAuto(t *testing.T) {
	schema := controlFieldSchema(reflect.TypeFor[string](), "terminal.login_shell")
	if !reflect.DeepEqual(schema["enum"], []string{"", "on", "off"}) {
		t.Fatalf("login choices lost Auto: %+v", schema)
	}
}
