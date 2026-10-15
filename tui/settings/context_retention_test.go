package settings

import (
	"testing"

	"crude/tui/theme"
	"crude/types"
)

func TestContextRetentionFieldDefaultsValidatesAndPersists(t *testing.T) {
	model := New(theme.DefaultDark(), types.AppSettings{}, nil)
	field := model.categories[catMain].Form.Field(fieldContextRetention)
	if field == nil || field.Value.String() != "" || field.Placeholder != "Auto" || field.Unit != "tokens" {
		t.Fatalf("retention field = %+v", field)
	}

	model.SetValueForTest(catMain, fieldContextRetention, "999")
	if !field.HasErrors() {
		t.Fatal("retention below the safe UI range was accepted")
	}
	model.SetValueForTest(catMain, fieldContextRetention, "200001")
	if !field.HasErrors() {
		t.Fatal("retention above the sensible UI range was accepted")
	}
	model.SetValueForTest(catMain, fieldContextRetention, "8000")
	if field.HasErrors() {
		t.Fatalf("valid retention rejected: %v", field.Errors)
	}
	got := model.AppSettings().Agents.Categories["default"].ContextRetentionTokens
	if got == nil || *got != 8000 {
		t.Fatalf("saved retention = %#v, want 8000", got)
	}
}

func TestContextRetentionFieldShowsConfiguredValue(t *testing.T) {
	configured := 32_000
	settings := types.AppSettings{Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"default": {ContextRetentionTokens: &configured},
	}}}
	model := New(theme.DefaultDark(), settings, nil)
	if got := model.categories[catMain].Form.Field(fieldContextRetention).Value.String(); got != "32000" {
		t.Fatalf("configured retention field = %q, want 32000", got)
	}
}
