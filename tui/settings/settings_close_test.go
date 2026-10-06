package settings

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
	"crude/tui/ui"
	captypes "crude/types/capability"
)

func TestSettingsCloseCoversEveryEditorDraft(t *testing.T) {
	for _, page := range []string{"account", "agent", "skill", "MCP", "source", "grant"} {
		t.Run(page, func(t *testing.T) {
			var m *Model
			var s *WorkspaceSettingsSurface
			var change func(string)
			th := theme.DefaultDark()
			switch page {
			case "account", "agent":
				m = New(th, accountSettingsFixture(), nil)
				s = NewSurface(th, m)
				switch page {
				case "account":
					s.ShowAccounts()
					s.openAccountEditor(false, "")
					change = func(value string) { m.SetValueForTest(catAccounts, fieldAccountLabel, value) }
				case "agent":
					s.ShowMainAgent()
					change = func(value string) { s.agents.draft.SetValue("agent-instructions", ui.StringValue(value)) }
				}
			default:
				kind := captypes.KindSkill
				if page == "MCP" || page == "grant" {
					kind = captypes.KindMCP
				}
				m, s, _ = capabilityFixture(t, kind)
				catalog := s.capCatalog
				if page == "source" {
					capabilityAction(catalog, "sources")
					capabilityAction(catalog, "inspect")
				} else {
					capabilityAction(catalog, "inspect")
					if page == "grant" {
						catalog.detailForm("tools")
						capabilityAction(catalog, "inspect-item")
						capabilityAction(catalog, "review-grant")
					} else {
						capabilityAction(catalog, "edit")
					}
				}
				field := "name"
				if page == "grant" {
					field = "grant-scope"
				}
				if m.capabilityDraft == nil || m.capabilityDraft.Field(field) == nil {
					t.Fatal("fixture did not open the requested editor")
				}
				change = func(value string) { m.capabilityDraft.SetValue(field, ui.StringValue(value)) }
			}
			change("Unsaved edit")
			title := s.PaneTitle()
			before := bufferPlain(paintAccountSurface(t, s, 124, 28))
			// Close must supersede stale back/scope intent, not confirm it.
			m.nextCategory, m.nextScope = catAppearance, "workspace"
			prompt := settingsPrompt(t, s.RequestClose())
			if prompt.Kind != PromptDiscardSettings || !s.PromptRelevant(prompt) {
				t.Fatalf("dirty close did not capture %s draft: %+v", page, prompt)
			}
			if s.PaneTitle() != title || bufferPlain(paintAccountSurface(t, s, 124, 28)) != before {
				t.Fatal("close navigated or changed the retained editor before a decision")
			}
			s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.discard.stay"})
			if !s.PromptRelevant(prompt) || s.PaneTitle() != title {
				t.Fatal("staying lost an unsaved editor draft")
			}
			// A close decision covers preferences as well as the nested draft.
			m.SetValueForTest(catAppearance, "preset", "light")
			if s.PromptRelevant(prompt) {
				t.Fatal("close prompt accepted newer preference edits")
			}
			prompt = settingsPrompt(t, s.RequestClose())
			change("Later edit")
			if s.PromptRelevant(prompt) {
				t.Fatal("close prompt accepted a newer editor draft")
			}
			prompt = settingsPrompt(t, s.RequestClose())
			if !s.PromptRelevant(prompt) {
				t.Fatal("unchanged close prompt became stale")
			}
			result := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.discard.confirm", Payload: prompt.Kind})
			if result.Cmd == nil {
				t.Fatal("confirmed close returned to a parent page")
			}
			if _, ok := result.Cmd().(CancelMsg); !ok || s.PaneTitle() != title {
				t.Fatal("confirmed close navigated or switched Settings scope")
			}
		})
	}
}

func TestSettingsCloseRejectsBusyAndReplacedDraft(t *testing.T) {
	m, s, _ := capabilityFixture(t, captypes.KindMCP)
	capabilityAction(s.capCatalog, "inspect")
	capabilityAction(s.capCatalog, "edit")
	m.capabilityDraft.SetValue("name", ui.StringValue("Unsaved edit"))
	prompt := settingsPrompt(t, s.RequestClose())
	values := formDraftBytes(m.capabilityDraft)
	original := m.capabilityDraft
	s.capCatalog.editForm()
	m.capabilityDraft.SetValue("name", ui.StringValue("Unsaved edit"))
	if string(formDraftBytes(m.capabilityDraft)) != string(values) || s.PromptRelevant(prompt) {
		t.Fatal("close prompt accepted a replacement draft with identical text")
	}
	m.capabilityDraft = original
	m.accountBusy = true
	if s.PromptRelevant(prompt) || s.RequestClose().Cmd != nil {
		t.Fatal("close raced an account/template persistence operation")
	}
}

func TestSettingsFooterEscapeStillNavigatesBack(t *testing.T) {
	m, s, host := capabilityFixture(t, captypes.KindMCP)
	capabilityAction(s.capCatalog, "inspect")
	host.MarkLayout()
	host.Layout()
	s.FocusActions()
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	if result.Cmd != nil || s.capCatalog.detail || m.SelectedCategory() != catMCP {
		t.Fatal("footer Esc closed Settings instead of navigating back")
	}
}
