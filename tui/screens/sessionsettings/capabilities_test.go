package sessionsettings

import (
	"slices"
	"testing"

	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	captypes "crude/types/capability"
)

func TestSessionCapabilitySelectionStagesOnlyOwningOverrides(t *testing.T) {
	base := types.Settings{Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{"default": {SkillModes: map[string]string{"team:review": "automatic"}}}}}
	screen := New(theme.DefaultDark(), base, base, nil)
	screen.SetCapabilities(captypes.Snapshot{Entries: []captypes.Entry{{ID: "team:review", Name: "Review", Kind: captypes.KindSkill, Version: "pinned", State: "available"}}, Connections: []captypes.ConnectionState{{ID: "fixture", State: "connected", Tools: []captypes.Tool{{Name: "mcp__fixture__read", OriginalName: "read", Effect: captypes.EffectRead, Scope: "workspace", Selected: true}}}}})
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "session-settings.capabilities.open"})
	screen.formModel.SetValue("skill/team:review", ui.StringValue("manual"))
	screen.formModel.SetValue("tool/mcp__fixture__read", ui.BoolValue(false))
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "session-settings.capabilities.apply"})
	settings := screen.ToSettings()
	if settings.SessionOverrides["default"].SkillModes["team:review"] != "manual" || !slices.Contains(settings.SessionOverrides["default"].DeniedTools, "mcp__fixture__read") {
		t.Fatalf("selection lost: %#v", settings.SessionOverrides)
	}
	if base.Agents.Categories["default"].SkillModes["team:review"] != "automatic" {
		t.Fatal("session edits changed inherited map")
	}
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "session-settings.capabilities.open"})
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "session-settings.capabilities.reset"})
	if screen.overrides["default"].SkillModes != nil {
		t.Fatal("reset left explicit skill modes")
	}
}
