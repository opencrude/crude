package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"crude/logger"
)

// Register the built-in wire protocols.
func init() {
	RegisterProtocol(&OpenAIStandard{})
	RegisterProtocol(&OpenAIResponsesStandard{})
	RegisterProtocol(&AnthropicStandard{})
	RegisterProtocol(&OllamaStandard{})
	RegisterProtocol(&CodexStandard{})
}

var (
	pkgLogger   *logger.Logger
	pkgLoggerMu sync.RWMutex
)

func SetLogger(lg *logger.Logger) {
	pkgLoggerMu.Lock()
	defer pkgLoggerMu.Unlock()
	pkgLogger = lg
}

func logIf(fn func(lg *logger.Logger)) {
	pkgLoggerMu.RLock()
	lg := pkgLogger
	pkgLoggerMu.RUnlock()
	if lg != nil {
		fn(lg)
	}
}

// Provider is the configuration of one backend: identity, endpoint,
// credentials, and transport tuning. It composes protocol (wire format) and
// Client (transport) for the workflows; it holds no request logic itself.
type Provider struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	BaseURL      string `json:"base_url"`
	APIKey       string `json:"api_key"`
	Model        string `json:"model"`
	StandardName string `json:"standard,omitempty"` // protocol ID; JSON key kept for stored configs

	dial           DialSettings
	extraBody      map[string]any
	thinkingEffort string
	oauth          *OAuthCredentials
	sessionID      string
}

// StandardInfo describes a registered protocol for display purposes.
type StandardInfo struct {
	ID          string
	Name        string
	Description string
}

// StandardList returns every registered protocol ordered by ID.
func StandardList() []StandardInfo {
	out := make([]StandardInfo, 0)
	for _, proto := range ProtocolList() {
		info := proto.Info()
		out = append(out, StandardInfo{ID: info.ID, Name: info.Name, Description: info.Description})
	}
	return out
}

// Protocol resolves the wire protocol for this provider: the configured
// protocol ID, else the preset default, else OpenAI Chat Completions.
func (p *Provider) Protocol() Protocol {
	if p.StandardName != "" {
		if proto, ok := ProtocolByID(p.StandardName); ok {
			return proto
		}
	}
	if preset := GetPreset(p.ID); preset != nil {
		if proto, ok := ProtocolByID(preset.StandardID); ok {
			return proto
		}
	}
	proto, _ := ProtocolByID("openai")
	return proto
}

// ProtocolID returns the resolved protocol identifier.
func (p *Provider) ProtocolID() string {
	if proto := p.Protocol(); proto != nil {
		return proto.Info().ID
	}
	return ""
}

// SetProtocolID configures the wire protocol by registry ID (aliases
// accepted).
func (p *Provider) SetProtocolID(id string) error {
	canonical, ok := canonicalProtocolID(id)
	if !ok {
		return fmt.Errorf("provider: unknown API protocol %q", id)
	}
	p.StandardName = canonical
	return nil
}

// SetStandardID is an alias of SetProtocolID kept for configuration paths
// that speak of "API standards".
func (p *Provider) SetStandardID(id string) error { return p.SetProtocolID(id) }

// DefaultProtocolID returns the default protocol for a provider preset.
func DefaultProtocolID(providerID string) string {
	if preset := GetPreset(providerID); preset != nil {
		if canonical, ok := canonicalProtocolID(preset.StandardID); ok {
			return canonical
		}
	}
	return "openai"
}

// DefaultStandardID is an alias of DefaultProtocolID.
func DefaultStandardID(providerID string) string { return DefaultProtocolID(providerID) }

// StandardID returns the resolved protocol identifier.
func (p *Provider) StandardID() string { return p.ProtocolID() }

func (p *Provider) SetExtraBody(extra map[string]any) { p.extraBody = cloneAnyMap(extra) }
func (p *Provider) ExtraBody() map[string]any         { return cloneAnyMap(p.extraBody) }
func (p *Provider) SetThinkingEffort(e string)        { p.thinkingEffort = e }
func (p *Provider) ThinkingEffort() string            { return p.thinkingEffort }

// SetDialSettings configures the transport tuning used by clients built
// from this provider.
func (p *Provider) SetDialSettings(settings DialSettings) { p.dial = settings }

// DialSettings returns the configured transport tuning.
func (p *Provider) DialSettings() DialSettings { return p.dial }

// Client builds a transport client for this provider. Every call shares the
// provider's current credentials; OAuth tokens are refreshed by the header
// source before each attempt as needed.
func (p *Provider) Client(opts ...ClientOption) (*Client, error) {
	proto := p.Protocol()
	if proto == nil {
		return nil, fmt.Errorf("provider %s: no protocol registered", p.ID)
	}
	all := append([]ClientOption{
		WithRequestExtras(p.requestExtras()),
		WithHeaderSource(p.headerSource(proto)),
	}, opts...)
	return NewClient(proto, p.BaseURL, p.effectiveAPIKey(), p.dial.resolve(), all...)
}

// requestExtras merges the protocol-derived extras with provider extras;
// provider extras win.
func (p *Provider) requestExtras() map[string]any {
	proto := p.Protocol()
	merged := map[string]any{}
	if re, ok := proto.(RequestExtras); ok {
		for key, value := range re.RequestExtras(p.Model, p.thinkingEffort) {
			merged[key] = value
		}
	}
	for key, value := range p.extraBody {
		merged[key] = cloneAnyValue(value)
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

// headerSource resolves the complete request header set per attempt:
// static protocol headers, credentials (refreshing OAuth when due),
// per-session headers for protocols that require them.
func (p *Provider) headerSource(proto Protocol) func(ctx context.Context) (http.Header, error) {
	return func(ctx context.Context) (http.Header, error) {
		if err := p.EnsureAuth(ctx); err != nil {
			return nil, fmt.Errorf("refresh oauth token: %w", err)
		}
		headers := protocolHeaders(proto)
		if key := p.effectiveAPIKey(); key != "" {
			if pa, ok := proto.(ProtocolAuth); ok {
				for key, values := range pa.AuthHeaders(key) {
					for _, value := range values {
						headers.Set(key, value)
					}
				}
			} else {
				headers.Set("Authorization", "Bearer "+key)
			}
		}
		if ss, ok := proto.(SessionScope); ok {
			if p.sessionID == "" {
				p.sessionID = newSessionUUID()
			}
			for key, values := range ss.SessionHeaders(p.authContext(), p.sessionID) {
				for _, value := range values {
					headers.Set(key, value)
				}
			}
		}
		return headers, nil
	}
}

// RequestHeaders resolves the complete header set a request from this
// provider carries, refreshing OAuth when due. The transport calls the
// header source directly per attempt; this exists for diagnostics and tests.
func (p *Provider) RequestHeaders(ctx context.Context) (http.Header, error) {
	return p.headerSource(p.Protocol())(ctx)
}

// authContext resolves the credentials passed to protocol capabilities.
func (p *Provider) authContext() AuthContext {
	return AuthContext{APIKey: p.effectiveAPIKey(), OAuth: p.oauth}
}

// effectiveAPIKey returns the credential used as the bearer: the OAuth access
// token when present (e.g. the ChatGPT subscription flow), falling back to the
// static API key.
func (p *Provider) effectiveAPIKey() string {
	if p.oauth != nil && p.oauth.AccessToken != "" {
		return p.oauth.AccessToken
	}
	return p.APIKey
}

// SetOAuth attaches OAuth credentials (from a Sign-in with ChatGPT flow) to
// the provider.
func (p *Provider) SetOAuth(creds *OAuthCredentials) {
	p.oauth = creds
}

// OAuth returns the stored OAuth credentials, or nil if none are set.
func (p *Provider) OAuth() *OAuthCredentials { return p.oauth }

// EnsureAuth refreshes the OAuth access token in memory if it is missing or
// about to expire. It is a no-op for providers without OAuth credentials.
// Refresh updates only the live provider state; the persisted refresh token
// remains valid across restarts, so a restart re-refreshes without re-sign-in.
func (p *Provider) EnsureAuth(ctx context.Context) error {
	if p.oauth == nil || p.oauth.RefreshToken == "" {
		return nil
	}
	if !p.oauth.expiredSoon() {
		return nil
	}
	creds, err := RefreshOpenAIToken(ctx, p.oauth.RefreshToken)
	if err != nil {
		return err
	}
	p.oauth.AccessToken = creds.AccessToken
	if creds.RefreshToken != "" {
		p.oauth.RefreshToken = creds.RefreshToken
	}
	if creds.IDToken != "" {
		p.oauth.IDToken = creds.IDToken
	}
	if creds.Email != "" {
		p.oauth.Email = creds.Email
	}
	p.oauth.ExpiresAt = creds.ExpiresAt
	if creds.AccountID != "" {
		p.oauth.AccountID = creds.AccountID
	}
	return nil
}

// ContextWindow returns the protocol's known context window for the model,
// or 0 when unknown.
func (p *Provider) ContextWindow(modelID string) int {
	if meta, ok := p.Protocol().(ProtocolMetadata); ok {
		return meta.ContextWindow(modelID)
	}
	return 0
}

func (p *Provider) Validate() error {
	if p.StandardName != "" && !isKnownProtocolID(p.StandardName) {
		return fmt.Errorf("provider: unknown API protocol %q", p.StandardName)
	}
	if strings.TrimSpace(p.BaseURL) == "" {
		return fmt.Errorf("provider: base URL is required")
	}
	parsed, err := url.Parse(p.BaseURL)
	if err != nil {
		return fmt.Errorf("provider: invalid base URL: %w", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("provider: base URL must be an absolute HTTP(S) URL")
	}
	return nil
}

type Preset struct {
	ID         string
	Name       string
	BaseURL    string
	HelpText   string
	StandardID string
	ExtraBody  map[string]any
}

var presets = []Preset{
	{
		ID:         "openrouter",
		Name:       "OpenRouter",
		BaseURL:    "https://openrouter.ai/api/v1",
		HelpText:   "API keys: openrouter.ai/keys",
		StandardID: "openai",
		ExtraBody:  map[string]any{"include_reasoning": true},
	},
	{
		ID:         "openai",
		Name:       "OpenAI",
		BaseURL:    "https://api.openai.com/v1",
		HelpText:   "API keys: platform.openai.com/api-keys",
		StandardID: "openai-responses",
	},
	{
		ID:         "openai-chat",
		Name:       "OpenAI (Chat Completions)",
		BaseURL:    "https://api.openai.com/v1",
		HelpText:   "Legacy Chat Completions compatibility.",
		StandardID: "openai",
	},
	{
		ID:         "anthropic",
		Name:       "Anthropic",
		BaseURL:    "https://api.anthropic.com/v1",
		HelpText:   "API keys: console.anthropic.com",
		StandardID: "anthropic",
	},
	{
		ID:         "google",
		Name:       "Google AI",
		BaseURL:    "https://generativelanguage.googleapis.com/v1beta/openai",
		HelpText:   "API keys: aistudio.google.com/apikey",
		StandardID: "openai",
	},
	{
		ID:         "groq",
		Name:       "Groq",
		BaseURL:    "https://api.groq.com/openai/v1",
		HelpText:   "API keys: console.groq.com/keys",
		StandardID: "openai",
	},
	{
		ID:         "together",
		Name:       "Together AI",
		BaseURL:    "https://api.together.xyz/v1",
		HelpText:   "API keys: api.together.ai/settings/api-keys",
		StandardID: "openai",
	},
	{
		ID:         "deepseek",
		Name:       "DeepSeek",
		BaseURL:    "https://api.deepseek.com/v1",
		HelpText:   "API keys: platform.deepseek.com/api_keys",
		StandardID: "openai",
	},
	{
		ID:         "mistral",
		Name:       "Mistral",
		BaseURL:    "https://api.mistral.ai/v1",
		HelpText:   "API keys: console.mistral.ai/api-keys",
		StandardID: "openai",
	},
	{
		ID:         "ollama",
		Name:       "Ollama (local)",
		BaseURL:    "http://localhost:11434/v1",
		HelpText:   "Local models. No API key needed.",
		StandardID: "openai",
	},
	{
		ID:         "alibaba",
		Name:       "Alibaba",
		BaseURL:    "https://dashscope-intl.aliyuncs.com/compatible-mode/v1",
		HelpText:   "API keys: dashscope.console.aliyun.com/apiKey",
		StandardID: "openai",
	},
	{
		ID:         "opencode-zen",
		Name:       "OpenCode Zen",
		BaseURL:    "https://opencode.ai/zen/v1",
		HelpText:   "API keys: opencode.ai/zen",
		StandardID: "openai",
	},
	{
		ID:         "opencode-go",
		Name:       "OpenCode Go",
		BaseURL:    "https://opencode.ai/zen/go/v1",
		HelpText:   "Subscription models. opencode.ai/go",
		StandardID: "openai",
	},
	{
		ID:         "wafer",
		Name:       "Wafer AI",
		BaseURL:    "https://pass.wafer.ai/v1",
		HelpText:   "API keys: wafer.ai/pass",
		StandardID: "openai",
	},
	{
		ID:         "crof",
		Name:       "Crof AI",
		BaseURL:    "https://crof.ai/v1",
		HelpText:   "API keys: crof.ai",
		StandardID: "openai",
	},
	{
		ID:         "ollama-cloud",
		Name:       "Ollama Cloud",
		BaseURL:    "https://ollama.com/api",
		HelpText:   "Hosted Ollama models. API keys: ollama.com/settings/keys",
		StandardID: "ollama",
	},
	{
		ID:         "synthetic",
		Name:       "Synthetic",
		BaseURL:    "https://api.synthetic.new/openai/v1",
		HelpText:   "API keys: synthetic.new",
		StandardID: "openai",
	},
	{
		ID:         "chatgpt",
		Name:       "OpenAI (ChatGPT subscription)",
		BaseURL:    "https://chatgpt.com/backend-api/codex",
		HelpText:   "Use a ChatGPT Plus/Pro subscription via Sign in with ChatGPT.",
		StandardID: "codex",
	},
	{
		ID:         "custom",
		Name:       "Custom",
		BaseURL:    "",
		HelpText:   "Any OpenAI-compatible API endpoint",
		StandardID: "openai",
	},
}

func PresetList() []Preset {
	out := make([]Preset, len(presets))
	for i, preset := range presets {
		out[i] = clonePreset(preset)
	}
	return out
}

func GetPreset(id string) *Preset {
	for _, preset := range presets {
		if preset.ID == id {
			copy := clonePreset(preset)
			return &copy
		}
	}
	return nil
}

// FromPreset builds a Provider from a preset, overriding the base URL when
// a custom one is configured. The preset's protocol is applied as the
// default wire format.
func FromPreset(presetID, apiKey, customBaseURL string) *Provider {
	preset := GetPreset(presetID)
	if preset == nil {
		logIf(func(lg *logger.Logger) {
			lg.Warn("PROVIDER", "unknown_preset", "requested", presetID, "fallback", "openrouter")
		})
		preset = GetPreset("openrouter")
	}

	baseURL := preset.BaseURL
	if customBaseURL != "" {
		baseURL = customBaseURL
	}

	p := &Provider{
		ID:           preset.ID,
		Name:         preset.Name,
		BaseURL:      baseURL,
		APIKey:       apiKey,
		StandardName: preset.StandardID,
	}
	if len(preset.ExtraBody) > 0 {
		p.extraBody = make(map[string]any)
		for k, v := range preset.ExtraBody {
			p.extraBody[k] = v
		}
	}
	return p
}

func clonePreset(preset Preset) Preset {
	preset.ExtraBody = cloneAnyMap(preset.ExtraBody)
	return preset
}

func cloneAnyMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = cloneAnyValue(value)
	}
	return out
}

func cloneAnyValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneAnyMap(typed)
	case map[string]string:
		out := make(map[string]string, len(typed))
		for key, item := range typed {
			out[key] = item
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = cloneAnyValue(item)
		}
		return out
	case []string:
		return append([]string(nil), typed...)
	case json.RawMessage:
		return append(json.RawMessage(nil), typed...)
	case []byte:
		return append([]byte(nil), typed...)
	default:
		return value
	}
}

func PresetNames() []string {
	names := make([]string, len(presets))
	for i, p := range presets {
		names[i] = p.Name
	}
	return names
}

func PresetIDs() []string {
	ids := make([]string, len(presets))
	for i, p := range presets {
		ids[i] = p.ID
	}
	return ids
}
