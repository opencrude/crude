package viewmodel

import (
	"testing"

	"crude/provider"
	settingsui "crude/tui/settings"
	ttypes "crude/tui/types"
	"crude/tui/ui"
	"crude/tui/ui/popup"
	"crude/tui/wm"
)

// TestChatGPTSignInResultUpdatesOpenSettingsWindow verifies the app-level
// wiring: when a ChatGPTSignInResultMsg arrives while the settings window is
// open, the open window's model must be updated (not just the persisted
// config). This reproduces the reported bug where the settings GUI kept
// showing "Not signed in" until the window was closed and reopened.
func TestChatGPTSignInResultUpdatesOpenSettingsWindow(t *testing.T) {
	app := newTestApp(t)
	app.Logger = noopLogger{}
	// Seed the workspace with a chatgpt provider so the settings form shows the
	// OAuth status row.
	app.SetAppDefaults(ttypes.AppSettings{
		ActiveProvider: "chatgpt",
		Providers:      map[string]ttypes.ProviderConfig{"chatgpt": {}},
	})
	app.openSettings(ttypes.SettingsScopeApplication)

	fw := app.windowManager.Window("workspace-settings")
	if fw == nil {
		t.Fatal("settings window not open")
	}
	ws := settingsWidgetForTest(t, fw)
	model := ws.ModelForTest()
	model.SelectCategory(0) // Providers

	// The settings model starts signed out.
	if status := model.ValueForTest(0, "chatgpt-status"); status != "Not signed in" {
		t.Fatalf("initial status = %q, want 'Not signed in'", status)
	}

	// Dispatch the sign-in result through the app Update, exactly as the
	// background sign-in command would.
	_, cmd := app.Update(settingsui.ChatGPTSignInResultMsg{
		Creds: &provider.OAuthCredentials{AccessToken: "AT", Email: "u@example.com"},
	})
	if cmd != nil {
		t.Fatalf("unexpected cmd: %v", cmd)
	}

	// The open window's model must now show signed in — this is the reported
	// bug: it stayed "Not signed in" until the window was reopened.
	if status := model.ValueForTest(0, "chatgpt-status"); status != "Signed in as u@example.com" {
		t.Errorf("status after sign-in = %q, want 'Signed in as u@example.com'", status)
	}

	// The persisted app settings must carry the OAuth creds.
	saved := app.LoadAppSettings()
	if saved.Providers["chatgpt"].OAuth == nil || saved.Providers["chatgpt"].OAuth.Email != "u@example.com" {
		t.Errorf("persisted chatgpt OAuth = %+v, want email u@example.com", saved.Providers["chatgpt"].OAuth)
	}
}

// TestSignInThenSwitchBackDoesNotCorruptConfig reproduces the reported config
// corruption: after signing in on chatgpt and switching the provider dropdown
// back to opencode-go, saving must not write the chatgpt codex URL into the
// opencode-go provider config.
func TestSignInThenSwitchBackDoesNotCorruptConfig(t *testing.T) {
	app := newTestApp(t)
	app.Logger = noopLogger{}
	app.SetAppDefaults(ttypes.AppSettings{
		ActiveProvider: "opencode-go",
		Providers: map[string]ttypes.ProviderConfig{
			"opencode-go": {Model: "deepseek-v4-flash", APIKey: "sk-opencode"},
			"chatgpt":     {},
		},
	})
	app.openSettings(ttypes.SettingsScopeApplication)

	fw := app.windowManager.Window("workspace-settings")
	if fw == nil {
		t.Fatal("settings window not open")
	}
	ws := settingsWidgetForTest(t, fw)
	model := ws.ModelForTest()
	model.SelectCategory(0) // Providers

	// Switch to chatgpt.
	model.SetValueForTest(0, "provider", "chatgpt")
	if model.ProviderIDForTest() != "chatgpt" {
		t.Fatalf("providerID = %q, want chatgpt", model.ProviderIDForTest())
	}

	// Sign in.
	_, cmd := app.Update(settingsui.ChatGPTSignInResultMsg{
		Creds: &provider.OAuthCredentials{AccessToken: "AT", Email: "u@example.com"},
	})
	if cmd != nil {
		t.Fatalf("unexpected cmd: %v", cmd)
	}

	// Switch back to opencode-go. The model must follow the dropdown.
	ws.ModelForTest().SetValueForTest(0, "provider", "opencode-go")
	if got := ws.ModelForTest().ProviderIDForTest(); got != "opencode-go" {
		t.Fatalf("providerID after switch back = %q, want opencode-go", got)
	}

	// Save via the app Update (the same path the Save button uses).
	_, cmd = app.Update(settingsui.SaveMsg{
		Scope:       ttypes.SettingsScopeApplication,
		AppSettings: ws.ModelForTest().AppSettings(),
	})
	if cmd != nil {
		t.Fatalf("unexpected cmd from save: %v", cmd)
	}

	// The persisted opencode-go provider must keep its own URL/standard.
	saved := app.LoadAppSettings()
	ocg := saved.Providers["opencode-go"]
	if ocg.ProviderURL != "https://opencode.ai/zen/go/v1" {
		t.Errorf("opencode-go ProviderURL = %q, want opencode-go URL (config corrupted)", ocg.ProviderURL)
	}
	if ocg.APIStandard != "openai" {
		t.Errorf("opencode-go APIStandard = %q, want openai", ocg.APIStandard)
	}
	if saved.ActiveProvider != "opencode-go" {
		t.Errorf("ActiveProvider = %q, want opencode-go", saved.ActiveProvider)
	}
	// chatgpt OAuth must survive the save.
	if saved.Providers["chatgpt"].OAuth == nil || saved.Providers["chatgpt"].OAuth.Email != "u@example.com" {
		t.Errorf("chatgpt OAuth lost after save: %+v", saved.Providers["chatgpt"].OAuth)
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
