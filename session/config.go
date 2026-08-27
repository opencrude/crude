package session

import (
	"crude/provider"
	ttypes "crude/types"
)

const (
	CategoryDefault    = "default"
	CategoryResearcher = "researcher"
	CategoryCrafter    = "crafter"
	CategoryAeron      = "aeron"
	CategoryMemory     = "memory"
)

// NormalizeModel returns the bare model id for a category. The provider is
// stored separately, so any legacy "provider/model" string is reduced to the
// model part — "google/gemini-2.0-flash-exp:free" → "gemini-2.0-flash-exp:free".
// The provider is never shown in front of crude's name; only the model is.
func NormalizeModel(model string) string {
	return provider.StripProviderPrefix(model)
}

// Live-inheritance markers used on a session category to point it at a
// configuration source rather than storing a copied value.
const (
	// InheritMain follows this session's main agent live.
	InheritMain = "main"
	// InheritWorkspace follows the workspace baseline's main agent live.
	InheritWorkspace = "workspace"
)

type CategoryConfig struct {
	Provider    string `json:"provider,omitempty"`
	ProviderURL string `json:"provider_url,omitempty"`
	// APIKey is filled at use time from Config.Providers. Agent profiles
	// store a provider ID, never credentials — json:"-" so legacy
	// category api_key fields are ignored on load and omitted on save.
	APIKey string `json:"-"`
	// OAuth is filled at use time from Config.Providers, like APIKey. It is
	// never persisted on a category — json:"-" keeps it out of agent profiles.
	OAuth          *provider.OAuthCredentials `json:"-"`
	APIStandard    string                     `json:"api_standard,omitempty"`
	Model          string                     `json:"model,omitempty"`
	ThinkingEffort string                     `json:"thinking_effort,omitempty"`
	// Inherit is the session-level live-inheritance marker: "main" follows
	// this session's main agent, "workspace" follows the workspace's main
	// agent. A category carrying a marker stores no provider/model values;
	// resolution follows the marker at read time. Empty means explicit.
	Inherit      string   `json:"inherit,omitempty"`
	Policy       string   `json:"policy,omitempty"`
	MaxSteps     int      `json:"max_steps,omitempty"`
	Description  string   `json:"description,omitempty"`
	AllowedTools []string `json:"allowed_tools,omitempty"`
	DeniedTools  []string `json:"denied_tools,omitempty"`

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

// DefaultAgentsConfig returns the built-in agent scaffolding. No category
// ships a model: the user configures the main agent (and any role that should
// not inherit it) explicitly. Roles without a model inherit the main agent's
// configuration at read time.
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
						Policy:       "read-only",
						MaxSteps:     1000,
						Description:  "Gatherer — explores codebase, searches web, runs experiments",
						AllowedTools: []string{"read_file", "glob_files", "grep_files", "list_directory", "web_search", "web_fetch", "arxiv_search", "run_javascript", "write_report", "question"},
						DeniedTools:  []string{"write_file", "edit_file", "delete_file", "move_file", "copy_file", "execute_command", "async_execute_command"},
					},
					CategoryCrafter: {
						Policy:      "no-delete",
						MaxSteps:    1000,
						Description: "Implementer — writes code, edits files, runs tests",
						DeniedTools: []string{"delete_file"},
					},
					CategoryAeron: {
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
	Memory          ttypes.MemorySettings

	// workspaceMain is a copy of the workspace baseline's main-agent
	// category. Categories carrying Inherit="workspace" resolve through it
	// at read time so they follow the live workspace main agent. It is
	// populated by configWithSessionOverrides and carried through copyConfig;
	// nil disables workspace-marker resolution (the marker is ignored).
	workspaceMain *CategoryConfig
}

type ProviderConfig struct {
	APIKey         string                     `json:"api_key,omitempty"`
	ProviderURL    string                     `json:"provider_url,omitempty"`
	APIStandard    string                     `json:"api_standard,omitempty"`
	Model          string                     `json:"model,omitempty"`
	ModelStandards map[string]string          `json:"model_standards,omitempty"`
	ThinkingEffort string                     `json:"thinking_effort,omitempty"`
	OAuth          *provider.OAuthCredentials `json:"oauth,omitempty"`
	Dialer         *provider.DialSettings     `json:"dialer,omitempty"`
}

func (c ProviderConfig) StandardForModel(model, providerID string) string {
	if standard := c.ModelStandards[model]; standard != "" {
		return standard
	}
	if c.APIStandard != "" {
		return c.APIStandard
	}
	return provider.DefaultStandardID(providerID)
}

// providerForCategory builds the provider for one agent category, resolving
// credentials and transport settings from the stored provider entry.
func (c *Config) providerForCategory(category CategoryConfig) (*provider.Provider, error) {
	p := provider.FromPreset(category.Provider, category.APIKey, category.ProviderURL)
	p.Model = category.Model
	p.SetThinkingEffort(category.ThinkingEffort)
	if err := p.SetStandardID(category.APIStandard); err != nil {
		return nil, err
	}
	if category.OAuth != nil {
		p.SetOAuth(category.OAuth)
	}
	if dialer := c.dialerFor(category.Provider); dialer != nil {
		p.SetDialSettings(*dialer)
	}
	return p, nil
}

// dialerFor returns the stored transport settings for a provider, falling back
// to the effective provider when the category's own entry has none.
func (c *Config) dialerFor(providerID string) *provider.DialSettings {
	if c == nil {
		return nil
	}
	if cfg, ok := c.Providers[providerID]; ok && cfg.Dialer != nil {
		return cfg.Dialer
	}
	if cfg, ok := c.Providers[c.EffectiveProviderID()]; ok && cfg.Dialer != nil {
		return cfg.Dialer
	}
	return nil
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

// EnsureAgentsDefaults restores product defaults that JSON omitempty cannot
// represent. A missing multi-agent flag is on (DefaultAgentsConfig), not the
// bool zero value that made refactored configs silently disable sub-agents.
func EnsureAgentsDefaults(a *AgentsConfig) {
	if a == nil {
		return
	}
	def := DefaultAgentsConfig()
	if a.ActiveProfile == "" {
		a.ActiveProfile = def.ActiveProfile
	}
	if a.MaxConcurrent == 0 {
		a.MaxConcurrent = def.MaxConcurrent
	}
	if a.Profiles == nil {
		a.Profiles = make(map[string]ProfileConfig)
	}
	prof := a.Profiles[a.ActiveProfile]
	if prof.MultiAgent == nil {
		enabled := true
		prof.MultiAgent = &enabled
		a.Profiles[a.ActiveProfile] = prof
	}
	if !a.Enabled {
		a.Enabled = *prof.MultiAgent
	}
}

func applySessionMultiAgent(cfg *Config, enabled bool) {
	if cfg == nil {
		return
	}
	cfg.Agents.Enabled = enabled
	prof, name := activeProfileCfg(cfg)
	if cfg.Agents.Profiles == nil {
		cfg.Agents.Profiles = make(map[string]ProfileConfig)
	}
	value := enabled
	prof.MultiAgent = &value
	cfg.Agents.Profiles[name] = prof
}

func cloneBoolPtr(src *bool) *bool {
	if src == nil {
		return nil
	}
	value := *src
	return &value
}

// GetActiveDefaultCategory resolves the main agent ("default", legacy "main")
// category. Inheritance is resolved live at read time:
//
//	main without a stored category → the workspace baseline (via the
//	  EffectiveProviderID + provider settings fallbacks)
//	main marked Inherit="workspace" → the workspace baseline main agent
//
// The returned value is a resolved copy; callers may mutate it freely.
func (c *Config) GetActiveDefaultCategory() CategoryConfig {
	prof, _ := activeProfileCfg(c)
	// Try "default" first, then backward-compat "main"
	for _, key := range []string{CategoryDefault, "main"} {
		if cat, ok := prof.Categories[key]; ok {
			if cat.Inherit == InheritWorkspace && c.workspaceMain != nil {
				resolved := *c.workspaceMain
				c.resolveCategoryDefaults(&resolved)
				return resolved
			}
			c.resolveCategoryDefaults(&cat)
			return cat
		}
	}
	cat := CategoryConfig{Provider: c.EffectiveProviderID()}
	c.resolveCategoryDefaults(&cat)
	return cat
}

// GetCategoryConfig resolves a role's effective configuration live:
//
//	role with stored manual values    → those values
//	role marked Inherit="main"        → this session's main agent
//	role marked Inherit="workspace"   → the workspace main agent
//	role absent from the profile      → this session's main agent
//	role present but no provider/model/thinking → its own values, with the
//	  empty fields field-inherited from the main agent
//
// Editing the source therefore propagates to every role that inherits it.
func (c *Config) GetCategoryConfig(category string) CategoryConfig {
	if category == "" || category == CategoryDefault || category == "main" {
		return c.GetActiveDefaultCategory()
	}
	prof, _ := activeProfileCfg(c)
	if cat, ok := prof.Categories[category]; ok {
		switch {
		case cat.Inherit == InheritWorkspace && c.workspaceMain != nil:
			resolved := *c.workspaceMain
			c.resolveCategoryDefaults(&resolved)
			return resolved
		case cat.Inherit == InheritMain:
			return c.GetActiveDefaultCategory()
		}
		c.resolveCategoryDefaults(&cat)
		// Field-level inheritance: a role that doesn't set its own
		// provider/model/thinking takes them from the main agent.
		if cat.Model == "" || cat.Provider == "" || cat.ThinkingEffort == "" || cat.APIStandard == "" {
			main := c.GetActiveDefaultCategory()
			if cat.Provider == "" {
				cat.Provider = main.Provider
			}
			if cat.Model == "" {
				cat.Model = main.Model
			}
			if cat.ThinkingEffort == "" {
				cat.ThinkingEffort = main.ThinkingEffort
			}
			if cat.APIStandard == "" {
				cat.APIStandard = main.APIStandard
			}
			c.resolveCategoryDefaults(&cat)
		}
		return cat
	}
	return c.GetActiveDefaultCategory()
}

// GetMemoryCategory resolves the memory agent's configuration. It behaves like
// any other agent: an explicit session override (profile Categories["memory"]),
// then the configured MemoryCategory, wins; otherwise it inherits the main
// agent live. Backward-compat Memory.Model (a bare override) is folded in when
// the memory category sets no model of its own.
func (c *Config) GetMemoryCategory() CategoryConfig {
	// A live session/workspace override stored in the active profile.
	prof, _ := activeProfileCfg(c)
	if cat, ok := prof.Categories[CategoryMemory]; ok {
		switch {
		case cat.Inherit == InheritWorkspace && c.workspaceMain != nil:
			resolved := *c.workspaceMain
			c.resolveCategoryDefaults(&resolved)
			return resolved
		case cat.Inherit == InheritMain:
			return c.GetActiveDefaultCategory()
		}
		c.resolveCategoryDefaults(&cat)
		return cat
	}
	cat := catSettingsToConfig(c.Memory.MemoryCategory)
	// A legacy bare model override still applies when no explicit memory
	// category model is set.
	if cat.Model == "" && c.Memory.Model != "" {
		cat.Model = c.Memory.Model
	}
	if cat.Model != "" || cat.Provider != "" || cat.ThinkingEffort != "" {
		c.resolveCategoryDefaults(&cat)
		return cat
	}
	return c.GetActiveDefaultCategory()
}

func (c *Config) resolveCategoryDefaults(cat *CategoryConfig) {
	if cat.Provider == "" {
		cat.Provider = c.EffectiveProviderID()
	}
	prov, ok := c.Providers[cat.Provider]
	if !ok {
		prov, ok = c.Providers[c.EffectiveProviderID()]
	}
	if ok {
		// Always take credentials from the provider. A stored category key
		// is leftover from older configs and must not shadow Settings.
		cat.APIKey = prov.APIKey
		cat.OAuth = prov.OAuth
		if cat.ProviderURL == "" {
			cat.ProviderURL = prov.ProviderURL
		}
		// No provider→model inheritance: an agent's model is set on the agent
		// (or inherited from the main agent), never on a provider.
		if cat.ThinkingEffort == "" {
			cat.ThinkingEffort = prov.ThinkingEffort
		}
	}
	if cat.ProviderURL == "" {
		p := provider.GetPreset(cat.Provider)
		if p != nil {
			cat.ProviderURL = p.BaseURL
		}
	}
	// The model is a bare model id; the provider is its own field. Normalize
	// any legacy "provider/model" string so display and callers never see the
	// provider doubled up in front of crude's name.
	cat.Model = NormalizeModel(cat.Model)
	if cat.APIStandard == "" {
		if configured, ok := c.Providers[cat.Provider]; ok {
			cat.APIStandard = configured.StandardForModel(cat.Model, cat.Provider)
		} else {
			cat.APIStandard = provider.DefaultStandardID(cat.Provider)
		}
	}
}

// StripStoredAgentAPIKeys removes credentials from every agent profile
// category. Profiles reference a provider; keys live on Config.Providers.
func StripStoredAgentAPIKeys(agents *AgentsConfig) {
	if agents == nil {
		return
	}
	for name, profile := range agents.Profiles {
		for id, cat := range profile.Categories {
			cat.APIKey = ""
			profile.Categories[id] = cat
		}
		agents.Profiles[name] = profile
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
		APIStandard:         v.APIStandard,
		Model:               v.Model,
		ThinkingEffort:      v.ThinkingEffort,
		Inherit:             v.Inherit,
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
		APIStandard:         v.APIStandard,
		Model:               NormalizeModel(v.Model),
		ThinkingEffort:      v.ThinkingEffort,
		Inherit:             v.Inherit,
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
	for k := range activeProf.Categories {
		// Resolve through the live inheritance chain so a role marked
		// Inherit="main"/"workspace" reports its effective provider/model.
		agentsSettings.Categories[k] = catConfigToSettings(cfg.GetCategoryConfig(k))
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
		APIStandard:     mainCat.APIStandard,
		ThinkingEffort:  mainCat.ThinkingEffort,
		TurnLogsEnabled: turnLogs,
		Agents:          agentsSettings,
		Memory:          cfg.Memory,
	}
}

// ApplyWorkspaceAgentDefaults overlays workspace-scoped agent defaults onto
// the active profile. Provider credentials remain app-global; new sessions
// inherit these role defaults unless their own DB contains an override.
func ApplyWorkspaceAgentDefaults(cfg *Config, agents ttypes.AgentsSettings) {
	if cfg == nil {
		return
	}
	if len(agents.Categories) == 0 && agents.ActiveProfile == "" &&
		agents.MaxConcurrent == 0 && len(agents.Profiles) == 0 {
		return
	}
	if cfg.Agents.Profiles == nil {
		cfg.Agents.Profiles = make(map[string]ProfileConfig)
	}
	profileName := agents.ActiveProfile
	if profileName == "" {
		profileName = cfg.Agents.ActiveProfile
	}
	if profileName == "" {
		profileName = "default"
	}
	cfg.Agents.ActiveProfile = profileName
	cfg.Agents.Enabled = agents.Enabled
	if agents.MaxConcurrent > 0 {
		cfg.Agents.MaxConcurrent = agents.MaxConcurrent
	}
	profile := cfg.Agents.Profiles[profileName]
	enabled := agents.Enabled
	profile.MultiAgent = &enabled
	if profile.Categories == nil {
		profile.Categories = make(map[string]CategoryConfig)
	}
	for category, value := range agents.Categories {
		existing := profile.Categories[category]
		merged := mergeCategoryConfig(existing, catSettingsToConfig(value))
		merged.APIKey = ""
		profile.Categories[category] = merged
	}
	cfg.Agents.Profiles[profileName] = profile
	StripStoredAgentAPIKeys(&cfg.Agents)
}
