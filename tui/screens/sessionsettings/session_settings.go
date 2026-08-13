package sessionsettings

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

const (
	ScreenID  ui.ComponentID = "session-settings"
	FormID    ui.ComponentID = "session-settings/form"
	ActionsID ui.ComponentID = "session-settings/actions"
)

const (
	fieldProvider = "provider"
	fieldModel    = "model"
	fieldThinking = "thinking-effort"
)

type SaveMsg struct {
	Target   *Screen
	Settings types.Settings
}
type CancelMsg struct{ Target *Screen }
type ResetMsg struct{ Target *Screen }

type FetchModelsMsg struct {
	ProviderID string
	BaseURL    string
	APIKey     string
}

type page uint8

const (
	rootPage page = iota
	rolePage
)

var agentRoles = []struct {
	Key  string
	Name string
}{
	{Key: "default", Name: "Main Agent"},
	{Key: "researcher", Name: "Researcher"},
	{Key: "crafter", Name: "Crafter"},
	{Key: "aeron", Name: "Aeron"},
}

// Screen edits per-session agent overrides using the shared native form
// system. Durable settings data is supplied by the workspace boundary; this
// screen owns only retained interaction state.
type Screen struct {
	ui.Base
	tokens    *ui.TokenSet
	page      page
	role      string
	formModel *ui.FormModel
	form      *ui.Form
	actions   *ui.ActionBar

	base      types.Settings
	effective types.Settings
	overrides map[string]types.CategorySettings
	presets   []types.ProviderPreset
	providers map[string]types.ProviderConfig
	models    map[string][]types.ModelEntry
	snapshot  *snapshot
}

type snapshot struct {
	effective types.Settings
	overrides map[string]types.CategorySettings
}

func New(th *theme.Theme, effective, base types.Settings, overrides map[string]types.CategorySettings) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	s := &Screen{
		Base: ui.NewBase(ScreenID), tokens: ui.NewTokenSet(th),
		base: cloneSettings(base), effective: cloneSettings(effective),
		overrides: copyCategoryMap(overrides), providers: make(map[string]types.ProviderConfig),
		models: make(map[string][]types.ModelEntry),
	}
	s.form = ui.NewForm(FormID, nil, "", "", s.tokens)
	s.form.SetFieldGap(1)
	s.openRoot()
	return s
}

func (s *Screen) ConfigureProviders(presets []types.ProviderPreset, providers map[string]types.ProviderConfig) {
	s.presets = append(s.presets[:0], presets...)
	s.providers = make(map[string]types.ProviderConfig, len(providers))
	for id, cfg := range providers {
		s.providers[id] = cfg
	}
	if s.page == rolePage {
		s.openRole(s.role)
	}
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	s.form.SetTokens(tokens)
	s.actions.SetTokens(tokens)
}

func (s *Screen) Children() []ui.Component { return []ui.Component{s.form, s.actions} }

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: s.PreferredHeight(cs.MaxH)})
}

func (s *Screen) PreferredHeight(maxHeight int) int {
	return min(maxHeight, s.form.PreferredHeight(max(0, maxHeight-1))+1)
}

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	formH := max(1, r.H-1)
	s.form.Layout(ctx, ui.Rect{X: 0, Y: 0, W: r.W, H: formH})
	s.actions.Layout(ctx, ui.Rect{X: 0, Y: max(0, r.H-1), W: r.W, H: 1})
}

func (s *Screen) Paint(_ *surface.CellBuffer, _, _ int, _ ui.PaintContext) {}
func (s *Screen) Handle(_ ui.EventContext, _ ui.Event) ui.EventResult       { return ui.EventResult{} }

func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case "session-settings.role.open":
		role, _ := action.Payload.(string)
		s.openRole(role)
		return focusFormResult(ui.InvalidateLayout)
	case "form.select.open":
		fieldID, _ := action.Payload.(string)
		return ui.EventResult{Handled: true, Cmd: s.openSelect(fieldID), Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	case "form.changed":
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	case "form.exit.backward":
		if s.page == rolePage {
			s.openRoot()
			return focusFormResult(ui.InvalidateLayout)
		}
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "panel.close"}, Cmd: func() tea.Msg { return CancelMsg{Target: s} }, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	case "form.exit.forward":
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: ActionsID}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "actionbar.exit.backward":
		s.form.FocusLast()
		return focusFormResult(0)
	case "actionbar.exit.forward":
		s.form.FocusFirst()
		return focusFormResult(0)
	case "session-settings.save":
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return SaveMsg{Target: s, Settings: s.ToSettings()} }, Invalidate: ui.InvalidatePaint}
	case "session-settings.cancel":
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "panel.close"}, Cmd: func() tea.Msg { return CancelMsg{Target: s} }, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	case "session-settings.reset":
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "panel.close"}, Cmd: func() tea.Msg { return ResetMsg{Target: s} }, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	case "session-settings.role.apply":
		s.applyRoleForm()
		s.openRoot()
		return focusFormResult(ui.InvalidateLayout)
	case "session-settings.role.back":
		s.openRoot()
		return focusFormResult(ui.InvalidateLayout)
	case "session-settings.role.reset":
		s.resetRole(s.role)
		s.openRoot()
		return focusFormResult(ui.InvalidateLayout)
	}
	return ui.EventResult{Action: &action}
}

func focusFormResult(extra ui.Invalidation) ui.EventResult {
	return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: FormID}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint | extra}
}

func (s *Screen) openRoot() {
	s.page, s.role = rootPage, ""
	model := ui.NewFormModel()
	for _, role := range agentRoles {
		cfg := categoryFor(s.effective, role.Key)
		detail := strings.Trim(strings.Join([]string{cfg.Provider, cfg.Model}, "  ·  "), "  ·")
		if detail == "" {
			detail = "workspace default"
		}
		_, overridden := s.overrides[role.Key]
		model.AddField(&ui.FieldState{
			ID: role.Key, Label: role.Name, Description: detail, Type: ui.FieldNav,
			Target: "session-settings.role.open", Inherited: !overridden, InheritedFrom: "workspace",
		})
	}
	s.formModel = model
	s.form.SetForm(model, "Session AI Settings", "Per-session provider, model, and thinking overrides.")
	s.actions = ui.NewActionBar(ActionsID, s.tokens,
		ui.ActionBarButton{ID: ui.NewID(ActionsID, "save"), Label: "Save", Action: "session-settings.save", Variant: ui.VariantPrimary, Enabled: true},
		ui.ActionBarButton{ID: ui.NewID(ActionsID, "cancel"), Label: "Cancel", Action: "session-settings.cancel", Variant: ui.VariantSecondary, Enabled: true},
		ui.ActionBarButton{ID: ui.NewID(ActionsID, "reset"), Label: "Reset", Action: "session-settings.reset", Variant: ui.VariantDanger, Enabled: true},
	)
}

func (s *Screen) openRole(role string) {
	name := ""
	for _, candidate := range agentRoles {
		if candidate.Key == role {
			name = candidate.Name
			break
		}
	}
	if name == "" {
		return
	}
	s.page, s.role = rolePage, role
	cfg := categoryFor(s.effective, role)
	providerID := cfg.Provider
	if providerID == "" {
		providerID = s.effective.ProviderID
	}
	thinking := cfg.ThinkingEffort
	if thinking == "" {
		thinking = "none"
	}
	model := ui.NewFormModel()
	model.AddField(ui.SelectFieldState(fieldProvider, "Provider", providerID, ui.ProviderOptions(s.presets), "Provider used by this agent for the session."))
	model.AddField(ui.ModelFieldState(fieldModel, "Model", cfg.Model, ui.ModelOptions(s.models[providerID]), "Model used by this agent for the session."))
	model.AddField(ui.ThinkingFieldState(fieldThinking, thinking, "Reasoning budget used by this agent for the session."))
	model.OnChanged(func(fieldID string) {
		if fieldID != fieldProvider {
			return
		}
		provider := model.Field(fieldProvider).Value.String()
		providerCfg := s.providers[provider]
		model.SetValue(fieldModel, ui.StringValue(providerCfg.Model))
		model.SetValue(fieldThinking, ui.StringValue(providerCfg.ThinkingEffort))
		if field := model.Field(fieldModel); field != nil {
			field.Options = ui.ModelOptions(s.models[provider])
			field.Errors, field.Loading, field.LoadMessage = nil, false, ""
		}
	})
	s.formModel = model
	s.form.SetForm(model, name, "Session override. Back keeps unapplied values out of the session.")
	s.actions = ui.NewActionBar(ActionsID, s.tokens,
		ui.ActionBarButton{ID: ui.NewID(ActionsID, "apply"), Label: "Apply", Action: "session-settings.role.apply", Variant: ui.VariantPrimary, Enabled: true},
		ui.ActionBarButton{ID: ui.NewID(ActionsID, "back"), Label: "Back", Action: "session-settings.role.back", Variant: ui.VariantSecondary, Enabled: true},
		ui.ActionBarButton{ID: ui.NewID(ActionsID, "reset-role"), Label: "Reset role", Action: "session-settings.role.reset", Variant: ui.VariantDanger, Enabled: true},
	)
}

func (s *Screen) openSelect(fieldID string) tea.Cmd {
	if s.page != rolePage || fieldID != fieldModel {
		return nil
	}
	field := s.formModel.Field(fieldModel)
	if field == nil || len(field.Options) > 0 || field.Loading {
		return nil
	}
	field.Loading, field.LoadMessage = true, "Fetching models..."
	providerID := s.formModel.Field(fieldProvider).Value.String()
	cfg := s.providers[providerID]
	return func() tea.Msg {
		return FetchModelsMsg{ProviderID: providerID, BaseURL: cfg.ProviderURL, APIKey: cfg.APIKey}
	}
}

func (s *Screen) ApplyModels(providerID string, models []types.ModelEntry) {
	s.models[providerID] = models
	if s.page == rolePage && s.formModel.Field(fieldProvider).Value.String() == providerID {
		field := s.formModel.Field(fieldModel)
		field.Options = ui.ModelOptions(models)
		field.Errors, field.Loading, field.LoadMessage = nil, false, ""
		s.form.SetForm(s.formModel, s.roleName(), "Session override. Back keeps unapplied values out of the session.")
	}
}

func (s *Screen) ApplyModelsError(providerID, message string) {
	if s.page == rolePage && s.formModel.Field(fieldProvider).Value.String() == providerID {
		field := s.formModel.Field(fieldModel)
		field.Errors, field.Loading, field.LoadMessage = []string{message}, false, ""
		s.form.SetForm(s.formModel, s.roleName(), "Session override. Back keeps unapplied values out of the session.")
	}
}

func (s *Screen) roleName() string {
	for _, role := range agentRoles {
		if role.Key == s.role {
			return role.Name
		}
	}
	return s.role
}

func (s *Screen) applyRoleForm() {
	if s.page != rolePage {
		return
	}
	cfg := categoryFor(s.effective, s.role)
	cfg.Provider = s.formModel.Field(fieldProvider).Value.String()
	cfg.Model = s.formModel.Field(fieldModel).Value.String()
	cfg.ThinkingEffort = s.formModel.Field(fieldThinking).Value.String()
	setCategory(&s.effective, s.role, cfg)
	s.overrides[s.role] = types.CategorySettings{Provider: cfg.Provider, Model: cfg.Model, ThinkingEffort: cfg.ThinkingEffort}
}

func (s *Screen) resetRole(role string) {
	setCategory(&s.effective, role, categoryFor(s.base, role))
	delete(s.overrides, role)
}

func (s *Screen) ToSettings() types.Settings {
	settings := cloneSettings(s.effective)
	settings.SessionOverrides = copyCategoryMap(s.overrides)
	main := categoryFor(settings, "default")
	settings.ProviderID, settings.Model, settings.ThinkingEffort = main.Provider, main.Model, main.ThinkingEffort
	if providerCfg, ok := s.providers[main.Provider]; ok {
		settings.ProviderURL, settings.ProviderAPIKey, settings.APIKey = providerCfg.ProviderURL, providerCfg.APIKey, providerCfg.APIKey
	}
	return settings
}

func (s *Screen) SaveSnapshot() {
	s.snapshot = &snapshot{effective: cloneSettings(s.effective), overrides: copyCategoryMap(s.overrides)}
}

func (s *Screen) RestoreSnapshot() {
	if s.snapshot == nil {
		return
	}
	s.effective, s.overrides = cloneSettings(s.snapshot.effective), copyCategoryMap(s.snapshot.overrides)
	s.openRoot()
}

func (s *Screen) ResetToWorkspace(base types.Settings) {
	s.base, s.effective, s.overrides = cloneSettings(base), cloneSettings(base), make(map[string]types.CategorySettings)
	s.openRoot()
}

func (s *Screen) SetInheritance(effective, base types.Settings, overrides map[string]types.CategorySettings) {
	s.base, s.effective, s.overrides = cloneSettings(base), cloneSettings(effective), copyCategoryMap(overrides)
	s.openRoot()
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Session settings"}
}

func categoryFor(settings types.Settings, role string) types.CategorySettings {
	if cfg, ok := settings.Agents.Categories[role]; ok {
		return cfg
	}
	if role != "default" {
		if cfg, ok := settings.Agents.Categories["default"]; ok {
			return cfg
		}
	}
	return types.CategorySettings{Provider: settings.ProviderID, Model: settings.Model, ThinkingEffort: settings.ThinkingEffort}
}

func setCategory(settings *types.Settings, role string, cfg types.CategorySettings) {
	if settings.Agents.Categories == nil {
		settings.Agents.Categories = make(map[string]types.CategorySettings)
	}
	settings.Agents.Categories[role] = cfg
}

func copyCategoryMap(src map[string]types.CategorySettings) map[string]types.CategorySettings {
	dst := make(map[string]types.CategorySettings, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func cloneSettings(src types.Settings) types.Settings {
	dst := src
	dst.Agents.Categories = copyCategoryMap(src.Agents.Categories)
	dst.SessionOverrides = copyCategoryMap(src.SessionOverrides)
	return dst
}

var _ ui.Component = (*Screen)(nil)
var _ ui.Parent = (*Screen)(nil)
var _ ui.ActionHandler = (*Screen)(nil)
