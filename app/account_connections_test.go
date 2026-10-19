package app

import (
	"crude/provider"
	"crude/types"
	"crude/workspace"
	"reflect"
	"testing"
)

func TestAccountConnectionChangesAreAtomicAndIndependent(t *testing.T) {
	defer setTestHome(t, t.TempDir())()
	connection := provider.Template{Name: "Gateway", ProviderURL: "https://old.example/v1", APIStandard: "openai", AuthType: provider.AuthBearer}
	fc := FileConfig{Accounts: map[string]provider.Account{"work": {ID: "work", ProviderID: "gateway", Label: "Work", APIKey: "work-secret", Connection: connection}, "home": {ID: "home", ProviderID: "gateway", Label: "Home", APIKey: "home-secret", Connection: connection}}}
	if err := SaveFileConfig(fc); err != nil {
		t.Fatal(err)
	}
	a, err := New(Opts{FC: fc})
	if err != nil {
		t.Fatal(err)
	}
	ws := &workspace.Workspace{Logger: &appLogger{}}
	a.configureWorkspace(ws)
	opened := ws.AppDefaults()
	before := provider.CloneAccount(fc.Accounts["work"])
	after := provider.CloneAccount(before)
	after.Connection.ProviderURL = "https://new.example/v1"
	after.Connection.Protocol = &provider.ProtocolSettings{Store: boolPtr(false)}
	if err := ws.SaveAccountChange(types.AccountChange{ID: after.ID, ProviderID: after.ProviderID, Before: &before, After: &after}); err != nil {
		t.Fatal(err)
	}
	if err := ws.SaveAppSettings(opened); err != nil {
		t.Fatal(err)
	}
	loaded := LoadFileConfig()
	if !reflect.DeepEqual(loaded.Accounts["work"], after) || !reflect.DeepEqual(loaded.Accounts["home"], fc.Accounts["home"]) {
		t.Fatal("preference save or connection edit affected another owner")
	}
	latest := provider.CloneAccount(before)
	latest.Connection.ProviderURL = "https://last-explicit.example/v1"
	if err := ws.SaveAccountChange(types.AccountChange{ID: latest.ID, ProviderID: latest.ProviderID, Before: &before, After: &latest}); err != nil {
		t.Fatal(err)
	}
	if got := LoadFileConfig().Accounts["work"]; got.Connection.ProviderURL != latest.Connection.ProviderURL || !reflect.DeepEqual(got.Connection.Protocol, after.Connection.Protocol) {
		t.Fatal("last explicit account field lost or neighbor replayed")
	}
}
