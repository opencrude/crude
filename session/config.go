package session

import (
	"crude/provider"
	ttypes "crude/types"
	"fmt"
)

const (
	CategoryDefault    = "default"
	CategoryResearcher = "researcher"
	CategoryCrafter    = "crafter"
	CategoryAeron      = "aeron"
	CategoryMemory     = "memory"
)

// Live-inheritance markers used on a session category to point it at a
// configuration source rather than storing a copied value.
const (
	// InheritMain follows this session's main agent live.
	InheritMain = "main"
	// InheritWorkspace follows the workspace baseline's main agent live.
	InheritWorkspace = "workspace"
)

type CategoryConfig struct {
	AccountID      string `json:"account_id,omitempty"`
	Model          string `json:"model,omitempty"`
	ThinkingEffort string `json:"thinking_effort,omitempty"`
	// Inherit is the session-level live-inheritance marker: "main" follows
	// this session's main agent, "workspace" follows the workspace's main
	// agent. A category carrying a marker stores no provider/model values;
	// resolution follows the marker at read time. Empty means explicit.
	Inherit      string   `json:"inherit,omitempty"`
	Policy       string   `json:"policy,omitempty"`
	Description  string   `json:"description,omitempty"`
	AllowedTools []string `json:"allowed_tools,omitempty"`
	DeniedTools  []string `json:"denied_tools,omitempty"`

	ModelContextWindow int     `json:"model_context_window,omitempty"`
	ModelInputPrice    float64 `json:"model_input_price,omitempty"`
	ModelOutputPrice   float64 `json:"model_output_price,omitempty"`
	ModelCachePrice    float64 `json:"model_cache_price,omitempty"`
	// Modalities lists accepted input types beyond text (image, pdf,
	// video, audio).
	ModelModalities    []string `json:"model_modalities,omitempty"`
	ModelSupportsTools bool     `json:"model_supports_tools,omitempty"`
	ModelMaxOutput     int      `json:"model_max_output,omitempty"`
	// Sampling overrides; nil leaves the backend default in effect.
	ModelTemperature         *float64 `json:"model_temperature,omitempty"`
	ModelTopP                *float64 `json:"model_top_p,omitempty"`
	ContextCompactionPercent *int     `json:"context_compaction_percent,omitempty"`
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
	Category                 string   `json:"category,omitempty"`
	Description              string   `json:"description,omitempty"`
	Model                    string   `json:"model,omitempty"`
	AccountID                string   `json:"account_id,omitempty"`
	Instructions             string   `json:"instructions,omitempty"`
	AllowedTools             []string `json:"allowed_tools,omitempty"`
	DeniedTools              []string `json:"denied_tools,omitempty"`
	FileOwnership            []string `json:"file_ownership,omitempty"`
	ContextCompactionPercent *int     `json:"context_compaction_percent,omitempty"`
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
						Description:  "Gatherer — explores codebase, searches web, runs experiments",
						AllowedTools: []string{"read_file", "glob_files", "grep_files", "list_directory", "web_search", "web_fetch", "arxiv_search", "write_report", "question"},
						DeniedTools:  []string{"write_file", "edit_file", "delete_file", "move_file", "copy_file", "execute_command", "async_execute_command"},
					},
					CategoryCrafter: {
						Policy:      "no-delete",
						Description: "Implementer — writes code, edits files, runs tests",
						DeniedTools: []string{"delete_file"},
					},
					CategoryAeron: {
						Policy:      "read-only",
						Description: "Grunt — formatting, linting, boilerplate, monitoring",
						DeniedTools: []string{"delete_file", "move_file", "copy_file", "execute_command", "async_execute_command", "web_search", "web_fetch", "arxiv_search"},
					},
				},
			},
		},
		AgentProfiles: map[string]AgentProfileConfig{
			"explorer": {
				Category:    CategoryResearcher,
				Description: "Read-only codebase explorer for research and investigation",
			},
			"reviewer": {
				Category:    CategoryResearcher,
				Description: "Code reviewer with expensive model for thorough analysis",
			},
			"fixer": {
				Category:    CategoryCrafter,
				Description: "Implementation-focused agent for fixing bugs and writing code",
			},
			"tester": {
				Category:    CategoryCrafter,
				Description: "Test writer agent focused on writing and running tests",
			},
			"formatter": {
				Category:    CategoryAeron,
				Description: "Quick formatting and linting agent",
			},
			"monitor": {
				Category:    CategoryAeron,
				Description: "Watches running sub-agents and reports status",
			},
		},
	}
}

type Config struct {
	// ActiveProvider is retained only as an app-settings projection. Runtime
	// routing never reads it; each agent must select an account ID.
	ActiveProvider  string
	Providers       map[string]ProviderConfig
	Accounts        map[string]provider.Account
	AccountManager  *provider.AccountManager
	TurnLogsEnabled bool
	Agents          AgentsConfig
	Memory          ttypes.MemorySettings
	Safety          ttypes.SafetySettings

	// workspaceMain is a copy of the workspace baseline's main-agent
	// category. Categories carrying Inherit="workspace" resolve through it
	// at read time so they follow the live workspace main agent. It is
	// populated by configWithSessionOverrides and carried through copyConfig;
	// nil disables workspace-marker resolution (the marker is ignored).
	workspaceMain *CategoryConfig
}

type ProviderConfig = provider.Template

// providerForCategory resolves all transport behavior from the selected
// account's template and account overrides, then binds that account's auth.
func (c *Config) providerForCategory(category CategoryConfig) (*provider.Provider, error) {
	account, err := c.resolveAccount(category.AccountID)
	if err != nil {
		return nil, err
	}
	template, err := c.templateForAccount(account)
	if err != nil {
		return nil, err
	}
	p := &provider.Provider{ID: account.ProviderID, Name: template.Name}
	if err := template.Apply(p); err != nil {
		return nil, fmt.Errorf("account %q template %q: %w", account.ID, account.ProviderID, err)
	}
	p.Model = category.Model
	p.SetThinkingEffort(category.ThinkingEffort)
	p.SetSampling(category.ModelTemperature, category.ModelTopP)
	if err := c.bindAccount(p, category.AccountID); err != nil {
		return nil, err
	}
	return p, nil
}

func (c *Config) GetActiveProviderConfig() ProviderConfig {
	if c.Providers == nil {
		return ProviderConfig{}
	}
	if cfg, ok := c.Providers[c.EffectiveProviderID()]; ok {
		return cfg
	}
	return ProviderConfig{}
}

func (c *Config) SetActiveProviderConfig(templateID string, cfg ProviderConfig) {
	if c.Providers == nil {
		c.Providers = make(map[string]ProviderConfig)
	}
	c.Providers[templateID] = provider.CloneTemplate(cfg)
}

func (c *Config) EffectiveProviderID() string {
	if c == nil {
		return ""
	}
	return c.templateIDForAccount(c.GetActiveDefaultCategory().AccountID)
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
// category. No provider or account is inferred when the category is absent.
func (c *Config) GetActiveDefaultCategory() CategoryConfig {
	prof, _ := activeProfileCfg(c)
	for _, key := range []string{CategoryDefault, "main"} {
		if cat, ok := prof.Categories[key]; ok {
			if cat.Inherit == InheritWorkspace && c.workspaceMain != nil {
				resolved := *c.workspaceMain
				if cat.AccountID != "" {
					resolved.AccountID = cat.AccountID
				}
				c.resolveCategoryDefaults(&resolved)
				return resolved
			}
			c.resolveCategoryDefaults(&cat)
			return cat
		}
	}
	cat := CategoryConfig{}
	c.resolveCategoryDefaults(&cat)
	return cat
}

// GetCategoryConfig resolves a role's effective configuration live:
//
//	role with stored manual values    → those values
//	role marked Inherit="main"        → this session's main agent
//	role marked Inherit="workspace"   → the workspace main agent
//	role absent from the profile      → this session's main agent
//	role present but no account/model/thinking → its own values, with empty
//	  fields inherited from the main agent
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
			if cat.AccountID != "" {
				resolved.AccountID = cat.AccountID
			}
			c.resolveCategoryDefaults(&resolved)
			return resolved
		case cat.Inherit == InheritMain:
			main := c.GetActiveDefaultCategory()
			if cat.AccountID != "" {
				main.AccountID = cat.AccountID
			}
			return main
		}
		inheritsAccount := cat.AccountID == ""
		c.resolveCategoryDefaults(&cat)
		// Models and thinking remain role-owned; only absent values inherit.
		if cat.Model == "" || inheritsAccount || cat.ThinkingEffort == "" {
			main := c.GetActiveDefaultCategory()
			if inheritsAccount {
				cat.AccountID = main.AccountID
			}
			if cat.Model == "" {
				cat.Model = main.Model
			}
			if cat.ThinkingEffort == "" {
				cat.ThinkingEffort = main.ThinkingEffort
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
			if cat.AccountID != "" {
				resolved.AccountID = cat.AccountID
			}
			c.resolveCategoryDefaults(&resolved)
			return resolved
		case cat.Inherit == InheritMain:
			main := c.GetActiveDefaultCategory()
			if cat.AccountID != "" {
				main.AccountID = cat.AccountID
			}
			return main
		}
		if cat.AccountID == "" {
			main := c.GetActiveDefaultCategory()
			cat.AccountID = main.AccountID
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
	if cat.Model != "" || cat.AccountID != "" || cat.ThinkingEffort != "" {
		if cat.AccountID == "" {
			main := c.GetActiveDefaultCategory()
			cat.AccountID = main.AccountID
		}
		c.resolveCategoryDefaults(&cat)
		return cat
	}
	return c.GetActiveDefaultCategory()
}

func (c *Config) resolveCategoryDefaults(cat *CategoryConfig) {
	if cat.ContextCompactionPercent == nil {
		value := ttypes.DefaultContextCompactionPercent
		cat.ContextCompactionPercent = &value
	} else {
		value := min(100, max(0, *cat.ContextCompactionPercent))
		cat.ContextCompactionPercent = &value
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
		AccountID:                v.AccountID,
		Model:                    v.Model,
		ThinkingEffort:           v.ThinkingEffort,
		Inherit:                  v.Inherit,
		Policy:                   v.Policy,
		Description:              v.Description,
		ModelContextWindow:       v.ModelContextWindow,
		ModelInputPrice:          v.ModelInputPrice,
		ModelOutputPrice:         v.ModelOutputPrice,
		ModelCachePrice:          v.ModelCachePrice,
		ModelModalities:          v.ModelModalities,
		ModelSupportsTools:       v.ModelSupportsTools,
		ModelMaxOutput:           v.ModelMaxOutput,
		ModelTemperature:         v.ModelTemperature,
		ModelTopP:                v.ModelTopP,
		ContextCompactionPercent: cloneIntPtr(v.ContextCompactionPercent),
	}
}

func catSettingsToConfig(v ttypes.CategorySettings) CategoryConfig {
	return CategoryConfig{
		AccountID:                v.AccountID,
		Model:                    v.Model,
		ThinkingEffort:           v.ThinkingEffort,
		Inherit:                  v.Inherit,
		Policy:                   v.Policy,
		Description:              v.Description,
		ModelContextWindow:       v.ModelContextWindow,
		ModelInputPrice:          v.ModelInputPrice,
		ModelOutputPrice:         v.ModelOutputPrice,
		ModelCachePrice:          v.ModelCachePrice,
		ModelModalities:          v.ModelModalities,
		ModelSupportsTools:       v.ModelSupportsTools,
		ModelMaxOutput:           v.ModelMaxOutput,
		ModelTemperature:         v.ModelTemperature,
		ModelTopP:                v.ModelTopP,
		ContextCompactionPercent: cloneIntPtr(v.ContextCompactionPercent),
	}
}

func cloneIntPtr(src *int) *int {
	if src == nil {
		return nil
	}
	value := *src
	return &value
}

func ConfigToSettings(cfg *Config) ttypes.Settings {
	mainCat := cfg.GetActiveDefaultCategory()
	mainProviderID := cfg.templateIDForAccount(mainCat.AccountID)
	var mainTemplate provider.Template
	if resolved, err := cfg.templateForAccountID(mainCat.AccountID); err == nil {
		mainTemplate = resolved
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
		agentsSettings.Categories[k] = categoryToSettings(cfg, cfg.GetCategoryConfig(k))
	}

	if _, hasDefault := agentsSettings.Categories[CategoryDefault]; !hasDefault {
		agentsSettings.Categories[CategoryDefault] = categoryToSettings(cfg, mainCat)
	}

	for name, p := range cfg.Agents.Profiles {
		ps := ttypes.ProfileSettings{Name: name, Categories: make(map[string]ttypes.CategorySettings)}
		for k := range p.Categories {
			ps.Categories[k] = categoryToSettings(cfg, cfg.GetCategoryConfig(k))
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
	safetySettings := cfg.Safety
	safetySettings.CommandRules = append([]ttypes.CommandSeverityRule(nil), cfg.Safety.CommandRules...)

	return ttypes.Settings{
		Model:           mainCat.Model,
		ProviderID:      mainProviderID,
		AccountID:       mainCat.AccountID,
		ProviderURL:     mainTemplate.ProviderURL,
		APIStandard:     mainTemplate.APIStandard,
		ThinkingEffort:  mainCat.ThinkingEffort,
		TurnLogsEnabled: turnLogs,
		Agents:          agentsSettings,
		Memory:          cfg.Memory,
		Safety:          safetySettings,
	}
}

// categoryToSettings exposes provider details only as a projection of the
// explicitly selected account; they are never read back as routing authority.
func categoryToSettings(cfg *Config, category CategoryConfig) ttypes.CategorySettings {
	settings := catConfigToSettings(category)
	settings.Provider = cfg.templateIDForAccount(category.AccountID)
	if template, err := cfg.templateForAccountID(category.AccountID); err == nil {
		settings.ProviderURL = template.ProviderURL
		settings.APIStandard = template.APIStandard
	}
	return settings
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
		// Workspace agent settings publish complete account selections. An empty
		// ID clears this layer rather than inheriting a prior account.
		merged.AccountID = value.AccountID
		profile.Categories[category] = merged
	}
	cfg.Agents.Profiles[profileName] = profile
}
