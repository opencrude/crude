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
			// Close supersedes stale back/scope intent and discards every
			// draft without asking: Close means cancel.
			m.nextCategory, m.nextScope = catAppearance, "workspace"
			result := s.RequestClose()
			if result.Cmd == nil {
				t.Fatalf("close with a %s draft did not close", page)
			}
			if _, ok := result.Cmd().(CancelMsg); !ok {
				t.Fatalf("close with a %s draft returned %T, want CancelMsg", page, result.Cmd())
			}
			if m.nextCategory != -1 || m.nextScope != "" {
				t.Fatal("close kept a stale navigation intent")
			}
		})
	}
}

func TestSettingsCloseRejectsBusyPersistence(t *testing.T) {
	m, s, _ := capabilityFixture(t, captypes.KindMCP)
	capabilityAction(s.capCatalog, "inspect")
	capabilityAction(s.capCatalog, "edit")
	m.capabilityDraft.SetValue("name", ui.StringValue("Unsaved edit"))
	m.accountBusy = true
	if s.RequestClose().Cmd != nil {
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
