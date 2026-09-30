package app

import (
	"os"
	"reflect"
	"testing"

	"crude/provider"
	"crude/workspace"
)

func TestTemplateChangesAreAtomicRevisionCheckedAndIndependentOfPreferences(t *testing.T) {
	defer setTestHome(t, t.TempDir())()

	custom := provider.Template{
		Name: "Acme", ProviderURL: "https://old.example/v1",
		APIStandard: "openai", AuthType: provider.AuthBearer,
	}
	other := provider.Template{
		Name: "Other", ProviderURL: "https://other.example/v1",
		APIStandard: "openai", AuthType: provider.AuthBearer,
	}
	fc := DefaultFileConfig()
	fc.Providers["acme"] = custom
	fc.Providers["other"] = other
	fc.Accounts = map[string]provider.Account{
		"work": {ID: "work", ProviderID: "acme", Label: "Work", APIKey: "work-secret"},
	}
	if err := SaveFileConfig(fc); err != nil {
		t.Fatal(err)
	}

	a, err := New(Opts{FC: fc, Home: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ws := &workspace.Workspace{Logger: &appLogger{}}
	a.configureWorkspace(ws)
	opened := FileConfigToAppSettings(fc)

	before := provider.CloneTemplate(custom)
	after := provider.CloneTemplate(custom)
	after.ProviderURL = "https://new.example/v1"
	after.Dialer = &provider.DialSettings{
		RequestTimeoutSeconds: 600, ConnectTimeoutSeconds: 60, MaxAttempts: 3, MaxBackoffSeconds: 60,
		ProgressStartupTimeoutSeconds: 120, ProgressTimeoutFloorSeconds: 60, ProgressTimeoutCeilingSeconds: 600,
	}
	if err := ws.SaveTemplateChange(provider.TemplateChange{ID: "acme", Before: &before, After: &after}); err != nil {
		t.Fatalf("save template edit: %v", err)
	}

	got := LoadFileConfig()
	if !reflect.DeepEqual(got.Providers["acme"], after) {
		t.Fatalf("edited template not persisted: got %+v want %+v", got.Providers["acme"], after)
	}
	if !reflect.DeepEqual(got.Providers["other"], other) {
		t.Fatalf("unrelated template changed: %+v", got.Providers["other"])
	}
	if !reflect.DeepEqual(got.Accounts, fc.Accounts) {
		t.Fatalf("template edit changed accounts: %+v", got.Accounts)
	}

	bytesBefore, err := os.ReadFile(ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	stale := provider.CloneTemplate(custom)
	if err := ws.SaveTemplateChange(provider.TemplateChange{ID: "acme", Before: &stale, After: &custom}); err == nil {
		t.Fatal("stale template precondition was accepted")
	}
	if err := ws.SaveTemplateChange(provider.TemplateChange{ID: "acme", After: &custom}); err == nil {
		t.Fatal("duplicate template ID was accepted")
	}
	used := provider.CloneTemplate(after)
	if err := ws.SaveTemplateChange(provider.TemplateChange{ID: "acme", Before: &used}); err == nil {
		t.Fatal("template used by an account was removed")
	}
	bytesAfter, err := os.ReadFile(ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(bytesAfter, bytesBefore) {
		t.Fatal("rejected template changes modified config.json")
	}

	newTemplate := provider.Template{
		Name: "Custom Gateway", ProviderURL: "https://gateway.example/v1",
		APIStandard: "openai", AuthType: provider.AuthBearer,
	}
	if err := ws.SaveTemplateChange(provider.TemplateChange{ID: "custom-gateway", After: &newTemplate}); err != nil {
		t.Fatalf("create custom template: %v", err)
	}

	// A preference form opened before either template edit cannot restore its
	// stale provider map over the explicitly committed templates.
	if err := ws.SaveAppSettings(opened); err != nil {
		t.Fatalf("save unrelated preferences: %v", err)
	}
	got = LoadFileConfig()
	if !reflect.DeepEqual(got.Providers["acme"], after) || !reflect.DeepEqual(got.Providers["custom-gateway"], newTemplate) {
		t.Fatalf("stale preferences replaced explicit templates: %+v", got.Providers)
	}
	if got.Accounts["work"].APIKey != "work-secret" {
		t.Fatal("preference save changed an account")
	}

}
