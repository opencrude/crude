package core

import (
	"context"
	"encoding/json"
	"reflect"
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
	run, _, known := a.prepareSettings(coreprotocol.Request{Method: coreprotocol.SettingsFetch, Payload: emptyPayload})
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
	if err := coreprotocol.Decode(got.Payload, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Accounts["one"].APIKeyPresent || !out.ProxyCredentialsPresent || !out.ConnectionSecretsPresent["mcp"] {
		t.Fatal("presence flags missing")
	}
	if ws.AllAccounts()["one"].APIKey != "api-secret" {
		t.Fatal("projection mutated credential owner")
	}
}

func TestSettingsFetchKeepsCapabilityOverridePresenceWhileRedacting(t *testing.T) {
	ws := workspace.NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, coreTestLogger{})
	defer ws.Close()
	stored := `{"capabilities":{"removed_group":"private","connections":{"one":{"disabled":false,"url":"http://user:private@example.test/?token=private","headers":{"Authorization":"private"},"args":["private"],"removed_token":"private","timeout_seconds":"obsolete"},"removed":null},"sources":{"local":{"location":"https://user:private@example.test/?token=private"}}}}`
	if err := ws.WorkspaceDB.SetSetting("workspace_settings", stored); err != nil {
		t.Fatal(err)
	}
	got := fetchSettingsForTest(t, &actor{ws: ws})
	var have, want any
	if err := json.Unmarshal(got.Overrides.Capabilities, &have); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"connections":{"one":{"disabled":false,"url":"http://example.test/"},"removed":null},"sources":{"local":{"location":"https://example.test/"}}}`), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(have, want) {
		t.Fatalf("redaction invented explicit fields or leaked secrets: %s", got.Overrides.Capabilities)
	}
	if ws.WorkspaceDB.GetSetting("workspace_settings") != stored {
		t.Fatal("reading settings rewrote the layer")
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
	draft := coreprotocol.AccountDraft{ID: "one", APIKey: coreprotocol.SecretString{Keep: true}, Headers: coreprotocol.SecretMap{Keep: true}}
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

func TestDraftModelsChecksIdentityWithoutConfigRevision(t *testing.T) {
	ws := workspace.NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, coreTestLogger{})
	defer ws.Close()
	ws.SetAppDefaults(types.AppSettings{Accounts: map[string]provider.Account{"one": {ID: "one", APIKey: "secret"}}})
	a := &actor{ws: ws}
	raw, err := coreprotocol.Marshal(coreprotocol.Ptr(coreprotocol.AccountModelsRequest{AccountID: "one", Draft: &coreprotocol.AccountDraft{ID: "another"}}))
	if err != nil {
		t.Fatal(err)
	}
	run, _, _ := a.prepareSettings(coreprotocol.Request{Method: coreprotocol.AccountModels, Payload: raw})
	got := run(context.Background())
	if got.Error == nil || got.Error.Code != "invalid_request" {
		t.Fatal("wrong query owner was admitted")
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
		if value.Agents.ActiveProfile != "alpha" || entityRevision(value) != first {
			t.Fatal("profile order or revision unstable")
		}
	}
}

// Terminal shells are a fact of the core's machine; a remote shell must offer
// the shells installed where its terminals will actually run.
func TestSettingsFetchCarriesCoreHostTerminalShells(t *testing.T) {
	ws := workspace.NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, coreTestLogger{})
	defer ws.Close()
	ws.SetAppDefaults(types.AppSettings{})
	a := &actor{ws: ws}
	run, _, known := a.prepareSettings(coreprotocol.Request{Method: coreprotocol.SettingsFetch, Payload: emptyPayload})
	if !known {
		t.Fatal("fetch not dispatched")
	}
	var out coreprotocol.SettingsFetchResult
	if err := coreprotocol.Decode(run(context.Background()).Payload, &out); err != nil {
		t.Fatal(err)
	}
	if want := workspace.DetectTerminalShells(); len(want) == 0 || !reflect.DeepEqual(out.TerminalShells, want) {
		t.Fatalf("fetched shells = %+v; want %+v", out.TerminalShells, want)
	}
}
