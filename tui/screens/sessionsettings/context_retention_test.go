package sessionsettings

import (
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"testing"
)

func TestRoleRetentionEditsAreValidatedAndDetached(t *testing.T) {
	value := 32000
	base := types.Settings{Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{"default": {Model: "model", ContextRetentionTokens: &value}}}}
	screen := New(theme.DefaultDark(), cloneSettings(base), base, nil)
	screen.openRole("default")
	field := screen.formModel.Field(fieldContextRetention)
	if field.Value.String() != "32000" || field.Unit != "tokens" {
		t.Fatal("missing retained budget control")
	}
	for _, invalid := range []string{"999", "200001", "no"} {
		screen.formModel.SetValue(fieldContextRetention, ui.StringValue(invalid))
		if !field.HasErrors() {
			t.Fatalf("invalid retention accepted: %s", invalid)
		}
		screen.applyRoleForm()
		if len(screen.overrides) != 0 {
			t.Fatal("invalid role draft was applied")
		}
	}
	screen.formModel.SetValue(fieldContextRetention, ui.StringValue("8000"))
	screen.applyRoleForm()
	settings := screen.ToSettings()
	got := settings.SessionOverrides["default"].ContextRetentionTokens
	if got == nil || *got != 8000 || value != 32000 {
		t.Fatal("retention override lost or mutated base")
	}
	*got = 9000
	if *screen.overrides["default"].ContextRetentionTokens != 8000 {
		t.Fatal("settings snapshot aliases draft")
	}
}
