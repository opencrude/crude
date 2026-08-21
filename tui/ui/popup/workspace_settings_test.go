package popup

import (
	"testing"

	"crude/provider"
	"crude/tui/settings"
	"crude/tui/theme"
	ttypes "crude/tui/types"
	"crude/tui/ui"
)

func TestSignInResultUpdatesOpenModel(t *testing.T) {
	th := theme.DefaultDark()
	ws := NewWorkspaceSettings(th, ttypes.AppSettings{
		ActiveProvider: "chatgpt",
		Providers:      map[string]ttypes.ProviderConfig{"chatgpt": {}},
	}, []ttypes.ProviderPreset{{ID: "chatgpt", Name: "OpenAI (ChatGPT subscription)", StandardID: "codex"}})

	frame := ui.NewWindowFrameWidget(th, "settings-frame", ui.FrameWindow, "Settings", ws.Component(), ws, true, 90, 24)
	ws.SetFocusHandler(frame.Focus)
	host := frame.Host()
	host.Resize(90, 24)
	host.Layout()

	model := ws.ModelForTest()
	model.SelectCategory(0) // Providers
	host.Layout()

	if status := model.ValueForTest(0, "chatgpt-status"); status != "Not signed in" {
		t.Fatalf("initial status = %q, want 'Not signed in'", status)
	}

	// Dispatch exactly as viewmodel.forwardEventToWindowContent does.
	frame.Dispatch(settings.ChatGPTSignInResultMsg{
		Creds: &provider.OAuthCredentials{AccessToken: "AT", Email: "u@example.com"},
	})

	if status := model.ValueForTest(0, "chatgpt-status"); status != "Signed in as u@example.com" {
		t.Errorf("status after dispatch = %q, want 'Signed in as u@example.com'", status)
	}
}

// TestProviderSwitchAfterSignInKeepsModelConsistent reproduces the reported
// bug: after a sign-in result is handled (which rebuilds the provider form),
// switching the provider dropdown no longer updates the model's providerID —
// the form's onChanged closure still points at the pre-sign-in model copy.
func TestProviderSwitchAfterSignInKeepsModelConsistent(t *testing.T) {
	th := theme.DefaultDark()
	ws := NewWorkspaceSettings(th, ttypes.AppSettings{
		ActiveProvider: "opencode-go",
		Providers: map[string]ttypes.ProviderConfig{
			"opencode-go": {Model: "deepseek-v4-flash", APIKey: "sk-opencode"},
			"chatgpt":     {},
		},
	}, []ttypes.ProviderPreset{
		{ID: "opencode-go", Name: "OpenCode Go", BaseURL: "https://opencode.ai/zen/go/v1", StandardID: "openai"},
		{ID: "chatgpt", Name: "OpenAI (ChatGPT subscription)", StandardID: "codex"},
	})

	frame := ui.NewWindowFrameWidget(th, "settings-frame", ui.FrameWindow, "Settings", ws.Component(), ws, true, 90, 24)
	ws.SetFocusHandler(frame.Focus)
	host := frame.Host()
	host.Resize(90, 24)
	host.Layout()

	model := ws.ModelForTest()
	model.SelectCategory(0) // Providers
	host.Layout()

	// Switch to chatgpt.
	model.SetValueForTest(0, "provider", "chatgpt")
	if model.ProviderIDForTest() != "chatgpt" {
		t.Fatalf("providerID = %q, want chatgpt", model.ProviderIDForTest())
	}

	// Sign in (rebuilds the provider form).
	frame.Dispatch(settings.ChatGPTSignInResultMsg{
		Creds: &provider.OAuthCredentials{AccessToken: "AT", Email: "u@example.com"},
	})

	// Switch back to opencode-go. The CURRENT model (ws.model) must follow the
	// dropdown. This is the reported bug: the form's onChanged closure points at
	// the pre-sign-in model copy, so ws.model stays on chatgpt while the form
	// shows opencode-go.
	ws.ModelForTest().SetValueForTest(0, "provider", "opencode-go")
	if got := ws.ModelForTest().ProviderIDForTest(); got != "opencode-go" {
		t.Errorf("providerID after switch back = %q, want opencode-go (form onChanged closure is stale)", got)
	}

	// Saving must not corrupt the opencode-go provider with chatgpt values.
	got := ws.ModelForTest().AppSettings()
	ocg := got.Providers["opencode-go"]
	if ocg.ProviderURL != "https://opencode.ai/zen/go/v1" {
		t.Errorf("opencode-go ProviderURL = %q, want opencode-go URL", ocg.ProviderURL)
	}
	if ocg.APIStandard != "openai" {
		t.Errorf("opencode-go APIStandard = %q, want openai", ocg.APIStandard)
	}
}
