package provider

import (
	"context"
	"crude/types/jsondoc"
	"encoding/json"
	"reflect"
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

func TestTemplateAndAccountConnectionClonesAreDetached(t *testing.T) {
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
	account := Account{ID: "work", ProviderID: "openrouter", APIKey: "secret", Connection: Template{
		ExtraBody: extra, Protocol: &ProtocolSettings{MediaReplayRecovery: &recovery},
	}}
	accountClone := CloneAccount(account)
	accountClone.Connection.ExtraBody["flag"] = false
	*accountClone.Connection.Protocol.MediaReplayRecovery = true
	accountClone.APIKey = "changed"
	if account.Connection.ExtraBody["flag"] != true || *account.Connection.Protocol.MediaReplayRecovery || account.APIKey != "secret" {
		t.Fatal("account clone shares credential or override data")
	}
}

func TestAccountConnectionNeverInheritsPresetChanges(t *testing.T) {
	a := Account{ID: "work", ProviderID: "openrouter", Connection: DefaultTemplates()["openrouter"]}
	a.Connection.ProviderURL = "https://account.example/v1"
	a.Connection.Headers = map[string]string{}
	if a.Connection.ProviderURL != "https://account.example/v1" || len(a.Connection.Headers) != 0 {
		t.Fatal("account connection inherited a preset")
	}
	if DefaultTemplates()["openrouter"].ProviderURL == a.Connection.ProviderURL {
		t.Fatal("editing an account changed preset data")
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

func TestAccountConnectionJSONRoundTrip(t *testing.T) {
	endpoint := "https://account.example/v1"
	headers := map[string]string{}
	extra := map[string]any{"enabled": false}
	account := Account{
		ID: "team", ProviderID: "openrouter", APIKey: "secret",
		Connection: Template{ProviderURL: endpoint, Headers: headers, ExtraBody: extra},
	}
	encoded, err := json.Marshal(account)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Account
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Connection.ProviderURL != endpoint || decoded.Connection.ExtraBody["enabled"] != false {
		t.Fatalf("complete connection JSON round trip = %#v", decoded.Connection)
	}
	if !reflect.DeepEqual(DefaultTemplates()["openrouter"].ExtraBody, jsondoc.Object{"include_reasoning": true}) {
		t.Fatal("embedded request options were not loaded")
	}
}

func boolPointer(value bool) *bool { return &value }
