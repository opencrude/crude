package session

import (
	"crude/provider"
	ttypes "crude/tui/types"
)

const (
	CategoryDefault    = "default"
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
	Description    string   `json:"description,omitempty"`
	AllowedTools   []string `json:"allowed_tools,omitempty"`
	DeniedTools    []string `json:"denied_tools,omitempty"`

	ModelContextWindow  int     `json:"model_context_window,omitempty"`
	ModelInputPrice     float64 `json:"model_input_price,omitempty"`
	ModelOutputPrice    float64 `json:"model_output_price,omitempty"`
	ModelCachePrice     float64 `json:"model_cache_price,omitempty"`
	ModelSupportsVision bool    `json:"model_supports_vision,omitempty"`
	ModelSupportsTools  bool    `json:"model_supports_tools,omitempty"`
	ModelMaxOutput      int     `json:"model_max_output,omitempty"`
	ModelAttachment     bool    `json:"model_attachment,omitempty"`
}

type ProfileConfig struct {
	Categories map[string]CategoryConfig `json:"categories,omitempty"`
	MultiAgent *bool                     `json:"multi_agent,omitempty"`
	TurnLogs   *bool                     `json:"turn_logs,omitempty"`
}

type AgentsConfig struct {
	Enabled       bool                          `json:"enabled,omitempty"`
	ActiveProfile string                        `json:"active_profile,omitempty"`
	MaxConcurrent int                           `json:"max_concurrent,omitempty"`
	Profiles      map[string]ProfileConfig      `json:"profiles,omitempty"`
	AgentProfiles map[string]AgentProfileConfig `json:"agent_profiles,omitempty"`
}

type AgentProfileConfig struct {
	Category      string   `json:"category,omitempty"`
	Description   string   `json:"description,omitempty"`
	Model         string   `json:"model,omitempty"`
	Provider      string   `json:"provider,omitempty"`
	Instructions  string   `json:"instructions,omitempty"`
	AllowedTools  []string `json:"allowed_tools,omitempty"`
	DeniedTools   []string `json:"denied_tools,omitempty"`
	FileOwnership []string `json:"file_ownership,omitempty"`
	MaxSteps      int      `json:"max_steps,omitempty"`
}

func DefaultAgentsConfig() AgentsConfig {
	t := true
	return AgentsConfig{
		Enabled:       true,
		ActiveProfile: "default",
		MaxConcurrent: 4,
		Profiles: map[string]ProfileConfig{
			"default": {
				MultiAgent: &t,
				TurnLogs:   &t,
				Categories: map[string]CategoryConfig{
					CategoryResearcher: {
						Model:        "google/gemini-2.0-flash-exp:free",
						Policy:       "read-only",
						MaxSteps:     1000,
						Description:  "Gatherer — explores codebase, searches web, runs experiments",
						AllowedTools: []string{"read_file", "glob_files", "grep_files", "list_directory", "web_search", "web_fetch", "arxiv_search", "run_javascript", "write_report", "question"},
						DeniedTools:  []string{"write_file", "edit_file", "delete_file", "move_file", "copy_file", "execute_command", "async_execute_command"},
					},
					CategoryCrafter: {
						Model:       "qwen/qwen3.5-235b-a22b",
						Policy:      "no-delete",
						MaxSteps:    1000,
						Description: "Implementer — writes code, edits files, runs tests",
						DeniedTools: []string{"delete_file"},
					},
					CategoryAeron: {
						Model:       "google/gemini-2.0-flash-exp:free",
						Policy:      "read-only",
						MaxSteps:    1000,
						Description: "Grunt — formatting, linting, boilerplate, monitoring",
						DeniedTools: []string{"delete_file", "move_file", "copy_file", "execute_command", "async_execute_command", "web_search", "web_fetch", "arxiv_search", "run_javascript"},
					},
				},
			},
		},
		AgentProfiles: map[string]AgentProfileConfig{
			"explorer": {
				Category:    CategoryResearcher,
				Description: "Read-only codebase explorer for research and investigation",
				MaxSteps:    1000,
			},
			"reviewer": {
				Category:    CategoryResearcher,
				Description: "Code reviewer with expensive model for thorough analysis",
				MaxSteps:    1000,
			},
			"fixer": {
				Category:    CategoryCrafter,
				Description: "Implementation-focused agent for fixing bugs and writing code",
				MaxSteps:    1000,
			},
			"tester": {
				Category:    CategoryCrafter,
				Description: "Test writer agent focused on writing and running tests",
				MaxSteps:    1000,
			},
			"formatter": {
				Category:    CategoryAeron,
				Description: "Quick formatting and linting agent",
				MaxSteps:    1000,
			},
			"monitor": {
				Category:    CategoryAeron,
				Description: "Watches running sub-agents and reports status",
				MaxSteps:    1000,
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
	ProviderURL    string `json:"provider_url,omitempty"`
	Model          string `json:"model,omitempty"`
	ThinkingEffort string `json:"thinking_effort,omitempty"`
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

func (c *Config) IsMultiAgentEnabled() bool {
	prof, _ := activeProfileCfg(c)
	if prof.MultiAgent != nil {
		return *prof.MultiAgent
	}
	return c.Agents.Enabled
}

func (c *Config) GetActiveDefaultCategory() CategoryConfig {
	prof, _ := activeProfileCfg(c)
	// Try "default" first, then backward-compat "main"
	if cat, ok := prof.Categories[CategoryDefault]; ok {
		c.resolveCategoryDefaults(&cat)
		return cat
	}
	if cat, ok := prof.Categories["main"]; ok {
		c.resolveCategoryDefaults(&cat)
		return cat
	}
	cat := CategoryConfig{Provider: c.EffectiveProviderID()}
	c.resolveCategoryDefaults(&cat)
	return cat
}

func (c *Config) GetCategoryConfig(category string) CategoryConfig {
	prof, _ := activeProfileCfg(c)
	if cat, ok := prof.Categories[category]; ok {
		c.resolveCategoryDefaults(&cat)
		return cat
	}
	return c.GetActiveDefaultCategory()
}

func (c *Config) resolveCategoryDefaults(cat *CategoryConfig) {
	if cat.Provider == "" {
		cat.Provider = c.EffectiveProviderID()
	}
	if cat.APIKey == "" || cat.ProviderURL == "" || cat.Model == "" || cat.ThinkingEffort == "" {
		if prov, ok := c.Providers[cat.Provider]; ok {
			if cat.APIKey == "" {
				cat.APIKey = prov.APIKey
			}
			if cat.ProviderURL == "" {
				cat.ProviderURL = prov.ProviderURL
			}
			if cat.Model == "" {
				cat.Model = prov.Model
			}
			if cat.ThinkingEffort == "" {
				cat.ThinkingEffort = prov.ThinkingEffort
			}
		} else if prov, ok := c.Providers[c.EffectiveProviderID()]; ok {
			if cat.APIKey == "" {
				cat.APIKey = prov.APIKey
			}
			if cat.ProviderURL == "" {
				cat.ProviderURL = prov.ProviderURL
			}
			if cat.Model == "" {
				cat.Model = prov.Model
			}
			if cat.ThinkingEffort == "" {
				cat.ThinkingEffort = prov.ThinkingEffort
			}
		}
	}
	if cat.ProviderURL == "" {
		p := provider.GetPreset(cat.Provider)
		if p != nil {
			cat.ProviderURL = p.BaseURL
		}
	}
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
		Provider:            v.Provider,
		ProviderURL:         v.ProviderURL,
		APIKey:              v.APIKey,
		Model:               v.Model,
		ThinkingEffort:      v.ThinkingEffort,
		Policy:              v.Policy,
		MaxSteps:            v.MaxSteps,
		Description:         v.Description,
		ModelContextWindow:  v.ModelContextWindow,
		ModelInputPrice:     v.ModelInputPrice,
		ModelOutputPrice:    v.ModelOutputPrice,
		ModelCachePrice:     v.ModelCachePrice,
		ModelSupportsVision: v.ModelSupportsVision,
		ModelSupportsTools:  v.ModelSupportsTools,
		ModelMaxOutput:      v.ModelMaxOutput,
		ModelAttachment:     v.ModelAttachment,
	}
}

func catSettingsToConfig(v ttypes.CategorySettings) CategoryConfig {
	return CategoryConfig{
		Provider:            v.Provider,
		ProviderURL:         v.ProviderURL,
		APIKey:              v.APIKey,
		Model:               v.Model,
		ThinkingEffort:      v.ThinkingEffort,
		Policy:              v.Policy,
		MaxSteps:            v.MaxSteps,
		Description:         v.Description,
		ModelContextWindow:  v.ModelContextWindow,
		ModelInputPrice:     v.ModelInputPrice,
		ModelOutputPrice:    v.ModelOutputPrice,
		ModelCachePrice:     v.ModelCachePrice,
		ModelSupportsVision: v.ModelSupportsVision,
		ModelSupportsTools:  v.ModelSupportsTools,
		ModelMaxOutput:      v.ModelMaxOutput,
		ModelAttachment:     v.ModelAttachment,
	}
}

func boolPtr(b bool) *bool { return &b }

func ConfigToSettings(cfg *Config) ttypes.Settings {
	mainCat := cfg.GetActiveDefaultCategory()

	providerURL := mainCat.ProviderURL
	if providerURL == "" {
		p := provider.GetPreset(mainCat.Provider)
		if p == nil {
			p = provider.GetPreset("openrouter")
		}
		providerURL = p.BaseURL
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
		Categories:    make(map[string]ttypes.CategorySettings),
	}
	for k, v := range activeProf.Categories {
		resolved := v
		cfg.resolveCategoryDefaults(&resolved)
		agentsSettings.Categories[k] = catConfigToSettings(resolved)
	}

	if _, hasDefault := agentsSettings.Categories[CategoryDefault]; !hasDefault {
		agentsSettings.Categories[CategoryDefault] = catConfigToSettings(mainCat)
	}

	for name, p := range cfg.Agents.Profiles {
		ps := ttypes.ProfileSettings{Name: name, Categories: make(map[string]ttypes.CategorySettings)}
		for k, v := range p.Categories {
			resolved := v
			cfg.resolveCategoryDefaults(&resolved)
			ps.Categories[k] = catConfigToSettings(resolved)
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
		APIKey:          mainCat.APIKey,
		Model:           mainCat.Model,
		ProviderID:      mainCat.Provider,
		ProviderURL:     providerURL,
		ProviderAPIKey:  mainCat.APIKey,
		ThinkingEffort:  mainCat.ThinkingEffort,
		TurnLogsEnabled: turnLogs,
		Agents:          agentsSettings,
	}
}

// ApplyWorkspaceAgentDefaults overlays workspace-scoped agent defaults onto
// the active profile. Provider credentials remain app-global; new sessions
// inherit these role defaults unless their own DB contains an override.
func ApplyWorkspaceAgentDefaults(cfg *Config, agents ttypes.AgentsSettings) {
	if cfg == nil || len(agents.Categories) == 0 {
		return
	}
	if cfg.Agents.Profiles == nil {
		cfg.Agents.Profiles = make(map[string]ProfileConfig)
	}
	profileName := cfg.Agents.ActiveProfile
	if profileName == "" {
		profileName = "default"
		cfg.Agents.ActiveProfile = profileName
	}
	profile := cfg.Agents.Profiles[profileName]
	if profile.Categories == nil {
		profile.Categories = make(map[string]CategoryConfig)
	}
	for category, value := range agents.Categories {
		existing := profile.Categories[category]
		profile.Categories[category] = mergeCategoryConfig(existing, catSettingsToConfig(value))
	}
	cfg.Agents.Profiles[profileName] = profile
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

	// Backward compat: migrate "main" to "default"
	if _, ok := activeProf.Categories[CategoryDefault]; !ok {
		if mc, ok := activeProf.Categories["main"]; ok {
			activeProf.Categories[CategoryDefault] = mc
			delete(activeProf.Categories, "main")
		}
	}
	mainCat := activeProf.Categories[CategoryDefault]
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

	if mainCat.Model == "" {
		mainCat.Model = pModel
	}
	if mainCat.Provider == "" {
		mainCat.Provider = prov
	}
	if mainCat.ProviderURL == "" {
		mainCat.ProviderURL = pURL
	}
	if mainCat.APIKey == "" {
		mainCat.APIKey = aKey
	}
	if mainCat.ThinkingEffort == "" {
		mainCat.ThinkingEffort = pEffort
	}
	activeProf.Categories[CategoryDefault] = mainCat
	profiles[activeProfile] = activeProf

	providers := make(map[string]ProviderConfig, len(base.Providers))
	for k, v := range base.Providers {
		providers[k] = v
	}

	providers[prov] = ProviderConfig{
		APIKey:         aKey,
		ProviderURL:    pURL,
		Model:          pModel,
		ThinkingEffort: pEffort,
	}

	agents := AgentsConfig{
		Enabled:       maEnabled,
		ActiveProfile: activeProfile,
		MaxConcurrent: s.Agents.MaxConcurrent,
		Profiles:      profiles,
	}

	return &Config{
		ActiveProvider:  prov,
		Providers:       providers,
		TurnLogsEnabled: tlOn,
		Agents:          agents,
	}
}
