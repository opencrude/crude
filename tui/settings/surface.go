package settings

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const (
	WorkspaceSettingsSurfaceID ui.ComponentID = "workspace-settings"
	WorkspaceSettingsSearchID  ui.ComponentID = "workspace-settings/search"
	WorkspaceSettingsListID    ui.ComponentID = "workspace-settings/categories"
	WorkspaceSettingsFormID    ui.ComponentID = "workspace-settings/form"
	WorkspaceSettingsActionID  ui.ComponentID = "workspace-settings/actions"
	WorkspaceSettingsSaveID    ui.ComponentID = "workspace-settings/save"
	WorkspaceSettingsCancelID  ui.ComponentID = "workspace-settings/cancel"
)

// WorkspaceSettingsSurface composes native category navigation, the shared
// FormModel/Form controls, and the shared ActionBar without a legacy bridge.
type WorkspaceSettingsSurface struct {
	ui.Base
	model      *Model
	tokens     *ui.TokenSet
	search     *settingsSearchField
	categories *settingsCategoryList
	form       *ui.Form
	actions    *ui.ActionBar
	focus      func(ui.ComponentID)
}

func NewSurface(th *theme.Theme, model *Model) *WorkspaceSettingsSurface {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	s := &WorkspaceSettingsSurface{Base: ui.NewBase(WorkspaceSettingsSurfaceID), model: model, tokens: tokens}
	s.search = &settingsSearchField{Base: ui.NewBase(WorkspaceSettingsSearchID), field: ui.NewFilterField(WorkspaceSettingsSearchID, tokens), owner: s}
	s.search.field.SetPrompt("> ")
	s.search.field.SetPlaceholder("Search settings")
	s.search.field.SetSubmitAction("")
	s.search.field.SetBackgroundColor(tokens.Color(ui.ColorPopupSubtleBg))
	s.search.field.SetPromptColor(tokens.Color(ui.ColorAccent))
	s.categories = &settingsCategoryList{Base: ui.NewBase(WorkspaceSettingsListID), list: ui.NewList(WorkspaceSettingsListID, tokens), owner: s}
	s.categories.list.SetActivateAction("settings.category.open")
	s.form = ui.NewForm(WorkspaceSettingsFormID, nil, "", "", tokens)
	s.actions = ui.NewActionBar(WorkspaceSettingsActionID, tokens,
		ui.ActionBarButton{ID: WorkspaceSettingsSaveID, Label: "Save", Action: "settings.save", Variant: ui.VariantPrimary, Enabled: true},
		ui.ActionBarButton{ID: WorkspaceSettingsCancelID, Label: "Cancel", Action: "settings.cancel", Variant: ui.VariantSecondary, Enabled: true},
	)
	s.actions.SetCancelAction("settings.cancel")
	s.syncModel()
	return s
}

func (s *WorkspaceSettingsSurface) Children() []ui.Component {
	return []ui.Component{s.search, s.categories, s.form, s.actions}
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
	if s.actions != nil {
		s.actions.SetTokens(tokens)
	}
}
func (s *WorkspaceSettingsSurface) SetFocusHandler(fn func(ui.ComponentID)) { s.focus = fn }
func (s *WorkspaceSettingsSurface) FocusSearch()                            { s.focusID(WorkspaceSettingsSearchID) }
func (s *WorkspaceSettingsSurface) FocusCategories()                        { s.focusID(WorkspaceSettingsListID) }
func (s *WorkspaceSettingsSurface) FocusForm()                              { s.focusID(WorkspaceSettingsFormID) }
func (s *WorkspaceSettingsSurface) FocusActions()                           { s.focusID(WorkspaceSettingsActionID) }
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
	s.form.SetForm(category.Form, category.Name, category.Description)
}

func (s *WorkspaceSettingsSurface) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}
func (s *WorkspaceSettingsSurface) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	leftW := s.leftWidth(r.W)
	formX, formW := leftW+3, max(1, r.W-leftW-4)
	s.search.Layout(ctx, ui.Rect{X: 0, Y: 0, W: leftW, H: 1})
	s.categories.Layout(ctx, ui.Rect{X: 0, Y: 2, W: leftW, H: max(1, r.H-2)})
	s.form.Layout(ctx, ui.Rect{X: formX, Y: 0, W: formW, H: max(1, r.H-2)})
	s.actions.Layout(ctx, ui.Rect{X: formX, Y: max(0, r.H-1), W: formW, H: 1})
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
	leftW := s.leftWidth(r.W)
	dividerX := ox + leftW + 1
	border := ui.FirstRune(ui.DefaultGlyphsFromTokens(s.tokens).BorderV, '|')
	for y := 0; y < r.H; y++ {
		buf.Set(dividerX, oy+y, cell.NewCell(border, divider, bg, 0))
	}
	buf.WriteString(ox, oy+1, "SECTIONS", muted, bg, cell.AttrBold)
	actionY := oy + r.H - 2
	formX := ox + leftW + 3
	for x := formX; x < ox+r.W; x++ {
		buf.Set(x, actionY, cell.NewCell(ui.FirstRune(ui.DefaultGlyphsFromTokens(s.tokens).BorderH, '-'), divider, bg, 0))
	}
}

// Handle receives keys bubbled from focused children (search TextField leaves
// Up/Down/Esc unhandled).
//
// Region Tab order (documented + tested): search → categories → form →
// actions → wrap. Shift+Tab reverses. Form Tab first walks fields, then
// emits form.exit.forward/backward to leave the form region.
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
			s.FocusActions()
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
		return ui.EventResult{Handled: true, Cmd: s.model.OpenSelect(fieldID)}
	case "form.exit.backward":
		s.FocusCategories()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "form.exit.forward":
		s.FocusActions()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "actionbar.exit.backward":
		s.form.FocusLast()
		s.FocusForm()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "actionbar.exit.forward":
		s.FocusSearch()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "settings.save":
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return SaveMsg{AppSettings: s.model.AppSettings()} }}
	case "settings.cancel":
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return CancelMsg{} }}
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
			if !key.Mod.Contains(tea.ModShift) {
				s.owner.model.SetSearchQuery(s.field.Value())
				s.owner.categories.syncItems()
				s.owner.refreshForm()
				s.owner.FocusCategories()
				return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
			}
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
