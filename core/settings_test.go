package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"crude/coreprotocol"
	"crude/network"
	"crude/provider"
	"crude/session"
	"crude/types"
	captypes "crude/types/capability"
	"crude/workspace"
)

func TestSettingsFetchRedactsAllCredentialContainers(t *testing.T) {
	ws := workspace.NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, coreTestLogger{})
	defer ws.Close()
	ws.SetAppDefaults(types.AppSettings{
		Accounts:     map[string]provider.Account{"one": {ID: "one", APIKey: "api-secret", Connection: provider.Template{Headers: map[string]string{"Authorization": "header-secret"}, ExtraBody: map[string]any{"token": "body-secret"}}}},
		Network:      network.Settings{ProxyURL: "http://user:proxy-secret@localhost:8080?token=query-secret"},
		Capabilities: captypes.Settings{Connections: map[string]captypes.ConnectionConfig{"mcp": {ID: "mcp", Environment: map[string]string{"KEY": "env-secret"}, Headers: map[string]string{"Authorization": "mcp-secret"}, Args: []string{"arg-secret"}}}},
	})
	a := &actor{ws: ws}
	run, _, known := a.prepareSettings(coreprotocol.Request{Method: coreprotocol.SettingsFetch, Payload: json.RawMessage(`{}`)})
	if !known {
		t.Fatal("fetch not dispatched")
	}
	got := run(context.Background())
	if got.Error != nil {
		t.Fatal(got.Error)
	}
	for _, secret := range []string{"api-secret", "header-secret", "body-secret", "proxy-secret", "query-secret", "env-secret", "mcp-secret", "arg-secret"} {
		if strings.Contains(string(got.Payload), secret) {
			t.Fatalf("credential leaked: %s", secret)
		}
	}
	var out coreprotocol.SettingsFetchResult
	if err := json.Unmarshal(got.Payload, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Accounts["one"].APIKeyPresent || !out.ProxyCredentialsPresent || !out.ConnectionSecretsPresent["mcp"] {
		t.Fatal("presence flags missing")
	}
	if ws.AllAccounts()["one"].APIKey != "api-secret" {
		t.Fatal("projection mutated credential owner")
	}
}

func TestSessionSettingsDTOCarriesOmittedFields(t *testing.T) {
	disabled := false
	value := publicSession(types.Settings{SessionMainAgent: "crafter", SessionMultiAgent: &disabled, SessionOverrides: map[string]types.CategorySettings{"root/crafter": {Model: "specific"}}, APIKey: "secret"})
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var got coreprotocol.SessionSettingsDTO
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.MainAgent != "crafter" || got.MultiAgent == nil || *got.MultiAgent || got.Overrides["root/crafter"].Model != "specific" {
		t.Fatal("session-only fields lost")
	}
	if strings.Contains(string(raw), "secret") {
		t.Fatal("session credential escaped")
	}
}

func TestAccountDraftSecretKeepIsExplicitAndDetached(t *testing.T) {
	before := provider.Account{ID: "one", APIKey: "secret", Connection: provider.Template{Headers: map[string]string{"Authorization": "secret"}}}
	draft := coreprotocol.AccountDraft{ID: "one", APIKey: coreprotocol.SecretUpdate[string]{Keep: true}, Headers: coreprotocol.SecretUpdate[map[string]string]{Keep: true}}
	got, err := resolveAccount(draft, before)
	if err != nil {
		t.Fatal(err)
	}
	if got.APIKey != "secret" {
		t.Fatal("explicit keep lost secret")
	}
	got.Connection.Headers["Authorization"] = "changed"
	if before.Connection.Headers["Authorization"] != "secret" {
		t.Fatal("draft aliases server headers")
	}
	draft.APIKey.Keep = false
	got, err = resolveAccount(draft, before)
	if err != nil || got.APIKey != "" {
		t.Fatal("clear inferred keep")
	}
	if publicAccount(before).Revision == publicAccount(got).Revision {
		t.Fatal("secret change did not change opaque revision")
	}
}

func TestDraftModelsRejectStalePreimageWithoutQuery(t *testing.T) {
	ws := workspace.NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, coreTestLogger{})
	defer ws.Close()
	ws.SetAppDefaults(types.AppSettings{Accounts: map[string]provider.Account{"one": {ID: "one", APIKey: "secret"}}})
	a := &actor{ws: ws}
	raw, err := json.Marshal(coreprotocol.AccountModelsRequest{AccountID: "one", ExpectedRevision: "old", Draft: &coreprotocol.AccountDraft{ID: "one"}})
	if err != nil {
		t.Fatal(err)
	}
	run, _, _ := a.prepareSettings(coreprotocol.Request{Method: coreprotocol.AccountModels, Payload: raw})
	got := run(context.Background())
	if got.Error == nil || got.Error.Code != "stale_preimage" {
		t.Fatal("stale query was admitted")
	}
}

func TestCapabilityFullJSONProjectionRemovesDiagnosticsAndPreviewCredentials(t *testing.T) {
	sentinel := "secret-sentinel"
	value := publicCapabilityResult(captypes.Snapshot{Entries: []captypes.Entry{{Reason: sentinel, Repository: "https://user:" + sentinel + "@example.com/?token=" + sentinel}}, Connections: []captypes.ConnectionState{{Error: sentinel, Logs: []string{sentinel}, AuthURL: sentinel}}}, []captypes.Entry{{Reason: sentinel}}, "")
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), sentinel) {
		t.Fatal("snapshot diagnostic leaked")
	}
	preview := publicCapabilityPreview(captypes.AcquisitionPreview{Source: captypes.SourceConfig{Location: "https://example.com/?token=" + sentinel}, Candidate: captypes.Entry{Reason: sentinel}, Existing: &captypes.Entry{Reason: sentinel}})
	raw, err = json.Marshal(preview)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), sentinel) {
		t.Fatal("source preview leaked")
	}
}

func TestConfigProfilesProduceStableSettingsRevision(t *testing.T) {
	cfg := &session.Config{Agents: session.AgentsConfig{ActiveProfile: "alpha", Profiles: map[string]session.ProfileConfig{"zeta": {}, "alpha": {}, "middle": {}}}}
	first := entityRevision(session.ConfigToSettings(cfg))
	for i := 0; i < 100; i++ {
		value := session.ConfigToSettings(cfg)
		if len(value.Agents.Profiles) != 3 || value.Agents.Profiles[0].Name != "alpha" || value.Agents.Profiles[1].Name != "middle" || value.Agents.Profiles[2].Name != "zeta" || entityRevision(value) != first {
			t.Fatal("profile order or revision unstable")
		}
	}
}
