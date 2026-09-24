package settings

import (
	tea "charm.land/bubbletea/v2"
	"strings"
	"testing"

	"crude/provider"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
)

func TestTemplateCatalogFiltersSeedNamesWithoutHidingAdd(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th, types.AppSettings{Providers: provider.DefaultTemplates()}, nil)
	s := NewSurface(th, m)
	host := ui.NewHost(th, s)
	host.Resize(124, 28)
	host.Layout()
	host.Focus(WorkspaceSettingsTemplateFilterID)
	for _, letter := range "codex" {
		host.Handle(tea.KeyPressMsg{Code: letter, Text: string(letter)})
	}
	if rows := m.providerRows(); len(rows) != 1 || rows[0].Cells[0].Text != "Codex" {
		t.Fatalf("Codex filter selected %d rows", len(rows))
	}
	buf := surface.NewCellBuffer(124, 28)
	host.Paint(buf)
	if out := bufferPlain(buf); !strings.Contains(out, "Codex") || !strings.Contains(out, "Add template") || strings.Contains(out, "OpenAI (ChatGPT subscription)") {
		t.Fatalf("template filter or concise seed name missing:\n%s", out)
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	if len(m.providerRows()) != len(provider.DefaultTemplates()) || !s.templateScroll.Needed() {
		t.Fatal("clearing template filter did not restore scrollable catalog")
	}
}

func TestTemplateSeedCreatesIndependentNamedTemplate(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{ActiveProvider: "openrouter", Providers: map[string]types.ProviderConfig{
		"openrouter": provider.DefaultTemplates()["openrouter"],
	}}, nil)
	s := NewSurface(theme.DefaultDark(), m)
	s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.template.add"})
	if m.providerEditor == nil || len(m.providers) != 1 {
		t.Fatal("Add template mutated the table before confirmation")
	}
	m.SetValueForTest(catProviders, templateFieldSeed, "groq")
	if endpoint := m.value(catProviders, templateFieldEndpoint); endpoint != "https://api.groq.com/openai/v1" {
		t.Fatalf("seed endpoint = %q", endpoint)
	}
	m.SetValueForTest(catProviders, templateFieldName, "My Groq")
	result := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.template.commit"})
	if result.Cmd == nil {
		t.Fatalf("template not valid: %s", m.providerError)
	}
	request := result.Cmd().(TemplateChangeMsg)
	if request.Change.Before != nil || request.Change.After == nil || request.Change.After.Name != "My Groq" || len(m.providers) != 1 {
		t.Fatalf("seed clone was not a separate staged template: %+v", request.Change)
	}
	settings := types.AppSettings{Providers: map[string]types.ProviderConfig{"openrouter": m.providers["openrouter"], request.Change.ID: *request.Change.After}}
	m.Update(TemplateChangeResultMsg{Target: m, Change: request.Change, Settings: settings})
	if len(m.providers) != 2 || m.providers["openrouter"].ProviderURL != "https://openrouter.ai/api/v1" || m.providerID != request.Change.ID || m.providerEditor != nil {
		t.Fatal("confirmed template replaced its source or failed to return to overview")
	}
}

func TestTemplateEditorKeepsWireAuthAndHeadersTogether(t *testing.T) {
	seed := provider.DefaultTemplates()["openai"]
	m := New(theme.DefaultDark(), types.AppSettings{ActiveProvider: "openai", Providers: map[string]types.ProviderConfig{"openai": seed}}, nil)
	s := NewSurface(theme.DefaultDark(), m)
	s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.template.edit"})
	if m.value(catProviders, templateFieldName) != "OpenAI" {
		t.Fatal("Edit did not load the selected template name")
	}
	if m.categories[catProviders].Form.Field(fieldModel) != nil {
		t.Fatal("template editor included an agent model")
	}
	m.SetValueForTest(catProviders, templateFieldName, "Lab")
	m.SetValueForTest(catProviders, templateFieldEndpoint, "https://lab.example/v1")
	m.SetValueForTest(catProviders, templateFieldAuthType, provider.AuthCustomHeader)
	m.SetValueForTest(catProviders, templateFieldAuthHeader, "X-Lab-Key")
	m.SetValueForTest(catProviders, templateFieldHeaders, "X-Trace: enabled")
	m.SetValueForTest(catProviders, templateFieldProtocol, "anthropic")
	result := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.template.commit"})
	if result.Cmd == nil {
		t.Fatalf("template change rejected: %s", m.providerError)
	}
	change := result.Cmd().(TemplateChangeMsg).Change
	if change.Before.Name != seed.Name || change.After.Name != "Lab" || change.After.ProviderURL != "https://lab.example/v1" || change.After.APIStandard != "anthropic" || change.After.AuthType != provider.AuthCustomHeader || change.After.AuthHeader != "X-Lab-Key" || change.After.Headers["X-Trace"] != "enabled" {
		t.Fatalf("template fields lost during edit: %+v", change.After)
	}
	if m.providers["openai"].Name != seed.Name {
		t.Fatal("unconfirmed edit changed saved template")
	}
}

func TestTemplateRemovalRefusesAccountsAndLeavesTableStable(t *testing.T) {
	m := New(theme.DefaultDark(), accountSettingsFixture(), nil)
	s := NewSurface(theme.DefaultDark(), m)
	request := settingsPrompt(t, s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.template.remove"}))
	if request.Target != m || request.Kind != PromptTemplateInUse || request.ID != "openrouter" || request.RelatedCount != 2 || request.Label != "OpenRouter" {
		t.Fatalf("in-use template prompt = %+v", request)
	}
	if !s.PromptRelevant(request) || len(m.providers) != 1 || m.providerError != "" {
		t.Fatal("in-use template changed the table or became an inline error")
	}
}

func TestTemplateRemovalPromptRetainsTableAndActionsAtBothWidths(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th, accountSettingsFixture(), nil)
	s := NewSurface(th, m)
	host := ui.NewHost(th, s)
	for _, size := range []ui.Size{{W: 124, H: 28}, {W: 70, H: 18}} {
		host.Resize(size.W, size.H)
		host.Layout()
		prompt := settingsPrompt(t, s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.template.remove"}))
		if prompt.Kind != PromptTemplateInUse {
			t.Fatalf("size=%+v prompt=%+v", size, prompt)
		}
		host.MarkLayout()
		host.Layout()
		buf := surface.NewCellBuffer(size.W, size.H)
		host.Paint(buf)
		out := bufferPlain(buf)
		for _, label := range []string{"Provider templates", "Template", "OpenRouter", "[ Edit ]", "[ Clone ]", "[ Remove ]"} {
			if !strings.Contains(out, label) {
				t.Fatalf("size=%+v missing %q while prompt is pending:\n%s", size, label, out)
			}
		}
		if strings.Contains(out, "Move accounts") || strings.Contains(out, "Remove OpenRouter?") || strings.Contains(out, "Keep template") {
			t.Fatalf("size=%+v inline prompt displaced template table:\n%s", size, out)
		}
	}
}
