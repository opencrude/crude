package settings

import (
	"strings"
	"testing"

	"crude/provider"
	"crude/tui/theme"
	"crude/tui/ui"
	modeltypes "crude/types"
)

func TestSafetyFormRoundTripsSeverityRules(t *testing.T) {
	current := modeltypes.AppSettings{Safety: modeltypes.SafetySettings{
		ConfirmAt: "network", UnknownCommandRisk: "project-write",
		ScriptExecutionRisk: "safe", NotifyDangerous: "off",
		CommandRules: []modeltypes.CommandSeverityRule{{Pattern: "scripts/*.sh", Risk: "safe"}},
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
	m := New(theme.DefaultDark(), modeltypes.AppSettings{
		Accounts: map[string]provider.Account{"review": {ID: "review", ProviderID: "openai", Label: "Review", Connection: provider.DefaultTemplates()["openai"]}},
		Safety: modeltypes.SafetySettings{
			ConfirmAt: "network", UnknownCommandRisk: "unknown", NotifyDangerous: "on",
			AutoPermission: modeltypes.AutoPermissionSettings{Enabled: "on", AccountID: "review", Provider: "openai", Model: "review-v1",
				Instructions: "No deployments", DenyMode: "deny", UnresolvedMode: "deny", MaxOutputTokens: 55, MaxInputBytes: 64 << 10},
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
	if policy.Field(fieldAutoPermThinking) == nil || policy.Field(fieldAutoPermRetries) == nil || policy.Field(fieldAutoPermGrowth) == nil || policy.Field(fieldAutoPermRetryCeiling) == nil {
		t.Fatal("Auto Review policy omits configurable reasoning or recovery controls")
	}
	m.SetValueForTest(catSafety, fieldSafetyConfirmAt, "destructive")
	s := NewSurface(theme.DefaultDark(), m)
	m.SelectCategory(catAgents)
	s.agents.begin("role/auto-review", false)
	if s.agents.draft.Field("agent-tool-mode") != nil || s.agents.draft.Field("agent-description") == nil || !s.agents.draft.Field("agent-description").ReadOnly {
		t.Fatal("Auto Review Agent must show purpose but cannot execute/configure tools")
	}
	if s.agents.draft.Field(fieldAutoPermThinking) == nil || s.agents.draft.Field(fieldAutoPermRetries) == nil || s.agents.draft.Field(fieldAutoPermGrowth) == nil {
		t.Fatal("Auto Review Agent editor does not own its runtime controls")
	}
	s.agents.setPage("tools")
	if s.agents.form.Field(fieldAutoPermThinking) == nil || s.agents.form.Field(fieldAutoPermRetryCeiling) == nil {
		t.Fatal("Auto Review Review tab did not mount its policy fields")
	}
	s.agents.draft.SetValue("agent-instructions", ui.StringValue("Never send builds"))
	s.agents.draft.SetValue(fieldModel, ui.StringValue("review-v2"))
	if !s.agents.apply() {
		t.Fatal("editing the reviewer identity failed")
	}
	got := m.AppSettings().Safety
	if got.ConfirmAt != "destructive" || got.AutoPermission.Model != "review-v2" || got.AutoPermission.Instructions != "Never send builds" || got.AutoPermission.DenyMode != "deny" || got.AutoPermission.UnresolvedMode != "deny" {
		t.Fatalf("one Security page erased another's draft: %+v", got)
	}
	// The two pages share safety.auto_permission. Each page reset removes only
	// what that page edits: the policy page never clears the reviewer
	// identity, and the workspace agent catalog never clears the policy.
	policyPage, _ := pageOwnershipFor(catAutoReviewPolicy, modeltypes.SettingsScopeApplication)
	agents, _ := pageOwnershipFor(catAgents, modeltypes.SettingsScopeWorkspace)
	for _, field := range []string{"enabled", "account_id", "model", "provider", "instructions"} {
		path := []string{"safety", "auto_permission", field}
		if policyPage.owns(path) || !agents.owns(path) {
			t.Fatalf("reviewer identity %s belongs to Agents, not the policy page", field)
		}
	}
	for _, field := range []string{"deny_mode", "unresolved_mode", "timeout_seconds"} {
		path := []string{"safety", "auto_permission", field}
		if !policyPage.owns(path) || agents.owns(path) {
			t.Fatalf("policy %s belongs to Auto Review Policy, not Agents", field)
		}
	}
	for _, edit := range policyPage.resetEdits() {
		if agents.owns(edit.Path) {
			t.Fatalf("policy reset removes reviewer identity %v", edit.Path)
		}
	}
}

func TestAutoReviewerRejectsNonsensicalRecoveryBounds(t *testing.T) {
	m := New(theme.DefaultDark(), modeltypes.AppSettings{}, nil)
	s := NewSurface(theme.DefaultDark(), m)
	m.SelectCategory(catAgents)
	c := s.agents
	c.begin("role/auto-review", false)
	c.draft.SetValue(fieldAutoPermOutputTokens, ui.StringValue("512"))
	c.draft.SetValue(fieldAutoPermRetries, ui.StringValue("2"))
	c.draft.SetValue(fieldAutoPermGrowth, ui.StringValue(modeltypes.AutoPermissionGrowth4x))
	c.draft.SetValue(fieldAutoPermRetryCeiling, ui.StringValue("128"))
	if c.apply() {
		t.Fatal("retry ceiling below the initial output budget was accepted")
	}
	if field := c.draft.Field(fieldAutoPermRetryCeiling); field == nil || len(field.Errors) == 0 || !strings.Contains(field.Errors[0], "at least") {
		t.Fatalf("retry ceiling warning = %+v", field)
	}
}

func TestAutoReviewerCannotEnableWithoutExplicitConnectionOrBypassRestrictionsCap(t *testing.T) {
	m := New(theme.DefaultDark(), modeltypes.AppSettings{}, nil)
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
