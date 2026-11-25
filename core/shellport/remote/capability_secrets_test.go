package remote

import (
	"testing"

	"crude/core/coreprotocol"
	captypes "crude/types/capability"
	"crude/types/setting"
)

func TestCapabilityMarkerUsesItsOwnSnapshotAndLayer(t *testing.T) {
	w := &Workspace{}
	w.settings = &settingsReplica{w: w}
	layer := func(endpoint string) setting.App {
		return setting.App{Capabilities: captypes.Settings{Connections: map[string]captypes.ConnectionConfig{
			"client": {ID: "client", URL: endpoint},
		}}}
	}
	app, effective := layer("https://app.example.test"), layer("https://workspace.example.test")
	w.settings.install(settingsSnapshot{fetch: coreprotocol.SettingsFetchResult{
		Application: app, Effective: effective,
		ApplicationConnectionSecretFields: map[string]coreprotocol.ConnectionSecretPresence{"client": {CredentialRef: true}},
		ConnectionSecretFields:            map[string]coreprotocol.ConnectionSecretPresence{"client": {Headers: true}},
	}})
	draft := w.AppDefaults().Capabilities.Connections["client"]
	project := w.LoadAppSettings().Capabilities.Connections["client"]
	if draft.CredentialRef != StoredSecret || len(draft.Headers) != 0 || project.CredentialRef != "" || project.Headers[storedKey] != StoredSecret {
		t.Fatal("stored fields crossed layers")
	}
	// A new settings publication must not change the meaning of a marker in an
	// already-open editor, even when fetch no longer reports that field.
	w.settings.install(settingsSnapshot{fetch: coreprotocol.SettingsFetchResult{Application: app, Effective: effective}})
	updates := connectionSecrets(draft, w.capabilityBase(captypes.ScopeApplication).Connections["client"])
	if !updates.Endpoint.Keep || !updates.CredentialRef.Keep || updates.CredentialRef.Value != nil {
		t.Fatal("an unchanged application draft would replace its endpoint or replay its marker")
	}
	updates = connectionSecrets(project, w.capabilityBase(captypes.ScopeWorkspace).Connections["client"])
	if !updates.Endpoint.Keep || !updates.Headers.Keep || updates.Headers.Value != nil {
		t.Fatal("an unchanged workspace draft would replace its endpoint or replay its marker")
	}
	draft.CredentialRef = "secret:NEW"
	updates = connectionSecrets(draft, app.Capabilities.Connections["client"])
	if updates.CredentialRef.Keep || updates.CredentialRef.Value == nil || *updates.CredentialRef.Value != "secret:NEW" {
		t.Fatal("the explicit write-only replacement was lost")
	}
}
