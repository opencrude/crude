package provider

import "fmt"

type Provider struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`

	standard      APIStandard
	extraBody     map[string]any
	thinkingEffort string
}

func (p *Provider) SetStandard(s APIStandard)   { p.standard = s }
func (p *Provider) StandardID() string {
	if p.standard == nil {
		return ""
	}
	return p.standard.ID()
}
func (p *Provider) Standard() APIStandard {
	if p.standard == nil {
		p.standard = &OpenAIStandard{}
	}
	return p.standard
}

func (p *Provider) SetExtraBody(extra map[string]any)  { p.extraBody = extra }
func (p *Provider) ExtraBody() map[string]any           { return p.extraBody }
func (p *Provider) SetThinkingEffort(e string)          { p.thinkingEffort = e }
func (p *Provider) ThinkingEffort() string               { return p.thinkingEffort }

func (p *Provider) ChatEndpoint() string {
	return p.Standard().ChatEndpoint(p.BaseURL)
}

func (p *Provider) ModelsEndpoint() string {
	return p.Standard().ModelsEndpoint(p.BaseURL)
}

func (p *Provider) Auth() (header, value string) {
	return p.Standard().Auth(p.APIKey)
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
			base[k] = v
		}
	}
	return base
}

func (p *Provider) Validate() error {
	if p.BaseURL == "" {
		return fmt.Errorf("provider: base URL is required")
	}
	if p.APIKey == "" {
		return fmt.Errorf("provider: API key is required")
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

var Presets = []Preset{
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
		HelpText:   "GPT-4o, o1, etc. platform.openai.com",
		StandardID: "openai",
	},
	{
		ID:         "anthropic",
		Name:       "Anthropic",
		BaseURL:    "https://api.anthropic.com/v1",
		HelpText:   "Claude models. console.anthropic.com",
		StandardID: "openai",
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
		ID:         "custom",
		Name:       "Custom",
		BaseURL:    "",
		HelpText:   "Any OpenAI-compatible API endpoint",
		StandardID: "openai",
	},
}

func GetPreset(id string) *Preset {
	for _, p := range Presets {
		if p.ID == id {
			return &p
		}
	}
	return nil
}

func standardByID(id string) APIStandard {
	switch id {
	case "openai":
		return &OpenAIStandard{}
	default:
		return &OpenAIStandard{}
	}
}

func FromPreset(presetID, apiKey, customBaseURL string) *Provider {
	preset := GetPreset(presetID)
	if preset == nil {
		preset = GetPreset("openrouter")
	}

	baseURL := preset.BaseURL
	if customBaseURL != "" {
		baseURL = customBaseURL
	}

	p := &Provider{
		ID:        preset.ID,
		Name:      preset.Name,
		BaseURL:   baseURL,
		APIKey:    apiKey,
		standard:  standardByID(preset.StandardID),
	}
	if len(preset.ExtraBody) > 0 {
		p.extraBody = make(map[string]any)
		for k, v := range preset.ExtraBody {
			p.extraBody[k] = v
		}
	}
	return p
}

func PresetNames() []string {
	names := make([]string, len(Presets))
	for i, p := range Presets {
		names[i] = p.Name
	}
	return names
}

func PresetIDs() []string {
	ids := make([]string, len(Presets))
	for i, p := range Presets {
		ids[i] = p.ID
	}
	return ids
}
