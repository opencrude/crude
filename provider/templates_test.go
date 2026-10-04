package provider

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestEmbeddedTemplatesSupplyPresetAdaptersAndProtocolDefaults(t *testing.T) {
	if len(DefaultTemplates()) != 20 {
		t.Fatalf("default template count = %d, want 20", len(DefaultTemplates()))
	}
	if name := DefaultTemplates()["chatgpt"].Name; name != "Codex" {
		t.Fatalf("subscription template name = %q, want Codex", name)
	}
	for _, preset := range PresetList() {
		if preset.ID == "" || preset.Name == "" {
			t.Fatalf("incomplete embedded template adapter: %#v", preset)
		}
		if preset.ID != "custom" && preset.BaseURL == "" {
			t.Errorf("template %q has no endpoint", preset.ID)
		}
		if got := DefaultProtocolID(preset.ID); got != preset.StandardID {
			t.Errorf("%s default protocol = %q, want %q", preset.ID, got, preset.StandardID)
		}
	}
	if got := FromPreset("chatgpt", "", "").ProtocolID(); got != "codex" {
		t.Fatalf("ChatGPT template protocol = %q, want codex", got)
	}
	if got := FromPreset("openai", "transient-key", "").APIKey; got != "transient-key" {
		t.Fatalf("transient CLI key = %q", got)
	}
	if got := FromPreset("unknown", "key", "").ID; got != "openrouter" {
		t.Fatalf("unknown preset fallback = %q", got)
	}
}

func TestTemplateAndAccountOverrideClonesAreDetached(t *testing.T) {
	template := Template{
		Headers: map[string]string{"X-Header": "template"},
		Dialer:  &DialSettings{MaxAttempts: 3},
		Protocol: &ProtocolSettings{
			Store: boolPointer(false), MediaReplayRecovery: boolPointer(true),
		},
		ExtraBody: map[string]any{"nested": map[string]any{"value": "template"}},
	}
	clone := CloneTemplate(template)
	clone.Headers["X-Header"] = "changed"
	clone.Dialer.MaxAttempts = 8
	*clone.Protocol.Store = true
	*clone.Protocol.MediaReplayRecovery = false
	clone.ExtraBody["nested"].(map[string]any)["value"] = "changed"
	if template.Headers["X-Header"] != "template" || template.Dialer.MaxAttempts != 3 || *template.Protocol.Store ||
		!*template.Protocol.MediaReplayRecovery ||
		template.ExtraBody["nested"].(map[string]any)["value"] != "template" {
		t.Fatal("template clone shares mutable data")
	}

	extra := map[string]any{"flag": true}
	recovery := false
	account := Account{ID: "work", ProviderID: "openrouter", APIKey: "secret", Overrides: TemplateOverrides{
		ExtraBody: &extra, Protocol: &ProtocolSettings{MediaReplayRecovery: &recovery},
	}}
	accountClone := CloneAccount(account)
	(*accountClone.Overrides.ExtraBody)["flag"] = false
	*accountClone.Overrides.Protocol.MediaReplayRecovery = true
	accountClone.APIKey = "changed"
	if (*account.Overrides.ExtraBody)["flag"] != true || *account.Overrides.Protocol.MediaReplayRecovery || account.APIKey != "secret" {
		t.Fatal("account clone shares credential or override data")
	}
}

func TestResolveTemplateHonorsExplicitEmptyAccountOverrides(t *testing.T) {
	base := DefaultTemplates()["openrouter"]
	emptyHeaders := map[string]string{}
	emptyExtra := map[string]any{}
	emptyEndpoint := ""
	resolved := ResolveTemplate("openrouter", base, TemplateOverrides{
		ProviderURL: &emptyEndpoint,
		Headers:     &emptyHeaders,
		ExtraBody:   &emptyExtra,
	})
	if resolved.ProviderURL != "" || resolved.Headers == nil || len(resolved.Headers) != 0 ||
		resolved.ExtraBody == nil || len(resolved.ExtraBody) != 0 {
		t.Fatalf("explicit empty overrides inherited template values: %#v", resolved)
	}
}

func TestTemplateAuthModesBuildExpectedHeaders(t *testing.T) {
	tests := []struct {
		name       string
		authType   string
		standard   string
		authHeader string
		wantAuth   string
		wantAPIKey string
	}{
		{name: "protocol auth", authType: AuthProtocol, standard: "anthropic", wantAPIKey: "secret"},
		{name: "bearer", authType: AuthBearer, standard: "anthropic", wantAuth: "Bearer secret"},
		{name: "custom header", authType: AuthCustomHeader, standard: "openai", authHeader: "X-Access-Key"},
		{name: "none", authType: AuthNone, standard: "openai"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := &Provider{ID: "custom", APIKey: "secret"}
			template := Template{
				Name: "Test", ProviderURL: "https://example.test/v1", APIStandard: test.standard,
				AuthType: test.authType, AuthHeader: test.authHeader,
				Headers: map[string]string{"X-Template": "present"},
			}
			if err := template.Apply(p); err != nil {
				t.Fatal(err)
			}
			headers, err := p.RequestHeaders(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got := headers.Get("Authorization"); got != test.wantAuth {
				t.Errorf("Authorization = %q, want %q", got, test.wantAuth)
			}
			if got := headers.Get("x-api-key"); got != test.wantAPIKey {
				t.Errorf("x-api-key = %q, want %q", got, test.wantAPIKey)
			}
			if test.authHeader != "" && headers.Get(test.authHeader) != "secret" {
				t.Errorf("custom auth header = %q", headers.Get(test.authHeader))
			}
			if got := headers.Get("X-Template"); got != "present" {
				t.Errorf("extra header = %q", got)
			}
		})
	}
}

func TestTemplateValidationRejectsUnsafeOrIncompatibleSettings(t *testing.T) {
	valid := Template{Name: "Gateway", ProviderURL: "https://example.test/v1", APIStandard: "openai", AuthType: AuthCustomHeader, AuthHeader: "X-Token"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid template: %v", err)
	}
	for _, test := range []struct {
		name string
		edit func(*Template)
	}{
		{name: "embedded credentials", edit: func(template *Template) { template.ProviderURL = "https://user:pass@example.test" }},
		{name: "unknown protocol", edit: func(template *Template) { template.APIStandard = "unknown" }},
		{name: "unsupported chatgpt protocol", edit: func(template *Template) { template.AuthType = AuthChatGPT }},
		{name: "auth header in extras", edit: func(template *Template) { template.Headers = map[string]string{"X-Token": "override"} }},
		{name: "header injection", edit: func(template *Template) { template.Headers = map[string]string{"X-Other": "safe\r\nInjected: yes"} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			template := CloneTemplate(valid)
			test.edit(&template)
			if err := template.Validate(); err == nil {
				t.Fatal("invalid template was accepted")
			}
		})
	}
}

func TestValidateTemplateChangeCreateEditDelete(t *testing.T) {
	created := Template{Name: "Acme", ProviderURL: "https://api.acme.test/v1", APIStandard: "openai", AuthType: AuthBearer}
	if err := ValidateTemplateChange(TemplateChange{ID: "acme", After: &created}, nil, nil); err != nil {
		t.Fatalf("create validation: %v", err)
	}
	if err := ValidateTemplateChange(TemplateChange{ID: "Bad ID", After: &created}, nil, nil); err == nil {
		t.Fatal("invalid template ID accepted")
	}

	current := map[string]Template{"acme": CloneTemplate(created)}
	before := CloneTemplate(created)
	edited := CloneTemplate(created)
	edited.Name = "Acme Work"
	change := TemplateChange{ID: "acme", Before: &before, After: &edited}
	if err := ValidateTemplateChange(change, current, nil); err != nil {
		t.Fatalf("edit validation: %v", err)
	}
	stale := CloneTemplate(before)
	stale.Name = "Stale"
	change.Before = &stale
	if err := ValidateTemplateChange(change, current, nil); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("stale edit error = %v", err)
	}
	change.Before = &before
	change.After = nil
	if err := ValidateTemplateChange(change, current, nil); err != nil {
		t.Fatalf("delete validation: %v", err)
	}
	accounts := map[string]Account{"work": {ID: "work", ProviderID: "acme"}}
	if err := ValidateTemplateChange(change, current, accounts); err == nil || !strings.Contains(err.Error(), "still used") {
		t.Fatalf("in-use delete error = %v", err)
	}
}

func TestTemplateAndAccountOverrideJSONRoundTrip(t *testing.T) {
	endpoint := "https://account.example/v1"
	headers := map[string]string{}
	extra := map[string]any{"enabled": false}
	account := Account{
		ID: "team", ProviderID: "openrouter", APIKey: "secret",
		Overrides: TemplateOverrides{ProviderURL: &endpoint, Headers: &headers, ExtraBody: &extra},
	}
	encoded, err := json.Marshal(account)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Account
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Overrides.ProviderURL == nil || *decoded.Overrides.ProviderURL != endpoint ||
		decoded.Overrides.Headers == nil || len(*decoded.Overrides.Headers) != 0 ||
		decoded.Overrides.ExtraBody == nil || (*decoded.Overrides.ExtraBody)["enabled"] != false {
		t.Fatalf("sparse override JSON round trip = %#v", decoded.Overrides)
	}
	if !reflect.DeepEqual(DefaultTemplates()["openrouter"].ExtraBody, map[string]any{"include_reasoning": true}) {
		t.Fatal("embedded request options were not loaded")
	}
}

func boolPointer(value bool) *bool { return &value }
