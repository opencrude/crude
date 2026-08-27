package settings

import (
	"testing"

	"crude/provider"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
)

func newTestModel(t *testing.T, current types.AppSettings) *Model {
	t.Helper()
	return New(theme.DefaultDark(), current, []types.ProviderPreset{
		{ID: "openai", Name: "OpenAI", StandardID: "openai-responses"},
		{ID: "anthropic", Name: "Anthropic", StandardID: "anthropic"},
	})
}

func TestProvidersFormRendersProtocolRowsByCapability(t *testing.T) {
	m := newTestModel(t, types.AppSettings{ActiveProvider: "openai"})
	form := m.categories[catProviders].Form

	// Responses protocol: store, ZDR, summaries, truncation, tier all exist.
	for _, id := range []string{fieldProtocolStore, fieldProtocolZDR, fieldProtocolSummaries, fieldProtocolTruncate, fieldProtocolTier} {
		if form.Field(id) == nil {
			t.Errorf("Responses protocol missing field %q", id)
		}
	}
	if got := form.Field(fieldProtocolStore).Value.String(); got != "default" {
		t.Errorf("store row = %q, want default", got)
	}
	if got := form.Field(fieldProtocolTier).Value.String(); got != "" {
		t.Errorf("service tier row = %q, want empty (inherit)", got)
	}

	// Truncation row: default option labeled with the wire default.
	truncate := form.Field(fieldProtocolTruncate)
	if got := truncate.Value.String(); got != "fail" {
		t.Errorf("truncation row = %q, want fail (default)", got)
	}

	// Anthropic declares only a service tier.
	m = newTestModel(t, types.AppSettings{ActiveProvider: "anthropic"})
	form = m.categories[catProviders].Form
	if form.Field(fieldProtocolStore) != nil {
		t.Error("Anthropic protocol must not expose a store row")
	}
	if form.Field(fieldProtocolTier) == nil {
		t.Error("Anthropic protocol must expose a service tier row")
	}
}

func TestProtocolSettingsRoundTripDefaultsToNil(t *testing.T) {
	m := newTestModel(t, types.AppSettings{ActiveProvider: "openai"})
	m.storeProviderForm("openai")
	if cfg := m.providers["openai"]; cfg.Protocol != nil {
		t.Fatalf("all-default protocol rows = %+v, want nil", cfg.Protocol)
	}
}

func TestProtocolSettingsRoundTripCapturesOverrides(t *testing.T) {
	m := newTestModel(t, types.AppSettings{ActiveProvider: "openai"})
	m.SetValueForTest(catProviders, fieldProtocolStore, "off")
	m.SetValueForTest(catProviders, fieldProtocolZDR, "yes")
	m.SetValueForTest(catProviders, fieldProtocolSummaries, "off")
	m.SetValueForTest(catProviders, fieldProtocolTruncate, "truncate")
	m.SetValueForTest(catProviders, fieldProtocolTier, "flex")
	m.storeProviderForm("openai")

	cfg := m.providers["openai"]
	if cfg.Protocol == nil {
		t.Fatal("overrides were not persisted")
	}
	if cfg.Protocol.Store == nil || *cfg.Protocol.Store {
		t.Errorf("Store = %v, want false", cfg.Protocol.Store)
	}
	if !cfg.Protocol.ZeroDataRetention {
		t.Error("ZeroDataRetention = false, want true")
	}
	if cfg.Protocol.ReasoningSummaries == nil || *cfg.Protocol.ReasoningSummaries {
		t.Errorf("ReasoningSummaries = %v, want false", cfg.Protocol.ReasoningSummaries)
	}
	if cfg.Protocol.Truncation == nil || !*cfg.Protocol.Truncation {
		t.Errorf("Truncation = %v, want true", cfg.Protocol.Truncation)
	}
	if cfg.Protocol.ServiceTier != "flex" {
		t.Errorf("ServiceTier = %q, want flex", cfg.Protocol.ServiceTier)
	}
}

func TestExtraBodyFieldValidatesJSON(t *testing.T) {
	m := newTestModel(t, types.AppSettings{ActiveProvider: "openai"})
	field := m.categories[catProviders].Form.Field(fieldExtraBody)
	if field == nil {
		t.Fatal("extra body field missing")
	}
	field.Validate()
	if len(field.Errors) != 0 {
		t.Errorf("blank input errored: %v", field.Errors)
	}
	field.Value = ui.StringValue(`{"top_k": 40}`)
	field.Validate()
	if len(field.Errors) != 0 {
		t.Errorf("valid JSON object errored: %v", field.Errors)
	}
	field.Value = ui.StringValue(`not json`)
	field.Validate()
	if len(field.Errors) == 0 {
		t.Error("invalid JSON did not error")
	}
	field.Value = ui.StringValue(`[1,2]`)
	field.Validate()
	if len(field.Errors) == 0 {
		t.Error("JSON array did not error; want object-only")
	}
}

func TestExtraBodyRoundTrip(t *testing.T) {
	if got := extraBodyFromForm(""); got != nil {
		t.Errorf("blank = %v, want nil", got)
	}
	decoded := extraBodyFromForm(`{"top_k":40,"nested":{"a":1}}`)
	if len(decoded) != 2 {
		t.Fatalf("decoded = %v, want 2 keys", decoded)
	}
	if _, ok := decoded["nested"].(map[string]any); !ok {
		t.Errorf("nested object decoded as %T", decoded["nested"])
	}
	if got := extraBodyFromForm("bogus"); got != nil {
		t.Errorf("invalid JSON = %v, want nil", got)
	}
}

func TestAgentFormHasCapabilityAndSamplingSections(t *testing.T) {
	m := newTestModel(t, types.AppSettings{})
	form := m.categories[catMain].Form
	for _, id := range []string{fieldCtxWindow, fieldMaxOutput, fieldTemperature, fieldTopP,
		fieldModalityPrefix + provider.ModalityImage, fieldModalityPrefix + provider.ModalityPDF,
		fieldModalityPrefix + provider.ModalityVideo, fieldModalityPrefix + provider.ModalityAudio} {
		if form.Field(id) == nil {
			t.Errorf("agent form missing field %q", id)
		}
	}
	// Defaults: blank windows, all modalities off, no sampling overrides.
	if got := form.Field(fieldCtxWindow).Value.String(); got != "" {
		t.Errorf("context window = %q, want blank", got)
	}
	if got := form.Field(fieldModalityPrefix + provider.ModalityImage).Value.String(); got != "no" {
		t.Errorf("image toggle = %q, want no", got)
	}
}

func TestCapabilityValidators(t *testing.T) {
	m := newTestModel(t, types.AppSettings{})
	form := m.categories[catMain].Form

	ctx := form.Field(fieldCtxWindow)
	ctx.Value = ui.StringValue("400000")
	ctx.Validate()
	if len(ctx.Errors) != 0 {
		t.Errorf("valid window errored: %v", ctx.Errors)
	}
	ctx.Value = ui.StringValue("-5")
	ctx.Validate()
	if len(ctx.Errors) == 0 {
		t.Error("negative window did not error")
	}

	temp := form.Field(fieldTemperature)
	temp.Value = ui.StringValue("0.7")
	temp.Validate()
	if len(temp.Errors) != 0 {
		t.Errorf("valid temperature errored: %v", temp.Errors)
	}
	temp.Value = ui.StringValue("2.5")
	temp.Validate()
	if len(temp.Errors) == 0 {
		t.Error("out-of-range temperature did not error")
	}
	temp.Value = ui.StringValue("abc")
	temp.Validate()
	if len(temp.Errors) == 0 {
		t.Error("non-numeric temperature did not error")
	}
}

func TestCollectAgentsReadsCapabilityAndSamplingFields(t *testing.T) {
	m := newTestModel(t, types.AppSettings{})
	m.SetValueForTest(catMain, fieldCtxWindow, "256000")
	m.SetValueForTest(catMain, fieldMaxOutput, "8192")
	m.SetValueForTest(catMain, fieldModalityPrefix+provider.ModalityImage, "yes")
	m.SetValueForTest(catMain, fieldModalityPrefix+provider.ModalityPDF, "yes")
	m.SetValueForTest(catMain, fieldTemperature, "0.4")
	m.SetValueForTest(catMain, fieldTopP, "0.9")

	var got types.AgentsSettings
	m.collectAgents(&got)
	cat := got.Categories["default"]
	if cat.ModelContextWindow != 256000 || cat.ModelMaxOutput != 8192 {
		t.Errorf("windows = %d/%d, want 256000/8192", cat.ModelContextWindow, cat.ModelMaxOutput)
	}
	if len(cat.ModelModalities) != 2 || cat.ModelModalities[0] != provider.ModalityImage ||
		cat.ModelModalities[1] != provider.ModalityPDF {
		t.Errorf("modalities = %v, want [image pdf]", cat.ModelModalities)
	}
	if cat.ModelTemperature == nil || *cat.ModelTemperature != 0.4 {
		t.Errorf("temperature = %v, want 0.4", cat.ModelTemperature)
	}
	if cat.ModelTopP == nil || *cat.ModelTopP != 0.9 {
		t.Errorf("top-p = %v, want 0.9", cat.ModelTopP)
	}
}

func TestAutofillCapabilitiesFromModelEntry(t *testing.T) {
	m := newTestModel(t, types.AppSettings{})
	m.setModels("openai", []types.ModelEntry{{
		ID: "gpt-test", Name: "GPT Test", ContextWindow: 128000, MaxOutput: 4096,
		Modalities: []string{provider.ModalityImage},
	}})
	m.SetValueForTest(catMain, fieldProvider, "openai")
	m.SetValueForTest(catMain, fieldModel, "gpt-test")

	form := m.categories[catMain].Form
	if got := form.Field(fieldCtxWindow).Value.String(); got != "128000" {
		t.Errorf("context window autofill = %q, want 128000", got)
	}
	if got := form.Field(fieldMaxOutput).Value.String(); got != "4096" {
		t.Errorf("max output autofill = %q, want 4096", got)
	}
	if got := form.Field(fieldModalityPrefix + provider.ModalityImage).Value.String(); got != "yes" {
		t.Errorf("image toggle autofill = %q, want yes", got)
	}
	if got := form.Field(fieldModalityPrefix + provider.ModalityAudio).Value.String(); got != "no" {
		t.Errorf("audio toggle autofill = %q, want no", got)
	}
}
