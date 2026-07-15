package widgets

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/components"
	"crude/tui/form"
	"crude/tui/rendering"
	"crude/tui/theme"
	ttypes "crude/tui/types"
)

type SessionSettingsSaveMsg struct {
	Settings ttypes.Settings
}

type SessionSettingsCancelMsg struct{}
type SessionSettingsResetMsg struct{}

type SessionSettingsFetchModelsMsg struct {
	ProviderID string
	BaseURL    string
	APIKey     string
}

type SessionSettingsModelsMsg struct {
	ProviderID string
	Models     []ttypes.ModelEntry
}

type SessionSettingsModelsErrorMsg struct {
	ProviderID string
	Error      string
}

const (
	sessionSettingsRoot = iota
	sessionSettingsRole
)

var sessionAgentRoles = []struct {
	Key  string
	Name string
}{
	{Key: "default", Name: "Main Agent"},
	{Key: "researcher", Name: "Researcher"},
	{Key: "crafter", Name: "Crafter"},
	{Key: "aeron", Name: "Aeron"},
}

// SessionSettings edits only provider/model/thinking overrides. The workspace
// baseline and explicit override map are kept separately so Reset truly
// restores inheritance instead of persisting a copied value.
type SessionSettings struct {
	th        *theme.Theme
	page      int
	role      string
	rootForm  form.Form
	roleForm  form.Form
	base      ttypes.Settings
	effective ttypes.Settings
	overrides map[string]ttypes.CategorySettings
	presets   []ttypes.ProviderPreset
	providers map[string]ttypes.ProviderConfig
	models    map[string][]ttypes.ModelEntry
	snapshot  *sessionSettingsSnapshot
}

type sessionSettingsSnapshot struct {
	effective ttypes.Settings
	overrides map[string]ttypes.CategorySettings
}

func NewSessionSettings(th *theme.Theme, initial ttypes.Settings) *SessionSettings {
	return NewSessionSettingsWithInheritance(th, initial, initial, nil)
}

func NewSessionSettingsWithInheritance(
	th *theme.Theme,
	effective ttypes.Settings,
	base ttypes.Settings,
	overrides map[string]ttypes.CategorySettings,
) *SessionSettings {
	m := &SessionSettings{
		th: th, base: cloneSettings(base), effective: cloneSettings(effective),
		overrides: copyCategoryMap(overrides),
		providers: make(map[string]ttypes.ProviderConfig),
		models:    make(map[string][]ttypes.ModelEntry),
	}
	m.rebuildRootForm()
	return m
}

func (m *SessionSettings) ConfigureProviders(presets []ttypes.ProviderPreset, providers map[string]ttypes.ProviderConfig) {
	m.presets = append([]ttypes.ProviderPreset(nil), presets...)
	m.providers = make(map[string]ttypes.ProviderConfig, len(providers))
	for id, cfg := range providers {
		m.providers[id] = cfg
	}
	if m.page == sessionSettingsRole {
		m.openRole(m.role)
	}
}

func (m *SessionSettings) Update(msg tea.Msg) (*SessionSettings, tea.Cmd) {
	switch result := msg.(type) {
	case SessionSettingsModelsMsg:
		m.setModels(result.ProviderID, result.Models)
		return m, nil
	case SessionSettingsModelsErrorMsg:
		if m.page == sessionSettingsRole && m.roleForm.ItemValue(0) == result.ProviderID {
			m.roleForm.SetItemLoading(1, false, "")
			m.roleForm.SetItemFetchError(1, result.Error)
		}
		return m, nil
	}

	if key, ok := msg.(tea.KeyPressMsg); ok {
		if !m.activeForm().IsEditing() && !m.activeForm().IsExpanded() {
			switch key.String() {
			case "s", "ctrl+s":
				if m.page == sessionSettingsRole {
					m.applyRoleForm()
					m.page = sessionSettingsRoot
					m.rebuildRootForm()
					return m, nil
				}
				return m, func() tea.Msg { return SessionSettingsSaveMsg{Settings: m.ToSettings()} }
			case "c":
				if m.page == sessionSettingsRole {
					m.page = sessionSettingsRoot
					m.rebuildRootForm()
					return m, nil
				}
				return m, func() tea.Msg { return SessionSettingsCancelMsg{} }
			case "r":
				if m.page == sessionSettingsRole {
					m.resetRole(m.role)
					m.page = sessionSettingsRoot
					m.rebuildRootForm()
					return m, nil
				}
				return m, func() tea.Msg { return SessionSettingsResetMsg{} }
			}
		}
		if key.String() == "j" && !m.activeForm().IsEditing() && !m.activeForm().IsExpanded() {
			msg = tea.KeyPressMsg{Code: tea.KeyDown}
		} else if key.String() == "k" && !m.activeForm().IsEditing() && !m.activeForm().IsExpanded() {
			msg = tea.KeyPressMsg{Code: tea.KeyUp}
		}
	}

	if m.page == sessionSettingsRole {
		return m.updateRole(msg)
	}
	return m.updateRoot(msg)
}

func (m *SessionSettings) updateRoot(msg tea.Msg) (*SessionSettings, tea.Cmd) {
	updated, cmd, action := m.rootForm.Update(msg)
	m.rootForm = updated
	switch action {
	case form.FormActionNavigate:
		if target, ok := m.rootForm.FocusedNavTarget(); ok {
			m.openRole(target)
		}
	case form.FormActionSave:
		return m, func() tea.Msg { return SessionSettingsSaveMsg{Settings: m.ToSettings()} }
	case form.FormActionCancel, form.FormActionBack:
		return m, func() tea.Msg { return SessionSettingsCancelMsg{} }
	case form.FormActionReset:
		return m, func() tea.Msg { return SessionSettingsResetMsg{} }
	}
	return m, cmd
}

func (m *SessionSettings) updateRole(msg tea.Msg) (*SessionSettings, tea.Cmd) {
	oldProvider := m.roleForm.ItemValue(0)
	updated, cmd, action := m.roleForm.Update(msg)
	m.roleForm = updated
	newProvider := m.roleForm.ItemValue(0)
	if newProvider != "" && newProvider != oldProvider {
		providerCfg := m.providers[newProvider]
		m.roleForm.SetItemValue(1, providerCfg.Model)
		m.roleForm.SetItemValue(2, providerCfg.ThinkingEffort)
		m.roleForm.SetItemSelectOptions(1, sessionModelItems(m.models[newProvider]))
	}
	if action == form.FormActionExpand && m.roleForm.ExpandedIndex() == 1 && m.roleForm.ItemOptionCount(1) == 0 {
		m.roleForm.SetItemLoading(1, true, "fetching models...")
		request := m.fetchModelsCmd(newProvider)
		if cmd != nil {
			return m, tea.Batch(cmd, request)
		}
		return m, request
	}

	switch action {
	case form.FormActionSave:
		m.applyRoleForm()
		m.page = sessionSettingsRoot
		m.rebuildRootForm()
	case form.FormActionCancel, form.FormActionBack:
		m.page = sessionSettingsRoot
		m.rebuildRootForm()
	case form.FormActionReset:
		m.resetRole(m.role)
		m.page = sessionSettingsRoot
		m.rebuildRootForm()
	}
	return m, cmd
}

func (m *SessionSettings) activeForm() *form.Form {
	if m.page == sessionSettingsRole {
		return &m.roleForm
	}
	return &m.rootForm
}

func (m *SessionSettings) rebuildRootForm() {
	items := make([]form.FormItem, 0, len(sessionAgentRoles))
	for _, role := range sessionAgentRoles {
		cfg := categoryFor(m.effective, role.Key)
		detail := strings.Trim(strings.Join([]string{cfg.Provider, cfg.Model}, "  ·  "), "  ·")
		if detail == "" {
			detail = "workspace default"
		} else if _, overridden := m.overrides[role.Key]; !overridden {
			detail += "  (inherited)"
		}
		items = append(items, form.NewNavItem(m.th, role.Name, m.th.Styles.DimLabel.Render(detail), "provider, model, and thinking level", role.Key))
	}
	m.rootForm = form.NewFormWithButtons(m.th, "Session AI Settings",
		"enter edit role · reset clears session overrides",
		[]form.Button{
			{Label: "Save", Key: "s", Color: m.th.Colors.PopupTitleFocused},
			{Label: "Cancel", Key: "c", Color: m.th.Colors.Muted, Cancel: true},
			{Label: "Reset", Key: "r", Color: m.th.Colors.Warning, Reset: true},
		},
		items...,
	)
}

func (m *SessionSettings) openRole(role string) {
	m.role = role
	m.page = sessionSettingsRole
	cfg := categoryFor(m.effective, role)
	providerID := cfg.Provider
	if providerID == "" {
		providerID = m.effective.ProviderID
	}
	providerItem := form.NewSelectItem(m.th, "provider", providerID, 10)
	options := make([]components.ListItem, 0, len(m.presets))
	for _, preset := range m.presets {
		options = append(options, components.ListItem{ID: preset.ID, Label: preset.Name, Detail: preset.HelpText})
	}
	providerItem.SetOptions(options)
	modelItem := form.NewSelectItem(m.th, "model", cfg.Model, 12)
	modelItem.SetOptions(sessionModelItems(m.models[providerID]))
	thinking := cfg.ThinkingEffort
	if thinking == "" {
		thinking = "none"
	}
	title := role
	for _, candidate := range sessionAgentRoles {
		if candidate.Key == role {
			title = candidate.Name
			break
		}
	}
	m.roleForm = form.NewFormWithButtons(m.th, title,
		"session override · enter choose · esc back",
		[]form.Button{
			{Label: "Apply", Key: "s", Color: m.th.Colors.PopupTitleFocused},
			{Label: "Back", Key: "c", Color: m.th.Colors.Muted, Cancel: true},
			{Label: "Reset role", Key: "r", Color: m.th.Colors.Warning, Reset: true},
		},
		providerItem,
		modelItem,
		form.NewCycleItem(m.th, "thinking level", []string{"none", "low", "medium", "high", "xhigh"}, thinking),
	)
}

func (m *SessionSettings) applyRoleForm() {
	cfg := categoryFor(m.effective, m.role)
	cfg.Provider = m.roleForm.ItemValue(0)
	cfg.Model = m.roleForm.ItemValue(1)
	cfg.ThinkingEffort = m.roleForm.ItemValue(2)
	setCategory(&m.effective, m.role, cfg)
	m.overrides[m.role] = ttypes.CategorySettings{
		Provider: cfg.Provider, Model: cfg.Model, ThinkingEffort: cfg.ThinkingEffort,
	}
}

func (m *SessionSettings) resetRole(role string) {
	setCategory(&m.effective, role, categoryFor(m.base, role))
	delete(m.overrides, role)
}

func (m *SessionSettings) fetchModelsCmd(providerID string) tea.Cmd {
	cfg := m.providers[providerID]
	return func() tea.Msg {
		return SessionSettingsFetchModelsMsg{
			ProviderID: providerID, BaseURL: cfg.ProviderURL, APIKey: cfg.APIKey,
		}
	}
}

func (m *SessionSettings) setModels(providerID string, models []ttypes.ModelEntry) {
	m.models[providerID] = models
	if m.page == sessionSettingsRole && m.roleForm.ItemValue(0) == providerID {
		m.roleForm.SetItemSelectOptions(1, sessionModelItems(models))
		m.roleForm.SetItemLoading(1, false, "")
	}
}

func sessionModelItems(models []ttypes.ModelEntry) []components.ListItem {
	items := make([]components.ListItem, 0, len(models))
	for _, model := range models {
		name := model.Name
		if name == "" {
			name = model.ID
		}
		detail := ""
		if model.ContextWindow > 0 {
			detail = rendering.FormatTokens(model.ContextWindow)
		}
		items = append(items, components.ListItem{ID: model.ID, Label: name, Detail: detail})
	}
	return items
}

func (m *SessionSettings) View(spinFrame int) string {
	return m.activeForm().View(70, spinFrame)
}

func (m *SessionSettings) Hint() string     { return m.activeForm().Hint() }
func (m *SessionSettings) Form() *form.Form { return m.activeForm() }

func (m *SessionSettings) ToSettings() ttypes.Settings {
	settings := cloneSettings(m.effective)
	settings.SessionOverrides = copyCategoryMap(m.overrides)
	main := categoryFor(settings, "default")
	settings.ProviderID = main.Provider
	settings.Model = main.Model
	settings.ThinkingEffort = main.ThinkingEffort
	if providerCfg, ok := m.providers[main.Provider]; ok {
		settings.ProviderURL = providerCfg.ProviderURL
		settings.ProviderAPIKey = providerCfg.APIKey
		settings.APIKey = providerCfg.APIKey
	}
	return settings
}

func (m *SessionSettings) SaveSnapshot() {
	m.snapshot = &sessionSettingsSnapshot{
		effective: cloneSettings(m.effective), overrides: copyCategoryMap(m.overrides),
	}
}

func (m *SessionSettings) RestoreSnapshot() {
	if m.snapshot == nil {
		return
	}
	m.effective = cloneSettings(m.snapshot.effective)
	m.overrides = copyCategoryMap(m.snapshot.overrides)
	m.page = sessionSettingsRoot
	m.rebuildRootForm()
}

func (m *SessionSettings) ResetToWorkspace(base ttypes.Settings) {
	m.base = cloneSettings(base)
	m.effective = cloneSettings(base)
	m.overrides = make(map[string]ttypes.CategorySettings)
	m.page = sessionSettingsRoot
	m.rebuildRootForm()
}

// SetInheritance refreshes workspace defaults while preserving the explicit
// override map owned by this session.
func (m *SessionSettings) SetInheritance(effective, base ttypes.Settings, overrides map[string]ttypes.CategorySettings) {
	m.base = cloneSettings(base)
	m.effective = cloneSettings(effective)
	m.overrides = copyCategoryMap(overrides)
	m.page = sessionSettingsRoot
	m.rebuildRootForm()
}

func categoryFor(settings ttypes.Settings, role string) ttypes.CategorySettings {
	if cfg, ok := settings.Agents.Categories[role]; ok {
		return cfg
	}
	if role != "default" {
		if cfg, ok := settings.Agents.Categories["default"]; ok {
			return cfg
		}
	}
	return ttypes.CategorySettings{
		Provider: settings.ProviderID, Model: settings.Model, ThinkingEffort: settings.ThinkingEffort,
	}
}

func setCategory(settings *ttypes.Settings, role string, cfg ttypes.CategorySettings) {
	if settings.Agents.Categories == nil {
		settings.Agents.Categories = make(map[string]ttypes.CategorySettings)
	}
	settings.Agents.Categories[role] = cfg
}

func copyCategoryMap(src map[string]ttypes.CategorySettings) map[string]ttypes.CategorySettings {
	dst := make(map[string]ttypes.CategorySettings, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func cloneSettings(src ttypes.Settings) ttypes.Settings {
	dst := src
	dst.Agents.Categories = copyCategoryMap(src.Agents.Categories)
	dst.SessionOverrides = copyCategoryMap(src.SessionOverrides)
	return dst
}
