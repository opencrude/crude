package sessionsettings

import (
	"slices"
	"strings"
	"testing"

	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func TestNamedAgentsAppearAndCanSetSessionSpeed(t *testing.T) {
	base := types.Settings{Agents: types.AgentsSettings{
		Categories: map[string]types.CategorySettings{
			"default":    {Model: "main"},
			"researcher": {Model: "role-model", ServiceTier: "standard"},
		},
		AgentProfiles: map[string]types.AgentProfileSettings{
			"reviewer": {Category: "researcher", Model: "review-model"},
			"writer":   {Category: "crafter", Model: "writer-model"},
		},
	}}
	screen := New(theme.DefaultDark(), base, base, nil)
	row := screen.formModel.Field("profile/reviewer")
	if row == nil || row.Label != "reviewer" || row.Columns[0] != "review-model" {
		t.Fatalf("named agent not present with its own model: %#v", row)
	}
	screen.openRole("profile/reviewer")
	if screen.page != rolePage || !strings.Contains(screen.title(screen.roleName()), "reviewer") {
		t.Fatalf("named-agent row did not open its own settings: page=%v role=%q", screen.page, screen.role)
	}
	if got := screen.actions.ButtonLabels(); !slices.Equal(got, []string{"Apply", "Back", "Reset"}) {
		t.Fatalf("named-agent actions duplicate reset/inheritance: %v", got)
	}
	speed := screen.formModel.Field("service-tier")
	if speed == nil {
		t.Fatal("session agent settings have no speed control")
	}
	screen.formModel.SetValue("service-tier", ui.StringValue("fast"))
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "form.changed"})
	if speed.Inherited {
		t.Fatal("explicit speed was still labelled inherited")
	}
	screen.applyRoleForm()
	saved := screen.ToSettings()
	if saved.SessionOverrides["profile/reviewer"].ServiceTier != "fast" {
		t.Fatalf("named agent's speed was not saved separately: %+v", saved.SessionOverrides)
	}
	if saved.SessionOverrides["researcher"].ServiceTier != "" || saved.SessionOverrides["profile/writer"].ServiceTier != "" {
		t.Fatalf("named agent's edit leaked to another agent: %+v", saved.SessionOverrides)
	}
	screen.openRole("profile/reviewer")
	if got := screen.formModel.Field("service-tier").Value.String(); got != "fast" {
		t.Fatalf("reopening named agent lost speed: %q", got)
	}
	screen.formModel.SetValue("service-tier", ui.StringValue(""))
	screen.applyRoleForm()
	if got := screen.ToSettings().SessionOverrides["profile/reviewer"].ServiceTier; got != "" {
		t.Fatalf("inherit did not clear the named-agent speed override: %q", got)
	}
}

func TestSessionBuiltInAgentHasSpeedControl(t *testing.T) {
	base := types.Settings{Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"default": {Model: "main"},
	}}}
	screen := New(theme.DefaultDark(), base, base, nil)
	screen.openRole("default")
	if screen.formModel.Field("service-tier") == nil {
		t.Fatal("main-agent session settings have no speed control")
	}
	screen.formModel.SetValue("service-tier", ui.StringValue("fast"))
	screen.applyRoleForm()
	if got := screen.ToSettings().SessionOverrides["default"].ServiceTier; got != "fast" {
		t.Fatalf("main-agent speed = %q, want fast", got)
	}
}

func TestNamedAgentRowsTrackRefreshedCatalog(t *testing.T) {
	base := types.Settings{Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"default": {Model: "main"},
	}}}
	screen := New(theme.DefaultDark(), base, base, nil)
	if screen.formModel.Field("profile/reviewer") != nil {
		t.Fatal("absent agent appeared in settings")
	}
	base.Agents.AgentProfiles = map[string]types.AgentProfileSettings{
		"reviewer": {Category: "researcher", Model: "review"},
	}
	screen.SetInheritance(base, base, nil)
	if row := screen.formModel.Field("profile/reviewer"); row == nil || row.Label != "reviewer" {
		t.Fatalf("new agent missing from refreshed settings: %+v", row)
	}
	base.Agents.AgentProfiles = nil
	screen.SetInheritance(base, base, nil)
	if screen.formModel.Field("profile/reviewer") != nil {
		t.Fatal("removed agent remained visible after refresh")
	}
}

func TestNamedAgentSpeedSurvivesModelPickerNavigation(t *testing.T) {
	base := types.Settings{Agents: types.AgentsSettings{
		Categories: map[string]types.CategorySettings{"researcher": {Model: "role"}},
		AgentProfiles: map[string]types.AgentProfileSettings{
			"reviewer": {Category: "researcher", Model: "first"},
		},
	}}
	screen := New(theme.DefaultDark(), base, base, nil)
	screen.openRole("profile/reviewer")
	screen.formModel.SetValue(fieldServiceTier, ui.StringValue("fast"))
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "session-settings.model.open"})
	if screen.page != modelPage {
		t.Fatal("model picker did not open")
	}
	screen.applyModelToEffective("second")
	screen.openRole("profile/reviewer")
	if got := screen.formModel.Field(fieldServiceTier).Value.String(); got != "fast" {
		t.Fatalf("navigating the picker lost staged speed: %q", got)
	}
	if got := screen.formModel.Field(fieldModel).Value.String(); got != "second" {
		t.Fatalf("navigating the picker lost the selected model: %q", got)
	}
}

func TestMemorySpeedOnlyShowsInheritedModel(t *testing.T) {
	base := types.Settings{
		Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{"default": {Model: "main"}}},
		Memory: types.MemorySettings{MemoryCategory: types.CategorySettings{ServiceTier: "fast"}},
	}
	screen := New(theme.DefaultDark(), base, base, nil)
	screen.openRole("memory")
	if got := screen.formModel.Field(fieldModel).Value.String(); got != "main" {
		t.Fatalf("memory speed-only preference hid the inherited model: %q", got)
	}
	if label := screen.formModel.Field(fieldServiceTier).Options[0].Label; !strings.Contains(label, "Fast") {
		t.Fatalf("memory inherited speed is not visible: %q", label)
	}
}
