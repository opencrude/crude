package session

import (
	"reflect"
	"strings"
	"testing"

	"crude/agent"
)

// categoryFieldRole classifies every CategoryConfig field. "provider" fields
// are request settings a child's provider must be built with; every other
// role names where the field is honored instead. Children once silently lost
// their thinking effort and sampling because a helper built providers from
// account and model alone. A new field must be classified here, so it cannot
// be dropped without a decision.
var categoryFieldRole = map[string]string{
	"AccountID":                "provider",
	"Model":                    "provider",
	"ThinkingEffort":           "provider",
	"ModelTemperature":         "provider",
	"ModelTopP":                "provider",
	"ModelContextWindow":       "provider",
	"Enabled":                  "launch gate",
	"Inherit":                  "resolution marker",
	"Policy":                   "tool policy",
	"Description":              "profile text",
	"Instructions":             "prompt",
	"AllowedTools":             "tool policy",
	"DeniedTools":              "tool policy",
	"SkillModes":               "capability policy",
	"ModelInputPrice":          "accounting",
	"ModelOutputPrice":         "accounting",
	"ModelCachePrice":          "accounting",
	"ModelModalities":          "media read policy",
	"ModelSupportsTools":       "tool shaping",
	"ModelMaxOutput":           "agent output cap",
	"ContextCompactionPercent": "agent compaction",
	"ContextRetentionTokens":   "agent compaction",
}

func TestEveryCategorySettingIsClassifiedForChildProviders(t *testing.T) {
	fields := reflect.TypeOf(CategoryConfig{})
	seen := make(map[string]bool, fields.NumField())
	for i := 0; i < fields.NumField(); i++ {
		name := fields.Field(i).Name
		seen[name] = true
		if _, ok := categoryFieldRole[name]; !ok {
			t.Errorf("CategoryConfig.%s is unclassified: decide whether child providers must carry it (childProviderConfig) and record its role here", name)
		}
	}
	for name := range categoryFieldRole {
		if !seen[name] {
			t.Errorf("categoryFieldRole lists %s, which CategoryConfig no longer has", name)
		}
	}
}

// fullChildSpec sets every provider-role setting to a distinct non-zero value.
func fullChildSpec() agent.SubAgentSpec {
	temperature, topP := 0.25, 0.75
	return agent.SubAgentSpec{
		ID: "child", Category: CategoryCrafter, AccountID: "account", Model: "child-model",
		ThinkingEffort: "high", ModelTemperature: &temperature, ModelTopP: &topP, ModelContextWindow: 123456,
	}
}

func TestChildProviderConfigCarriesEveryRequestSetting(t *testing.T) {
	selection := reflect.ValueOf(childProviderConfig(fullChildSpec()))
	for name, role := range categoryFieldRole {
		if role == "provider" && selection.FieldByName(name).IsZero() {
			t.Errorf("childProviderConfig dropped provider setting %s", name)
		}
	}
	p, err := modelAttributionConfig("http://127.0.0.1:1", "root-model").providerForCategory(childProviderConfig(fullChildSpec()))
	if err != nil {
		t.Fatal(err)
	}
	sampling := p.Sampling()
	if p.Model != "child-model" || p.AccountID != "account" || p.ThinkingEffort() != "high" ||
		sampling.Temperature == nil || *sampling.Temperature != 0.25 || sampling.TopP == nil || *sampling.TopP != 0.75 ||
		p.ContextWindow("child-model") != 123456 {
		t.Fatalf("built provider lost a request setting: model=%q account=%q effort=%q sampling=%+v window=%d",
			p.Model, p.AccountID, p.ThinkingEffort(), sampling, p.ContextWindow("child-model"))
	}
}

func TestApplyConnectionKeepsOrResetsModelSpecificSettings(t *testing.T) {
	spec := fullChildSpec()
	applyConnection(&spec, childConnection{AccountID: "account", Model: "child-model", ThinkingEffort: "low"})
	if spec.ThinkingEffort != "low" || spec.ModelTemperature == nil || spec.ModelContextWindow != 123456 {
		t.Fatalf("effort-only change dropped the same model's settings: %+v", spec)
	}
	applyConnection(&spec, childConnection{AccountID: "account", Model: "other-model", ThinkingEffort: "low"})
	if spec.Model != "other-model" || spec.ModelTemperature != nil || spec.ModelTopP != nil || spec.ModelContextWindow != 0 {
		t.Fatalf("a different model kept settings that described the previous one: %+v", spec)
	}
}

func TestConnectionRequestValidatesEffortAndKeepsUnsetFields(t *testing.T) {
	if _, err := parseConnectionRequest(map[string]any{"thinking_effort": "turbo"}); err == nil || !strings.Contains(err.Error(), "xhigh") {
		t.Fatalf("unknown effort accepted or unexplained: %v", err)
	}
	request, err := parseConnectionRequest(map[string]any{"thinking_effort": "DEFAULT"})
	if err != nil {
		t.Fatal(err)
	}
	current := childConnection{AccountID: "account", Model: "m", ThinkingEffort: "max"}
	if next := request.resolve(current, "medium"); next != (childConnection{AccountID: "account", Model: "m", ThinkingEffort: "medium"}) {
		t.Fatalf("default did not restore the role effort or changed other fields: %+v", next)
	}
	request, _ = parseConnectionRequest(map[string]any{"model": "m2"})
	if next := request.resolve(current, "medium"); next.Model != "m2" || next.ThinkingEffort != "max" || next.AccountID != "account" {
		t.Fatalf("unset fields changed: %+v", next)
	}
	if empty, _ := parseConnectionRequest(map[string]any{"agent_id": "x"}); !empty.empty() {
		t.Fatal("a request without changes was not reported empty")
	}
}
