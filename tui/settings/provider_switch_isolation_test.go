package settings

import (
	"testing"

	"crude/provider"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
)

// TestProviderSwitchDoesNotCorruptOtherProviderConfig reproduces the reported
// bug where switching to chatgpt and back corrupted the opencode-go provider's
// base URL / standard with the chatgpt codex values.
func TestProviderSwitchDoesNotCorruptOtherProviderConfig(t *testing.T) {
	th := theme.DefaultDark()
	model := New(th, types.AppSettings{
		ActiveProvider: "opencode-go",
		Providers: map[string]types.ProviderConfig{
			"opencode-go": {Model: "deepseek-v4-flash", APIKey: "sk-opencode"},
			"chatgpt":     {},
		},
	}, []types.ProviderPreset{
		{ID: "opencode-go", Name: "OpenCode Go", BaseURL: "https://opencode.ai/zen/go/v1", StandardID: "openai"},
		chatgptPreset(),
	})

	// Switch to chatgpt.
	model.categories[catProviders].Form.SetValue(fieldProvider, ui.StringValue("chatgpt"))
	if model.providerID != "chatgpt" {
		t.Fatalf("providerID = %q, want chatgpt", model.providerID)
	}

	// Sign in. Update mutates the model in place (the popup HandleEvent keeps
	// the same pointer so form onChanged closures stay valid).
	_, _ = model.Update(ChatGPTSignInResultMsg{Creds: &provider.OAuthCredentials{AccessToken: "AT", Email: "u@example.com"}})

	// Switch back to opencode-go.
	model.categories[catProviders].Form.SetValue(fieldProvider, ui.StringValue("opencode-go"))
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
	// chatgpt OAuth must survive the round trip.
	if got.Providers["chatgpt"].OAuth == nil || got.Providers["chatgpt"].OAuth.Email != "u@example.com" {
		t.Errorf("chatgpt OAuth lost: %+v", got.Providers["chatgpt"].OAuth)
	}
}
