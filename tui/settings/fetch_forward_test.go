package settings

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
)

func TestAgentModelFirstFetchPaintsChoicesAndCapabilities(t *testing.T) {
	for _, currentModel := range []string{"", "vision-model"} {
		t.Run("current="+currentModel, func(t *testing.T) {
			th := theme.DefaultDark()
			m := New(th, types.AppSettings{

				Accounts: map[string]provider.Account{"work": {ID: "work", ProviderID: "openai", Label: "Work", APIKey: "test", Connection: provider.Template{ProviderURL: "https://example.test/v1", APIStandard: "openai"}}},
				Agents:   types.AgentsSettings{Categories: map[string]types.CategorySettings{"default": {AccountID: "work", Model: currentModel}}},
			}, nil)
			s := NewSurface(th, m)
			bridge := &popupBridge{m: m, surf: s}
			widget := ui.NewWindowFrameWidget(th, "settings-frame", ui.FrameWindow, "Settings", s, bridge, true, 140, 40)
			s.SetFocusHandler(widget.Focus)
			host := widget.Host()
			host.Resize(140, 40)
			host.Layout()
			s.ShowMainAgent()
			host.MarkLayout()
			host.Layout()
			c := s.agents
			c.form.FocusField(fieldModel)
			host.Focus(AgentSettingsFormID)
			result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
			if result.Cmd == nil || !c.form.Field(fieldModel).SelectOpen() {
				t.Fatal("first model dropdown open did not start discovery")
			}
			request, ok := result.Cmd().(FetchModelsMsg)
			if !ok {
				t.Fatal("dropdown did not issue an account-scoped model request")
			}
			widget.HandleEvent(ModelsMsg{ProviderID: request.ProviderID, AccountID: request.AccountID, Generation: request.Generation, Models: []types.ModelEntry{
				{ID: "text-model", Name: "Text Mini"},
				{ID: "vision-model", Name: "Sight Pro", ContextWindow: 128000, MaxOutput: 8192, Modalities: []string{provider.ModalityImage, provider.ModalityPDF}},
			}})
			buf := surface.NewCellBuffer(140, 40)
			host.Paint(buf)
			if !c.form.Field(fieldModel).SelectOpen() || !strings.Contains(bufferPlain(buf), "Text Mini") {
				t.Fatalf("first fetched choices were not painted in the open dropdown:\n%s", bufferPlain(buf))
			}
			if currentModel == "" {
				host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
			}
			host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
			host.Paint(buf)
			for _, modality := range []string{provider.ModalityImage, provider.ModalityPDF} {
				if got := c.draft.Field(fieldModalityPrefix + modality).Value.String(); got != "yes" {
					t.Errorf("fetched %s capability = %q, want yes", modality, got)
				}
			}
			if !strings.Contains(bufferPlain(buf), "128000") {
				t.Errorf("fetched context window was not painted:\n%s", bufferPlain(buf))
			}
		})
	}
}

// TestModelFetchResultClearsLoadingViaFrameForwarding simulates the full
// production path: a ModelsMsg arrives at the viewmodel, which forwards it
// through WindowFrameWidget.HandleEvent → host → (fallback) eventTarget →
// popup.WorkspaceSettings.HandleEvent → model.Update → setModels. If the
// retained form field's Loading flag is not cleared, the UI shows a
// permanent "Fetching models..." spinner.
func TestModelFetchResultClearsLoadingViaFrameForwarding(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th, types.AppSettings{

		Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
			"default": {Provider: "openai", AccountID: "work"},
		}},
		Accounts: map[string]provider.Account{"work": {ID: "work", ProviderID: "openai", Label: "Work", APIKey: "secret", Connection: provider.Template{Name: "OpenAI", ProviderURL: "https://api.example/v1", APIStandard: "openai-responses", AuthType: provider.AuthProtocol}}},
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
	// Focus the Main Agent form and open its Model select.
	surf.FocusForm()
	m.SelectCategory(catMain)
	surf.FocusForm()
	result := m.OpenSelect(fieldModel)
	if result == nil {
		t.Fatal("opening model select did not request models")
	}
	field := m.currentForm().Field(fieldModel)
	if !field.Loading {
		t.Fatal("field should be in Loading state after OpenSelect")
	}

	// Simulate the viewmodel forwarding a ModelsMsg into the window content.
	// The host must NOT claim the custom message as Handled: if it does, the
	// WindowFrameWidget returns early and the eventTarget fallback (which
	// calls model.Update → setModels) never runs, leaving Loading stuck on.
	msg := ModelsMsg{ProviderID: "openai", AccountID: "work", Generation: m.fetchGeneration[accountModelKey("openai", "work")], Models: []types.ModelEntry{{ID: "gpt-5", Name: "GPT-5"}}}
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
	if _, ok := bridge.m.models[accountModelKey("openai", "work")]; !ok {
		t.Fatal("model list not stored on the settings model after fetch result")
	}
}

// TestModelFetchErrorClearsLoadingViaFrameForwarding covers the failure path:
// a ModelsErrorMsg must also reach model.Update and clear the spinner while
// surfacing the error on the field.
func TestModelFetchErrorClearsLoadingViaFrameForwarding(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th, types.AppSettings{

		Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
			"default": {Provider: "openai", AccountID: "work"},
		}},
		Accounts: map[string]provider.Account{"work": {ID: "work", ProviderID: "openai", Label: "Work", APIKey: "secret", Connection: provider.Template{Name: "OpenAI", ProviderURL: "https://api.example/v1", APIStandard: "openai-responses", AuthType: provider.AuthProtocol}}},
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
	m.SelectCategory(catMain)
	surf.FocusForm()
	if result := m.OpenSelect(fieldModel); result == nil {
		t.Fatal("opening model select did not request models")
	}
	if !m.currentForm().Field(fieldModel).Loading {
		t.Fatal("field should be in Loading state after OpenSelect")
	}

	widget.HandleEvent(ModelsErrorMsg{ProviderID: "openai", AccountID: "work", Generation: m.fetchGeneration[accountModelKey("openai", "work")], Error: "openai requires an API key to list models"})

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
	_, cmd := b.m.Update(msg)
	b.surf.SetModel(b.m)
	if oldPane == 1 && b.m.Pane() == 0 {
		b.surf.FocusCategories()
	}
	return cmd
}
