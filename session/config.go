package session

import (
	"crude/provider"
	ttypes "crude/tui/types"
)

type Config struct {
	ActiveProvider  string
	Providers       map[string]ProviderConfig
	TurnLogsEnabled bool
}

type ProviderConfig struct {
	APIKey         string `json:"api_key,omitempty"`
	Model          string `json:"model,omitempty"`
	ProviderURL    string `json:"provider_url,omitempty"`
	ThinkingEffort string `json:"thinking_effort,omitempty"`

	ModelContextWindow  int     `json:"model_context_window,omitempty"`
	MainInputPrice      float64 `json:"main_input_price,omitempty"`
	MainOutputPrice     float64 `json:"main_output_price,omitempty"`
	MainCacheReadPrice  float64 `json:"main_cache_read_price,omitempty"`
}

func (c *Config) GetActiveProviderConfig() ProviderConfig {
	if c.Providers == nil {
		return ProviderConfig{}
	}
	if cfg, ok := c.Providers[c.ActiveProvider]; ok {
		return cfg
	}
	return ProviderConfig{}
}

func (c *Config) SetActiveProviderConfig(providerID string, cfg ProviderConfig) {
	if c.Providers == nil {
		c.Providers = make(map[string]ProviderConfig)
	}
	c.Providers[providerID] = cfg
}

func (c *Config) EffectiveProviderID() string {
	if c.ActiveProvider != "" {
		return c.ActiveProvider
	}
	return "openrouter"
}

func ConfigToSettings(cfg *Config) ttypes.Settings {
	providerID := cfg.EffectiveProviderID()

	providerURL := ""
	apiKey := ""
	model := ""
	thinkingEffort := ""

	if pc, ok := cfg.Providers[providerID]; ok {
		providerURL = pc.ProviderURL
		apiKey = pc.APIKey
		model = pc.Model
		thinkingEffort = pc.ThinkingEffort
	}

	if providerURL == "" {
		p := provider.GetPreset(providerID)
		if p == nil {
			p = provider.GetPreset("openrouter")
		}
		providerURL = p.BaseURL
	}

	ctxWindow := 0
	mainInputPrice := 0.0
	mainOutputPrice := 0.0
	mainCacheReadPrice := 0.0
	if pc, ok := cfg.Providers[providerID]; ok {
		ctxWindow = pc.ModelContextWindow
		mainInputPrice = pc.MainInputPrice
		mainOutputPrice = pc.MainOutputPrice
		mainCacheReadPrice = pc.MainCacheReadPrice
	}

	turnLogs := "on"
	if !cfg.TurnLogsEnabled {
		turnLogs = "off"
	}

	return ttypes.Settings{
		APIKey:             apiKey,
		Model:              model,
		ModelContextWindow: ctxWindow,
		MainInputPrice:     mainInputPrice,
		MainOutputPrice:    mainOutputPrice,
		MainCacheReadPrice: mainCacheReadPrice,
		ProviderID:         providerID,
		ProviderURL:        providerURL,
		ProviderAPIKey:     apiKey,
		ThinkingEffort:     thinkingEffort,
		TurnLogsEnabled:    turnLogs,
	}
}

func settingsToConfig(s ttypes.Settings, base *Config) *Config {
	providerID := s.ProviderID
	if providerID == "" {
		providerID = base.EffectiveProviderID()
	}

	apiKey := s.APIKey
	if apiKey == "" {
		apiKey = s.ProviderAPIKey
	}

	providers := make(map[string]ProviderConfig, len(base.Providers))
	for k, v := range base.Providers {
		providers[k] = v
	}
	providers[providerID] = ProviderConfig{
		APIKey:              apiKey,
		Model:               s.Model,
		ModelContextWindow:  s.ModelContextWindow,
		MainInputPrice:      s.MainInputPrice,
		MainOutputPrice:     s.MainOutputPrice,
		MainCacheReadPrice:  s.MainCacheReadPrice,
		ProviderURL:         s.ProviderURL,
		ThinkingEffort:      s.ThinkingEffort,
	}

	return &Config{
		ActiveProvider:  providerID,
		Providers:      providers,
		TurnLogsEnabled: s.TurnLogsEnabled != "off",
	}
}
