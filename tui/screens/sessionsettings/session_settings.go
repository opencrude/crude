package sessionsettings

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

const (
	ScreenID  ui.ComponentID = "session-settings"
	FormID    ui.ComponentID = "session-settings/form"
	ActionsID ui.ComponentID = "session-settings/actions"
	FilterID  ui.ComponentID = "session-settings/model-filter"
	ListID    ui.ComponentID = "session-settings/model-list"
)

const (
	fieldProvider   = "provider"
	fieldModel      = "model"
	fieldStandard   = "api-standard"
	fieldThinking   = "thinking-effort"
	fieldMultiAgent = "multi-agent"
)

type SaveMsg struct {
	Target   *Screen
	Settings types.Settings
}
type SaveAsWorkspaceMsg struct {
	Target   *Screen
	Settings types.Settings
}
type CancelMsg struct{ Target *Screen }
type ResetMsg struct{ Target *Screen }

type FetchModelsMsg struct {
	ProviderID string
	BaseURL    string
	APIKey     string
	StandardID string
}

type page uint8

const (
	rootPage page = iota
	rolePage
	modelPage
	providerPage
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

	base        types.Settings
	effective   types.Settings
	overrides   map[string]types.CategorySettings
	presets     []types.ProviderPreset
	providers   map[string]types.ProviderConfig
	models      map[string][]types.ModelEntry
	snapshot    *snapshot
	modelFilter *ui.FilterField
	modelList   *ui.List
	modelQuery  string
	ruleBleedX  int
	ruleBleedW  int
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
	s.form = ui.NewDockedForm(FormID, nil, "", "", s.tokens)
	s.modelFilter = ui.NewFilterField(FilterID, s.tokens)
	s.modelFilter.SetPrompt(ui.SearchGlyphEnd(s.tokens))
	s.modelFilter.SetPromptPlacement(ui.PromptEnd)
	s.modelFilter.SetPlaceholder("Filter…")
	s.modelFilter.OnChanged(func(q string) { s.modelQuery = q; s.syncPickerList() })
	s.modelList = ui.NewList(ListID, s.tokens)
	s.modelList.SetActivateAction("session-settings.model.select")
	s.modelList.SetDefaultRowBackground(ui.ColorBg)
	s.openRoot()
	return s
}

func (s *Screen) ConfigureProviders(presets []types.ProviderPreset, providers map[string]types.ProviderConfig) {
	s.presets = append(s.presets[:0], presets...)
	s.providers = make(map[string]types.ProviderConfig, len(providers))
	for id, cfg := range providers {
		cfg.ModelStandards = cloneStandards(cfg.ModelStandards)
		s.providers[id] = cfg
	}
	switch s.page {
	case rolePage:
		s.openRole(s.role)
	case modelPage, providerPage:
		s.syncPickerList()
	}
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	s.form.SetTokens(tokens)
	s.actions.SetTokens(tokens)
	if s.modelFilter != nil {
		s.modelFilter.SetTokens(tokens)
	}
	if s.modelList != nil {
		s.modelList.SetTokens(tokens)
		s.modelList.SetDefaultRowBackground(ui.ColorBg)
	}
}

// SetRuleBleed records the deck row geometry supplied by the parent
// PanelDeck so the picker's divider can span the row edge to edge while
// list rows stay in the padded column; width 0 keeps the divider bounded.
func (s *Screen) SetRuleBleed(offsetX, width int) { s.ruleBleedX, s.ruleBleedW = offsetX, width }

func (s *Screen) Children() []ui.Component {
	if s.pickerPage() {
		return []ui.Component{s.actions}
	}
	return []ui.Component{s.form, s.actions}
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: s.PreferredHeight(cs.MaxH)})
}

func (s *Screen) PreferredHeight(maxHeight int) int {
	chrome := s.actionChrome()
	if s.pickerPage() {
		return min(maxHeight, 2+1+8+chrome)
	}
	return min(maxHeight, s.form.PreferredHeight(max(0, maxHeight-chrome))+chrome)
}

func (s *Screen) actionChrome() int {
	if s.actions == nil {
		return 2
	}
	return max(1, s.actions.PreferredHeight())
}

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	chrome := s.actionChrome()
	bodyH := max(1, r.H-chrome)
	if s.pickerPage() {
		s.modelFilter.Layout(ctx, ui.Rect{X: 0, Y: 2, W: r.W, H: 1})
		listH := max(1, bodyH-3)
		s.modelList.Layout(ctx, ui.Rect{X: 0, Y: 3, W: r.W, H: listH})
	} else {
		s.form.Layout(ctx, ui.Rect{X: 0, Y: 0, W: r.W, H: bodyH})
	}
	s.actions.Layout(ctx, ui.Rect{X: 0, Y: max(0, r.H-chrome), W: r.W, H: chrome})
}

func (s *Screen) Paint(buf *surface.CellBuffer, originX, originY int, ctx ui.PaintContext) {
	if !s.pickerPage() {
		return
	}
	r := s.Bounds()
	x, y := originX+r.X, originY+r.Y
	bg := s.tokens.Color(ui.ColorBg)
	fg := s.tokens.Color(ui.ColorFg)
	divider := s.tokens.Color(ui.ColorPopupDivider)
	leaf := "Model"
	if s.page == providerPage {
		leaf = "Provider"
	}
	title := s.title(s.roleName(), leaf)
	divX, divW := x, r.W
	if s.ruleBleedW-s.ruleBleedX > r.W {
		divX, divW = x-s.ruleBleedX, s.ruleBleedW
	}
	buf.WriteString(x, y, rendering.TruncateVisual(title, r.W, ""), fg, bg, cell.AttrBold)
	buf.WriteString(divX, y+1, strings.Repeat(string(ui.FirstRune(ui.DefaultGlyphsFromTokens(s.tokens).BorderH, '─')), max(0, divW)), divider, bg, 0)
	s.modelFilter.Paint(buf, x, y, ctx)
	s.modelList.Paint(buf, x, y, ctx)
}

func (s *Screen) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if !s.pickerPage() {
		return ui.EventResult{}
	}
	if evt.Kind == ui.EventMouseClick || evt.Kind == ui.EventMouseMotion || evt.Kind == ui.EventMouseWheel {
		fr := s.modelFilter.Bounds()
		if fr.Contains(ctx.LocalX, ctx.LocalY) {
			return s.modelFilter.Handle(ui.EventContext{
				Theme: ctx.Theme, Focused: true,
				LocalX: ctx.LocalX - fr.X, LocalY: ctx.LocalY - fr.Y,
				ScreenX: ctx.ScreenX, ScreenY: ctx.ScreenY,
			}, evt)
		}
		lr := s.modelList.Bounds()
		if lr.Contains(ctx.LocalX, ctx.LocalY) {
			res := s.modelList.Handle(ui.EventContext{
				Theme: ctx.Theme, Focused: true,
				LocalX: ctx.LocalX - lr.X, LocalY: ctx.LocalY - lr.Y,
				ScreenX: ctx.ScreenX, ScreenY: ctx.ScreenY,
			}, evt)
			if res.Action != nil && s.isPickerSelectAction(res.Action.ID) {
				return s.commitPickerAction(res)
			}
			return res
		}
		return ui.EventResult{}
	}
	if !evt.IsKey() {
		return ui.EventResult{}
	}
	key := evt.Key
	if key.Code == tea.KeyEsc {
		if s.modelFilter.Value() != "" {
			return s.modelFilter.Handle(ctx, evt)
		}
		s.openRole(s.role)
		return focusFormResult(ui.InvalidateLayout)
	}
	switch key.Code {
	case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd, tea.KeyEnter:
		res := s.modelList.Handle(ctx, evt)
		if res.Action != nil && s.isPickerSelectAction(res.Action.ID) {
			return s.commitPickerAction(res)
		}
		return res
	case tea.KeyTab:
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: ActionsID}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	return s.modelFilter.Handle(ctx, evt)
}

func (s *Screen) isPickerSelectAction(id string) bool {
	return id == "session-settings.model.select" || id == "session-settings.provider.select"
}

func (s *Screen) commitPickerAction(res ui.EventResult) ui.EventResult {
	if s.page == modelPage {
		s.commitModelSelection()
	} else {
		s.commitProviderSelection()
	}
	s.openRole(s.role)
	return focusFormResult(ui.InvalidateLayout)
}

// applyModelToEffective stages a model picker selection into the effective
// settings so returning to the role page (which rebuilds the form from
// effective) keeps the picked model instead of reverting to the old value.
func (s *Screen) applyModelToEffective(id string) {
	if s.role == "" || id == "" {
		return
	}
	cfg := categoryFor(s.effective, s.role)
	cfg.Model = id
	setCategory(&s.effective, s.role, cfg)
}

// applyProviderToEffective stages a provider picker selection into the
// effective settings for the current role so the role page rebuild keeps it.
func (s *Screen) applyProviderToEffective(id string) {
	if s.role == "" || id == "" {
		return
	}
	cfg := categoryFor(s.effective, s.role)
	cfg.Provider = id
	providerCfg := s.providers[id]
	cfg.Model = providerCfg.Model
	cfg.APIStandard = "inherit"
	cfg.ThinkingEffort = providerCfg.ThinkingEffort
	setCategory(&s.effective, s.role, cfg)
}

func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case "session-settings.role.open":
		role, _ := action.Payload.(string)
		s.openRole(role)
		return focusFormResult(ui.InvalidateLayout)
	case "session-settings.model.open":
		return ui.EventResult{Handled: true, Cmd: s.openModelPicker(), Invalidate: ui.InvalidateLayout | ui.InvalidatePaint, Action: &ui.Action{ID: "ui.focus", Payload: ScreenID}}
	case "session-settings.provider.open":
		return ui.EventResult{Handled: true, Cmd: s.openProviderPicker(), Invalidate: ui.InvalidateLayout | ui.InvalidatePaint, Action: &ui.Action{ID: "ui.focus", Payload: ScreenID}}
	case "session-settings.model.select":
		s.commitModelSelection()
		s.openRole(s.role)
		return focusFormResult(ui.InvalidateLayout)
	case "session-settings.provider.select":
		s.commitProviderSelection()
		s.openRole(s.role)
		return focusFormResult(ui.InvalidateLayout)
	case "form.select.open":
		fieldID, _ := action.Payload.(string)
		return ui.EventResult{Handled: true, Cmd: s.openSelect(fieldID), Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	case "form.changed":
		s.applyRootForm()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	case "form.cancel":
		return s.cancelOrBack()
	case "form.exit.backward":
		s.actions.FocusLast()
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: ActionsID}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "form.exit.forward":
		s.actions.FocusFirst()
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: ActionsID}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "actionbar.exit.backward":
		if s.pickerPage() {
			return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: ScreenID}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		}
		s.form.FocusLast()
		return focusFormResult(0)
	case "actionbar.exit.forward":
		if s.pickerPage() {
			return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: ScreenID}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		}
		s.form.FocusFirst()
		return focusFormResult(0)
	case "session-settings.save":
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return SaveMsg{Target: s, Settings: s.ToSettings()} }, Invalidate: ui.InvalidatePaint}
	case "session-settings.save-workspace":
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return SaveAsWorkspaceMsg{Target: s, Settings: s.ToSettings()} }, Invalidate: ui.InvalidatePaint}
	case "session-settings.cancel":
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "panel.close"}, Cmd: func() tea.Msg { return CancelMsg{Target: s} }, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	case "session-settings.reset":
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "panel.close"}, Cmd: func() tea.Msg { return ResetMsg{Target: s} }, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	case "session-settings.role.apply":
		s.applyRoleForm()
		s.openRoot()
		return focusFormResult(ui.InvalidateLayout)
	case "session-settings.role.back":
		if s.pickerPage() {
			s.openRole(s.role)
			return focusFormResult(ui.InvalidateLayout)
		}
		s.openRoot()
		return focusFormResult(ui.InvalidateLayout)
	case "session-settings.role.inherit-main":
		s.inheritMain(s.role)
		s.openRoot()
		return focusFormResult(ui.InvalidateLayout)
	case "session-settings.role.reset-workspace":
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
		_, overridden := s.overrides[role.Key]
		model.AddField(&ui.FieldState{
			ID: role.Key, Label: role.Name, Type: ui.FieldNav,
			Columns:       []string{cfg.Model, cfg.Provider},
			Target:        "session-settings.role.open",
			Inherited:     !overridden,
			InheritedFrom: "workspace",
		})
	}
	model.AddField(ui.SpacerFieldState("roles-gap"))
	multi := ui.BoolFieldState(fieldMultiAgent, "Multi Agent", s.effective.Agents.Enabled,
		"Allow this session to spawn researcher, crafter, and aeron sub-agents.")
	// One spacer slot before the cycle control: the switch aligns with the
	// agent rows' first value column (· provider/model) instead of hugging
	// the label.
	multi.Columns = []string{""}
	if s.effective.SessionMultiAgent == nil {
		multi.Inherited = true
		multi.InheritedFrom = "workspace"
	}
	model.AddField(multi)
	s.formModel = model
	s.form.SetForm(model, s.title(), "")
	s.actions = s.rootActions()
}

func (s *Screen) rootActions() *ui.ActionBar {
	return ui.NewDockedActionBar(ActionsID, s.tokens,
		ui.ActionBarButton{ID: ui.NewID(ActionsID, "save"), Label: "Save", Action: "session-settings.save", Variant: ui.VariantPrimary, Enabled: true},
		ui.ActionBarButton{ID: ui.NewID(ActionsID, "cancel"), Label: "Cancel", Action: "session-settings.cancel", Variant: ui.VariantSecondary, Enabled: true},
		ui.ActionBarButton{ID: ui.NewID(ActionsID, "reset"), Label: "Reset", Action: "session-settings.reset", Variant: ui.VariantDanger, Enabled: true},
		// Least common action sits last so it never crowds Save/Cancel/Reset.
		ui.ActionBarButton{ID: ui.NewID(ActionsID, "save-workspace"), Label: "Save as workspace", Action: "session-settings.save-workspace", Variant: ui.VariantSecondary, Enabled: true},
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
	standard := cfg.APIStandard
	if standard == "" {
		standard = "inherit"
	}
	model := ui.NewFormModel()
	model.AddField(&ui.FieldState{
		ID: fieldProvider, Label: "Provider", Type: ui.FieldNav,
		Value: ui.StringValue(providerID), Description: s.providerLabel(providerID),
		Target: "session-settings.provider.open",
	})
	model.AddField(&ui.FieldState{
		ID: fieldModel, Label: "Model", Type: ui.FieldNav,
		Value: ui.StringValue(cfg.Model), Description: cfg.Model,
		Target: "session-settings.model.open",
	})
	standardField := ui.SelectFieldState(fieldStandard, "Protocol", standard, s.standardOptions(providerID, cfg.Model), "")
	standardField.Type = ui.FieldCycle
	model.AddField(standardField)
	model.AddField(ui.ThinkingFieldState(fieldThinking, thinking, ""))
	s.formModel = model
	s.form.SetForm(model, s.title(name), "")
	buttons := []ui.ActionBarButton{
		{ID: ui.NewID(ActionsID, "apply"), Label: "Apply", Action: "session-settings.role.apply", Variant: ui.VariantPrimary, Enabled: true},
		{ID: ui.NewID(ActionsID, "back"), Label: "Back", Action: "session-settings.role.back", Variant: ui.VariantSecondary, Enabled: true},
	}
	// The main agent is the source of "inherit from main"; it cannot inherit
	// from itself, so that button only appears on the other roles.
	if role != "default" {
		buttons = append(buttons, ui.ActionBarButton{ID: ui.NewID(ActionsID, "inherit-main"), Label: "Inherit from main", Action: "session-settings.role.inherit-main", Variant: ui.VariantSecondary, Enabled: true})
	}
	buttons = append(buttons, ui.ActionBarButton{ID: ui.NewID(ActionsID, "reset-workspace"), Label: "Reset to workspace", Action: "session-settings.role.reset-workspace", Variant: ui.VariantDanger, Enabled: true})
	s.actions = ui.NewDockedActionBar(ActionsID, s.tokens, buttons...)
}

func (s *Screen) standardOptions(providerID, model string) []ui.FormOption {
	cfg := s.providers[providerID]
	inherited := cfg.APIStandard
	if override := cfg.ModelStandards[model]; override != "" {
		inherited = override
	}
	if inherited == "" {
		inherited = provider.DefaultStandardID(providerID)
	}
	options := []ui.FormOption{{
		ID: "inherit", Label: "Use provider/model default",
		Detail: standardLabel(inherited),
	}}
	for _, standard := range provider.StandardList() {
		options = append(options, ui.FormOption{
			ID: standard.ID, Label: standard.Name, Detail: standard.Description,
		})
	}
	return options
}

func (s *Screen) refreshStandardOptions(model *ui.FormModel) {
	if model == nil {
		return
	}
	field := model.Field(fieldStandard)
	if field == nil {
		return
	}
	field.Options = s.standardOptions(
		model.Field(fieldProvider).Value.String(),
		model.Field(fieldModel).Value.String(),
	)
}

func standardLabel(id string) string {
	for _, standard := range provider.StandardList() {
		if standard.ID == id {
			return standard.Name
		}
	}
	return id
}

func cloneStandards(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	out := make(map[string]string, len(src))
	for model, standard := range src {
		out[model] = standard
	}
	return out
}

func (s *Screen) cancelOrBack() ui.EventResult {
	switch s.page {
	case modelPage, providerPage:
		s.openRole(s.role)
		return focusFormResult(ui.InvalidateLayout)
	case rolePage:
		s.openRoot()
		return focusFormResult(ui.InvalidateLayout)
	default:
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "panel.close"}, Cmd: func() tea.Msg { return CancelMsg{Target: s} }, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	}
}

func (s *Screen) currentProviderID() string {
	if s.formModel != nil {
		if field := s.formModel.Field(fieldProvider); field != nil {
			return field.Value.String()
		}
	}
	cfg := categoryFor(s.effective, s.role)
	if cfg.Provider != "" {
		return cfg.Provider
	}
	return s.effective.ProviderID
}

func (s *Screen) OpenModelPicker(role string) tea.Cmd {
	if role == "" {
		role = "default"
	}
	s.openRole(role)
	return s.openModelPicker()
}

func (s *Screen) openModelPicker() tea.Cmd {
	if s.role == "" {
		s.openRole("default")
	}
	s.page = modelPage
	s.modelQuery = ""
	if s.modelFilter != nil {
		s.modelFilter.SetValue("")
		s.modelFilter.SetPlaceholder("Filter models…")
	}
	if s.modelList != nil {
		s.modelList.SetActivateAction("session-settings.model.select")
	}
	s.syncPickerList()
	s.actions = ui.NewDockedActionBar(ActionsID, s.tokens,
		ui.ActionBarButton{ID: ui.NewID(ActionsID, "back"), Label: "Back", Action: "session-settings.role.back", Variant: ui.VariantSecondary, Enabled: true},
	)
	return s.fetchModelsCmd()
}

func (s *Screen) openProviderPicker() tea.Cmd {
	if s.role == "" {
		s.openRole("default")
	}
	s.page = providerPage
	s.modelQuery = ""
	if s.modelFilter != nil {
		s.modelFilter.SetValue("")
		s.modelFilter.SetPlaceholder("Filter providers…")
	}
	if s.modelList != nil {
		s.modelList.SetActivateAction("session-settings.provider.select")
	}
	s.syncPickerList()
	s.actions = ui.NewDockedActionBar(ActionsID, s.tokens,
		ui.ActionBarButton{ID: ui.NewID(ActionsID, "back"), Label: "Back", Action: "session-settings.role.back", Variant: ui.VariantSecondary, Enabled: true},
	)
	return nil
}

func (s *Screen) fetchModelsCmd() tea.Cmd {
	providerID := s.currentProviderID()
	if providerID == "" {
		return nil
	}
	if len(s.models[providerID]) > 0 {
		s.syncPickerList()
		return nil
	}
	cfg := s.providers[providerID]
	standardID := cfg.APIStandard
	if standardID == "" {
		standardID = provider.DefaultStandardID(providerID)
	}
	s.syncPickerList()
	return func() tea.Msg {
		return FetchModelsMsg{
			ProviderID: providerID, BaseURL: cfg.ProviderURL,
			APIKey: cfg.APIKey, StandardID: standardID,
		}
	}
}

func (s *Screen) syncPickerList() {
	if s.page == providerPage {
		s.syncProviderList()
		return
	}
	s.syncModelList()
}

func (s *Screen) syncModelList() {
	if s.modelList == nil {
		return
	}
	providerID := s.currentProviderID()
	options := ui.ModelOptions(s.models[providerID])
	query := strings.ToLower(strings.TrimSpace(s.modelQuery))
	items := make([]ui.ListItem, 0, len(options))
	for _, option := range options {
		if query != "" && !strings.Contains(strings.ToLower(option.Label), query) && !strings.Contains(strings.ToLower(option.ID), query) && !strings.Contains(strings.ToLower(option.Detail), query) {
			continue
		}
		items = append(items, ui.ListItem{ID: option.ID, Label: option.Label, Detail: option.Detail, Payload: option.ID})
	}
	if len(items) == 0 && len(s.models[providerID]) == 0 {
		items = append(items, ui.ListItem{ID: "loading", Label: "Fetching models…", Disabled: true})
	}
	s.modelList.SetItems(items)
	s.selectPickerItem(fieldModel, items)
}

func (s *Screen) syncProviderList() {
	if s.modelList == nil {
		return
	}
	query := strings.ToLower(strings.TrimSpace(s.modelQuery))
	items := make([]ui.ListItem, 0, len(s.presets))
	for _, preset := range s.presets {
		if query != "" && !strings.Contains(strings.ToLower(preset.Name), query) && !strings.Contains(strings.ToLower(preset.ID), query) && !strings.Contains(strings.ToLower(preset.HelpText), query) {
			continue
		}
		items = append(items, ui.ListItem{ID: preset.ID, Label: preset.Name, Detail: preset.HelpText, Payload: preset.ID})
	}
	if len(items) == 0 {
		items = append(items, ui.ListItem{ID: "empty", Label: "No providers", Disabled: true})
	}
	s.modelList.SetItems(items)
	s.selectPickerItem(fieldProvider, items)
}

func (s *Screen) selectPickerItem(fieldID string, items []ui.ListItem) {
	if s.formModel == nil {
		return
	}
	field := s.formModel.Field(fieldID)
	if field == nil {
		return
	}
	current := field.Value.String()
	if current == "" {
		current = field.Description
	}
	for i, item := range items {
		if item.ID == current {
			s.modelList.SelectIndex(i)
			return
		}
	}
}

func (s *Screen) commitModelSelection() {
	item, ok := s.modelList.Selected()
	if !ok || !item.Selectable() {
		return
	}
	id, _ := item.Payload.(string)
	if id == "" {
		id = item.ID
	}
	if s.formModel != nil {
		s.formModel.SetValue(fieldModel, ui.StringValue(id))
		if field := s.formModel.Field(fieldModel); field != nil {
			field.Description = item.Label
		}
	}
	s.applyModelToEffective(id)
}

func (s *Screen) commitProviderSelection() {
	item, ok := s.modelList.Selected()
	if !ok || !item.Selectable() {
		return
	}
	id, _ := item.Payload.(string)
	if id == "" {
		id = item.ID
	}
	s.applyProvider(id)
	s.applyProviderToEffective(id)
}

func (s *Screen) applyProvider(providerID string) {
	if s.formModel == nil || providerID == "" {
		return
	}
	s.formModel.SetValue(fieldProvider, ui.StringValue(providerID))
	if field := s.formModel.Field(fieldProvider); field != nil {
		field.Description = s.providerLabel(providerID)
	}
	providerCfg := s.providers[providerID]
	if field := s.formModel.Field(fieldModel); field != nil {
		s.formModel.SetValue(fieldModel, ui.StringValue(providerCfg.Model))
		field.Description = providerCfg.Model
		field.Options = ui.ModelOptions(s.models[providerID])
		field.Errors, field.Loading, field.LoadMessage = nil, false, ""
	}
	s.formModel.SetValue(fieldStandard, ui.StringValue("inherit"))
	s.formModel.SetValue(fieldThinking, ui.StringValue(providerCfg.ThinkingEffort))
	s.refreshStandardOptions(s.formModel)
}

func (s *Screen) openSelect(fieldID string) tea.Cmd {
	switch fieldID {
	case fieldModel:
		return s.openModelPicker()
	case fieldProvider:
		return s.openProviderPicker()
	}
	return nil
}

func (s *Screen) ApplyModels(providerID string, models []types.ModelEntry) {
	s.models[providerID] = models
	if s.formModel != nil && s.currentProviderID() == providerID {
		if field := s.formModel.Field(fieldModel); field != nil {
			field.Options = ui.ModelOptions(models)
			field.Errors, field.Loading, field.LoadMessage = nil, false, ""
		}
	}
	switch s.page {
	case modelPage, providerPage:
		s.syncPickerList()
	case rolePage:
		s.form.SetForm(s.formModel, s.title(s.roleName()), "")
	}
}

func (s *Screen) ApplyModelsError(providerID, message string) {
	if s.formModel != nil && s.currentProviderID() == providerID {
		if field := s.formModel.Field(fieldModel); field != nil {
			field.Errors, field.Loading, field.LoadMessage = []string{message}, false, ""
		}
	}
	switch s.page {
	case modelPage:
		if s.modelList != nil {
			s.modelList.SetItems([]ui.ListItem{{ID: "error", Label: message, Disabled: true}})
		}
	case providerPage:
		s.syncPickerList()
	case rolePage:
		s.form.SetForm(s.formModel, s.title(s.roleName()), "")
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

func (s *Screen) title(parts ...string) string {
	return ui.TitleBreadcrumb(append([]string{"Session Settings"}, parts...)...)
}

func (s *Screen) pickerPage() bool {
	return s.page == modelPage || s.page == providerPage
}

func (s *Screen) providerLabel(id string) string {
	for _, preset := range s.presets {
		if preset.ID == id {
			if preset.Name != "" {
				return preset.Name
			}
			return preset.ID
		}
	}
	if id == "" {
		return "Not set"
	}
	return id
}

func (s *Screen) applyRootForm() {
	if s.page != rootPage || s.formModel == nil {
		return
	}
	field := s.formModel.Field(fieldMultiAgent)
	if field == nil {
		return
	}
	enabled := ui.BoolFieldValue(field)
	s.effective.Agents.Enabled = enabled
	s.effective.SessionMultiAgent = boolPtr(enabled)
	field.Inherited = false
	field.InheritedFrom = ""
}

func boolPtr(v bool) *bool { return &v }

func (s *Screen) applyRoleForm() {
	if s.page != rolePage {
		return
	}
	cfg := categoryFor(s.effective, s.role)
	cfg.Provider = s.formModel.Field(fieldProvider).Value.String()
	cfg.Model = s.formModel.Field(fieldModel).Value.String()
	cfg.APIStandard = s.formModel.Field(fieldStandard).Value.String()
	if cfg.APIStandard == "inherit" {
		cfg.APIStandard = ""
	}
	cfg.ThinkingEffort = s.formModel.Field(fieldThinking).Value.String()
	setCategory(&s.effective, s.role, cfg)
	s.overrides[s.role] = types.CategorySettings{
		Provider: cfg.Provider, Model: cfg.Model,
		APIStandard: cfg.APIStandard, ThinkingEffort: cfg.ThinkingEffort,
	}
}

func (s *Screen) resetRole(role string) {
	setCategory(&s.effective, role, categoryFor(s.base, role))
	delete(s.overrides, role)
}

// inheritMain copies the main agent's effective category into the given role
// and records it as a session override, so the role follows the main agent.
func (s *Screen) inheritMain(role string) {
	if role == "default" {
		return
	}
	mainCfg := categoryFor(s.effective, "default")
	setCategory(&s.effective, role, mainCfg)
	s.overrides[role] = types.CategorySettings{
		Provider: mainCfg.Provider, Model: mainCfg.Model,
		APIStandard: mainCfg.APIStandard, ThinkingEffort: mainCfg.ThinkingEffort,
	}
}

func (s *Screen) ToSettings() types.Settings {
	s.applyRootForm()
	settings := cloneSettings(s.effective)
	settings.SessionOverrides = copyCategoryMap(s.overrides)
	settings.Agents.Enabled = s.effective.Agents.Enabled
	settings.SessionMultiAgent = cloneBoolPtr(s.effective.SessionMultiAgent)
	main := categoryFor(settings, "default")
	settings.ProviderID, settings.Model = main.Provider, main.Model
	settings.APIStandard, settings.ThinkingEffort = main.APIStandard, main.ThinkingEffort
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
	s.effective.SessionMultiAgent = nil
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
	return types.CategorySettings{
		Provider: settings.ProviderID, Model: settings.Model,
		APIStandard: settings.APIStandard, ThinkingEffort: settings.ThinkingEffort,
	}
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
	dst.SessionMultiAgent = cloneBoolPtr(src.SessionMultiAgent)
	return dst
}

func cloneBoolPtr(src *bool) *bool {
	if src == nil {
		return nil
	}
	value := *src
	return &value
}

var _ ui.Component = (*Screen)(nil)
var _ ui.Parent = (*Screen)(nil)
var _ ui.ActionHandler = (*Screen)(nil)
