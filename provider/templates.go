package provider

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
)

//go:embed templates.json
var templateSeedFS embed.FS

// Template describes request transport, not an account, model, or agent.
// Application configuration stores editable templates keyed by stable ID.
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
	ExtraBody   map[string]any    `json:"extra_body,omitempty"`
}

const (
	AuthProtocol     = "protocol"
	AuthBearer       = "bearer"
	AuthCustomHeader = "custom-header"
	AuthNone         = "none"
	AuthChatGPT      = "chatgpt"
)

// TemplateOverrides uses explicit presence. Empty strings, empty maps, and
// zero-valued settings intentionally replace the template value; nil inherits.
type TemplateOverrides struct {
	Name        *string            `json:"name,omitempty"`
	ProviderURL *string            `json:"provider_url,omitempty"`
	APIStandard *string            `json:"api_standard,omitempty"`
	AuthType    *string            `json:"auth_type,omitempty"`
	AuthHeader  *string            `json:"auth_header,omitempty"`
	Headers     *map[string]string `json:"headers,omitempty"`
	Dialer      *DialSettings      `json:"dialer,omitempty"`
	Protocol    *ProtocolSettings  `json:"protocol,omitempty"`
	ExtraBody   *map[string]any    `json:"extra_body,omitempty"`
}

// TemplateChange is a revision-checked create, edit, or delete request shared
// by the provider foundation and its application integration.
type TemplateChange struct {
	ID     string
	Before *Template
	After  *Template
}

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

// DefaultTemplates returns detached copies of the editable built-in template
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

// CloneTemplateOverrides deep-copies account-level sparse overrides.
func CloneTemplateOverrides(overrides TemplateOverrides) TemplateOverrides {
	if overrides.Name != nil {
		value := *overrides.Name
		overrides.Name = &value
	}
	if overrides.ProviderURL != nil {
		value := *overrides.ProviderURL
		overrides.ProviderURL = &value
	}
	if overrides.APIStandard != nil {
		value := *overrides.APIStandard
		overrides.APIStandard = &value
	}
	if overrides.AuthType != nil {
		value := *overrides.AuthType
		overrides.AuthType = &value
	}
	if overrides.AuthHeader != nil {
		value := *overrides.AuthHeader
		overrides.AuthHeader = &value
	}
	if overrides.Headers != nil {
		headers := *overrides.Headers
		if headers != nil {
			copy := make(map[string]string, len(headers))
			for key, value := range headers {
				copy[key] = value
			}
			headers = copy
		}
		overrides.Headers = &headers
	}
	if overrides.Dialer != nil {
		dialer := *overrides.Dialer
		overrides.Dialer = &dialer
	}
	overrides.Protocol = cloneProtocolSettings(overrides.Protocol)
	if overrides.ExtraBody != nil {
		extra := cloneAnyMap(*overrides.ExtraBody)
		overrides.ExtraBody = &extra
	}
	return overrides
}

// CloneAccount returns a detached account and detached template overrides.
func CloneAccount(account Account) Account {
	if account.OAuth != nil {
		oauth := *account.OAuth
		account.OAuth = &oauth
	}
	account.Overrides = CloneTemplateOverrides(account.Overrides)
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

// ResolveTemplate layers account overrides over the configured template and
// uses embedded seed values only when a keyed definition is absent or partial.
func ResolveTemplate(id string, configured Template, overrides TemplateOverrides) Template {
	template := CloneTemplate(configured)
	if seeded, ok := builtinTemplates[id]; ok {
		if template.Name == "" {
			template.Name = seeded.Name
		}
		if template.HelpText == "" {
			template.HelpText = seeded.HelpText
		}
		if template.ProviderURL == "" {
			template.ProviderURL = seeded.ProviderURL
		}
		if template.APIStandard == "" {
			template.APIStandard = seeded.APIStandard
		}
		if template.AuthType == "" {
			template.AuthType = seeded.AuthType
		}
		if template.AuthHeader == "" {
			template.AuthHeader = seeded.AuthHeader
		}
		if template.Headers == nil {
			template.Headers = seeded.Headers
		}
		if template.Dialer == nil {
			template.Dialer = seeded.Dialer
		}
		if template.Protocol == nil {
			template.Protocol = seeded.Protocol
		}
		if template.ExtraBody == nil {
			template.ExtraBody = seeded.ExtraBody
		}
	}
	if template.Name == "" {
		template.Name = id
	}
	if template.APIStandard == "" {
		template.APIStandard = "openai"
	}
	if template.AuthType == "" {
		template.AuthType = AuthProtocol
	}
	if overrides.Name != nil {
		template.Name = *overrides.Name
	}
	if overrides.ProviderURL != nil {
		template.ProviderURL = *overrides.ProviderURL
	}
	if overrides.APIStandard != nil {
		template.APIStandard = *overrides.APIStandard
	}
	if overrides.AuthType != nil {
		template.AuthType = *overrides.AuthType
	}
	if overrides.AuthHeader != nil {
		template.AuthHeader = *overrides.AuthHeader
	}
	if overrides.Headers != nil {
		template.Headers = *overrides.Headers
	}
	if overrides.Dialer != nil {
		template.Dialer = overrides.Dialer
	}
	if overrides.Protocol != nil {
		template.Protocol = overrides.Protocol
	}
	if overrides.ExtraBody != nil {
		template.ExtraBody = *overrides.ExtraBody
	}
	return CloneTemplate(template)
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

// ValidateTemplateChange validates create/edit/delete intent against the
// current keyed definitions. Deletes fail while any account still references
// the template; stale Before snapshots fail closed.
func ValidateTemplateChange(change TemplateChange, templates map[string]Template, accounts map[string]Account) error {
	if !validTemplateID(change.ID) {
		return fmt.Errorf("template ID must contain only lowercase letters, digits, hyphens, and underscores")
	}
	current, exists := templates[change.ID]
	if exists != (change.Before != nil) {
		return fmt.Errorf("template %q changed; reload templates before saving", change.ID)
	}
	if change.Before != nil && !reflect.DeepEqual(current, *change.Before) {
		return fmt.Errorf("template %q changed; reload templates before saving", change.ID)
	}
	if change.After == nil {
		if !exists {
			return fmt.Errorf("template %q does not exist", change.ID)
		}
		for _, account := range accounts {
			if account.ProviderID == change.ID {
				return fmt.Errorf("template %q is still used by account %q", change.ID, account.ID)
			}
		}
		return nil
	}
	if err := change.After.Validate(); err != nil {
		return fmt.Errorf("template %q: %w", change.ID, err)
	}
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
