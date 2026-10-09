package core

import (
	"context"
	"encoding/json"
	"testing"

	"crude/coreprotocol"
	"crude/provider"
	"crude/session"
	"crude/types"
	"crude/workspace"
)

func TestSessionSettingsMutationBridge(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
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
	prepare := func(method string, revision uint64) (func(context.Context) coreprotocol.Response, func(coreprotocol.Response)) {
		t.Helper()
		raw, err := json.Marshal(coreprotocol.SessionSettingsMutationRequest{SessionID: owner.SessionID, ExpectedRevision: revision, Settings: dto})
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
	save, applySave := prepare(coreprotocol.SessionSettingsSave, initial.Revision)
	out := save(context.Background())
	if out.Error != nil {
		t.Fatal(out.Error)
	}
	if owner.Settings != beforeProjection {
		t.Fatal("worker published workspace settings")
	}
	var saved coreprotocol.SessionSettingsResult
	if err := json.Unmarshal(out.Payload, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Effective.MainAgent != "crafter" || saved.Effective.MultiAgent == nil || *saved.Effective.MultiAgent || saved.Overrides["root/crafter"].DeniedTools[0] != "execute_command" {
		t.Fatal("DTO omitted fields lost on save")
	}
	applySave(out)
	if owner.Settings == beforeProjection {
		t.Fatal("actor failed to publish")
	}
	stale, applyStale := prepare(coreprotocol.SessionMultiAgentToggle, initial.Revision)
	staleOut := stale(context.Background())
	if staleOut.Error == nil || staleOut.Error.Code != "stale_preimage" {
		t.Fatal("stale mutation was not rejected")
	}
	applyStale(staleOut)
	for _, method := range []string{coreprotocol.SessionMultiAgentToggle, coreprotocol.SessionSettingsReset} {
		run, apply := prepare(method, backend.SettingsSnapshots().Revision)
		out := run(context.Background())
		if out.Error != nil {
			t.Fatal(out.Error)
		}
		apply(out)
	}
	final := backend.SettingsSnapshots()
	if final.Effective.SessionMainAgent != "" || final.Effective.SessionMultiAgent != nil || final.Overrides["default"].AccountID != "one" {
		t.Fatal("reset failed current contract")
	}
}
