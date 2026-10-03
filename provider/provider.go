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

// Provider is a backend connection. Application credentials are owned by an
// AccountManager; direct APIKey/SetOAuth remain transient client credentials.
type Provider struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	BaseURL      string `json:"base_url"`
	APIKey       string `json:"-"`
	AccountID    string `json:"-"` // application account identity, not OAuth.AccountID
	Model        string `json:"model"`
	StandardName string `json:"standard,omitempty"` // protocol ID; JSON key kept for stored configs

	dial             DialSettings
	extraBody        map[string]any
	thinkingEffort   string
	contextWindow    int
	protocolSettings *ProtocolSettings
	sampling         Sampling
	authType         string
	authHeader       string
	headers          map[string]string
	authMu           sync.RWMutex
	oauth            *OAuthCredentials
	binding          *accountBinding
	sessionID        string
	sessionOnce      sync.Once
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

// Sampling carries the optional per-model sampling overrides applied to
// every chat request built for this provider. nil pointers mean the backend
// default applies.
type Sampling struct {
	Temperature *float64
	TopP        *float64
}

// SetSampling configures the sampling overrides. nil pointers clear them.
func (p *Provider) SetSampling(temperature, topP *float64) {
	p.sampling = Sampling{}
	if temperature != nil {
		temperature := *temperature
		p.sampling.Temperature = &temperature
	}
	if topP != nil {
		topP := *topP
		p.sampling.TopP = &topP
	}
}

// Sampling returns the configured sampling overrides.
func (p *Provider) Sampling() Sampling { return p.sampling }

// SetDialSettings configures the transport tuning used by clients built
// from this provider.
func (p *Provider) SetDialSettings(settings DialSettings) { p.dial = settings }

// DialSettings returns the configured transport tuning.
func (p *Provider) DialSettings() DialSettings { return p.dial }

// SetProtocolSettings configures the protocol-level request switches
// (storage, retention, tiers) used by clients built from this provider. A
// nil argument clears the overrides.
func (p *Provider) SetProtocolSettings(settings *ProtocolSettings) {
	p.protocolSettings = cloneProtocolSettings(settings)
}

// ProtocolSettings returns the configured protocol-level request switches,
// or nil when unset.
func (p *Provider) ProtocolSettings() *ProtocolSettings {
	return cloneProtocolSettings(p.protocolSettings)
}

func cloneProtocolSettings(settings *ProtocolSettings) *ProtocolSettings {
	if settings == nil {
		return nil
	}
	cloned := *settings
	if settings.Store != nil {
		store := *settings.Store
		cloned.Store = &store
	}
	if settings.ReasoningSummaries != nil {
		summaries := *settings.ReasoningSummaries
		cloned.ReasoningSummaries = &summaries
	}
	if settings.Truncation != nil {
		truncation := *settings.Truncation
		cloned.Truncation = &truncation
	}
	return &cloned
}

// Client builds a transport client whose headers resolve the current bound
// account at every attempt, including refresh and reconnect checks.
func (p *Provider) Client(opts ...ClientOption) (*Client, error) {
	proto := p.Protocol()
	if proto == nil {
		return nil, fmt.Errorf("provider %s: no protocol registered", p.ID)
	}
	all := append([]ClientOption{
		WithRequestExtras(p.requestExtras()),
		WithHeaderSource(p.headerSource(proto)),
	}, opts...)
	return NewClient(proto, p.BaseURL, "", p.dial.resolve(), all...)
}

// requestExtras merges the protocol-derived extras with provider extras;
// provider extras win except the explicit retention policy.
func (p *Provider) requestExtras() map[string]any {
	proto := p.Protocol()
	merged := map[string]any{}
	if re, ok := proto.(RequestExtras); ok {
		var settings ProtocolSettings
		if p.protocolSettings != nil {
			settings = *p.protocolSettings
		}
		for key, value := range re.RequestExtras(p.Model, p.thinkingEffort, settings) {
			merged[key] = value
		}
	}
	for key, value := range p.extraBody {
		merged[key] = cloneAnyValue(value)
	}
	// Generic extras cannot silently re-enable an explicit Off or ZDR choice.
	if settings := p.protocolSettings; settings != nil && proto != nil && proto.Info().Options.Store && (settings.Store != nil || settings.ZeroDataRetention) {
		merged["store"] = settings.ResolveStore(proto.Info().Options.StoreDefault)
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
		authType := p.authType
		if authType == "" {
			authType = AuthProtocol
		}
		var auth AuthContext
		if authType != AuthNone {
			var err error
			auth, err = p.resolveAuth(ctx)
			if err != nil {
				return nil, fmt.Errorf("resolve account authentication: %w", err)
			}
		}
		headers := protocolHeaders(proto)
		for key, value := range p.headers {
			headers.Set(key, value)
		}
		switch authType {
		case AuthNone:
		case AuthCustomHeader:
			if auth.APIKey != "" {
				headers.Set(p.authHeader, auth.APIKey)
			}
		case AuthBearer, AuthChatGPT:
			if auth.APIKey != "" {
				headers.Set("Authorization", "Bearer "+auth.APIKey)
			}
		default:
			if auth.APIKey != "" {
				if pa, ok := proto.(ProtocolAuth); ok {
					for key, values := range pa.AuthHeaders(auth.APIKey) {
						for _, value := range values {
							headers.Set(key, value)
						}
					}
				} else {
					headers.Set("Authorization", "Bearer "+auth.APIKey)
				}
			}
		}
		if ss, ok := proto.(SessionScope); ok {
			p.sessionOnce.Do(func() { p.sessionID = newSessionUUID() })
			for key, values := range ss.SessionHeaders(auth, p.sessionID) {
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

// resolveAuth obtains a consistent bearer/session-identity pair. For bound
// providers the manager is the only credential authority.
func (p *Provider) resolveAuth(ctx context.Context) (AuthContext, error) {
	p.authMu.RLock()
	binding := p.binding
	p.authMu.RUnlock()
	if binding != nil {
		auth, err := binding.auth(ctx)
		if err != nil {
			return AuthContext{}, err
		}
		p.authMu.RLock()
		unchanged := p.binding == binding
		p.authMu.RUnlock()
		if !unchanged {
			return AuthContext{}, fmt.Errorf("account connection changed during request; retry with the new connection")
		}
		return auth, nil
	}
	if err := p.EnsureAuth(ctx); err != nil {
		return AuthContext{}, err
	}
	p.authMu.RLock()
	defer p.authMu.RUnlock()
	var oauth *OAuthCredentials
	if p.oauth != nil {
		copy := *p.oauth
		oauth = &copy
	}
	key := p.APIKey
	if oauth != nil && oauth.AccessToken != "" {
		key = oauth.AccessToken
	}
	return AuthContext{APIKey: key, OAuth: oauth}, nil
}

// SetOAuth attaches detached transient OAuth credentials. Application account
// clients must use AccountManager.Bind instead, for durable refresh rotation.
func (p *Provider) SetOAuth(creds *OAuthCredentials) {
	p.authMu.Lock()
	defer p.authMu.Unlock()
	p.binding = nil
	p.AccountID = ""
	p.oauth = nil
	if creds != nil {
		copy := *creds
		p.oauth = &copy
	}
}

// OAuth returns a detached credential snapshot; callers cannot mutate state.
func (p *Provider) OAuth() *OAuthCredentials {
	p.authMu.RLock()
	binding := p.binding
	if binding == nil {
		defer p.authMu.RUnlock()
		if p.oauth == nil {
			return nil
		}
		copy := *p.oauth
		return &copy
	}
	p.authMu.RUnlock()
	account, err := binding.snapshot()
	if err != nil {
		return nil
	}
	return account.OAuth
}

// EnsureAuth refreshes transient OAuth in memory or a bound account durably.
func (p *Provider) EnsureAuth(ctx context.Context) error {
	p.authMu.RLock()
	binding := p.binding
	p.authMu.RUnlock()
	if binding != nil {
		_, err := binding.auth(ctx)
		return err
	}
	p.authMu.Lock()
	defer p.authMu.Unlock()
	if p.oauth == nil || p.oauth.RefreshToken == "" || !p.oauth.expiredSoon() {
		return nil
	}
	creds, err := RefreshOpenAIToken(ctx, p.oauth.RefreshToken)
	if err != nil {
		return err
	}
	mergeOAuth(p.oauth, creds)
	return nil
}

func (p *Provider) SetContextWindow(window int) { p.contextWindow = window }

func (p *Provider) ContextWindow(modelID string) int {
	if modelID == p.Model && p.contextWindow > 0 {
		return p.contextWindow
	}
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

	template := CloneTemplate(builtinTemplates[preset.ID])
	if customBaseURL != "" {
		template.ProviderURL = customBaseURL
	}
	p := &Provider{ID: preset.ID, APIKey: apiKey}
	if err := template.apply(p); err != nil {
		// Embedded seeds are validated by tests; retain a deterministic fallback
		// because this legacy constructor cannot return an error.
		p.Name = preset.Name
		p.BaseURL = template.ProviderURL
		p.StandardName = preset.StandardID
	}
	return p
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
