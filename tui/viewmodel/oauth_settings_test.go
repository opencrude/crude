package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"errors"
	"strings"
	"testing"

	"crude/provider"
	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/tui/ui/popup"
	"crude/tui/wm"
	coretypes "crude/types"
)

// TestChatGPTSignInResultUpdatesOpenSettingsWindow verifies the app-level
// wiring: when a ChatGPTSignInResultMsg arrives while the settings window is
// open, the open window's model must be updated (not just the persisted
// config). This reproduces the reported bug where the settings GUI kept
// showing "Not signed in" until the window was closed and reopened.
func TestChatGPTSignInResultUpdatesOpenSettingsWindow(t *testing.T) {
	app := newTestApp(t)
	wsOf(app).Logger = noopLogger{}
	// Seed an account so the Application Accounts page shows OAuth status.
	wsOf(app).SetAppDefaults(coretypes.AppSettings{

		Accounts: map[string]provider.Account{"personal": {ID: "personal", ProviderID: "chatgpt", Label: "Personal", Connection: provider.DefaultTemplates()["chatgpt"]}},
	})
	app.openSettings(coretypes.SettingsScopeApplication)

	fw := app.windowManager.Window("workspace-settings")
	if fw == nil {
		t.Fatal("settings window not open")
	}
	ws := settingsWidgetForTest(t, fw)
	model := ws.ModelForTest()
	model.SelectAccounts()
	surfaceRoot := ws.Component().(*settingsui.WorkspaceSettingsSurface)
	surfaceRoot.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.edit"})

	// The settings model starts signed out.
	if status := model.ValueForTest(model.SelectedCategory(), "chatgpt-status"); status != "Not signed in" {
		t.Fatalf("initial status = %q, want 'Not signed in'", status)
	}

	// Dispatch the sign-in result through the app Update, exactly as the
	// background sign-in command would.
	accountID, generation := model.SignInRequest()
	_, cmd := app.Update(settingsui.ChatGPTSignInResultMsg{AccountID: accountID, Generation: generation,
		Creds: &provider.OAuthCredentials{AccessToken: "AT", Email: "u@example.com"},
	})
	if cmd != nil {
		t.Fatalf("unexpected cmd: %v", cmd)
	}

	// The open window's model must now show signed in — this is the reported
	// bug: it stayed "Not signed in" until the window was reopened.
	if status := model.ValueForTest(model.SelectedCategory(), "chatgpt-status"); status != "Sign-in stored" {
		t.Errorf("status after sign-in = %q, want stored sign-in", status)
	}

	// Authentication is staged in the account editor until confirmed.
	if app.ws.LoadAppSettings().Accounts["personal"].OAuth != nil {
		t.Fatal("sign-in persisted before confirming the account editor")
	}
	wsOf(app).OnAppAccountChange = func(change coretypes.AccountChange) error {
		settings := app.ws.LoadAppSettings()
		settings.Accounts[change.ID] = *change.After
		wsOf(app).SetAppDefaults(settings)
		return nil
	}
	change := surfaceRoot.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.commit"}).Cmd().(settingsui.AccountChangeMsg)
	_, save := app.Update(change)
	_, _ = app.Update(save())
	saved := app.ws.LoadAppSettings()
	if saved.Accounts["personal"].OAuth == nil || saved.Accounts["personal"].OAuth.Email != "u@example.com" {
		t.Errorf("persisted chatgpt OAuth = %+v, want email u@example.com", saved.Accounts["personal"].OAuth)
	}
}

// TestSignInThenSwitchBackDoesNotCorruptConfig reproduces the reported config
// corruption: after signing in on chatgpt and switching the provider dropdown
// back to opencode-go, saving must not write the chatgpt codex URL into the
// opencode-go provider config.
func TestSignInThenSwitchBackDoesNotCorruptConfig(t *testing.T) {
	app := newTestApp(t)
	wsOf(app).Logger = noopLogger{}
	wsOf(app).SetAppDefaults(coretypes.AppSettings{

		Accounts: map[string]provider.Account{
			"opencode": {ID: "opencode", ProviderID: "opencode-go", Label: "Work", APIKey: "sk-opencode", Connection: provider.DefaultTemplates()["opencode-go"]},
			"personal": {ID: "personal", ProviderID: "chatgpt", Label: "Personal", Connection: provider.DefaultTemplates()["chatgpt"]},
		},
	})
	app.openSettings(coretypes.SettingsScopeApplication)

	fw := app.windowManager.Window("workspace-settings")
	if fw == nil {
		t.Fatal("settings window not open")
	}
	ws := settingsWidgetForTest(t, fw)
	model := ws.ModelForTest()
	model.SelectAccountForTest("personal")
	model.SelectAccounts()
	surfaceRoot := ws.Component().(*settingsui.WorkspaceSettingsSurface)
	surfaceRoot.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.edit"})

	// Sign in.
	accountID, generation := model.SignInRequest()
	_, cmd := app.Update(settingsui.ChatGPTSignInResultMsg{AccountID: accountID, Generation: generation,
		Creds: &provider.OAuthCredentials{AccessToken: "AT", Email: "u@example.com"},
	})
	if cmd != nil {
		t.Fatalf("unexpected cmd: %v", cmd)
	}
	wsOf(app).OnAppAccountChange = func(change coretypes.AccountChange) error {
		settings := app.ws.LoadAppSettings()
		settings.Accounts[change.ID] = *change.After
		wsOf(app).SetAppDefaults(settings)
		return nil
	}
	change := surfaceRoot.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.commit"}).Cmd().(settingsui.AccountChangeMsg)
	_, save := app.Update(change)
	_, _ = app.Update(save())

	// Save via the app Update (the same path the Save button uses).
	_, cmd = app.Update(settingsui.SaveMsg{
		Scope:       coretypes.SettingsScopeApplication,
		AppSettings: ws.ModelForTest().AppSettings(),
	})
	if cmd != nil {
		t.Fatalf("unexpected cmd from save: %v", cmd)
	}

	// The persisted opencode-go provider must keep its own URL/standard.
	saved := app.ws.LoadAppSettings()
	ocg := saved.Accounts["opencode"].Connection
	if ocg.ProviderURL != provider.DefaultTemplates()["opencode-go"].ProviderURL {
		t.Errorf("opencode-go ProviderURL changed after Codex sign-in: %q", ocg.ProviderURL)
	}
	if ocg.APIStandard != provider.DefaultTemplates()["opencode-go"].APIStandard {
		t.Errorf("opencode-go wire protocol changed after Codex sign-in: %q", ocg.APIStandard)
	}
	// chatgpt OAuth must survive the save.
	if saved.Accounts["personal"].OAuth == nil || saved.Accounts["personal"].OAuth.Email != "u@example.com" {
		t.Errorf("chatgpt OAuth lost after save: %+v", saved.Accounts["personal"].OAuth)
	}
}

// noopLogger satisfies workspace.Logger without writing anywhere.
type noopLogger struct{}

func (noopLogger) Info(tag, msg string, pairs ...any)  {}
func (noopLogger) Warn(tag, msg string, pairs ...any)  {}
func (noopLogger) Error(tag, msg string, pairs ...any) {}
func (noopLogger) Debug(tag, msg string, pairs ...any) {}

// settingsWidgetForTest extracts the *popup.WorkspaceSettings from the open
// settings window's frame eventTarget.
func settingsWidgetForTest(t *testing.T, fw *wm.FloatingWindow) *popup.WorkspaceSettings {
	t.Helper()
	frame, ok := fw.Content.(*ui.WindowFrameWidget)
	if !ok {
		t.Fatalf("settings window content is %T, want *ui.WindowFrameWidget", fw.Content)
	}
	target := frame.EventTargetForTest()
	ws, ok := target.(*popup.WorkspaceSettings)
	if !ok {
		t.Fatalf("settings event target is %T, want *popup.WorkspaceSettings", target)
	}
	return ws
}

func TestAccountEditorStaysWithinUnmovedSettingsWindow(t *testing.T) {
	app := newTestApp(t)
	wsOf(app).Logger = noopLogger{}
	app.width, app.height = 160, 40
	wsOf(app).SetAppDefaults(coretypes.AppSettings{})
	app.openApplicationSettings()
	fw := app.windowManager.Window(string(ui.OverlayWorkspaceSettings))
	if fw == nil {
		t.Fatal("settings window did not open")
	}
	frame := fw.Content.(*ui.WindowFrameWidget)
	surfaceRoot := frame.Body().(*settingsui.WorkspaceSettingsSurface)
	surfaceRoot.ShowAccounts()
	frame.Host().MarkLayout()
	frame.Host().Layout()
	x, y := fw.Position()
	w, h := fw.Size()
	result := surfaceRoot.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.begin-add"})
	if !result.Handled {
		t.Fatal("Add account did not enter editor")
	}
	frame.Host().MarkLayout()
	frame.Host().Layout()
	_, _ = app.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	newX, newY := fw.Position()
	newW, newH := fw.Size()
	if x != newX || y != newY || w != newW || h != newH {
		t.Fatalf("Add account moved/resized settings: before=(%d,%d,%d,%d) after=(%d,%d,%d,%d)", x, y, w, h, newX, newY, newW, newH)
	}
	// The window frame title is fixed: navigation is reported by the pane.
	if title := frame.Frame().Title(); title != "Application Settings" {
		t.Fatalf("window title must stay fixed during navigation: %q", title)
	}
	if pane := surfaceRoot.PaneTitle(); !strings.Contains(pane, "Accounts") || !strings.Contains(pane, "Add account") {
		t.Fatalf("pane title missing editor breadcrumb: %q", pane)
	}
	if surfaceRoot.Model().SelectedCategory() < 0 {
		t.Fatal("editor lost account category")
	}
}

func TestAccountSaveFailureKeepsOpenFormAndPersistedState(t *testing.T) {
	app := newTestApp(t)
	wsOf(app).Logger = noopLogger{}
	wsOf(app).SetAppDefaults(coretypes.AppSettings{Accounts: map[string]provider.Account{"personal": {ID: "personal", ProviderID: "chatgpt", Label: "Personal", Connection: provider.DefaultTemplates()["chatgpt"]}}})
	app.openSettings(coretypes.SettingsScopeApplication)
	fw := app.windowManager.Window("workspace-settings")
	model := settingsWidgetForTest(t, fw).ModelForTest()
	model.SelectAccounts()
	surfaceRoot := settingsWidgetForTest(t, fw).Component().(*settingsui.WorkspaceSettingsSurface)
	surfaceRoot.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.edit"})
	id, generation := model.SignInRequest()
	_, _ = app.Update(settingsui.ChatGPTSignInResultMsg{AccountID: id, Generation: generation, Creds: &provider.OAuthCredentials{AccessToken: "staged"}})
	wsOf(app).OnAppAccountChange = func(coretypes.AccountChange) error { return errors.New("disk unavailable") }
	change := surfaceRoot.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.commit"}).Cmd().(settingsui.AccountChangeMsg)
	_, save := app.Update(change)
	_, _ = app.Update(save())
	if app.windowManager.Window("workspace-settings") != fw {
		t.Fatal("failed save closed the form")
	}
	if app.ws.LoadAppSettings().Accounts[id].OAuth != nil {
		t.Fatal("failed save changed persisted account state")
	}
	if model.ValueForTest(model.SelectedCategory(), "chatgpt-status") != "Sign-in stored" {
		t.Fatal("failed save discarded staged credentials")
	}
}
