package session

import (
	"crude/provider"
	ttypes "crude/tui/types"
)

const (
	CategoryMain       = "main"
	CategoryResearcher = "researcher"
	CategoryCrafter    = "crafter"
	CategoryAeron      = "aeron"
)

type CategoryConfig struct {
	Provider       string   `json:"provider,omitempty"`
	ProviderURL    string   `json:"provider_url,omitempty"`
	APIKey         string   `json:"api_key,omitempty"`
	Model          string   `json:"model,omitempty"`
	ThinkingEffort string   `json:"thinking_effort,omitempty"`
	Policy         string   `json:"policy,omitempty"`
	MaxSteps       int      `json:"max_steps,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
	Description    string   `json:"description,omitempty"`
	AllowedTools   []string `json:"allowed_tools,omitempty"`
	DeniedTools    []string `json:"denied_tools,omitempty"`
}

type ProfileConfig struct {
	Categories map[string]CategoryConfig `json:"categories,omitempty"`
	MultiAgent *bool                     `json:"multi_agent,omitempty"`
	TurnLogs   *bool                     `json:"turn_logs,omitempty"`
}

type AgentsConfig struct {
	Enabled       bool                      `json:"enabled,omitempty"`
	ActiveProfile string                    `json:"active_profile,omitempty"`
	MaxConcurrent int                       `json:"max_concurrent,omitempty"`
	MaxDepth      int                       `json:"max_depth,omitempty"`
	Profiles      map[string]ProfileConfig  `json:"profiles,omitempty"`
}

func DefaultAgentsConfig() AgentsConfig {
	t := true
	return AgentsConfig{
		Enabled:       true,
		ActiveProfile: "default",
		MaxConcurrent: 4,
		MaxDepth:      2,
		Profiles: map[string]ProfileConfig{
			"default": {
				MultiAgent: &t,
				TurnLogs:   &t,
				Categories: map[string]CategoryConfig{
					CategoryResearcher: {
						Model:          "google/gemini-2.0-flash-exp:free",
						Policy:         "read-only",
						MaxSteps:       30,
						TimeoutSeconds: 120,
						Description:    "Gatherer — explores codebase, searches web, runs experiments",
						AllowedTools:   []string{"read_file", "glob_files", "grep_files", "list_directory", "web_search", "web_fetch", "arxiv_search", "run_javascript", "write_report", "question"},
						DeniedTools:    []string{"write_file", "edit_file", "delete_file", "move_file", "copy_file", "execute_command", "async_execute_command"},
					},
					CategoryCrafter: {
						Model:          "qwen/qwen3.5-235b-a22b",
						Policy:         "no-delete",
						MaxSteps:       60,
						TimeoutSeconds: 300,
						Description:    "Implementer — writes code, edits files, runs tests",
						DeniedTools:    []string{"delete_file"},
					},
					CategoryAeron: {
						Model:          "google/gemini-2.0-flash-exp:free",
						Policy:         "read-only",
						MaxSteps:       10,
						TimeoutSeconds: 60,
						Description:    "Grunt — formatting, linting, boilerplate, monitoring",
						DeniedTools:    []string{"delete_file", "move_file", "copy_file", "execute_command", "async_execute_command", "web_search", "web_fetch", "arxiv_search", "run_javascript"},
					},
				},
			},
		},
	}
}

type Config struct {
	ActiveProvider  string
	Providers       map[string]ProviderConfig
	TurnLogsEnabled bool
	Agents          AgentsConfig
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

func activeProfileCfg(cfg *Config) (ProfileConfig, string) {
	ap := cfg.Agents.ActiveProfile
	if ap == "" {
		ap = "default"
	}
	if p, ok := cfg.Agents.Profiles[ap]; ok {
		return p, ap
	}
	return ProfileConfig{}, ap
}

func catConfigToSettings(v CategoryConfig) ttypes.CategorySettings {
	return ttypes.CategorySettings{
		Provider:       v.Provider,
		ProviderURL:    v.ProviderURL,
		APIKey:         v.APIKey,
		Model:          v.Model,
		ThinkingEffort: v.ThinkingEffort,
		Policy:         v.Policy,
		MaxSteps:       v.MaxSteps,
		TimeoutSeconds: v.TimeoutSeconds,
		Description:    v.Description,
	}
}

func catSettingsToConfig(v ttypes.CategorySettings) CategoryConfig {
	return CategoryConfig{
		Provider:       v.Provider,
		ProviderURL:    v.ProviderURL,
		APIKey:         v.APIKey,
		Model:          v.Model,
		ThinkingEffort: v.ThinkingEffort,
		Policy:         v.Policy,
		MaxSteps:       v.MaxSteps,
		TimeoutSeconds: v.TimeoutSeconds,
		Description:    v.Description,
	}
}

func boolPtr(b bool) *bool { return &b }

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

	activeProf, ap := activeProfileCfg(cfg)

	maEnabled := cfg.Agents.Enabled
	tlOn := cfg.TurnLogsEnabled
	if activeProf.MultiAgent != nil {
		maEnabled = *activeProf.MultiAgent
	}
	if activeProf.TurnLogs != nil {
		tlOn = *activeProf.TurnLogs
	}

	turnLogs := "on"
	if !tlOn {
		turnLogs = "off"
	}

	agentsSettings := ttypes.AgentsSettings{
		Enabled:       maEnabled,
		ActiveProfile: ap,
		MaxConcurrent: cfg.Agents.MaxConcurrent,
		MaxDepth:      cfg.Agents.MaxDepth,
		Categories:    make(map[string]ttypes.CategorySettings),
	}
	for k, v := range activeProf.Categories {
		agentsSettings.Categories[k] = catConfigToSettings(v)
	}

	if _, hasMain := agentsSettings.Categories["main"]; !hasMain {
		agentsSettings.Categories["main"] = ttypes.CategorySettings{
			Provider:       providerID,
			ProviderURL:    providerURL,
			APIKey:         apiKey,
			Model:          model,
			ThinkingEffort: thinkingEffort,
		}
	}

	for name, p := range cfg.Agents.Profiles {
		ps := ttypes.ProfileSettings{Name: name, Categories: make(map[string]ttypes.CategorySettings)}
		for k, v := range p.Categories {
			ps.Categories[k] = catConfigToSettings(v)
		}
		if p.MultiAgent != nil {
			if *p.MultiAgent {
				ps.MultiAgent = "on"
			} else {
				ps.MultiAgent = "off"
			}
		}
		if p.TurnLogs != nil {
			if *p.TurnLogs {
				ps.TurnLogs = "on"
			} else {
				ps.TurnLogs = "off"
			}
		}
		agentsSettings.Profiles = append(agentsSettings.Profiles, ps)
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
		Agents:             agentsSettings,
	}
}

func settingsToConfig(s ttypes.Settings, base *Config) *Config {
	activeProfile := s.Agents.ActiveProfile
	if activeProfile == "" {
		activeProfile = "default"
	}

	profiles := make(map[string]ProfileConfig)
	for _, ps := range s.Agents.Profiles {
		pc := ProfileConfig{
			Categories: make(map[string]CategoryConfig),
		}
		for k, v := range ps.Categories {
			pc.Categories[k] = catSettingsToConfig(v)
		}
		if ps.MultiAgent == "on" {
			pc.MultiAgent = boolPtr(true)
		} else if ps.MultiAgent == "off" {
			pc.MultiAgent = boolPtr(false)
		}
		if ps.TurnLogs == "on" {
			pc.TurnLogs = boolPtr(true)
		} else if ps.TurnLogs == "off" {
			pc.TurnLogs = boolPtr(false)
		}
		profiles[ps.Name] = pc
	}

	if _, exists := profiles[activeProfile]; !exists {
		profiles[activeProfile] = ProfileConfig{
			Categories: make(map[string]CategoryConfig),
		}
	}

	activeProf := profiles[activeProfile]

	maEnabled := base.Agents.Enabled
	tlOn := base.TurnLogsEnabled
	if activeProf.MultiAgent != nil {
		maEnabled = *activeProf.MultiAgent
	}
	if activeProf.TurnLogs != nil {
		tlOn = *activeProf.TurnLogs
	}

	providerID := s.ProviderID
	if providerID == "" {
		providerID = base.EffectiveProviderID()
	}

	mainCat := activeProf.Categories["main"]

	prov := mainCat.Provider
	if prov == "" {
		prov = providerID
	}
	pURL := mainCat.ProviderURL
	if pURL == "" {
		pURL = s.ProviderURL
	}
	aKey := mainCat.APIKey
	if aKey == "" {
		aKey = s.APIKey
		if aKey == "" {
			aKey = s.ProviderAPIKey
		}
	}
	pModel := mainCat.Model
	if pModel == "" {
		pModel = s.Model
	}
	pEffort := mainCat.ThinkingEffort
	if pEffort == "" {
		pEffort = s.ThinkingEffort
	}

	if prov == "" {
		prov = base.EffectiveProviderID()
	}

	providers := make(map[string]ProviderConfig, len(base.Providers))
	for k, v := range base.Providers {
		providers[k] = v
	}

	providers[prov] = ProviderConfig{
		APIKey:              aKey,
		Model:               pModel,
		ModelContextWindow:  s.ModelContextWindow,
		MainInputPrice:      s.MainInputPrice,
		MainOutputPrice:     s.MainOutputPrice,
		MainCacheReadPrice:  s.MainCacheReadPrice,
		ProviderURL:         pURL,
		ThinkingEffort:      pEffort,
	}

	agents := AgentsConfig{
		Enabled:       maEnabled,
		ActiveProfile: activeProfile,
		MaxConcurrent: s.Agents.MaxConcurrent,
		MaxDepth:      s.Agents.MaxDepth,
		Profiles:      profiles,
	}

	return &Config{
		ActiveProvider:  prov,
		Providers:      providers,
		TurnLogsEnabled: tlOn,
		Agents:         agents,
	}
}
