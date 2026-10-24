package provider

import (
	"crude/types/jsondoc"
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

//go:embed templates.json
var templateSeedFS embed.FS

// Template describes request transport, not an account, model, or agent.
// Each account owns one complete connection; embedded templates only seed new accounts.
type Template struct {
	Name        string            `json:"name,omitempty"`
	HelpText    string            `json:"help_text,omitempty"`
	ProviderURL string            `json:"provider_url,omitempty"`
	APIStandard string            `json:"api_standard,omitempty"`
	AuthType    string            `json:"auth_type,omitempty"`
	AuthHeader  string            `json:"auth_header,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Dialer      *DialSettings     `json:"dialer,omitempty"`
	Protocol    *ProtocolSettings `json:"protocol,omitempty"`
	ExtraBody   jsondoc.Object    `json:"extra_body,omitempty"`
}

const (
	AuthProtocol     = "protocol"
	AuthBearer       = "bearer"
	AuthCustomHeader = "custom-header"
	AuthNone         = "none"
	AuthChatGPT      = "chatgpt"
)

type templateSeed struct {
	ID       string   `json:"id"`
	Template Template `json:"template"`
}

var builtinTemplates, builtinTemplateOrder = loadBuiltinTemplates()

func loadBuiltinTemplates() (map[string]Template, []string) {
	data, err := templateSeedFS.ReadFile("templates.json")
	if err != nil {
		panic(fmt.Errorf("provider: read embedded templates: %w", err))
	}
	var seeds []templateSeed
	if err := json.Unmarshal(data, &seeds); err != nil {
		panic(fmt.Errorf("provider: decode embedded templates: %w", err))
	}
	templates := make(map[string]Template, len(seeds))
	order := make([]string, 0, len(seeds))
	for _, seed := range seeds {
		if !validTemplateID(seed.ID) {
			panic(fmt.Sprintf("provider: invalid embedded template ID %q", seed.ID))
		}
		if _, exists := templates[seed.ID]; exists {
			panic(fmt.Sprintf("provider: duplicate embedded template ID %q", seed.ID))
		}
		templates[seed.ID] = CloneTemplate(seed.Template)
		order = append(order, seed.ID)
	}
	return templates, order
}

// DefaultTemplates returns detached copies of the immutable built-in template
// seed data. It does not read or write user configuration.
func DefaultTemplates() map[string]Template {
	return CloneTemplates(builtinTemplates)
}

// CloneTemplates deep-copies a keyed template set.
func CloneTemplates(src map[string]Template) map[string]Template {
	if src == nil {
		return nil
	}
	out := make(map[string]Template, len(src))
	for id, template := range src {
		out[id] = CloneTemplate(template)
	}
	return out
}

// CloneTemplate deep-copies every mutable template field.
func CloneTemplate(template Template) Template {
	if template.Headers != nil {
		headers := make(map[string]string, len(template.Headers))
		for key, value := range template.Headers {
			headers[key] = value
		}
		template.Headers = headers
	}
	if template.Dialer != nil {
		dialer := *template.Dialer
		template.Dialer = &dialer
	}
	template.Protocol = cloneProtocolSettings(template.Protocol)
	template.ExtraBody = cloneAnyMap(template.ExtraBody)
	return template
}

// CloneAccount returns a detached account and complete connection configuration.
func CloneAccount(account Account) Account {
	if account.OAuth != nil {
		oauth := *account.OAuth
		account.OAuth = &oauth
	}
	account.Connection = CloneTemplate(account.Connection)
	return account
}

// CloneAccounts deep-copies a keyed account set.
func CloneAccounts(src map[string]Account) map[string]Account {
	if src == nil {
		return nil
	}
	out := make(map[string]Account, len(src))
	for id, account := range src {
		out[id] = CloneAccount(account)
	}
	return out
}

func (template Template) StandardForModel(_ string, providerID string) string {
	if template.APIStandard != "" {
		return template.APIStandard
	}
	return DefaultStandardID(providerID)
}

func (template Template) Validate() error {
	if strings.TrimSpace(template.Name) == "" || strings.ContainsAny(template.Name, "\r\n") {
		return fmt.Errorf("template name is required")
	}
	parsed, err := url.Parse(template.ProviderURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("endpoint must be an HTTP(S) URL without embedded credentials or fragment")
	}
	if _, ok := canonicalProtocolID(template.APIStandard); !ok {
		return fmt.Errorf("select a supported wire protocol")
	}
	switch template.AuthType {
	case AuthProtocol, AuthBearer, AuthNone:
	case AuthCustomHeader:
		if !validHeaderName(template.AuthHeader) {
			return fmt.Errorf("enter a valid API-key header name")
		}
	case AuthChatGPT:
		if template.APIStandard != "codex" {
			return fmt.Errorf("ChatGPT authentication requires the Codex wire protocol")
		}
	default:
		return fmt.Errorf("select a supported authentication type")
	}
	for key, value := range template.Headers {
		if !validHeaderName(key) || strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("headers contain an invalid name or value")
		}
		if strings.EqualFold(key, "Authorization") || strings.EqualFold(key, "x-api-key") ||
			(template.AuthType == AuthCustomHeader && strings.EqualFold(key, template.AuthHeader)) {
			return fmt.Errorf("authentication headers belong in the account credentials, not extra headers")
		}
	}
	if template.Dialer != nil {
		if err := template.Dialer.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func validHeaderName(name string) bool {
	return strings.TrimSpace(name) == name && http.CanonicalHeaderKey(name) != ""
}

func (template Template) Apply(provider *Provider) error {
	if err := template.Validate(); err != nil {
		return err
	}
	return template.apply(provider)
}

func (template Template) apply(provider *Provider) error {
	if provider == nil {
		return fmt.Errorf("provider: cannot apply a template to nil")
	}
	canonical, ok := canonicalProtocolID(template.APIStandard)
	if !ok {
		return fmt.Errorf("provider: unknown wire protocol %q", template.APIStandard)
	}
	provider.BaseURL, provider.Name = template.ProviderURL, template.Name
	provider.StandardName = canonical
	provider.authType = template.AuthType
	provider.authHeader = template.AuthHeader
	provider.headers = CloneTemplate(template).Headers
	if template.Dialer != nil {
		provider.SetDialSettings(*template.Dialer)
	} else {
		provider.SetDialSettings(DialSettings{})
	}
	provider.SetProtocolSettings(template.Protocol)
	provider.SetExtraBody(template.ExtraBody)
	return nil
}

func validTemplateID(id string) bool {
	if id == "" || strings.TrimSpace(id) != id {
		return false
	}
	for i, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || (i > 0 && (r == '-' || r == '_')) {
			continue
		}
		return false
	}
	return true
}

// Preset remains a presentation adapter for existing callers. Its values are
// derived exclusively from the embedded editable-template seed.
type Preset struct {
	ID         string
	Name       string
	BaseURL    string
	HelpText   string
	StandardID string
	ExtraBody  map[string]any
}

func PresetList() []Preset {
	presets := make([]Preset, 0, len(builtinTemplateOrder))
	for _, id := range builtinTemplateOrder {
		preset := GetPreset(id)
		presets = append(presets, *preset)
	}
	return presets
}

func GetPreset(id string) *Preset {
	template, ok := builtinTemplates[id]
	if !ok {
		return nil
	}
	return &Preset{
		ID: id, Name: template.Name, BaseURL: template.ProviderURL,
		HelpText: template.HelpText, StandardID: template.APIStandard,
		ExtraBody: cloneAnyMap(template.ExtraBody),
	}
}

func PresetNames() []string {
	names := make([]string, 0, len(builtinTemplateOrder))
	for _, id := range builtinTemplateOrder {
		names = append(names, builtinTemplates[id].Name)
	}
	return names
}

func PresetIDs() []string {
	return append([]string(nil), builtinTemplateOrder...)
}

// TemplateIDs returns all configured IDs in stable sorted order.
func TemplateIDs(templates map[string]Template) []string {
	ids := make([]string, 0, len(templates))
	for id := range templates {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
