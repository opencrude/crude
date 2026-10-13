package popup

import (
	"testing"

	"crude/provider"
	"crude/tui/settings"
	"crude/tui/theme"
	"crude/tui/ui"
	ttypes "crude/types"
)

func TestSignInResultUpdatesOpenModel(t *testing.T) {
	th := theme.DefaultDark()
	ws := NewWorkspaceSettings(th, ttypes.AppSettings{

		Accounts: map[string]provider.Account{"personal": {ID: "personal", ProviderID: "chatgpt", Label: "Personal", Connection: provider.DefaultTemplates()["chatgpt"]}},
	}, []ttypes.ProviderPreset{{ID: "chatgpt", Name: "Codex", StandardID: "codex"}})

	frame := ui.NewWindowFrameWidget(th, "settings-frame", ui.FrameWindow, "Settings", ws.Component(), ws, true, 90, 24)
	ws.SetFocusHandler(frame.Focus)
	host := frame.Host()
	host.Resize(90, 24)
	host.Layout()

	model := ws.ModelForTest()
	ws.ShowAccounts()
	ws.Component().(*settings.WorkspaceSettingsSurface).HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.edit"})
	host.MarkLayout()
	host.Layout()

	if status := model.ValueForTest(model.SelectedCategory(), "chatgpt-status"); status != "Not signed in" {
		t.Fatalf("initial status = %q, want 'Not signed in'", status)
	}

	// Dispatch exactly as viewmodel.forwardEventToWindowContent does.
	id, generation := model.SignInRequest()
	frame.Dispatch(settings.ChatGPTSignInResultMsg{AccountID: id, Generation: generation,
		Creds: &provider.OAuthCredentials{AccessToken: "AT", Email: "u@example.com"},
	})

	if status := model.ValueForTest(model.SelectedCategory(), "chatgpt-status"); status != "Sign-in stored" {
		t.Errorf("status after dispatch = %q, want stored sign-in", status)
	}
}

// Sign-in updates only the account draft. A template selected in the table
// stays independent even while the account editor receives async OAuth data.
func TestAccountSignInKeepsSelectedTemplateIndependent(t *testing.T) {
	th := theme.DefaultDark()
	ws := NewWorkspaceSettings(th, ttypes.AppSettings{

		Accounts: map[string]provider.Account{"personal": {ID: "personal", ProviderID: "chatgpt", Label: "Personal", Connection: provider.DefaultTemplates()["chatgpt"]}, "work": {ID: "work", ProviderID: "opencode-go", Label: "Work", APIKey: "sk-opencode", Connection: provider.DefaultTemplates()["opencode-go"]}},
	}, []ttypes.ProviderPreset{
		{ID: "opencode-go", Name: "OpenCode Go", BaseURL: "https://opencode.ai/zen/go/v1", StandardID: "openai"},
		{ID: "chatgpt", Name: "Codex", StandardID: "codex"},
	})

	frame := ui.NewWindowFrameWidget(th, "settings-frame", ui.FrameWindow, "Settings", ws.Component(), ws, true, 90, 24)
	ws.SetFocusHandler(frame.Focus)
	host := frame.Host()
	host.Resize(90, 24)
	host.Layout()

	model := ws.ModelForTest()
	model.SelectAccountForTest("personal")
	ws.ShowAccounts()
	ws.Component().(*settings.WorkspaceSettingsSurface).HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.edit"})

	// Sign in (rebuilds only the retained account editor).
	id, generation := model.SignInRequest()
	frame.Dispatch(settings.ChatGPTSignInResultMsg{AccountID: id, Generation: generation,
		Creds: &provider.OAuthCredentials{AccessToken: "AT", Email: "u@example.com"},
	})
	if ws.ModelForTest() != model {
		t.Fatal("async result replaced the model pointer or selected a different template")
	}
	if model.AppSettings().Accounts["personal"].OAuth != nil {
		t.Fatal("unconfirmed sign-in changed a saved account")
	}
	got := ws.ModelForTest().AppSettings()
	ocg := got.Accounts["work"].Connection
	if ocg.ProviderURL != "https://opencode.ai/zen/go/v1" {
		t.Errorf("opencode-go ProviderURL = %q, want opencode-go URL", ocg.ProviderURL)
	}
	if ocg.APIStandard != provider.DefaultTemplates()["opencode-go"].APIStandard {
		t.Errorf("opencode-go APIStandard changed by account sign-in: %q", ocg.APIStandard)
	}
}
