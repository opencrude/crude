package viewmodel

import (
	"testing"

	"crude/agent/provider"
	"crude/agent/session"
	"crude/internal/providersim"
	"crude/tui/screens/sessionsettings"
	"crude/types/setting"
	"crude/workspace"
)

func TestSessionModelsUseAccountEndpointAndOriginatingDeck(t *testing.T) {
	server := providersim.New(t, providersim.WithModels(providersim.Chat, "account-model"))
	app := newFullTestApp(t)
	accounts := map[string]provider.Account{"work": {
		ID: "work", ProviderID: "custom", Label: "Work", APIKey: "test",
		Connection: provider.Template{Name: "Custom", ProviderURL: server.BaseURL(providersim.Chat), APIStandard: "openai", AuthType: provider.AuthBearer},
	}}
	wsOf(app).SetBackendConfig(&session.Config{Accounts: accounts}, t.TempDir())
	settings := setting.Settings{AccountID: "work", ProviderID: "custom"}
	app.ActiveSession = &workspace.Session{SessionID: "first", Settings: workspace.NewSessionSettingsState(settings, settings, nil)}
	app.openSessionSettings()
	screen := app.sessionSettings()
	screen.ConfigureAccounts(accounts)
	request := screen.OpenModelPicker("default")().(sessionsettings.FetchModelsMsg)
	app.ActiveSession = &workspace.Session{SessionID: "second", Settings: workspace.NewSessionSettingsState(settings, settings, nil)}
	app.openSessionSettings()
	_, cmd := app.Update(request)
	if cmd == nil {
		t.Fatal("model request was dropped")
	}
	result, ok := cmd().(sessionSettingsModelsResultMsg)
	if !ok || result.err != nil || result.target != screen || len(result.models) != 1 || result.models[0].ID != "account-model" {
		t.Fatalf("discovery used template defaults or the wrong deck: %+v", result)
	}
	_, _ = app.Update(result)
}
