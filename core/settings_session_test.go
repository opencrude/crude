package core

import (
	"context"
	"testing"

	"crude/coreprotocol"
	"crude/provider"
	"crude/session"
	"crude/types"
	"crude/workspace"
)

func TestSessionSettingsMutationBridge(t *testing.T) {
	setCoreTestHome(t, t.TempDir())
	ws := workspace.NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, coreTestLogger{})
	t.Cleanup(ws.Close)
	cfg := &session.Config{Accounts: map[string]provider.Account{"one": {ID: "one", ProviderID: "openai", APIKey: "secret", Connection: provider.Template{Name: "test", AuthType: provider.AuthBearer, APIStandard: "openai", ProviderURL: "https://example.com"}}}, Agents: session.AgentsConfig{ActiveProfile: "default", Profiles: map[string]session.ProfileConfig{"default": {Categories: map[string]session.CategoryConfig{"default": {AccountID: "one", Model: "initial"}, "crafter": {AccountID: "one", Model: "crafter-model"}}}}}}
	owner, err := ws.CreateSession(cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &actor{ws: ws}
	backend := ws.BackendSession(owner.SessionID)
	initial := backend.SettingsSnapshots()
	disabled := false
	dto := publicSession(initial.Effective)
	dto.MainAgent, dto.MultiAgent = "crafter", &disabled
	dto.Overrides = map[string]types.CategorySettings{"default": {AccountID: "one", Model: "saved"}, "root/crafter": {AccountID: "one", DeniedTools: []string{"execute_command"}}}
	prepare := func(method string) (func(context.Context) coreprotocol.Response, func(coreprotocol.Response)) {
		t.Helper()
		local := types.SessionPreferences{MainAgent: dto.MainAgent, Overrides: dto.Overrides, MultiAgent: dto.MultiAgent}
		raw, err := coreprotocol.Marshal(coreprotocol.Ptr(coreprotocol.SessionSettingsMutationRequest{SessionID: owner.SessionID, Edits: types.SettingsEdits(types.SessionPreferencesOf(initial.Effective), local)}))
		if err != nil {
			t.Fatal(err)
		}
		run, apply, known := a.prepareSessionSettings(coreprotocol.Request{Method: method, Payload: raw})
		if !known || run == nil || apply == nil {
			t.Fatal("mutation not prepared")
		}
		return run, apply
	}
	beforeProjection := owner.Settings
	save, applySave := prepare(coreprotocol.SessionSettingsSave)
	out := save(context.Background())
	if out.Error != nil {
		t.Fatal(out.Error)
	}
	if owner.Settings != beforeProjection {
		t.Fatal("worker published workspace settings")
	}
	var saved coreprotocol.SessionSettingsResult
	if err := coreprotocol.Decode(out.Payload, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Effective.MainAgent != "crafter" || saved.Effective.MultiAgent == nil || *saved.Effective.MultiAgent || saved.Overrides["root/crafter"].DeniedTools[0] != "execute_command" {
		t.Fatal("DTO omitted fields lost on save")
	}
	applySave(out)
	if owner.Settings == beforeProjection {
		t.Fatal("actor failed to publish")
	}
	for _, method := range []string{coreprotocol.SessionMultiAgentToggle, coreprotocol.SessionSettingsReset} {
		run, apply := prepare(method)
		out := run(context.Background())
		if out.Error != nil {
			t.Fatal(out.Error)
		}
		apply(out)
	}
	final := backend.SettingsSnapshots()
	if final.Effective.SessionMainAgent != "" || final.Effective.SessionMultiAgent != nil || len(final.Overrides) != 0 {
		t.Fatal("reset failed current contract")
	}
}
