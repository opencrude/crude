package settings

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/widget"
)

const (
	WorkspaceSettingsSurfaceID ui.ComponentID = "workspace-settings"
	WorkspaceSettingsSearchID  ui.ComponentID = "workspace-settings/search"
	WorkspaceSettingsListID    ui.ComponentID = "workspace-settings/categories"
	WorkspaceSettingsFormID    ui.ComponentID = "workspace-settings/form"
	WorkspaceSettingsSaveID    ui.ComponentID = "workspace-settings/save"
	WorkspaceSettingsCancelID  ui.ComponentID = "workspace-settings/cancel"
)

// WorkspaceSettingsSurface composes the native settings shell around the
// existing form body. The form body remains a deliberate Phase 8 bridge; the
// search, category collection, and action bar are native retained controls.
type WorkspaceSettingsSurface struct {
	ui.Base
	model  *Model
	tokens *ui.TokenSet

	search     *settingsSearchField
	categories *settingsCategoryList
	form       *ui.LegacyWidget
	save       *settingsButton
	cancel     *settingsButton

	focus func(ui.ComponentID)
}

func NewSurface(th *theme.Theme, model *Model) *WorkspaceSettingsSurface {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	s := &WorkspaceSettingsSurface{
		Base:   ui.NewBase(WorkspaceSettingsSurfaceID),
		model:  model,
		tokens: tokens,
	}
	s.search = &settingsSearchField{
		Base:  ui.NewBase(WorkspaceSettingsSearchID),
		field: ui.NewTextField(WorkspaceSettingsSearchID, tokens),
		owner: s,
	}
	s.search.field.SetPrompt("> ")
	s.search.field.SetPlaceholder("Search settings")
	s.search.field.SetSubmitAction("")
	s.categories = &settingsCategoryList{
		Base:  ui.NewBase(WorkspaceSettingsListID),
		list:  ui.NewList(WorkspaceSettingsListID, tokens),
		owner: s,
	}
	s.categories.list.SetActivateAction("settings.category.open")
	s.form = ui.NewLegacyWidget(WorkspaceSettingsFormID, &settingsFormBody{model: model})
	s.save = newSettingsButton(s, WorkspaceSettingsSaveID, "Save", func() tea.Msg {
		return SaveMsg{AppSettings: s.model.AppSettings()}
	})
	s.cancel = newSettingsButton(s, WorkspaceSettingsCancelID, "Cancel", func() tea.Msg {
		return CancelMsg{}
	})
	s.syncModel()
	return s
}

func (s *WorkspaceSettingsSurface) Children() []ui.Component {
	return []ui.Component{s.search, s.categories, s.form, s.save, s.cancel}
}

func (s *WorkspaceSettingsSurface) SetFocusHandler(fn func(ui.ComponentID)) {
	s.focus = fn
}

func (s *WorkspaceSettingsSurface) FocusSearch() { s.focusID(WorkspaceSettingsSearchID) }
func (s *WorkspaceSettingsSurface) FocusCategories() {
	s.focusID(WorkspaceSettingsListID)
}
func (s *WorkspaceSettingsSurface) FocusForm() { s.focusID(WorkspaceSettingsFormID) }

func (s *WorkspaceSettingsSurface) focusID(id ui.ComponentID) {
	if s.focus != nil {
		s.focus(id)
	}
}

func (s *WorkspaceSettingsSurface) SetModel(model *Model) {
	s.model = model
	s.search.owner = s
	s.categories.owner = s
	if body, ok := s.form.Inner().(*settingsFormBody); ok {
		body.model = model
	}
	s.syncModel()
}

func (s *WorkspaceSettingsSurface) syncModel() {
	if s.model == nil {
		return
	}
	s.search.field.SetValue(s.model.searchText())
	s.categories.syncItems()
}

func (s *WorkspaceSettingsSurface) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (s *WorkspaceSettingsSurface) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	leftW := 21
	if s.model != nil {
		for _, category := range s.model.categories {
			if w := rendering.VisibleWidth(category.Name) + 4; w > leftW {
				leftW = w
			}
		}
	}
	if maxLeft := r.W / 3; maxLeft > 0 && leftW > maxLeft {
		leftW = maxLeft
	}
	if leftW < 12 {
		leftW = 12
	}

	formX := leftW + 3
	formW := r.W - formX - 1
	if formW < 1 {
		formW = 1
	}
	formH := r.H - 2
	if formH < 1 {
		formH = 1
	}
	listH := r.H - 2
	if listH < 1 {
		listH = 1
	}

	s.search.Layout(ctx, ui.Rect{X: 0, Y: 0, W: leftW, H: 1})
	s.categories.Layout(ctx, ui.Rect{X: 0, Y: 2, W: leftW, H: listH})
	s.form.Layout(ctx, ui.Rect{X: formX, Y: 0, W: formW, H: formH})
	s.save.Layout(ctx, ui.Rect{X: formX, Y: r.H - 1, W: 10, H: 1})
	s.cancel.Layout(ctx, ui.Rect{X: formX + 12, Y: r.H - 1, W: 12, H: 1})

	if s.model != nil {
		s.model.SetSurfaceLayout(settingsLayout{
			height: r.H, formX: formX, formY: 0,
		})
	}
}

func (s *WorkspaceSettingsSurface) Paint(_ *surface.CellBuffer, _, _ int, _ ui.PaintContext) {}

func (s *WorkspaceSettingsSurface) Handle(_ ui.EventContext, _ tea.Msg) ui.EventResult {
	return ui.EventResult{}
}

func (s *WorkspaceSettingsSurface) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Workspace settings"}
}

type settingsFormBody struct {
	model *Model
}

func (f *settingsFormBody) Measure(availW, availH int) (int, int) { return availW, availH }

func (f *settingsFormBody) Render(surf *surface.Surface, ctx *widget.RenderContext) {
	if f == nil || f.model == nil || surf == nil {
		return
	}
	spin := 0
	if ctx != nil {
		spin = ctx.SpinFrame
	}
	rendering.RenderToSurface(surf, f.model.FormView(surf.Width(), spin))
}

type settingsSearchField struct {
	ui.Base
	field *ui.TextField
	owner *WorkspaceSettingsSurface
}

func (s *settingsSearchField) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return s.field.Measure(ctx, cs)
}

func (s *settingsSearchField) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	s.field.Layout(ctx, r)
}

func (s *settingsSearchField) Paint(buf *surface.CellBuffer, originX, originY int, ctx ui.PaintContext) {
	s.field.Paint(buf, originX, originY, ctx)
}

func (s *settingsSearchField) Handle(ctx ui.EventContext, msg tea.Msg) ui.EventResult {
	if s.owner == nil || s.owner.model == nil {
		return s.field.Handle(ctx, msg)
	}
	result := s.field.Handle(ctx, msg)
	s.owner.model.SetSearchActive(true)

	switch key := msg.(type) {
	case tea.KeyPressMsg:
		switch key.Code {
		case tea.KeyEnter:
			s.owner.model.SetSearchQuery(s.field.Value())
			s.owner.model.EnterForm()
			s.owner.categories.syncItems()
			s.owner.FocusForm()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus}
		case tea.KeyTab:
			if !key.Mod.Contains(tea.ModShift) {
				s.owner.model.SetSearchQuery(s.field.Value())
				s.owner.model.EnterForm()
				s.owner.categories.syncItems()
				s.owner.FocusForm()
				return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus}
			}
		case tea.KeyEsc:
			if s.field.Edit().Len() > 0 {
				s.field.SetValue("")
				s.owner.model.SetSearchQuery("")
				s.owner.categories.syncItems()
				return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
			}
		}
	case tea.MouseClickMsg, tea.PasteMsg:
		// The value is synchronized below after the native edit handles the event.
	}
	s.owner.model.SetSearchQuery(s.field.Value())
	s.owner.categories.syncItems()
	return result
}

func (s *settingsSearchField) Semantics() ui.Semantics { return s.field.Semantics() }

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
	s.list.Layout(ctx, r)
}

func (s *settingsCategoryList) Paint(buf *surface.CellBuffer, originX, originY int, ctx ui.PaintContext) {
	s.list.Paint(buf, originX, originY, ctx)
}

func (s *settingsCategoryList) Handle(ctx ui.EventContext, msg tea.Msg) ui.EventResult {
	result := s.list.Handle(ctx, msg)
	if s.owner == nil || s.owner.model == nil {
		return result
	}
	if selected, ok := s.list.Selected(); ok {
		if index, ok := selected.Payload.(int); ok {
			s.owner.model.SelectCategory(index)
		}
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
		if (key.Code == tea.KeyEnter || key.Code == tea.KeyTab || key.Code == tea.KeyRight) && !key.Mod.Contains(tea.ModShift) {
			s.owner.model.EnterForm()
			s.owner.FocusForm()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus}
		}
	}
	return result
}

func (s *settingsCategoryList) Semantics() ui.Semantics { return s.list.Semantics() }

func (s *settingsCategoryList) syncItems() {
	if s.owner == nil || s.owner.model == nil {
		return
	}
	items := s.owner.model.CategoryItems()
	s.list.SetItems(items)
	selected := s.owner.model.SelectedCategory()
	for i, item := range items {
		if index, ok := item.Payload.(int); ok && index == selected {
			s.list.SelectIndex(i)
			break
		}
	}
}

type settingsButton struct {
	ui.Base
	button *ui.Button
	msg    func() tea.Msg
}

func newSettingsButton(owner *WorkspaceSettingsSurface, id ui.ComponentID, label string, msg func() tea.Msg) *settingsButton {
	button := ui.NewButton(id, label, string(id)+".activate")
	button.SetTokens(owner.tokens)
	return &settingsButton{Base: ui.NewBase(id), button: button, msg: msg}
}

func (s *settingsButton) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return s.button.Measure(ctx, cs)
}

func (s *settingsButton) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	s.button.Layout(ctx, r)
}

func (s *settingsButton) Paint(buf *surface.CellBuffer, originX, originY int, ctx ui.PaintContext) {
	s.button.Paint(buf, originX, originY, ctx)
}

func (s *settingsButton) Handle(ctx ui.EventContext, msg tea.Msg) ui.EventResult {
	result := s.button.Handle(ctx, msg)
	if result.Action == nil || s.msg == nil {
		return result
	}
	result.Action = nil
	result.Cmd = s.msg
	return result
}

func (s *settingsButton) Semantics() ui.Semantics { return s.button.Semantics() }

var _ ui.Component = (*WorkspaceSettingsSurface)(nil)
var _ ui.Parent = (*WorkspaceSettingsSurface)(nil)
var _ widget.Widget = (*settingsFormBody)(nil)
