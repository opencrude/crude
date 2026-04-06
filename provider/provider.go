package provider

import "fmt"

// Provider represents an LLM API provider configuration.
type Provider struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	BaseURL    string `json:"base_url"`
	ModelsURL  string `json:"models_url"`
	AuthHeader string `json:"auth_header"`
	AuthPrefix string `json:"auth_prefix"`
	APIKey     string `json:"api_key"`
	ChatPath   string `json:"chat_path"`
	ModelsPath string `json:"models_path"`
}

// ChatEndpoint returns the full URL for chat completions.
func (p *Provider) ChatEndpoint() string {
	return p.BaseURL + p.ChatPath
}

// ModelsEndpoint returns the full URL for listing models.
func (p *Provider) ModelsEndpoint() string {
	if p.ModelsURL != "" {
		return p.ModelsURL
	}
	return p.BaseURL + p.ModelsPath
}

// AuthValue returns the formatted Authorization header value.
func (p *Provider) AuthValue() string {
	if p.APIKey == "" {
		return ""
	}
	if p.AuthPrefix != "" {
		return p.AuthPrefix + " " + p.APIKey
	}
	return "Bearer " + p.APIKey
}

// Validate checks if the provider is properly configured.
func (p *Provider) Validate() error {
	if p.BaseURL == "" {
		return fmt.Errorf("provider: base URL is required")
	}
	if p.APIKey == "" {
		return fmt.Errorf("provider: API key is required")
	}
	return nil
}

// Preset is a pre-configured provider template.
type Preset struct {
	ID         string
	Name       string
	BaseURL    string
	AuthPrefix string
	HelpText   string
}

// Presets contains all built-in provider presets.
var Presets = []Preset{
	{
		ID:         "openrouter",
		Name:       "OpenRouter",
		BaseURL:    "https://openrouter.ai/api/v1",
		AuthPrefix: "Bearer",
		HelpText:   "Access 200+ models. Free tier available. openrouter.ai",
	},
	{
		ID:         "openai",
		Name:       "OpenAI",
		BaseURL:    "https://api.openai.com/v1",
		AuthPrefix: "Bearer",
		HelpText:   "GPT-4o, o1, etc. platform.openai.com",
	},
	{
		ID:         "anthropic",
		Name:       "Anthropic",
		BaseURL:    "https://api.anthropic.com/v1",
		AuthPrefix: "Bearer",
		HelpText:   "Claude models. console.anthropic.com",
	},
	{
		ID:         "google",
		Name:       "Google AI",
		BaseURL:    "https://generativelanguage.googleapis.com/v1beta/openai",
		AuthPrefix: "Bearer",
		HelpText:   "Gemini models. aistudio.google.com",
	},
	{
		ID:         "groq",
		Name:       "Groq",
		BaseURL:    "https://api.groq.com/openai/v1",
		AuthPrefix: "Bearer",
		HelpText:   "Fast inference. Free tier. console.groq.com",
	},
	{
		ID:         "together",
		Name:       "Together AI",
		BaseURL:    "https://api.together.xyz/v1",
		AuthPrefix: "Bearer",
		HelpText:   "Open models. together.ai",
	},
	{
		ID:         "deepseek",
		Name:       "DeepSeek",
		BaseURL:    "https://api.deepseek.com/v1",
		AuthPrefix: "Bearer",
		HelpText:   "DeepSeek models. platform.deepseek.com",
	},
	{
		ID:         "mistral",
		Name:       "Mistral",
		BaseURL:    "https://api.mistral.ai/v1",
		AuthPrefix: "Bearer",
		HelpText:   "Mistral models. console.mistral.ai",
	},
	{
		ID:         "ollama",
		Name:       "Ollama (local)",
		BaseURL:    "http://localhost:11434/v1",
		AuthPrefix: "Bearer",
		HelpText:   "Local models. No API key needed. ollama.com",
	},
	{
		ID:         "alibaba",
		Name:       "Alibaba",
		BaseURL:    "https://dashscope-intl.aliyuncs.com/compatible-mode/v1",
		AuthPrefix: "Bearer",
		HelpText:   "Qwen models. dashscope.aliyun.com",
	},
	{
		ID:         "custom",
		Name:       "Custom",
		BaseURL:    "",
		AuthPrefix: "Bearer",
		HelpText:   "Any OpenAI-compatible API endpoint",
	},
}

// GetPreset returns a preset by ID, or nil if not found.
func GetPreset(id string) *Preset {
	for _, p := range Presets {
		if p.ID == id {
			return &p
		}
	}
	return nil
}

// FromPreset creates a Provider from a preset with the given API key.
func FromPreset(presetID, apiKey, customBaseURL string) *Provider {
	preset := GetPreset(presetID)
	if preset == nil {
		preset = GetPreset("openrouter")
	}

	baseURL := preset.BaseURL
	if customBaseURL != "" {
		baseURL = customBaseURL
	}

	return &Provider{
		ID:         preset.ID,
		Name:       preset.Name,
		BaseURL:    baseURL,
		AuthHeader: "Authorization",
		AuthPrefix: preset.AuthPrefix,
		APIKey:     apiKey,
		ChatPath:   "/chat/completions",
		ModelsPath: "/models",
	}
}

// PresetNames returns a list of preset names for display.
func PresetNames() []string {
	names := make([]string, len(Presets))
	for i, p := range Presets {
		names[i] = p.Name
	}
	return names
}

// PresetIDs returns a list of preset IDs.
func PresetIDs() []string {
	ids := make([]string, len(Presets))
	for i, p := range Presets {
		ids[i] = p.ID
	}
	return ids
}
