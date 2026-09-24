package settings

import (
	"testing"

	"crude/provider"
	"crude/tui/theme"
	"crude/tui/types"
)

// TestProviderSwitchDoesNotCorruptOtherProviderConfig reproduces the reported
// bug where switching to chatgpt and back corrupted the opencode-go provider's
// base URL / standard with the chatgpt codex values.
func TestProviderSwitchDoesNotCorruptOtherProviderConfig(t *testing.T) {
	th := theme.DefaultDark()
	model := New(th, types.AppSettings{
		ActiveProvider: "opencode-go",
		Providers: map[string]types.ProviderConfig{
			"opencode-go": provider.DefaultTemplates()["opencode-go"],
			"chatgpt":     provider.DefaultTemplates()["chatgpt"],
		},
		Accounts: map[string]provider.Account{"cg": {ID: "cg", ProviderID: "chatgpt", Label: "Work"}},
	}, []types.ProviderPreset{
		{ID: "opencode-go", Name: "OpenCode Go", BaseURL: "https://opencode.ai/zen/go/v1", StandardID: "openai"},
		chatgptPreset(),
	})

	// Selecting Codex account does not switch or mutate the OpenCode template.
	model.selectAccount("cg")
	if err := model.beginAccountEdit(false, ""); err != nil {
		t.Fatal(err)
	}

	// Sign in. Update mutates the model in place (the popup HandleEvent keeps
	// the same pointer so form onChanged closures stay valid).
	_, generation := model.SignInRequest()
	_, _ = model.Update(ChatGPTSignInResultMsg{AccountID: "cg", Generation: generation, Creds: &provider.OAuthCredentials{AccessToken: "AT", Email: "u@example.com"}})

	// Keep browsing account drafts independent of template selection.
	if model.providerID != "opencode-go" {
		t.Fatalf("providerID = %q, want opencode-go", model.providerID)
	}

	// Save.
	got := model.AppSettings()

	ocg := got.Providers["opencode-go"]
	if ocg.ProviderURL != "https://opencode.ai/zen/go/v1" {
		t.Errorf("opencode-go ProviderURL = %q, want opencode-go URL", ocg.ProviderURL)
	}
	if ocg.APIStandard != "openai" {
		t.Errorf("opencode-go APIStandard = %q, want openai", ocg.APIStandard)
	}
	if got.ActiveProvider != "opencode-go" {
		t.Errorf("ActiveProvider = %q, want opencode-go", got.ActiveProvider)
	}
	// Preferences cannot commit the still-unconfirmed account edit.
	if got.Accounts["cg"].OAuth != nil || model.accountEditor.draft.OAuth == nil || model.accountEditor.draft.OAuth.Email != "u@example.com" {
		t.Fatal("provider switch committed or discarded an account draft")
	}
}
