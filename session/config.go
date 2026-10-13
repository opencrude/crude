package session

import (
	"crude/provider"
	"crude/sessiondb"
	ttypes "crude/types"
	captypes "crude/types/capability"
	"fmt"
	"slices"
	"strings"
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
	Enabled        *bool  `json:"enabled,omitempty"`
	AccountID      string `json:"account_id,omitempty"`
	Model          string `json:"model,omitempty"`
	ThinkingEffort string `json:"thinking_effort,omitempty"`
	// Inherit is the session-level live-inheritance marker: "main" follows
	// this session's main agent, "workspace" follows the workspace's main
	// agent. A category carrying a marker stores no provider/model values;
	// resolution follows the marker at read time. Empty means explicit.
	Inherit      string            `json:"inherit,omitempty"`
	Policy       string            `json:"policy,omitempty"`
	Description  string            `json:"description,omitempty"`
	Instructions string            `json:"instructions,omitempty"`
	AllowedTools []string          `json:"allowed_tools,omitempty"`
	DeniedTools  []string          `json:"denied_tools,omitempty"`
	SkillModes   map[string]string `json:"skill_modes,omitempty"`

	ModelContextWindow int     `json:"model_context_window,omitempty"`
	ModelInputPrice    float64 `json:"model_input_price,omitempty"`
	ModelOutputPrice   float64 `json:"model_output_price,omitempty"`
	ModelCachePrice    float64 `json:"model_cache_price,omitempty"`
	// Modalities lists accepted input types beyond text (image, pdf,
	// video, audio).
	ModelModalities    []string `json:"model_modalities"`
	ModelSupportsTools bool     `json:"model_supports_tools,omitempty"`
	ModelMaxOutput     int      `json:"model_max_output,omitempty"`
	// Sampling overrides; nil leaves the backend default in effect.
	ModelTemperature         *float64 `json:"model_temperature,omitempty"`
	ModelTopP                *float64 `json:"model_top_p,omitempty"`
	ContextCompactionPercent *int     `json:"context_compaction_percent,omitempty"`
	ContextRetentionTokens   *int     `json:"context_retention_tokens,omitempty"`
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

type AgentProfileConfig = ttypes.AgentProfileSettings

// DefaultAgentsConfig returns the built-in agent scaffolding. No category
// ships a model: the user configures the main agent (and any role that should
// not inherit it) explicitly. Roles without a model inherit the main agent's
// configuration at read time.
func DefaultAgentsConfig() AgentsConfig {
	t := true
	roles := ttypes.DefaultAgentRoleCategories()
	delete(roles, CategoryDefault)
	delete(roles, CategoryMemory)
	categories := make(map[string]CategoryConfig, len(roles))
	for name, role := range roles {
		categories[name] = catSettingsToConfig(role)
	}
	return AgentsConfig{
		Enabled:       true,
		ActiveProfile: "default",
		MaxConcurrent: 4,
		Profiles: map[string]ProfileConfig{
			"default": {
				MultiAgent: &t,
				TurnLogs:   &t,
				Categories: categories,
			},
		},
		// Named agents are user-created; none ship with a clean install.
		AgentProfiles: map[string]AgentProfileConfig{},
	}
}

type Config struct {
	SelectedAgent        string // session-only root agent choice; never a workspace default
	selectedMainOverride *sessiondb.AgentSettingOverride
	Accounts             map[string]provider.Account
	AccountManager       *provider.AccountManager
	TurnLogsEnabled      bool
	Agents               AgentsConfig
	Memory               ttypes.MemorySettings
	Capabilities         captypes.Settings
	Safety               ttypes.SafetySettings

	// workspaceMain is a copy of the workspace baseline's main-agent
	// category. Categories carrying Inherit="workspace" resolve through it
	// at read time so they follow the live workspace main agent. It is
	// populated by configWithSessionOverrides and carried through copyConfig;
	// nil disables workspace-marker resolution (the marker is ignored).
	workspaceMain *CategoryConfig
}

// providerForCategory clones all transport behavior from the selected account's
// authoritative connection, then binds that exact account's credentials.
func (c *Config) providerForCategory(category CategoryConfig) (*provider.Provider, error) {
	account, err := c.resolveAccount(category.AccountID)
	if err != nil {
		return nil, err
	}
	template := provider.CloneTemplate(account.Connection)
	p := &provider.Provider{ID: account.ProviderID, Name: template.Name}
	if err := template.Apply(p); err != nil {
		return nil, fmt.Errorf("account %q connection: %w", account.ID, err)
	}
	p.Model = category.Model
	p.SetContextWindow(category.ModelContextWindow)
	p.SetThinkingEffort(category.ThinkingEffort)
	p.SetSampling(category.ModelTemperature, category.ModelTopP)
	if err := c.bindAccount(p, category.AccountID); err != nil {
		return nil, err
	}
	return p, nil
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

// GetActiveDefaultCategory resolves the main agent ("default") category.
// No provider or account is inferred when the category is absent.
func (c *Config) GetActiveDefaultCategory() CategoryConfig {
	prof, _ := activeProfileCfg(c)
	if c.SelectedAgent != "" {
		if selected, err := c.selectedMainCategory(prof); err == nil {
			c.resolveCategoryDefaults(&selected)
			return selected
		}
	}
	cat := prof.Categories[CategoryDefault]
	if cat.Inherit == InheritWorkspace && c.workspaceMain != nil {
		resolved := *c.workspaceMain
		if cat.AccountID != "" {
			resolved.AccountID = cat.AccountID
		}
		resolved = mergeCategoryConfig(resolved, CategoryConfig{AllowedTools: cat.AllowedTools, DeniedTools: cat.DeniedTools, SkillModes: cat.SkillModes})
		c.resolveCategoryDefaults(&resolved)
		return resolved
	}
	c.resolveCategoryDefaults(&cat)
	return cat
}

// selectedMainCategory derives the selected root from its live role/profile,
// then overlays only explicit session edits. The original workspace Main Agent
// remains the inheritance source; resolving through GetCategoryConfig here
// would recurse when a role inherits the selected main.
func (c *Config) selectedMainCategory(prof ProfileConfig) (CategoryConfig, error) {
	main := prof.Categories[CategoryDefault]
	if main.Inherit == InheritWorkspace && c.workspaceMain != nil {
		main = *c.workspaceMain
	}
	id := c.SelectedAgent
	role := strings.TrimPrefix(id, "profile/")
	var named *AgentProfileConfig
	if role != id {
		profile, exists := c.Agents.AgentProfiles[role]
		if !exists {
			return CategoryConfig{}, fmt.Errorf("selected agent %q was removed", role)
		}
		if profile.Enabled != nil && !*profile.Enabled {
			return CategoryConfig{}, fmt.Errorf("selected agent %q is disabled", role)
		}
		named = &profile
		role = profile.Category
	}
	if role != CategoryResearcher && role != CategoryCrafter && role != CategoryAeron {
		return CategoryConfig{}, fmt.Errorf("unknown root agent role %q", role)
	}
	selected, exists := prof.Categories[role]
	if !exists {
		return CategoryConfig{}, fmt.Errorf("selected agent role %q is unavailable", role)
	}
	if selected.Enabled != nil && !*selected.Enabled {
		return CategoryConfig{}, fmt.Errorf("selected agent role %q is disabled", role)
	}
	if selected.Inherit == InheritMain {
		selected = main
	} else if selected.Inherit == InheritWorkspace && c.workspaceMain != nil {
		selected = *c.workspaceMain
	}
	if selected.AccountID == "" {
		selected.AccountID = main.AccountID
	}
	if selected.Model == "" {
		selected.Model = main.Model
	}
	if selected.ThinkingEffort == "" {
		selected.ThinkingEffort = main.ThinkingEffort
	}
	if named != nil {
		if named.AccountID != "" {
			selected.AccountID = named.AccountID
		}
		if named.Model != "" {
			if named.Model != selected.Model {
				selected.ModelContextWindow = 0
				selected.ModelInputPrice, selected.ModelOutputPrice, selected.ModelCachePrice = 0, 0, 0
				selected.ModelModalities = nil
				selected.ModelTemperature, selected.ModelTopP = nil, nil
			}
			selected.Model = named.Model
		}
		if named.ThinkingEffort != "" {
			selected.ThinkingEffort = named.ThinkingEffort
		}
		if named.ModelContextWindow > 0 {
			selected.ModelContextWindow = named.ModelContextWindow
		}
		if named.ModelMaxOutput > 0 {
			selected.ModelMaxOutput = named.ModelMaxOutput
		}
		if named.ModelModalities != nil {
			selected.ModelModalities = append([]string{}, named.ModelModalities...)
		}
		if named.ModelTemperature != nil {
			selected.ModelTemperature = cloneFloatPtr(named.ModelTemperature)
		}
		if named.ModelTopP != nil {
			selected.ModelTopP = cloneFloatPtr(named.ModelTopP)
		}
		if named.ContextCompactionPercent != nil {
			selected.ContextCompactionPercent = cloneIntPtr(named.ContextCompactionPercent)
		}
		if named.ContextRetentionTokens != nil {
			selected.ContextRetentionTokens = cloneIntPtr(named.ContextRetentionTokens)
		}
		selected.Instructions = strings.TrimSpace(strings.Join([]string{selected.Instructions, named.Instructions}, "\n\n"))
		selected.DeniedTools = append(append([]string(nil), selected.DeniedTools...), named.DeniedTools...)
		selected = mergeCategoryConfig(selected, CategoryConfig{SkillModes: named.SkillModes})
		if len(named.AllowedTools) > 0 {
			selected.AllowedTools = append([]string(nil), named.AllowedTools...)
		}
	}
	if over := c.selectedMainOverride; over != nil {
		selected = mergeCategoryConfig(selected, CategoryConfig{AllowedTools: over.AllowedTools, DeniedTools: over.DeniedTools, SkillModes: over.SkillModes})
		if over.AccountID != "" {
			selected.AccountID = over.AccountID
		}
		if over.Model != "" {
			selected.Model = over.Model
		}
		if over.ThinkingEffort != "" {
			selected.ThinkingEffort = over.ThinkingEffort
		}
		if over.ContextCompactionPercent != nil {
			selected.ContextCompactionPercent = cloneIntPtr(over.ContextCompactionPercent)
		}
		if over.ContextRetentionTokens != nil {
			selected.ContextRetentionTokens = cloneIntPtr(over.ContextRetentionTokens)
		}
	}
	return selected, nil
}

func (c *Config) ValidateSelectedAgent() error {
	if c.SelectedAgent == "" {
		main := c.GetActiveDefaultCategory()
		if main.Enabled != nil && !*main.Enabled {
			return fmt.Errorf("main agent is disabled")
		}
		return nil
	}
	prof, _ := activeProfileCfg(c)
	_, err := c.selectedMainCategory(prof)
	return err
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
	if category == "" || category == CategoryDefault {
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
			resolved = mergeCategoryConfig(resolved, CategoryConfig{AllowedTools: cat.AllowedTools, DeniedTools: cat.DeniedTools, SkillModes: cat.SkillModes})
			return resolved
		case cat.Inherit == InheritMain:
			main := c.GetActiveDefaultCategory()
			if cat.AccountID != "" {
				main.AccountID = cat.AccountID
			}
			main = mergeCategoryConfig(main, CategoryConfig{AllowedTools: cat.AllowedTools, DeniedTools: cat.DeniedTools, SkillModes: cat.SkillModes})
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
// agent live.
func (c *Config) GetMemoryCategory() CategoryConfig {
	memory := catSettingsToConfig(c.Memory.MemoryCategory)
	finish := func(cat CategoryConfig) CategoryConfig {
		if memory.Enabled != nil {
			cat.Enabled = cloneBoolPtr(memory.Enabled)
		}
		if cat.Instructions == "" {
			cat.Instructions = memory.Instructions
		}
		return cat
	}
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
			return finish(resolved)
		case cat.Inherit == InheritMain:
			main := c.GetActiveDefaultCategory()
			if cat.AccountID != "" {
				main.AccountID = cat.AccountID
			}
			return finish(main)
		}
		if cat.AccountID == "" {
			main := c.GetActiveDefaultCategory()
			cat.AccountID = main.AccountID
		}
		c.resolveCategoryDefaults(&cat)
		return finish(cat)
	}
	cat := memory
	main := c.GetActiveDefaultCategory()
	if cat.AccountID == "" && cat.Model == "" && cat.ThinkingEffort == "" && cat.ModelMaxOutput == 0 && cat.ModelContextWindow == 0 && cat.ModelTemperature == nil && cat.ModelTopP == nil && cat.ModelModalities == nil {
		main.Enabled = cloneBoolPtr(cat.Enabled)
		main.Instructions = cat.Instructions
		return main
	}
	if cat.AccountID == "" {
		cat.AccountID = main.AccountID
	}
	if cat.Model == "" {
		cat.Model = main.Model
	}
	if cat.ThinkingEffort == "" {
		cat.ThinkingEffort = main.ThinkingEffort
	}
	c.resolveCategoryDefaults(&cat)
	return cat
}

func (c *Config) resolveCategoryDefaults(cat *CategoryConfig) {
	if cat.ContextCompactionPercent == nil {
		value := ttypes.DefaultContextCompactionPercent
		cat.ContextCompactionPercent = &value
	} else {
		value := min(100, max(0, *cat.ContextCompactionPercent))
		cat.ContextCompactionPercent = &value
	}
	if cat.ContextRetentionTokens == nil {
		value := ttypes.DefaultContextRetentionTokens
		cat.ContextRetentionTokens = &value
	} else {
		value := min(ttypes.MaxContextRetentionTokens, max(ttypes.MinContextRetentionTokens, *cat.ContextRetentionTokens))
		cat.ContextRetentionTokens = &value
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
		Enabled:      cloneBoolPtr(v.Enabled),
		Instructions: v.Instructions,
		AllowedTools: append([]string(nil), v.AllowedTools...), DeniedTools: append([]string(nil), v.DeniedTools...),
		SkillModes:               v.SkillModes,
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
		ContextRetentionTokens:   cloneIntPtr(v.ContextRetentionTokens),
	}
}

func catSettingsToConfig(v ttypes.CategorySettings) CategoryConfig {
	return CategoryConfig{
		Enabled:      cloneBoolPtr(v.Enabled),
		Instructions: v.Instructions,
		AllowedTools: append([]string(nil), v.AllowedTools...), DeniedTools: append([]string(nil), v.DeniedTools...),
		SkillModes:               v.SkillModes,
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
		ContextRetentionTokens:   cloneIntPtr(v.ContextRetentionTokens),
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
	mainProviderID := cfg.providerIDForAccount(mainCat.AccountID)
	var mainTemplate provider.Template
	if resolved, err := cfg.connectionForAccountID(mainCat.AccountID); err == nil {
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
		AgentProfiles: ttypes.CloneAgentProfiles(cfg.Agents.AgentProfiles),
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

	profileNames := make([]string, 0, len(cfg.Agents.Profiles))
	for name := range cfg.Agents.Profiles {
		profileNames = append(profileNames, name)
	}
	slices.Sort(profileNames)
	for _, name := range profileNames {
		p := cfg.Agents.Profiles[name]
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
		SessionMainAgent: cfg.SelectedAgent,
		Model:            mainCat.Model,
		ProviderID:       mainProviderID,
		AccountID:        mainCat.AccountID,
		ProviderURL:      mainTemplate.ProviderURL,
		APIStandard:      mainTemplate.APIStandard,
		ThinkingEffort:   mainCat.ThinkingEffort,
		TurnLogsEnabled:  turnLogs,
		Agents:           agentsSettings,
		Memory:           cfg.Memory,
		Capabilities:     captypes.Clone(cfg.Capabilities),
		Safety:           safetySettings,
	}
}

// categoryToSettings exposes provider details only as a projection of the
// explicitly selected account; they are never read back as routing authority.
func categoryToSettings(cfg *Config, category CategoryConfig) ttypes.CategorySettings {
	settings := catConfigToSettings(category)
	settings.Provider = cfg.providerIDForAccount(category.AccountID)
	if template, err := cfg.connectionForAccountID(category.AccountID); err == nil {
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
	if agents.AgentProfiles != nil {
		cfg.Agents.AgentProfiles = ttypes.CloneAgentProfiles(agents.AgentProfiles)
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
		merged.Enabled = cloneBoolPtr(value.Enabled)
		merged.Model = value.Model
		merged.ThinkingEffort = value.ThinkingEffort
		merged.Description = value.Description
		merged.Policy = value.Policy
		merged.ModelContextWindow = value.ModelContextWindow
		merged.ModelMaxOutput = value.ModelMaxOutput
		merged.ModelModalities = slices.Clone(value.ModelModalities)
		merged.ModelTemperature = cloneFloatPtr(value.ModelTemperature)
		merged.ModelTopP = cloneFloatPtr(value.ModelTopP)
		merged.ContextCompactionPercent = cloneIntPtr(value.ContextCompactionPercent)
		merged.ContextRetentionTokens = cloneIntPtr(value.ContextRetentionTokens)
		merged.Instructions = value.Instructions
		merged.AllowedTools = append([]string(nil), value.AllowedTools...)
		merged.DeniedTools = append([]string(nil), value.DeniedTools...)
		profile.Categories[category] = merged
	}
	cfg.Agents.Profiles[profileName] = profile
}
