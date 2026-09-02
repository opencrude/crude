package settings

import (
	"testing"

	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
)

func TestSafetyFormRoundTripsSeverityRules(t *testing.T) {
	current := types.AppSettings{Safety: types.SafetySettings{
		ConfirmAt: "network", UnknownCommandRisk: "project-write",
		ScriptExecutionRisk: "safe", NotifyDangerous: "off",
		CommandRules: []types.CommandSeverityRule{{Pattern: "scripts/*.sh", Risk: "safe"}},
	}}
	model := New(theme.DefaultDark(), current, nil)
	got := model.AppSettings().Safety
	if got.ConfirmAt != "network" || got.UnknownCommandRisk != "project-write" ||
		got.ScriptExecutionRisk != "safe" || got.NotifyDangerous != "off" ||
		len(got.CommandRules) != 1 || got.CommandRules[0].Pattern != "scripts/*.sh" {
		t.Fatalf("safety settings = %#v", got)
	}
}

func TestCommandRuleValidationRejectsMalformedSeverity(t *testing.T) {
	errors := commandRulesValidator(ui.StringValue("script.sh=definitely-safe"))
	if len(errors) == 0 {
		t.Fatal("invalid severity was accepted")
	}
}
