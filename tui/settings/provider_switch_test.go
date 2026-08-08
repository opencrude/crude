package settings

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
)

// TestProviderSwitchUpdatesBaseURLValue uses the exact config shape from the
// user's local ~/.crude/config.json (only openrouter, no key) and switches to
// Groq. It asserts the base URL model value changes to the Groq preset URL.
func TestProviderSwitchUpdatesBaseURLValue(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th, types.AppSettings{
		ActiveProvider: "openrouter",
		Providers: map[string]types.ProviderConfig{
			"openrouter": {},
		},
	}, []types.ProviderPreset{{ID: "openrouter", Name: "OpenRouter"}, {ID: "groq", Name: "Groq"}})

	surf := NewSurface(th, m)
	cw := ui.NewComponentWidget(th, surf, 90, 28)
	surf.SetFocusHandler(cw.Focus)
	cw.Host().Resize(90, 28)
	cw.Host().Layout()

	if got := m.value(catProviders, fieldBaseURL); got != "https://openrouter.ai/api/v1" {
		t.Fatalf("initial base URL=%q, want openrouter preset", got)
	}

	// Open the provider select (first focused field), pick Groq, commit.
	surf.FocusForm()
	cw.Host().Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	cw.Host().Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	cw.Host().Handle(tea.KeyPressMsg{Code: tea.KeyEnter})

	if got := m.value(catProviders, fieldBaseURL); got != "https://api.groq.com/openai/v1" {
		t.Fatalf("base URL after switch=%q, want groq preset", got)
	}
	if m.providerID != "groq" {
		t.Fatalf("providerID=%q, want groq", m.providerID)
	}
	if got := m.AppSettings().Providers["openrouter"].ProviderURL; got != "https://openrouter.ai/api/v1" {
		t.Fatalf("previous provider's URL not stored on switch=%q", got)
	}
}
