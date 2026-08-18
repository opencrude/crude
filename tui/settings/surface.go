package settings

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
)

const (
	WorkspaceSettingsSurfaceID    ui.ComponentID = "workspace-settings"
	WorkspaceSettingsScopeID      ui.ComponentID = "workspace-settings/scope"
	WorkspaceSettingsSearchID     ui.ComponentID = "workspace-settings/search"
	WorkspaceSettingsListID       ui.ComponentID = "workspace-settings/categories"
	WorkspaceSettingsFormID       ui.ComponentID = "workspace-settings/form"
	WorkspaceSettingsActionID     ui.ComponentID = "workspace-settings/actions"
	WorkspaceSettingsResetBarID   ui.ComponentID = "workspace-settings/reset-bar"
	WorkspaceSettingsSaveID       ui.ComponentID = "workspace-settings/save"
	WorkspaceSettingsCancelID     ui.ComponentID = "workspace-settings/cancel"
	WorkspaceSettingsResetID      ui.ComponentID = "workspace-settings/reset"
	WorkspaceSettingsListScrollID ui.ComponentID = "workspace-settings/list-scroll"
	WorkspaceSettingsFormScrollID ui.ComponentID = "workspace-settings/form-scroll"
	WorkspaceSettingsDividerID    ui.ComponentID = "workspace-settings/divider"
	workspaceSettingsInset                       = 2
	workspaceSettingsGap                         = 3
)

// WorkspaceSettingsSurface composes native category navigation, the shared
// FormModel/Form controls, and the shared ActionBar without a legacy bridge.
type WorkspaceSettingsSurface struct {
	ui.Base
	model      *Model
	tokens     *ui.TokenSet
	scope      *settingsScopeBar
	search     *settingsSearchField
	categories *settingsCategoryList
	form       *ui.Form
	reset      *ui.ActionBar
	actions    *ui.ActionBar
	listScroll *ui.ScrollbarControl
	formScroll *ui.ScrollbarControl
	split      *ui.SplitModel
	divider    *ui.SplitDivider
	focus      func(ui.ComponentID)
}

func NewSurface(th *theme.Theme, model *Model) *WorkspaceSettingsSurface {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	s := &WorkspaceSettingsSurface{Base: ui.NewBase(WorkspaceSettingsSurfaceID), model: model, tokens: tokens}
	s.scope = &settingsScopeBar{Base: ui.NewBase(WorkspaceSettingsScopeID), owner: s, hover: -1}
	s.search = &settingsSearchField{Base: ui.NewBase(WorkspaceSettingsSearchID), field: ui.NewFilterField(WorkspaceSettingsSearchID, tokens), owner: s}
	s.search.field.SetPrompt(ui.SearchGlyphEnd(tokens))
	s.search.field.SetPromptPlacement(ui.PromptEnd)
	s.search.field.SetPlaceholder("Search settings")
	s.search.field.SetSubmitAction("")
	s.search.field.SetBackgroundColor(tokens.Color(ui.ColorPopupSubtleBg))
	s.search.field.SetPromptColor(tokens.Color(ui.ColorAccent))
	s.categories = &settingsCategoryList{Base: ui.NewBase(WorkspaceSettingsListID), list: ui.NewList(WorkspaceSettingsListID, tokens), owner: s}
	s.categories.list.SetActivateAction("settings.category.open")
	s.categories.list.SetSelectionMarker(false)
	s.form = ui.NewForm(WorkspaceSettingsFormID, nil, "", "", tokens)
	s.form.SetFieldGap(1)
	s.form.SetArrowExitsForm(true)
	s.listScroll = ui.NewScrollbarControl(WorkspaceSettingsListScrollID, s.categories.list.Scroll(), tokens)
	s.formScroll = ui.NewScrollbarControl(WorkspaceSettingsFormScrollID, s.form.Scroll(), tokens)
	s.split = ui.NewSplitModel(ui.Horizontal)
	s.split.SetDividerSize(workspaceSettingsGap)
	s.split.SetMinSizes(12, 20)
	s.divider = ui.NewSplitDivider(WorkspaceSettingsDividerID, s.split)
	s.divider.SetTokens(tokens)
	s.divider.SetColorRole(ui.ColorPopupDivider)
	s.divider.SetTransparent(true)
	s.rebuildActions()
	s.syncModel()
	return s
}

func (s *WorkspaceSettingsSurface) rebuildActions() {
	s.actions = ui.NewActionBar(WorkspaceSettingsActionID, s.tokens,
		ui.ActionBarButton{ID: WorkspaceSettingsSaveID, Label: "Save", Action: "settings.save", Variant: ui.VariantPrimary, Enabled: true},
		ui.ActionBarButton{ID: WorkspaceSettingsCancelID, Label: "Cancel", Action: "settings.cancel", Variant: ui.VariantSecondary, Enabled: true},
	)
	s.actions.SetCancelAction("settings.cancel")
	s.reset = nil
	if s.model != nil && s.model.Scope() == types.SettingsScopeWorkspace {
		s.reset = ui.NewActionBar(WorkspaceSettingsResetBarID, s.tokens,
			ui.ActionBarButton{ID: WorkspaceSettingsResetID, Label: "Reset section", Action: "settings.reset", Variant: ui.VariantDanger, Enabled: true},
		)
		s.reset.SetAlignEnd(false)
		s.reset.SetExitActions("resetbar.exit.forward", "resetbar.exit.backward")
	}
	// Replacing the bars drops button bounds. Re-layout into the current
	// surface rect so the next paint still shows Save/Cancel without waiting
	// for a host size-change (SetModel used to hit this on overlay ticks).
	if !s.Bounds().Empty() {
		s.Layout(ui.LayoutContext{}, s.Bounds())
	}
}

func (s *WorkspaceSettingsSurface) Children() []ui.Component {
	children := []ui.Component{s.scope, s.search, s.categories, s.form}
	if s.reset != nil {
		children = append(children, s.reset)
	}
	children = append(children, s.actions)
	if s.listScroll != nil && s.listScroll.Needed() {
		children = append(children, s.listScroll)
	}
	if s.formScroll != nil && s.formScroll.Needed() {
		children = append(children, s.formScroll)
	}
	if s.divider != nil {
		children = append(children, s.divider)
	}
	return children
}
func (s *WorkspaceSettingsSurface) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	if s.search != nil {
		s.search.SetTokens(tokens)
		s.search.field.SetBackgroundColor(tokens.Color(ui.ColorPopupSubtleBg))
		s.search.field.SetPromptColor(tokens.Color(ui.ColorAccent))
	}
	if s.categories != nil {
		s.categories.SetTokens(tokens)
	}
	if s.form != nil {
		s.form.SetTokens(tokens)
	}
	if s.reset != nil {
		s.reset.SetTokens(tokens)
	}
	if s.actions != nil {
		s.actions.SetTokens(tokens)
	}
	if s.listScroll != nil {
		s.listScroll.SetTokens(tokens)
	}
	if s.formScroll != nil {
		s.formScroll.SetTokens(tokens)
	}
	if s.divider != nil {
		s.divider.SetTokens(tokens)
		s.divider.SetColorRole(ui.ColorPopupDivider)
	}
	if s.search != nil {
		s.search.field.SetPrompt(ui.SearchGlyphEnd(tokens))
		s.search.field.SetPromptPlacement(ui.PromptEnd)
	}
}
func (s *WorkspaceSettingsSurface) SetFocusHandler(fn func(ui.ComponentID)) { s.focus = fn }
func (s *WorkspaceSettingsSurface) FocusScope()                             { s.focusID(WorkspaceSettingsScopeID) }
func (s *WorkspaceSettingsSurface) FocusSearch()                            { s.focusID(WorkspaceSettingsSearchID) }
func (s *WorkspaceSettingsSurface) FocusCategories()                        { s.focusID(WorkspaceSettingsListID) }
func (s *WorkspaceSettingsSurface) FocusForm()                              { s.focusID(WorkspaceSettingsFormID) }
func (s *WorkspaceSettingsSurface) FocusReset() {
	if s.reset != nil {
		s.focusID(WorkspaceSettingsResetBarID)
		return
	}
	s.FocusActions()
}
func (s *WorkspaceSettingsSurface) FocusActions() { s.focusID(WorkspaceSettingsActionID) }
func (s *WorkspaceSettingsSurface) focusID(id ui.ComponentID) {
	if s.focus != nil {
		s.focus(id)
	}
}

func (s *WorkspaceSettingsSurface) SetModel(model *Model) {
	s.model = model
	s.search.owner, s.categories.owner = s, s
	s.syncModel()
}
func (s *WorkspaceSettingsSurface) syncModel() {
	if s.model == nil {
		return
	}
	s.search.field.SetValue(s.model.searchText())
	s.categories.syncItems()
	s.refreshForm()
}
func (s *WorkspaceSettingsSurface) refreshForm() {
	if s.model == nil || s.model.selected < 0 || s.model.selected >= len(s.model.categories) {
		s.form.SetForm(nil, "", "")
		return
	}
	category := s.model.categories[s.model.selected]
	s.form.SetForm(category.Form, "", "")
}

func (s *WorkspaceSettingsSurface) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}
func (s *WorkspaceSettingsSurface) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	innerW := max(1, r.W-workspaceSettingsInset*2)
	leftW, gap := s.railGeometry(innerW)
	railX := workspaceSettingsInset
	formX, formW := railX+leftW+gap, max(1, r.W-(railX+leftW+gap)-workspaceSettingsInset)
	footerH := 2
	topPad := s.topPad()
	searchY := topPad
	catY, catH := topPad+2, max(1, r.H-footerH-(topPad+2))
	formH := max(1, r.H-footerH-topPad)
	footerY := max(0, r.H-1)
	s.search.Layout(ctx, ui.Rect{X: railX, Y: searchY, W: leftW, H: 1})
	s.categories.Layout(ctx, ui.Rect{X: railX, Y: catY, W: leftW, H: catH})
	s.form.Layout(ctx, ui.Rect{X: formX, Y: topPad, W: formW, H: formH})
	if s.listScroll != nil && s.listScroll.Needed() {
		s.categories.Layout(ctx, ui.Rect{X: railX, Y: catY, W: max(1, leftW-1), H: catH})
		s.listScroll.Layout(ctx, ui.Rect{X: railX + leftW - 1, Y: catY, W: 1, H: catH})
	} else if s.listScroll != nil {
		s.listScroll.Layout(ctx, ui.Rect{})
	}
	if s.formScroll != nil && s.formScroll.Needed() {
		s.form.Layout(ctx, ui.Rect{X: formX, Y: topPad, W: max(1, formW-1), H: formH})
		s.formScroll.Layout(ctx, ui.Rect{X: formX + formW - 1, Y: topPad, W: 1, H: formH})
	} else if s.formScroll != nil {
		s.formScroll.Layout(ctx, ui.Rect{})
	}
	if s.scope != nil {
		s.scope.Layout(ctx, ui.Rect{X: railX, Y: footerY, W: leftW, H: 1})
	}
	resetW := 0
	if s.reset != nil {
		resetW = s.reset.Measure(ctx, ui.Loose(formW, 1)).W
		s.reset.Layout(ctx, ui.Rect{X: formX, Y: footerY, W: max(1, resetW), H: 1})
	}
	actionsX, actionsW := formX, formW
	if resetW > 0 {
		gap := 2
		if s.tokens != nil {
			gap = s.tokens.ButtonGap()
		}
		actionsX = formX + resetW + gap
		actionsW = max(1, formW-resetW-gap)
	}
	s.actions.Layout(ctx, ui.Rect{X: actionsX, Y: footerY, W: actionsW, H: 1})
	if s.divider != nil {
		s.divider.SetCurrentFirst(leftW)
		s.divider.Layout(ctx, ui.Rect{X: railX + leftW, Y: 0, W: gap, H: r.H})
	}
}

func (s *WorkspaceSettingsSurface) topPad() int {
	pad := 1
	if s.tokens != nil {
		pad = s.tokens.FormTitlePad()
	}
	if pad < 1 {
		return 1
	}
	return pad
}

func (s *WorkspaceSettingsSurface) railGeometry(innerW int) (leftW, gap int) {
	gap = workspaceSettingsGap
	if s.split != nil {
		s.split.SetDividerSize(gap)
		s.split.SetMinSizes(12, 20)
		maxFirst := innerW * 2 / 3
		if maxFirst < 12 {
			maxFirst = 12
		}
		s.split.SetMaxFirst(maxFirst)
	}
	if s.split != nil && s.split.HasFirstSize() {
		leftW, gap, _ = s.split.Resolve(innerW)
		return leftW, gap
	}
	return s.leftWidth(innerW), gap
}

func (s *WorkspaceSettingsSurface) leftWidth(width int) int {
	leftW := 24
	if s.model != nil {
		for _, category := range s.model.categories {
			leftW = max(leftW, rendering.VisibleWidth(category.Name)+5)
		}
	}
	if maxLeft := width / 3; maxLeft > 0 && leftW > maxLeft {
		leftW = maxLeft
	}
	return max(12, leftW)
}

func (s *WorkspaceSettingsSurface) Paint(buf *surface.CellBuffer, originX, originY int, _ ui.PaintContext) {
	r := s.Bounds()
	if r.Empty() {
		return
	}
	bg, divider, muted := cell.NoColor, cell.NoColor, cell.NoColor
	if s.tokens != nil {
		bg = s.tokens.Color(ui.ColorPopupBg)
		divider = s.tokens.Color(ui.ColorPopupDivider)
		muted = s.tokens.Color(ui.ColorMuted)
	}
	ui.FillRect(buf, originX, originY, r, cell.BlankCell(bg))
	ox, oy := originX+r.X, originY+r.Y
	innerW := max(1, r.W-workspaceSettingsInset*2)
	leftW, _ := s.railGeometry(innerW)
	railX := workspaceSettingsInset
	actionY := oy + r.H - 2
	searchY := oy + s.topPad() + 1
	for x := ox + railX; x < ox+railX+leftW; x++ {
		buf.Set(x, searchY, cell.NewCell(ui.FirstRune(ui.DefaultGlyphsFromTokens(s.tokens).BorderH, '-'), divider, bg, 0))
	}
	_ = muted
	for x := ox + railX; x < ox+r.W-workspaceSettingsInset; x++ {
		buf.Set(x, actionY, cell.NewCell(ui.FirstRune(ui.DefaultGlyphsFromTokens(s.tokens).BorderH, '-'), divider, bg, 0))
	}
}

// Handle receives keys bubbled from focused children (search TextField leaves
// Up/Down/Esc unhandled).
//
// Region Tab order (documented + tested): search → categories → form →
// reset (workspace) → actions → scope → wrap. Shift+Tab reverses. Form Tab
// first walks fields, then emits form.exit.forward/backward to leave the form.
//
// Esc behaviour:
//   - search with non-empty query: clear query (handled by search field)
//   - form (no select open): leave form → categories (form.exit.backward)
//   - categories / empty search / actions / surface: CancelMsg (close +
//     revert live theme preview to the last saved preset)
func (s *WorkspaceSettingsSurface) Handle(_ ui.EventContext, evt ui.Event) ui.EventResult {
	if s.model == nil || !evt.IsKey() {
		return ui.EventResult{}
	}
	key := evt.Key
	switch key.Code {
	case tea.KeyUp:
		if s.model.MoveCategory(-1) {
			s.categories.syncItems()
			s.refreshForm()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
		}
		return ui.EventResult{Handled: true}
	case tea.KeyDown:
		if s.model.MoveCategory(1) {
			s.categories.syncItems()
			s.refreshForm()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
		}
		return ui.EventResult{Handled: true}
	case tea.KeyTab:
		if key.Mod.Contains(tea.ModShift) {
			s.FocusScope()
		} else {
			s.model.SetSearchQuery(s.search.field.Value())
			s.categories.syncItems()
			s.refreshForm()
			s.FocusCategories()
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidateLayout | ui.InvalidatePaint}
	case tea.KeyEsc:
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return CancelMsg{} }}
	}
	return ui.EventResult{}
}

func (s *WorkspaceSettingsSurface) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case "form.changed":
		fieldID, _ := action.Payload.(string)
		if fieldID == "preset" {
			preset := s.model.value(catAppearance, "preset")
			return ui.EventResult{
				Handled: true,
				Cmd:     func() tea.Msg { return ThemePreviewMsg{Preset: preset} },
			}
		}
		return ui.EventResult{Handled: true}
	case "form.select.open":
		fieldID, _ := action.Payload.(string)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint, Cmd: s.model.OpenSelect(fieldID)}
	case "form.cancel":
		s.FocusCategories()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "form.exit.backward":
		s.FocusCategories()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "form.exit.forward":
		s.FocusReset()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "resetbar.exit.backward":
		s.form.FocusLast()
		s.FocusForm()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "resetbar.exit.forward":
		s.FocusActions()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "actionbar.exit.backward":
		if s.reset != nil {
			s.FocusReset()
		} else {
			s.form.FocusLast()
			s.FocusForm()
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "actionbar.exit.forward":
		s.FocusScope()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "settings.save":
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg {
			return SaveMsg{Scope: s.model.Scope(), AppSettings: s.model.AppSettings(), Overrides: s.model.Overrides()}
		}}
	case "settings.reset":
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return ResetSectionMsg{Key: s.model.currentOverrideKey()} }}
	case "settings.cancel":
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return CancelMsg{} }}
	case ui.ActionSplitResize:
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	}
	return ui.EventResult{Action: &action}
}
func (s *WorkspaceSettingsSurface) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Workspace settings"}
}

type settingsSearchField struct {
	ui.Base
	field *ui.FilterField
	owner *WorkspaceSettingsSurface
}

func (s *settingsSearchField) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return s.field.Measure(ctx, cs)
}
func (s *settingsSearchField) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	s.field.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}
func (s *settingsSearchField) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	s.field.Paint(buf, ox+s.Bounds().X, oy+s.Bounds().Y, ctx)
}
func (s *settingsSearchField) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if s.owner == nil || s.owner.model == nil {
		return s.field.Handle(ctx, evt)
	}
	result := s.field.Handle(ctx, evt)
	if evt.Kind == ui.EventMouseClick && s.owner.form.CloseOpenSelects() {
		result.Invalidate |= ui.InvalidatePaint | ui.InvalidateLayout
	}
	s.owner.model.SetSearchActive(true)
	if evt.IsKey() {
		key := evt.Key
		switch key.Code {
		case tea.KeyEnter:
			s.owner.model.SetSearchQuery(s.field.Value())
			s.owner.model.EnterForm()
			s.owner.categories.syncItems()
			s.owner.refreshForm()
			s.owner.form.FocusFirst()
			s.owner.FocusForm()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
		case tea.KeyTab:
			if key.Mod.Contains(tea.ModShift) {
				s.owner.FocusScope()
				return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus}
			}
			s.owner.model.SetSearchQuery(s.field.Value())
			s.owner.categories.syncItems()
			s.owner.refreshForm()
			s.owner.FocusCategories()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
		}
		// Esc with a non-empty query is cleared by the FilterField itself;
		// Esc with an empty query is unhandled so the surface cancels.
	}
	s.owner.model.SetSearchQuery(s.field.Value())
	s.owner.categories.syncItems()
	s.owner.refreshForm()
	if result.Invalidate == 0 {
		result.Invalidate = ui.InvalidateLayout | ui.InvalidatePaint
	} else {
		result.Invalidate |= ui.InvalidateLayout | ui.InvalidatePaint
	}
	return result
}
func (s *settingsSearchField) Semantics() ui.Semantics       { return s.field.Semantics() }
func (s *settingsSearchField) SetTokens(tokens *ui.TokenSet) { s.field.SetTokens(tokens) }

type settingsCategoryList struct {
	ui.Base
	list  *ui.List
	owner *WorkspaceSettingsSurface
}

func (s *settingsCategoryList) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return s.list.Measure(ctx, cs)
}
func (s *settingsCategoryList) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	s.list.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}
func (s *settingsCategoryList) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	s.list.Paint(buf, ox+s.Bounds().X, oy+s.Bounds().Y, ctx)
}
func (s *settingsCategoryList) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	result := s.list.Handle(ctx, evt)
	if s.owner == nil || s.owner.model == nil {
		return result
	}
	if evt.Kind == ui.EventMouseClick && s.owner.form.CloseOpenSelects() {
		result.Invalidate |= ui.InvalidatePaint | ui.InvalidateLayout
	}
	if selected, ok := s.list.Selected(); ok {
		if index, ok := selected.Payload.(int); ok && index != s.owner.model.SelectedCategory() {
			s.owner.model.SelectCategory(index)
			s.owner.refreshForm()
			result.Invalidate |= ui.InvalidateLayout | ui.InvalidatePaint
		}
	}
	if evt.IsKey() {
		key := evt.Key
		switch key.Code {
		case tea.KeyTab:
			if key.Mod.Contains(tea.ModShift) {
				s.owner.FocusSearch()
				return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus}
			}
			s.owner.model.EnterForm()
			s.owner.refreshForm()
			s.owner.form.FocusFirst()
			s.owner.FocusForm()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
		case tea.KeyEnter, tea.KeyRight:
			s.owner.model.EnterForm()
			s.owner.refreshForm()
			s.owner.form.FocusFirst()
			s.owner.FocusForm()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
		}
	}
	return result
}
func (s *settingsCategoryList) Semantics() ui.Semantics       { return s.list.Semantics() }
func (s *settingsCategoryList) SetTokens(tokens *ui.TokenSet) { s.list.SetTokens(tokens) }
func (s *settingsCategoryList) syncItems() {
	if s.owner == nil || s.owner.model == nil {
		return
	}
	items := s.owner.model.CategoryItems()
	s.list.SetItems(items)
	for index, item := range items {
		if selected, ok := item.Payload.(int); ok && selected == s.owner.model.SelectedCategory() {
			s.list.SelectIndex(index)
			break
		}
	}
}

var _ ui.Component = (*WorkspaceSettingsSurface)(nil)
var _ ui.Parent = (*WorkspaceSettingsSurface)(nil)
var _ ui.ActionHandler = (*WorkspaceSettingsSurface)(nil)

type settingsScopeBar struct {
	ui.Base
	owner *WorkspaceSettingsSurface
	hover int
}

func (s *settingsScopeBar) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: 1})
}
func (s *settingsScopeBar) Layout(_ ui.LayoutContext, r ui.Rect) { s.SetBounds(r) }

func (s *settingsScopeBar) segmentWidths() (appW, wsW int) {
	r := s.Bounds()
	appW = rendering.VisibleWidth("Application") + 2
	wsW = rendering.VisibleWidth("Workspace") + 2
	if r.W > 0 && appW+wsW > r.W {
		appW = r.W / 2
		wsW = r.W - appW
	}
	return appW, wsW
}

func (s *settingsScopeBar) hitSegment(x int) int {
	appW, wsW := s.segmentWidths()
	if x < 0 {
		return -1
	}
	if x < appW {
		return 0
	}
	if x < appW+wsW {
		return 1
	}
	return -1
}

func (s *settingsScopeBar) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	r := s.Bounds()
	if r.Empty() || s.owner == nil || s.owner.tokens == nil {
		return
	}
	tokens := s.owner.tokens
	baseBG := tokens.Color(ui.ColorPopupBg)
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(baseBG))
	active := 0
	if s.owner.model != nil && s.owner.model.Scope() == types.SettingsScopeWorkspace {
		active = 1
	}
	appW, wsW := s.segmentWidths()
	s.paintSegment(buf, ox+r.X, oy+r.Y, appW, "Application", active == 0, s.hover == 0, ctx.Focused, tokens)
	s.paintSegment(buf, ox+r.X+appW, oy+r.Y, wsW, "Workspace", active == 1, s.hover == 1, ctx.Focused, tokens)
}

func (s *settingsScopeBar) paintSegment(buf *surface.CellBuffer, x, y, width int, label string, active, hovered, barFocused bool, tokens *ui.TokenSet) {
	if width < 1 {
		return
	}
	fg, bg := tokens.Color(ui.ColorMuted), tokens.Color(ui.ColorPopupBg)
	attr := cell.Attrs(0)
	switch {
	case active:
		fg = tokens.Color(ui.ColorAccent)
		bg = tokens.Color(ui.ColorPopupSubtleBg)
		attr = cell.AttrBold
		if barFocused {
			bg = tokens.Color(ui.ColorSelection)
		}
	case hovered:
		fg, bg = tokens.ListRowColors(ui.StateHovered)
	}
	ui.FillRect(buf, 0, 0, ui.Rect{X: x, Y: y, W: width, H: 1}, cell.BlankCell(bg))
	text := rendering.TruncateVisual(" "+label+" ", width, "")
	buf.WriteString(x, y, text, fg, bg, attr)
}

func (s *settingsScopeBar) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if s.owner == nil || s.owner.model == nil {
		return ui.EventResult{}
	}
	if evt.Kind == ui.EventMouseMotion {
		h := s.hitSegment(ctx.LocalX)
		if h != s.hover {
			s.hover = h
			return ui.EventResult{Handled: h >= 0, Invalidate: ui.InvalidatePaint}
		}
		return ui.EventResult{Handled: h >= 0}
	}
	if evt.Kind == ui.EventMouseClick && evt.MouseClick.Button == tea.MouseLeft {
		if seg := s.hitSegment(ctx.LocalX); seg >= 0 {
			return s.selectSegment(seg)
		}
		return ui.EventResult{}
	}
	if !evt.IsKey() {
		return ui.EventResult{}
	}
	key := evt.Key
	switch key.Code {
	case tea.KeyLeft:
		return s.selectSegment(0)
	case tea.KeyRight:
		return s.selectSegment(1)
	case tea.KeyTab:
		if key.Mod.Contains(tea.ModShift) {
			s.owner.FocusActions()
		} else {
			s.owner.FocusSearch()
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case tea.KeyUp:
		s.owner.FocusCategories()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	return ui.EventResult{}
}

func (s *settingsScopeBar) selectSegment(seg int) ui.EventResult {
	next := types.SettingsScopeApplication
	if seg == 1 {
		next = types.SettingsScopeWorkspace
	}
	if s.owner.model.Scope() == next {
		return ui.EventResult{Handled: true}
	}
	return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return ScopeMsg{Scope: next} }}
}

func (s *settingsScopeBar) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleButton, Label: "Settings scope"}
}
