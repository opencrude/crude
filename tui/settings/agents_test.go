package settings

import (
	"slices"
	"strings"
	"testing"

	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func TestAgentCatalogCreatesStructuredProfilesWithoutMutatingDefaults(t *testing.T) {
	defaults := types.AppSettings{Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{"default": {Instructions: "Original instructions"}}, AgentProfiles: map[string]types.AgentProfileSettings{"existing": {Category: "researcher", Instructions: "Keep this"}}}}
	m := New(theme.DefaultDark(), defaults, nil)
	s := NewSurface(theme.DefaultDark(), m)
	m.SelectCategory(catAgents)
	c := s.agents
	c.begin("", false)
	c.draft.SetValue("agent-name", ui.StringValue("review-specialist"))
	c.draft.SetValue("agent-role", ui.StringValue("researcher"))
	c.draft.SetValue("agent-description", ui.StringValue("Review correctness and tests"))
	c.draft.SetValue("agent-instructions", ui.StringValue("Inspect failure paths.\nReport actionable findings."))
	c.draft.SetValue("agent-tool-mode", ui.StringValue("Custom selection"))
	if field := c.draft.Field("agent-tool/write_file"); !field.Disabled || field.Value.Bool() {
		t.Fatal("researcher editor offered a forbidden write tool")
	}
	c.draft.SetValue("agent-tool/web_fetch", ui.BoolValue(false))
	if !c.apply() {
		t.Fatal("valid structured agent draft did not apply")
	}
	got := m.AppSettings().Agents.AgentProfiles["review-specialist"]
	if got.Category != "researcher" || !strings.Contains(got.Instructions, "failure paths") || !slices.Contains(got.DeniedTools, "web_fetch") {
		t.Fatalf("agent controls were not persisted: %+v", got)
	}
	if _, ok := defaults.Agents.AgentProfiles["review-specialist"]; ok {
		t.Fatal("editing preferences mutated the saved defaults")
	}
	c.begin("role/default", false)
	c.draft.SetValue("agent-instructions", ui.StringValue("Updated system prompt"))
	c.draft.SetValue(fieldMultiAgent, ui.StringValue("on"))
	c.apply()
	if !m.AppSettings().Agents.Enabled {
		t.Fatal("Main Agent editor dropped the multi-agent control")
	}
	if defaults.Agents.Categories["default"].Instructions != "Original instructions" {
		t.Fatal("role editor mutated defaults before Save")
	}
	if m.AppSettings().Agents.Categories["default"].Instructions != "Updated system prompt" {
		t.Fatal("built-in role prompt edit was lost")
	}
}

func TestAgentCatalogDraftValidationAndDiscard(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{}, nil)
	s := NewSurface(theme.DefaultDark(), m)
	m.SelectCategory(catAgents)
	c := s.agents
	c.begin("", false)
	c.draft.SetValue("agent-name", ui.StringValue("bad name"))
	if c.apply() {
		t.Fatal("invalid name accepted")
	}
	s.cancelSettings()
	if c.draft == nil || c.confirm != "discard" {
		t.Fatal("closing discarded an unapplied agent without confirmation")
	}
	c.HandleAction(ui.EventContext{}, ui.Action{ID: "agents.discard"})
	if c.draft != nil || len(m.AppSettings().Agents.AgentProfiles) != 0 {
		t.Fatal("discard committed the draft")
	}
}

func TestAgentCatalogPaintsNativeTabsAndToolControls(t *testing.T) {
	for _, width := range []int{80, 130} {
		m := New(theme.DefaultDark(), types.AppSettings{}, nil)
		s := NewSurface(theme.DefaultDark(), m)
		m.SelectCategory(catAgents)
		if painted := bufferPlain(paintAccountSurface(t, s, width, 32)); !strings.Contains(painted, "Main Agent") {
			t.Fatal("agent catalog is not painted")
		}
		s.agents.begin("role/crafter", false)
		s.agents.setPage("tools")
		painted := bufferPlain(paintAccountSurface(t, s, width, 32))
		if !strings.Contains(painted, "Instructions") || !strings.Contains(painted, "Tool Access") || !strings.Contains(painted, "Tool access") {
			t.Fatalf("native editor missing controls at width %d:\n%s", width, painted)
		}
	}
}

func TestInactiveAgentCatalogDoesNotPaintItsActions(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{}, nil)
	s := NewSurface(theme.DefaultDark(), m)
	firstRow := strings.SplitN(bufferPlain(paintAccountSurface(t, s, 120, 32)), "\n", 2)[0]
	if strings.Contains(firstRow, "Add") || strings.Contains(firstRow, "Clone") {
		t.Fatal("hidden agent catalog painted its action bar over another settings page")
	}
}
