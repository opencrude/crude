package settings

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func TestNamedAgentSpeedIsVisibleAndSaved(t *testing.T) {
	for _, width := range []int{80, 130} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			model := New(theme.DefaultDark(), types.AppSettings{}, nil)
			surface := NewSurface(theme.DefaultDark(), model)
			model.SelectCategory(catAgents)
			catalog := surface.agents
			catalog.begin("", false)
			catalog.draft.SetValue("agent-name", ui.StringValue("reviewer"))
			catalog.draft.SetValue("agent-role", ui.StringValue("researcher"))
			catalog.draft.SetValue(fieldServiceTier, ui.StringValue("fast"))
			painted := bufferPlain(paintAccountSurface(t, surface, width, 32))
			if !strings.Contains(painted, "Speed") || !strings.Contains(painted, "Fast") {
				t.Fatalf("agent model page does not show the speed control at %d columns:\n%s", width, painted)
			}
			if !catalog.apply() {
				t.Fatal("could not apply named-agent settings")
			}
			raw, err := json.Marshal(model.AppSettings().Agents.AgentProfiles["reviewer"])
			if err != nil {
				t.Fatal(err)
			}
			var profile map[string]any
			if err := json.Unmarshal(raw, &profile); err != nil {
				t.Fatal(err)
			}
			if profile["service_tier"] != "fast" {
				t.Fatalf("named-agent speed not persisted: %s", raw)
			}
			catalog.begin("profile/reviewer", false)
			if got := catalog.draft.Field(fieldServiceTier).Value.String(); got != "fast" {
				t.Fatalf("reopening named agent lost speed: %q", got)
			}
		})
	}
}

func TestAutoReviewerSpeedUsesItsOwnSetting(t *testing.T) {
	model := New(theme.DefaultDark(), types.AppSettings{}, nil)
	surface := NewSurface(theme.DefaultDark(), model)
	model.SelectCategory(catAgents)
	catalog := surface.agents
	catalog.begin("role/auto-review", false)
	catalog.draft.SetValue(fieldServiceTier, ui.StringValue("fast"))
	if !catalog.apply() {
		t.Fatal("could not apply reviewer settings")
	}
	if got := model.AppSettings().Safety.AutoPermission.ServiceTier; got != "fast" {
		t.Fatalf("reviewer speed = %q, want fast", got)
	}
	catalog.begin("role/auto-review", false)
	if got := catalog.draft.Field(fieldServiceTier).Value.String(); got != "fast" {
		t.Fatalf("reopened reviewer lost speed: %q", got)
	}
}

func TestNamedAgentSpeedWorkspaceOverrideStaysSparse(t *testing.T) {
	app := types.AppSettings{Agents: types.AgentsSettings{AgentProfiles: map[string]types.AgentProfileSettings{
		"reviewer": {Category: "researcher", Model: "review-model", Instructions: "Keep instructions"},
		"other":    {Category: "crafter", Model: "other-model"},
	}}}
	model := NewScoped(theme.DefaultDark(), app, nil, types.SettingsScopeWorkspace, app, types.WorkspaceOverrides{})
	surface := NewSurface(theme.DefaultDark(), model)
	model.SelectCategory(catAgents)
	catalog := surface.agents
	catalog.begin("profile/reviewer", false)
	catalog.draft.SetValue(fieldServiceTier, ui.StringValue("fast"))
	if !catalog.apply() {
		t.Fatal("could not apply named agent")
	}
	overrides := model.Overrides()
	effective := types.MergeAppWorkspace(app, overrides)
	if got := effective.Agents.AgentProfiles["reviewer"]; got.ServiceTier != "fast" || got.Instructions != "Keep instructions" || got.Model != "review-model" {
		t.Fatalf("workspace speed edit lost the profile: %+v", got)
	}
	if got := effective.Agents.AgentProfiles["other"]; got.Model != "other-model" || got.ServiceTier != "" {
		t.Fatalf("workspace speed edit changed another profile: %+v", got)
	}
	if app.Agents.AgentProfiles["reviewer"].ServiceTier != "" {
		t.Fatal("workspace edit mutated the application")
	}
}
