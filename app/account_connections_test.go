package app

import (
	"crude/provider"
	"crude/types"
	"crude/workspace"
	"os"
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
	bytesBefore, err := os.ReadFile(ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.SaveAccountChange(types.AccountChange{ID: after.ID, ProviderID: after.ProviderID, Before: &before, After: &before}); err == nil {
		t.Fatal("stale account edit was accepted")
	}
	bytesAfter, err := os.ReadFile(ConfigPath())
	if err != nil || string(bytesAfter) != string(bytesBefore) {
		t.Fatal("rejected stale edit changed config bytes")
	}
}
