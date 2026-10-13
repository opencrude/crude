package settings

import (
	"testing"

	"crude/provider"
	"crude/tui/theme"
	"crude/types"
)

// TestAccountEditDoesNotCorruptOtherAccountConnection reproduces the reported
// cross-connection corruption with the current account-owned transport model.
func TestAccountEditDoesNotCorruptOtherAccountConnection(t *testing.T) {
	th := theme.DefaultDark()
	model := New(th, types.AppSettings{
		Accounts: map[string]provider.Account{
			"ocg": {ID: "ocg", ProviderID: "opencode-go", Label: "OpenCode", Connection: provider.Template{Name: "OpenCode Go", ProviderURL: "https://opencode.ai/zen/go/v1", APIStandard: "openai", AuthType: provider.AuthProtocol}},
			"cg":  {ID: "cg", ProviderID: "chatgpt", Label: "Work", Connection: provider.DefaultTemplates()["chatgpt"]},
		},
	}, []types.ProviderPreset{
		{ID: "opencode-go", Name: "OpenCode Go", BaseURL: "https://opencode.ai/zen/go/v1", StandardID: "openai"},
		chatgptPreset(),
	})

	// Editing the ChatGPT account must not switch or mutate OpenCode's connection.
	model.selectAccount("cg")
	if err := model.beginAccountEdit(false, ""); err != nil {
		t.Fatal(err)
	}

	// Sign in. Update mutates the model in place (the popup HandleEvent keeps
	// the same pointer so form onChanged closures stay valid).
	_, generation := model.SignInRequest()
	_, _ = model.Update(ChatGPTSignInResultMsg{AccountID: "cg", Generation: generation, Creds: &provider.OAuthCredentials{AccessToken: "AT", Email: "u@example.com"}})

	// Save.
	got := model.AppSettings()

	ocg := got.Accounts["ocg"].Connection
	if ocg.ProviderURL != "https://opencode.ai/zen/go/v1" {
		t.Errorf("OpenCode account ProviderURL = %q, want opencode-go URL", ocg.ProviderURL)
	}
	if ocg.APIStandard != "openai" {
		t.Errorf("OpenCode account APIStandard = %q, want openai", ocg.APIStandard)
	}
	// Preferences cannot commit the still-unconfirmed account edit.
	if got.Accounts["cg"].OAuth != nil || model.accountEditor.draft.OAuth == nil || model.accountEditor.draft.OAuth.Email != "u@example.com" {
		t.Fatal("provider switch committed or discarded an account draft")
	}
}
