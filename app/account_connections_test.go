package app

import (
	"crude/agent/provider"
	"crude/types/setting"
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
	if err := ws.SaveAccountChange(setting.AccountChange{ID: after.ID, ProviderID: after.ProviderID, Before: &before, After: &after}); err != nil {
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
	if err := ws.SaveAccountChange(setting.AccountChange{ID: latest.ID, ProviderID: latest.ProviderID, Before: &before, After: &latest}); err != nil {
		t.Fatal(err)
	}
	if got := LoadFileConfig().Accounts["work"]; got.Connection.ProviderURL != latest.Connection.ProviderURL || !reflect.DeepEqual(got.Connection.Protocol, after.Connection.Protocol) {
		t.Fatal("last explicit account field lost or neighbor replayed")
	}
}

// Forgetting compaction checks rides on an ordinary account save and refuses
// a stale editor, so it can never clear evidence for a different account state.
func TestForgetCompactionChecksNeedsTheReviewedAccount(t *testing.T) {
	defer setTestHome(t, t.TempDir())()
	connection := provider.Template{Name: "Gateway", ProviderURL: "https://gateway.example/v1", APIStandard: "openai-responses", AuthType: provider.AuthBearer}
	fc := FileConfig{Accounts: map[string]provider.Account{"work": {ID: "work", ProviderID: "gateway", Label: "Work", APIKey: "secret", Connection: connection}}}
	if err := SaveFileConfig(fc); err != nil {
		t.Fatal(err)
	}
	a, err := New(Opts{FC: fc})
	if err != nil {
		t.Fatal(err)
	}
	ws := &workspace.Workspace{Logger: &appLogger{}}
	a.configureWorkspace(ws)
	reviewed := provider.CloneAccount(fc.Accounts["work"])
	stale := provider.CloneAccount(reviewed)
	stale.Connection.ProviderURL = "https://elsewhere.example/v1"
	after := provider.CloneAccount(reviewed)
	after.Connection.Protocol = &provider.ProtocolSettings{Compaction: provider.CompactionProviderOnly}
	if err := ws.SaveAccountChange(setting.AccountChange{ID: "work", ProviderID: "gateway", Before: &stale, After: &after, ForgetCompaction: true}); err == nil {
		t.Fatal("forgot checks for an account the user did not review")
	}
	if got := LoadFileConfig().Accounts["work"]; got.Connection.Protocol != nil {
		t.Fatal("refused change was saved")
	}
	if err := ws.SaveAccountChange(setting.AccountChange{ID: "work", ProviderID: "gateway", Before: &reviewed, After: &after, ForgetCompaction: true}); err != nil {
		t.Fatal(err)
	}
	if got := LoadFileConfig().Accounts["work"]; got.Connection.Protocol == nil || got.Connection.Protocol.Compaction != provider.CompactionProviderOnly {
		t.Fatal("compaction choice was not saved with the forget request")
	}
}
