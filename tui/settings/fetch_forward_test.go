package settings

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
)

// TestModelFetchResultClearsLoadingViaFrameForwarding simulates the full
// production path: a ModelsMsg arrives at the viewmodel, which forwards it
// through WindowFrameWidget.HandleEvent → host → (fallback) eventTarget →
// popup.WorkspaceSettings.HandleEvent → model.Update → setModels. If the
// retained form field's Loading flag is not cleared, the UI shows a
// permanent "Fetching models..." spinner.
func TestModelFetchResultClearsLoadingViaFrameForwarding(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th, types.AppSettings{
		ActiveProvider: "openai",
		Providers: map[string]types.ProviderConfig{
			"openai": {ProviderURL: "https://api.example/v1", APIKey: "secret"},
		},
	}, []types.ProviderPreset{{ID: "openai", Name: "OpenAI"}})

	// Build the real surface + popup bridge.
	surf := NewSurface(th, m)
	bridge := &popupBridge{m: m, surf: surf}
	widget := ui.NewWindowFrameWidget(th, "settings-frame", ui.FrameWindow, "Settings",
		surf, bridge, true, 90, 28)
	surf.SetFocusHandler(widget.Focus)
	host := widget.Host()
	host.Resize(90, 28)
	host.Layout()
	surf.FocusForm()
	for range 3 {
		host.Handle(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result.Cmd == nil {
		t.Fatal("opening model select did not request models")
	}
	if !surf.form.Field(fieldModel).SelectOpen() {
		t.Fatal("model select should be open while loading")
	}
	field := m.currentForm().Field(fieldModel)
	if !field.Loading {
		t.Fatal("field should be in Loading state after OpenSelect")
	}

	// Simulate the viewmodel forwarding a ModelsMsg into the window content.
	// The host must NOT claim the custom message as Handled: if it does, the
	// WindowFrameWidget returns early and the eventTarget fallback (which
	// calls model.Update → setModels) never runs, leaving Loading stuck on.
	msg := ModelsMsg{ProviderID: "openai", Models: []types.ModelEntry{{ID: "gpt-5", Name: "GPT-5"}}}
	if hostResult := host.Handle(msg); hostResult.Handled {
		t.Fatal("host claimed the ModelsMsg as handled — eventTarget fallback never runs, fetch result is swallowed")
	}

	cmd := widget.HandleEvent(msg)
	_ = cmd

	field = bridge.m.currentForm().Field(fieldModel)
	if field.Loading {
		t.Fatal("Loading flag not cleared after ModelsMsg forwarded through WindowFrameWidget — UI would show permanent Fetching")
	}
	if len(field.Options) != 1 || field.Options[0].Label != "GPT-5" {
		t.Fatalf("options=%#v, want GPT-5", field.Options)
	}
	if _, ok := bridge.m.models["openai"]; !ok {
		t.Fatal("model list not stored on the settings model after fetch result")
	}
}

// TestModelFetchErrorClearsLoadingViaFrameForwarding covers the failure path:
// a ModelsErrorMsg must also reach model.Update and clear the spinner while
// surfacing the error on the field.
func TestModelFetchErrorClearsLoadingViaFrameForwarding(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th, types.AppSettings{
		ActiveProvider: "openai",
		Providers: map[string]types.ProviderConfig{
			"openai": {ProviderURL: "https://api.example/v1", APIKey: "secret"},
		},
	}, []types.ProviderPreset{{ID: "openai", Name: "OpenAI"}})

	surf := NewSurface(th, m)
	bridge := &popupBridge{m: m, surf: surf}
	widget := ui.NewWindowFrameWidget(th, "settings-frame", ui.FrameWindow, "Settings",
		surf, bridge, true, 90, 28)
	surf.SetFocusHandler(widget.Focus)
	host := widget.Host()
	host.Resize(90, 28)
	host.Layout()
	surf.FocusForm()
	for range 3 {
		host.Handle(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	if result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}); result.Cmd == nil {
		t.Fatal("opening model select did not request models")
	}
	if !m.currentForm().Field(fieldModel).Loading {
		t.Fatal("field should be in Loading state after OpenSelect")
	}

	widget.HandleEvent(ModelsErrorMsg{ProviderID: "openai", Error: "openai requires an API key to list models"})

	field := m.currentForm().Field(fieldModel)
	if field.Loading {
		t.Fatal("Loading flag not cleared after ModelsErrorMsg — UI would show permanent Fetching")
	}
	if len(field.Errors) != 1 || !strings.Contains(field.Errors[0], "API key") {
		t.Fatalf("field errors=%#v, want API-key guidance", field.Errors)
	}
}

// popupBridge mimics popup.WorkspaceSettings.HandleEvent so the
// WindowFrameWidget eventTarget fallback calls model.Update.
type popupBridge struct {
	m    *Model
	surf *WorkspaceSettingsSurface
}

func (b *popupBridge) HandleEvent(msg tea.Msg) tea.Cmd {
	oldPane := b.m.Pane()
	updated, cmd := b.m.Update(msg)
	b.m = &updated
	b.surf.SetModel(b.m)
	if oldPane == 1 && b.m.Pane() == 0 {
		b.surf.FocusCategories()
	}
	return cmd
}