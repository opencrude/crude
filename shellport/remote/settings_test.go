package remote

import (
	"encoding/json"
	"strings"
	"testing"

	"crude/coreprotocol"
	"crude/provider"
	"crude/types"
)

func TestRemoteAccountStaleFormFailsBeforeRPC(t *testing.T) {
	w := &Workspace{}
	w.settings = &settingsReplica{w: w}
	w.settings.install(settingsSnapshot{fetch: coreprotocol.SettingsFetchResult{Accounts: map[string]coreprotocol.AccountView{"work": {ID: "work", Revision: "original", APIKeyPresent: true, HeadersPresent: true}}}})
	before := w.AllAccounts()["work"]
	if before.ObservationRevision != "original" || before.APIKey != StoredSecret {
		t.Fatal("redacted account lost its exact preimage")
	}
	w.settings.install(settingsSnapshot{fetch: coreprotocol.SettingsFetchResult{Accounts: map[string]coreprotocol.AccountView{"work": {ID: "work", Revision: "replacement"}}}})
	after := provider.CloneAccount(before)
	after.Label = "changed"
	if err := w.SaveAccountChange(types.AccountChange{ID: "work", Before: &before, After: &after}); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatal("stale editor reached RPC or accepted replacement account", err)
	}
	raw, err := json.Marshal(before)
	if err != nil || strings.Contains(string(raw), "original") {
		t.Fatal("client preimage observation became persisted credential data", err)
	}
}

func TestRemoteStoredCredentialsProduceExplicitKeep(t *testing.T) {
	view := coreprotocol.AccountView{ID: "work", Revision: "exact", APIKeyPresent: true, OAuthPresent: true, HeadersPresent: true, ExtraBodyPresent: true, Connection: provider.Template{ProviderURL: "https://example.test"}}
	w := &Workspace{}
	w.settings = &settingsReplica{w: w}
	w.settings.install(settingsSnapshot{fetch: coreprotocol.SettingsFetchResult{Accounts: map[string]coreprotocol.AccountView{"work": view}}})
	account := w.AllAccounts()["work"]
	value := draft(account, view, true)
	if !value.APIKey.Keep || !value.OAuth.Keep || !value.Headers.Keep || !value.ExtraBody.Keep || !value.Endpoint.Keep {
		t.Fatal("unmodified redacted credential would be cleared", value)
	}
	account.APIKey = "replacement"
	if next := draft(account, view, true); next.APIKey.Keep || next.APIKey.Value == nil || *next.APIKey.Value != "replacement" {
		t.Fatal("explicit replacement ignored")
	}
}
