package settings

import (
	"strings"
	"testing"

	"crude/provider"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
	modeltypes "crude/types"
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

func TestSecurityPagesKeepReviewerIdentityAndPolicyIndependent(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{
		Accounts: map[string]provider.Account{"review": {ID: "review", ProviderID: "openai", Label: "Review"}},
		Safety: types.SafetySettings{
			ConfirmAt: "network", UnknownCommandRisk: "unknown", NotifyDangerous: "on",
			AutoPermission: modeltypes.AutoPermissionSettings{Enabled: "on", AccountID: "review", Provider: "openai", Model: "review-v1",
				Instructions: "No deployments", DenyMode: "deny", MaxOutputTokens: 55, MaxInputBytes: 2048, MaxReviewsPerTurn: 7},
		},
	}, nil)
	for _, page := range []int{catSafety, catAutoReviewPolicy} {
		if m.categories[page].Group != "Security" {
			t.Fatalf("%q is outside the Security category group", m.categories[page].Name)
		}
	}
	securityPages := 0
	for _, category := range m.categories {
		if category.Group == "Security" {
			securityPages++
			if category.Name == "Protections" {
				t.Fatal("non-interactive Protections page returned to the settings rail")
			}
		}
	}
	if securityPages != 2 {
		t.Fatalf("Security has %d pages, want Command Safety and Auto Review Policy", securityPages)
	}
	if m.categories[catSafety].Form.Field(fieldAutoPermDenyMode) != nil || m.categories[catAutoReviewPolicy].Form.Field(fieldSafetyRules) != nil {
		t.Fatal("Command Safety and Auto Review Policy are duplicated rather than separate")
	}
	policy := m.categories[catAutoReviewPolicy].Form
	if detail := policy.Field(fieldAutoPermDenyMode).Description; !strings.Contains(detail, "permanent grant") {
		t.Fatalf("review outcome omits the human-only grant boundary: %q", detail)
	}
	if detail := policy.Field(fieldAutoPermInputBytes).Description; !strings.Contains(detail, "conversation") {
		t.Fatalf("input budget omits the remote payload boundary: %q", detail)
	}
	m.SetValueForTest(catSafety, fieldSafetyConfirmAt, "destructive")
	m.SetValueForTest(catAutoReviewPolicy, fieldAutoPermReviews, "3")
	s := NewSurface(theme.DefaultDark(), m)
	m.SelectCategory(catAgents)
	s.agents.begin("role/auto-review", false)
	if s.agents.draft.Field("agent-tool-mode") != nil || s.agents.draft.Field("agent-description") == nil || !s.agents.draft.Field("agent-description").ReadOnly {
		t.Fatal("Auto Review Agent must show purpose but cannot execute/configure tools")
	}
	s.agents.draft.SetValue("agent-instructions", ui.StringValue("Never send builds"))
	s.agents.draft.SetValue(fieldModel, ui.StringValue("review-v2"))
	if !s.agents.apply() {
		t.Fatal("editing the reviewer identity failed")
	}
	got := m.AppSettings().Safety
	if got.ConfirmAt != "destructive" || got.AutoPermission.Model != "review-v2" || got.AutoPermission.Instructions != "Never send builds" || got.AutoPermission.MaxReviewsPerTurn != 3 || got.AutoPermission.DenyMode != "deny" {
		t.Fatalf("one Security page erased another's draft: %+v", got)
	}
	for index, row := range s.agents.table.Rows() {
		if row.ID == "role/auto-review" {
			s.agents.table.SelectIndex(index)
		}
	}
	s.agents.HandleAction(ui.EventContext{}, ui.Action{ID: "agents.reset"})
	s.agents.HandleAction(ui.EventContext{}, ui.Action{ID: "agents.reset-confirm"})
	reset := m.AppSettings().Safety.AutoPermission
	if reset.Enabled != "off" || reset.Model != "" || reset.AccountID != "" || reset.Instructions != "" || reset.DenyMode != "deny" || reset.MaxReviewsPerTurn != 3 {
		t.Fatalf("resetting reviewer identity changed its bounded policy: %+v", reset)
	}
}

func TestAutoReviewerCannotEnableWithoutExplicitConnectionOrBypassRestrictionsCap(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{}, nil)
	s := NewSurface(theme.DefaultDark(), m)
	m.SelectCategory(catAgents)
	c := s.agents
	c.begin("role/auto-review", false)
	c.draft.SetValue("agent-enabled", ui.StringValue("on"))
	if c.apply() || c.draft == nil || len(c.draft.Field(fieldConnection).Errors) == 0 {
		t.Fatal("unconfigured reviewer was enabled without an explicit account/model")
	}
	c.draft.SetValue("agent-enabled", ui.StringValue("off"))
	c.draft.SetValue("agent-instructions", ui.StringValue(strings.Repeat("x", 1025)))
	if c.apply() {
		t.Fatal("instructions exceeding the backend cap were accepted")
	}
}
