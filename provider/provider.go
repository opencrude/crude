package provider

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"crude/logger"
)

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

type Provider struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	BaseURL      string `json:"base_url"`
	APIKey       string `json:"api_key"`
	Model        string `json:"model"`
	StandardName string `json:"standard,omitempty"`

	standard       APIStandard
	extraBody      map[string]any
	thinkingEffort string
}

type StandardInfo struct {
	ID          string
	Name        string
	Description string
}

var standardInfos = []StandardInfo{
	{ID: "openai-responses", Name: "OpenAI Responses", Description: "Native Responses API with reasoning-item continuity."},
	{ID: "openai", Name: "OpenAI Chat Completions", Description: "Chat Completions and compatible gateways."},
	{ID: "anthropic", Name: "Anthropic Messages", Description: "Native Claude Messages with signed thinking blocks."},
}

func StandardList() []StandardInfo {
	return append([]StandardInfo(nil), standardInfos...)
}

func (p *Provider) SetStandard(s APIStandard) {
	p.standard = s
	p.StandardName = ""
	if s != nil {
		p.StandardName = s.ID()
	}
}

func (p *Provider) SetStandardID(id string) error {
	canonical, ok := canonicalStandardID(id)
	if !ok {
		return fmt.Errorf("provider: unknown API standard %q", id)
	}
	p.standard = standardByID(canonical)
	p.StandardName = canonical
	return nil
}

func DefaultStandardID(providerID string) string {
	if preset := GetPreset(providerID); preset != nil {
		if canonical, ok := canonicalStandardID(preset.StandardID); ok {
			return canonical
		}
	}
	return "openai"
}
func (p *Provider) StandardID() string {
	return p.Standard().ID()
}
func (p *Provider) Standard() APIStandard {
	if p.StandardName != "" {
		if p.standard != nil && p.standard.ID() == p.StandardName {
			return p.standard
		}
		return standardByID(p.StandardName)
	}
	if p.standard != nil {
		return p.standard
	}
	standardID := ""
	if preset := GetPreset(p.ID); preset != nil {
		standardID = preset.StandardID
	}
	return standardByID(standardID)
}

func (p *Provider) SetExtraBody(extra map[string]any) { p.extraBody = cloneAnyMap(extra) }
func (p *Provider) ExtraBody() map[string]any         { return cloneAnyMap(p.extraBody) }
func (p *Provider) SetThinkingEffort(e string)        { p.thinkingEffort = e }
func (p *Provider) ThinkingEffort() string            { return p.thinkingEffort }

func (p *Provider) ChatEndpoint() string {
	return p.Standard().ChatEndpoint(p.BaseURL)
}

func (p *Provider) ModelsEndpoint() string {
	return p.Standard().ModelsEndpoint(p.BaseURL)
}

func (p *Provider) Auth() (header, value string) {
	return p.Standard().Auth(p.APIKey)
}

func (p *Provider) Headers() map[string]string {
	headers := p.Standard().Headers(p.APIKey)
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for key, value := range headers {
		out[key] = value
	}
	return out
}

func (p *Provider) EncodeRequest(req *ChatRequest) ([]byte, error) {
	return p.Standard().EncodeRequest(req)
}

func (p *Provider) DecodeResponse(body []byte) (*ChatResponse, error) {
	return p.Standard().DecodeResponse(body)
}

func (p *Provider) DecodeStreamChunk(data string) (*ChatResponse, error) {
	return p.Standard().DecodeStreamChunk(data)
}

func (p *Provider) NewStreamDecoder() StreamDecoder {
	return p.Standard().NewStreamDecoder()
}

func (p *Provider) DecodeModels(body []byte) ([]ModelInfo, error) {
	return p.Standard().DecodeModels(body)
}

func (p *Provider) ContextWindow() int {
	return p.Standard().ContextWindow(p.Model)
}

func (p *Provider) BuildExtraBody() map[string]any {
	std := p.Standard()
	base := std.ExtraBody(p.Model, p.thinkingEffort)
	if p.extraBody != nil {
		if base == nil {
			base = make(map[string]any)
		}
		for k, v := range p.extraBody {
			base[k] = cloneAnyValue(v)
		}
	}
	return base
}

func (p *Provider) Validate() error {
	if p.StandardName != "" && !isKnownStandardID(p.StandardName) {
		return fmt.Errorf("provider: unknown API standard %q", p.StandardName)
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
		HelpText:   "Access 200+ models. Free tier available. openrouter.ai",
		StandardID: "openai",
		ExtraBody:  map[string]any{"include_reasoning": true},
	},
	{
		ID:         "openai",
		Name:       "OpenAI",
		BaseURL:    "https://api.openai.com/v1",
		HelpText:   "Native Responses API. platform.openai.com",
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
		HelpText:   "Claude models. console.anthropic.com",
		StandardID: "anthropic",
	},
	{
		ID:         "google",
		Name:       "Google AI",
		BaseURL:    "https://generativelanguage.googleapis.com/v1beta/openai",
		HelpText:   "Gemini models. aistudio.google.com",
		StandardID: "openai",
	},
	{
		ID:         "groq",
		Name:       "Groq",
		BaseURL:    "https://api.groq.com/openai/v1",
		HelpText:   "Fast inference. Free tier. console.groq.com",
		StandardID: "openai",
	},
	{
		ID:         "together",
		Name:       "Together AI",
		BaseURL:    "https://api.together.xyz/v1",
		HelpText:   "Open models. together.ai",
		StandardID: "openai",
	},
	{
		ID:         "deepseek",
		Name:       "DeepSeek",
		BaseURL:    "https://api.deepseek.com/v1",
		HelpText:   "DeepSeek models. platform.deepseek.com",
		StandardID: "openai",
	},
	{
		ID:         "mistral",
		Name:       "Mistral",
		BaseURL:    "https://api.mistral.ai/v1",
		HelpText:   "Mistral models. console.mistral.ai",
		StandardID: "openai",
	},
	{
		ID:         "ollama",
		Name:       "Ollama (local)",
		BaseURL:    "http://localhost:11434/v1",
		HelpText:   "Local models. No API key needed. ollama.com",
		StandardID: "openai",
	},
	{
		ID:         "alibaba",
		Name:       "Alibaba",
		BaseURL:    "https://dashscope-intl.aliyuncs.com/compatible-mode/v1",
		HelpText:   "Qwen models. dashscope.aliyun.com",
		StandardID: "openai",
	},
	{
		ID:         "opencode-zen",
		Name:       "OpenCode Zen",
		BaseURL:    "https://opencode.ai/zen/v1",
		HelpText:   "Curated models by OpenCode team. opencode.ai/zen",
		StandardID: "openai",
	},
	{
		ID:         "opencode-go",
		Name:       "OpenCode Go",
		BaseURL:    "https://opencode.ai/zen/go/v1",
		HelpText:   "Low-cost subscription for open coding models. $5/mo first month, $10/mo after. opencode.ai/go",
		StandardID: "openai",
	},
	{
		ID:         "wafer",
		Name:       "Wafer AI",
		BaseURL:    "https://pass.wafer.ai/v1",
		HelpText:   "Fast open-source models: Qwen3.5-397B-A17B, GLM-5.1. wafer.ai/pass",
		StandardID: "openai",
	},
	{
		ID:         "crof",
		Name:       "Crof AI",
		BaseURL:    "https://crof.ai/v1",
		HelpText:   "Shitty and useless lobotomized models. crof.ai/",
		StandardID: "openai",
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

func standardByID(id string) APIStandard {
	switch id {
	case "openai", "openai-chat":
		return &OpenAIStandard{}
	case "openai-responses", "responses":
		return &OpenAIResponsesStandard{}
	case "anthropic":
		return &AnthropicStandard{}
	default:
		return &OpenAIStandard{}
	}
}

func isKnownStandardID(id string) bool {
	_, ok := canonicalStandardID(id)
	return ok
}

func canonicalStandardID(id string) (string, bool) {
	switch id {
	case "openai", "openai-chat", "openai-responses", "responses", "anthropic":
		switch id {
		case "openai-chat":
			return "openai", true
		case "responses":
			return "openai-responses", true
		default:
			return id, true
		}
	default:
		return "", false
	}
}

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
		standard:     standardByID(preset.StandardID),
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
