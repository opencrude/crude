package core

import (
	"context"
	"crude/capability"
	"crude/coreprotocol"
	"crude/provider"
	"crude/types"
	captypes "crude/types/capability"
	"crude/workspace"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestPreparedAccountCommitKeepsSecretsAndPublishesOnlyOnApply(t *testing.T) {
	ws := workspace.NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, coreTestLogger{})
	defer ws.Close()
	before := provider.Account{ID: "one", ProviderID: "provider", Label: "Before", APIKey: "private-key"}
	ws.SetAppDefaults(types.AppSettings{Accounts: map[string]provider.Account{"one": before}})
	calls := 0
	ws.OnAppAccountCommit = func(change types.AccountChange) (types.AppSettings, error) {
		calls++
		if change.Before.APIKey != "private-key" || change.After.APIKey != "private-key" {
			t.Fatal("private preimage or explicit keep lost")
		}
		return types.AppSettings{Accounts: map[string]provider.Account{"one": *change.After}}, nil
	}
	a := &actor{ws: ws}
	fetched := fetchSettingsForTest(t, a)
	input := coreprotocol.AccountChangeRequest{ID: "one", ExpectedRevision: fetched.Accounts["one"].Revision, After: &coreprotocol.AccountDraft{ID: "one", ProviderID: "provider", Label: "After", APIKey: coreprotocol.SecretUpdate[string]{Keep: true}}}
	raw, _ := json.Marshal(input)
	run, apply, known := a.prepareApplicationSettings(coreprotocol.Request{Method: coreprotocol.AccountChange, Payload: raw})
	if !known {
		t.Fatal("not dispatched")
	}
	out := run(context.Background())
	if out.Error != nil {
		t.Fatal(out.Error)
	}
	if strings.Contains(string(out.Payload), "private-key") {
		t.Fatal("credential escaped")
	}
	if ws.AllAccounts()["one"].Label != "Before" {
		t.Fatal("worker published policy")
	}
	_ = run(context.Background())
	if calls != 1 {
		t.Fatal("duplicate durable commit")
	}
	apply(out)
	if ws.AllAccounts()["one"].Label != "After" {
		t.Fatal("actor did not publish")
	}
}

func TestPreparedAccountRejectsStalePreimageWithoutCommit(t *testing.T) {
	ws := workspace.NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, coreTestLogger{})
	defer ws.Close()
	ws.SetAppDefaults(types.AppSettings{Accounts: map[string]provider.Account{"one": {ID: "one"}}})
	ws.OnAppAccountCommit = func(types.AccountChange) (types.AppSettings, error) {
		t.Fatal("stale request committed")
		return types.AppSettings{}, nil
	}
	a := &actor{ws: ws}
	run, _, _ := a.prepareApplicationSettings(coreprotocol.Request{Method: coreprotocol.AccountChange, Payload: json.RawMessage(`{"id":"one","expected_revision":"old","after":null}`)})
	if out := run(context.Background()); out.Error == nil || out.Error.Code != "stale_preimage" {
		t.Fatalf("missing conflict: %+v", out)
	}
}

func TestPreparedPreferencesPublishesPrivateSnapshotAndKeepsProxy(t *testing.T) {
	ws := workspace.NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, coreTestLogger{})
	defer ws.Close()
	before := ws.AppDefaults()
	before.Network.ProxyURL = "http://user:private-proxy@localhost:8000"
	ws.SetAppDefaults(before)
	before = ws.AppDefaults()
	a := &actor{ws: ws}
	fetched := fetchSettingsForTest(t, a)
	input := coreprotocol.ApplicationPreferencesRequest{ExpectedRevision: fetched.ApplicationRevision, Value: fetched.Application, ProxyURL: coreprotocol.SecretUpdate[string]{Keep: true}}
	input.Value.Theme.Preset = "changed"
	ws.OnAppPreferencesCommit = func(old, next types.AppSettings) (types.AppSettings, error) {
		if next.Network.ProxyURL != before.Network.ProxyURL {
			t.Fatal("explicit proxy keep lost")
		}
		return next, nil
	}
	raw, _ := json.Marshal(input)
	run, apply, _ := a.prepareApplicationSettings(coreprotocol.Request{Method: coreprotocol.ApplicationPreferencesSave, Payload: raw})
	out := run(context.Background())
	if out.Error != nil {
		t.Fatal(out.Error)
	}
	if strings.Contains(string(out.Payload), "private-proxy") {
		t.Fatal("proxy credential leaked")
	}
	if ws.AppDefaults().Theme.Preset == "changed" {
		t.Fatal("worker published")
	}
	apply(out)
	if ws.AppDefaults().Theme.Preset != "changed" {
		t.Fatal("actor did not publish")
	}
}

func fetchSettingsForTest(t *testing.T, a *actor) coreprotocol.SettingsFetchResult {
	t.Helper()
	run, _, _ := a.prepareSettings(coreprotocol.Request{Method: coreprotocol.SettingsFetch, Payload: json.RawMessage(`{}`)})
	response := run(context.Background())
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	var value coreprotocol.SettingsFetchResult
	if err := json.Unmarshal(response.Payload, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestFetchedAccountLabelOnlyKeepsEndpointBytes(t *testing.T) {
	ws := workspace.NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, coreTestLogger{})
	defer ws.Close()
	before := provider.Account{ID: "one", ProviderID: "custom", Label: "before", APIKey: "sentinel", Connection: provider.Template{ProviderURL: "https://example.com/api?token=sentinel", Headers: map[string]string{"Authorization": "sentinel"}, ExtraBody: map[string]any{"key": "sentinel"}}}
	ws.SetAppDefaults(types.AppSettings{Accounts: map[string]provider.Account{"one": before}})
	a := &actor{ws: ws}
	fetched := fetchSettingsForTest(t, a)
	view := fetched.Accounts["one"]
	ws.OnAppAccountCommit = func(change types.AccountChange) (types.AppSettings, error) {
		after := *change.After
		after.Label = before.Label
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("label-only edit changed private bytes: %+v", after)
		}
		return types.AppSettings{Accounts: map[string]provider.Account{"one": *change.After}}, nil
	}
	in := coreprotocol.AccountChangeRequest{ID: "one", ExpectedRevision: view.Revision, After: &coreprotocol.AccountDraft{ID: view.ID, ProviderID: view.ProviderID, Label: "after", Connection: view.Connection, Endpoint: coreprotocol.SecretUpdate[string]{Keep: true}, APIKey: coreprotocol.SecretUpdate[string]{Keep: true}, Headers: coreprotocol.SecretUpdate[map[string]string]{Keep: true}, ExtraBody: coreprotocol.SecretUpdate[map[string]any]{Keep: true}}}
	raw, _ := json.Marshal(in)
	run, apply, _ := a.prepareSettings(coreprotocol.Request{Method: coreprotocol.AccountChange, Payload: raw})
	out := run(context.Background())
	if out.Error != nil {
		t.Fatal(out.Error)
	}
	if strings.Contains(string(out.Payload), "sentinel") {
		t.Fatal("secret leaked")
	}
	apply(out)
}

func TestCapabilityFetchedSecretKeepRoundtrip(t *testing.T) {
	ws := workspace.NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, coreTestLogger{})
	defer ws.Close()
	before := ws.AppDefaults()
	conn := captypes.ConnectionConfig{ID: "one", Name: "before", Transport: "http", URL: "https://example.com/mcp?token=sentinel", Environment: map[string]string{"KEY": "sentinel"}, Headers: map[string]string{"Authorization": "sentinel"}, SecretRefs: map[string]string{"KEY": "sentinel"}, CredentialRef: "sentinel", Args: []string{"sentinel"}, Scope: captypes.ScopeApplication}
	source := captypes.SourceConfig{ID: "private", Name: "before", Kind: "registry", Location: "https://example.com/?token=sentinel", Scope: captypes.ScopeApplication}
	before.Capabilities.Connections[conn.ID] = conn
	before.Capabilities.Sources[source.ID] = source
	ws.SetAppDefaults(before)
	// The production service is initialized by Workspace construction; configuration
	// is explicit setup here, never an opaque revision shortcut.
	service, err := capability.New(ws.RootPath, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ws.Capabilities = service
	if err := ws.Capabilities.Configure(context.Background(), ws.LoadAppSettings().Capabilities); err != nil {
		t.Fatal(err)
	}
	ws.OnAppPreferencesCommit = func(old, next types.AppSettings) (types.AppSettings, error) {
		oldBytes, _ := json.Marshal(old.Capabilities)
		currentBytes, _ := json.Marshal(ws.AppDefaults().Capabilities)
		if string(oldBytes) != string(currentBytes) {
			return types.AppSettings{}, workspace.ErrApplicationSettingsConflict
		}
		return next, nil
	}
	a := &actor{ws: ws}
	fetched := fetchSettingsForTest(t, a)
	connView := fetched.Application.Capabilities.Connections["one"]
	connView.Name = "after"
	in := coreprotocol.CapabilityActionRequest{ExpectedRevision: fetched.CapabilitiesRevision, Action: captypes.Action{Operation: "save-connection", ID: "one", Scope: captypes.ScopeApplication, Connection: &connView}, ConnectionSecrets: coreprotocol.ConnectionSecretUpdates{Endpoint: coreprotocol.SecretUpdate[string]{Keep: true}, Args: coreprotocol.SecretUpdate[[]string]{Keep: true}, Environment: coreprotocol.SecretUpdate[map[string]string]{Keep: true}, Headers: coreprotocol.SecretUpdate[map[string]string]{Keep: true}, SecretRefs: coreprotocol.SecretUpdate[map[string]string]{Keep: true}, CredentialRef: coreprotocol.SecretUpdate[string]{Keep: true}}}
	raw, _ := json.Marshal(in)
	run, apply, _ := a.prepareSettings(coreprotocol.Request{Method: coreprotocol.CapabilityAction, Payload: raw})
	out := run(context.Background())
	if out.Error != nil {
		t.Fatal(out.Error)
	}
	if strings.Contains(string(out.Payload), "sentinel") {
		t.Fatal("secret leaked")
	}
	apply(out)
	saved := ws.AppDefaults().Capabilities.Connections["one"]
	saved.Name = conn.Name
	if !reflect.DeepEqual(saved, conn) {
		t.Fatalf("private connection bytes lost: %+v", saved)
	}
	var result coreprotocol.CapabilityResult
	if err := json.Unmarshal(out.Payload, &result); err != nil {
		t.Fatal(err)
	}
	sourceView := result.Snapshot.Settings.Sources["private"]
	sourceView.Name = "after"
	in = coreprotocol.CapabilityActionRequest{ExpectedRevision: result.Revision, Action: captypes.Action{Operation: "save-source", ID: "private", Scope: captypes.ScopeApplication, Source: &sourceView}, SourceLocation: coreprotocol.SecretUpdate[string]{Keep: true}}
	raw, _ = json.Marshal(in)
	run, apply, _ = a.prepareSettings(coreprotocol.Request{Method: coreprotocol.CapabilityAction, Payload: raw})
	out = run(context.Background())
	if out.Error != nil {
		t.Fatal(out.Error)
	}
	apply(out)
	if ws.AppDefaults().Capabilities.Sources["private"].Location != source.Location {
		t.Fatal("source location bytes lost")
	}
}
