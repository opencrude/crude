package statusoverview

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	ttypes "crude/types"
)

type ViewID string

const (
	ViewTabs          ViewID = "tabs"
	ViewWorkspace     ViewID = "workspace"
	ViewActivity      ViewID = "activity"
	ViewNotifications ViewID = "notifications"
)

const (
	SurfaceID        ui.ComponentID = "status-overview"
	ViewsID          ui.ComponentID = "status-overview/views"
	SearchID         ui.ComponentID = "status-overview/search"
	ListID           ui.ComponentID = "status-overview/list"
	DetailsID        ui.ComponentID = "status-overview/details"
	UtilityActionsID ui.ComponentID = "status-overview/actions/utility"
	PrimaryActionsID ui.ComponentID = "status-overview/actions/primary"
	ListScrollID     ui.ComponentID = "status-overview/list-scroll"
	DetailScrollID   ui.ComponentID = "status-overview/detail-scroll"
	DividerID        ui.ComponentID = "status-overview/divider"
)

type DetailLine struct {
	Label string
	Value string
	Role  ui.SemanticColor
	Blank bool
}

type UsageSection struct {
	ID    string
	Label string
	Lines []DetailLine
}

type Item struct {
	ID                 string
	Label              string
	Summary            string
	Subtitle           string
	Icon               string
	Header             bool
	Active             bool
	Owner              ttypes.ResourceRef
	Lines              []DetailLine
	Usage              []UsageSection
	PrimaryAction      string
	PrimaryLabel       string
	PrimaryEnabled     bool
	DisabledReason     string
	NotificationID     string
	NotificationUnread bool
	Actions            []ttypes.StatusAction
}

type Projection struct {
	Views  map[ViewID][]Item
	Active ViewID
}

type NavigateMsg struct {
	Item   Item
	Action string
}

type CopySummaryMsg struct{ Item Item }
type MoreMsg struct{ Item Item }
type ViewChangedMsg struct{ View ViewID }
type MarkReadMsg struct{ ID string }
type DismissMsg struct{ ID string }
type CloseMsg struct{}

type Surface struct {
	ui.Base
	tokens         *ui.TokenSet
	views          *ui.Tabs
	search         *searchControl
	list           *listControl
	details        *detailControl
	utilityActions *ui.ActionBar
	primaryActions *ui.ActionBar
	listScroll     *ui.ScrollbarControl
	detailScroll   *ui.ScrollbarControl
	split          *ui.SplitModel
	divider        *dividerControl
	focus          func(ui.ComponentID)
	projection     Projection
	activeView     ViewID
	query          string
	items          map[string]Item
	narrow         bool
	detailMode     bool
}

func New(th *theme.Theme, projection Projection) *Surface {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	s := &Surface{Base: ui.NewBase(SurfaceID), tokens: tokens, items: make(map[string]Item)}
	s.views = ui.NewTabs(ViewsID, tokens)
	s.views.SetItems([]ui.TabItem{
		{ID: string(ViewTabs), Label: "Tabs"},
		{ID: string(ViewWorkspace), Label: "Workspace"},
		{ID: string(ViewActivity), Label: "Activity"},
		{ID: string(ViewNotifications), Label: "Notifications"},
	}, string(ViewTabs))
	s.search = &searchControl{Base: ui.NewBase(SearchID), owner: s, field: ui.NewFilterField(SearchID, tokens)}
	s.search.field.SetPrompt(ui.SearchGlyphEnd(tokens))
	s.search.field.SetPromptPlacement(ui.PromptEnd)
	s.search.field.SetPlaceholder("Search status")
	s.search.field.SetSubmitAction("")
	s.search.field.SetBackgroundColor(tokens.Color(ui.ColorPopupSubtleBg))
	s.search.field.SetPromptColor(tokens.Color(ui.ColorAccent))
	s.list = &listControl{Base: ui.NewBase(ListID), owner: s, list: ui.NewList(ListID, tokens)}
	s.list.list.SetActivateAction("status.item.open")
	s.details = newDetailControl(s, tokens)
	s.listScroll = ui.NewScrollbarControl(ListScrollID, s.list.list.Scroll(), tokens)
	s.listScroll.SetSurfaceBackground(ui.ColorPopupBg)
	s.detailScroll = ui.NewScrollbarControl(DetailScrollID, s.details.scroll, tokens)
	s.detailScroll.SetSurfaceBackground(ui.ColorPopupBg)
	s.split = ui.NewSplitModel(ui.Horizontal)
	s.split.SetDividerSize(3)
	s.split.SetMinSizes(16, 24)
	innerDivider := ui.NewSplitDivider(DividerID, s.split)
	innerDivider.SetTokens(tokens)
	innerDivider.SetColorRole(ui.ColorPopupDivider)
	innerDivider.SetTransparent(true)
	s.divider = &dividerControl{Base: ui.NewBase(DividerID), owner: s, inner: innerDivider}
	s.utilityActions = ui.NewActionBar(UtilityActionsID, tokens)
	s.utilityActions.SetAlignEnd(false)
	s.utilityActions.SetExitActions("status.focus.primary", "status.focus.details")
	s.primaryActions = ui.NewActionBar(PrimaryActionsID, tokens)
	s.primaryActions.SetExitActions("status.focus.views", "status.focus.utility")
	s.SetProjection(projection)
	return s
}

func (s *Surface) Children() []ui.Component {
	children := []ui.Component{s.views}
	if !s.narrow || !s.detailMode {
		children = append(children, s.search, s.list)
		if s.listScroll.Needed() {
			children = append(children, s.listScroll)
		}
	}
	if !s.narrow || s.detailMode {
		children = append(children, s.details)
		if s.detailScroll.Needed() {
			children = append(children, s.detailScroll)
		}
	}
	children = append(children, s.utilityActions, s.primaryActions)
	if !s.narrow {
		children = append(children, s.divider)
	}
	return children
}

func (s *Surface) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	s.views.SetTokens(tokens)
	s.search.field.SetTokens(tokens)
	s.search.field.SetPrompt(ui.SearchGlyphEnd(tokens))
	s.search.field.SetBackgroundColor(tokens.Color(ui.ColorPopupSubtleBg))
	s.search.field.SetPromptColor(tokens.Color(ui.ColorAccent))
	s.list.list.SetTokens(tokens)
	s.listScroll.SetTokens(tokens)
	s.detailScroll.SetTokens(tokens)
	s.details.setTokens(tokens)
	s.divider.SetTokens(tokens)
	s.divider.SetColorRole(ui.ColorPopupDivider)
	s.utilityActions.SetTokens(tokens)
	s.primaryActions.SetTokens(tokens)
	s.rebuildActions()
}

func (s *Surface) SetFocusHandler(fn func(ui.ComponentID)) { s.focus = fn }
func (s *Surface) FocusSearch()                            { s.focusID(SearchID) }
func (s *Surface) FocusList()                              { s.focusID(ListID) }
func (s *Surface) FocusDetails()                           { s.focusID(DetailsID) }
func (s *Surface) FocusUtilityActions()                    { s.focusID(UtilityActionsID) }
func (s *Surface) FocusPrimaryActions()                    { s.focusID(PrimaryActionsID) }
func (s *Surface) focusID(id ui.ComponentID) {
	if s.focus != nil {
		s.focus(id)
	}
}

func (s *Surface) SetProjection(projection Projection) {
	s.projection = projection
	if projection.Active != "" {
		s.activeView = projection.Active
	}
	if s.activeView == "" {
		s.activeView = ViewTabs
	}
	s.views.SetItems([]ui.TabItem{
		{ID: string(ViewTabs), Label: "Tabs"},
		{ID: string(ViewWorkspace), Label: "Workspace"},
		{ID: string(ViewActivity), Label: "Activity"},
		{ID: string(ViewNotifications), Label: "Notifications"},
	}, string(s.activeView))
	s.syncItems(true)
}

func (s *Surface) ActiveView() ViewID { return s.activeView }

func (s *Surface) Select(id string) bool {
	for index, item := range s.list.list.Items() {
		if item.ID == id && s.list.list.SelectIndex(index) {
			s.details.syncSelected()
			s.rebuildActions()
			return true
		}
	}
	return false
}

func (s *Surface) Selected() (Item, bool) {
	row, ok := s.list.list.Selected()
	if !ok {
		return Item{}, false
	}
	item, ok := s.items[row.ID]
	return item, ok
}

func (s *Surface) syncItems(stable bool) {
	s.items = make(map[string]Item)
	rows := make([]ui.ListItem, 0)
	query := strings.ToLower(strings.TrimSpace(s.query))
	for _, item := range s.projection.Views[s.activeView] {
		if item.Header {
			if query == "" {
				rows = append(rows, ui.ListItem{ID: item.ID, Label: item.Label, Header: true})
			}
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(item.Label+" "+item.Summary), query) {
			continue
		}
		label := item.Label
		if item.Active {
			label = "• " + label
		}
		s.items[item.ID] = item
		rows = append(rows, ui.ListItem{ID: item.ID, Label: label, Detail: item.Summary, Icon: item.Icon})
	}
	if stable {
		s.list.list.SetItemsStable(rows)
	} else {
		s.list.list.SetItems(rows)
	}
	s.details.syncSelected()
	s.rebuildActions()
}

func (s *Surface) rebuildActions() {
	item, ok := s.Selected()
	utility := make([]ui.ActionBarButton, 0, 2)
	if ok && !s.narrow {
		utility = append(utility, ui.ActionBarButton{ID: "status-overview/copy", Label: "Copy summary", Action: "status.copy", Variant: ui.VariantSecondary, Enabled: true})
		if item.NotificationID != "" || len(item.Actions) > 0 {
			utility = append(utility, ui.ActionBarButton{ID: "status-overview/more", Label: "More", Action: "status.more", Variant: ui.VariantSecondary, Enabled: true})
		}
	}
	primary := make([]ui.ActionBarButton, 0, 2)
	if s.narrow && s.detailMode {
		primary = append(primary, ui.ActionBarButton{ID: "status-overview/back", Label: "Back", Action: "status.back", Variant: ui.VariantSecondary, Enabled: true})
	}
	if ok && s.narrow {
		primary = append(primary, ui.ActionBarButton{ID: "status-overview/more", Label: "More", Action: "status.more", Variant: ui.VariantSecondary, Enabled: true})
	}
	if ok && item.PrimaryLabel != "" {
		primary = append(primary, ui.ActionBarButton{ID: "status-overview/primary", Label: item.PrimaryLabel, Action: "status.primary", Variant: ui.VariantPrimary, Enabled: item.PrimaryEnabled})
	}
	primary = append(primary, ui.ActionBarButton{ID: "status-overview/close", Label: "Close", Action: "status.close", Variant: ui.VariantSecondary, Enabled: true})
	s.utilityActions.SetButtons(utility...)
	s.primaryActions.SetButtons(primary...)
	s.primaryActions.SetCancelAction("status.close")
}

func (s *Surface) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (s *Surface) SplitFirst() int                { return s.split.FirstSize() }
func (s *Surface) SetSplitFirst(width int)        { s.split.SetFirstSize(width) }
func (s *Surface) PaneBounds() (ui.Rect, ui.Rect) { return s.list.Bounds(), s.details.Bounds() }

func (s *Surface) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	narrow := r.W < 82
	if narrow != s.narrow {
		s.narrow = narrow
		s.rebuildActions()
	}
	// Content hugs the two-row tab strip and the action row sits flush on
	// the bottom edge; the rule separates it from the panes.
	topY := min(2, max(0, r.H-2))
	ruleY := max(topY+1, r.H-2)
	actionY := min(r.H-1, ruleY+1)
	innerX, innerW := 2, max(1, r.W-4)
	s.views.Layout(ctx, ui.Rect{X: innerX, Y: 0, W: innerW, H: min(2, r.H)})
	primarySize := s.primaryActions.Measure(ctx, ui.Constraints{MaxW: innerW, MaxH: 1})
	primaryW := min(innerW, primarySize.W)
	primaryX := innerX + innerW - primaryW
	s.primaryActions.Layout(ctx, ui.Rect{X: primaryX, Y: actionY, W: primaryW, H: 1})
	utilityW := max(0, primaryX-innerX-2)
	if s.narrow {
		utilityW = 0
	}
	s.utilityActions.Layout(ctx, ui.Rect{X: innerX, Y: actionY, W: utilityW, H: 1})
	contentH := max(1, ruleY-topY)
	if s.narrow {
		if s.detailMode {
			s.details.Layout(ctx, ui.Rect{X: innerX, Y: topY, W: innerW, H: contentH})
			s.layoutDetailScroll(ctx)
			s.search.Layout(ctx, ui.Rect{})
			s.list.Layout(ctx, ui.Rect{})
			s.listScroll.Layout(ctx, ui.Rect{})
		} else {
			s.search.Layout(ctx, ui.Rect{X: innerX, Y: topY, W: innerW, H: 1})
			s.list.Layout(ctx, ui.Rect{X: innerX, Y: topY + 2, W: innerW, H: max(1, contentH-2)})
			s.layoutListScroll(ctx)
			s.details.Layout(ctx, ui.Rect{})
			s.detailScroll.Layout(ctx, ui.Rect{})
		}
		s.divider.Layout(ctx, ui.Rect{})
		return
	}
	s.split.SetDividerSize(3)
	s.split.SetMinSizes(24, 48)
	if !s.split.HasFirstSize() {
		s.split.SetFirstSize(min(28, max(24, innerW/3)))
	}
	leftW, gap, rightW := s.split.Resolve(innerW)
	s.search.Layout(ctx, ui.Rect{X: innerX, Y: topY, W: leftW, H: 1})
	s.list.Layout(ctx, ui.Rect{X: innerX, Y: topY + 2, W: leftW, H: max(1, contentH-2)})
	s.details.Layout(ctx, ui.Rect{X: innerX + leftW + gap, Y: topY, W: rightW, H: contentH})
	s.divider.SetCurrentFirst(leftW)
	s.divider.Layout(ctx, ui.Rect{X: innerX + leftW, Y: topY, W: gap, H: contentH})
	s.layoutListScroll(ctx)
	s.layoutDetailScroll(ctx)
}

func (s *Surface) layoutListScroll(ctx ui.LayoutContext) {
	r := s.list.Bounds()
	if s.listScroll.Needed() && r.W > 2 {
		s.list.Layout(ctx, ui.Rect{X: r.X, Y: r.Y, W: r.W - 2, H: r.H})
		s.listScroll.Layout(ctx, ui.Rect{X: r.X + r.W - 2, Y: r.Y, W: 2, H: r.H})
	} else {
		s.listScroll.Layout(ctx, ui.Rect{})
	}
}

func (s *Surface) layoutDetailScroll(ctx ui.LayoutContext) {
	r := s.details.Bounds()
	if s.detailScroll.Needed() && r.W > 2 {
		s.details.Layout(ctx, ui.Rect{X: r.X, Y: r.Y, W: r.W - 2, H: r.H})
		s.detailScroll.Layout(ctx, ui.Rect{X: r.X + r.W - 2, Y: r.Y, W: 2, H: r.H})
	} else {
		s.detailScroll.Layout(ctx, ui.Rect{})
	}
}

func (s *Surface) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	r := s.Bounds()
	if r.Empty() {
		return
	}
	bg, divider := s.tokens.Color(ui.ColorPopupBg), s.tokens.Color(ui.ColorPopupDivider)
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	ruleY := max(0, r.H-2)
	for x := ox + r.X + 2; x < ox+r.X+r.W-2; x++ {
		buf.Set(x, oy+r.Y+ruleY, cell.NewCell(ui.FirstRune(s.tokens.Glyphs().BorderH, '-'), divider, bg, 0))
	}
	if !s.narrow || !s.detailMode {
		y := oy + r.Y + s.search.Bounds().Y + 1
		for x := ox + r.X + 2; x < ox+r.X+2+s.search.Bounds().W; x++ {
			buf.Set(x, y, cell.NewCell(ui.FirstRune(s.tokens.Glyphs().BorderH, '-'), divider, bg, 0))
		}
	}
}

func (s *Surface) Handle(_ ui.EventContext, evt ui.Event) ui.EventResult {
	if !evt.IsKey() {
		return ui.EventResult{}
	}
	switch evt.Key.Code {
	case tea.KeyEsc:
		if s.narrow && s.detailMode {
			s.detailMode = false
			s.rebuildActions()
			s.FocusList()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
		}
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return CloseMsg{} }}
	case tea.KeyTab:
		if evt.Key.Mod.Contains(tea.ModShift) {
			s.FocusPrimaryActions()
		} else if !s.narrow || !s.detailMode {
			s.FocusSearch()
		} else {
			s.FocusDetails()
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	return ui.EventResult{}
}

func (s *Surface) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case "tabs.activate":
		if id, ok := action.Payload.(string); ok {
			s.activeView, s.query = ViewID(id), ""
			if s.narrow {
				s.detailMode = false
			}
			s.search.field.SetValue("")
			s.syncItems(false)
			s.FocusSearch()
		}
		return ui.EventResult{Handled: true, Cmd: tea.Batch(func() tea.Msg { return ViewChangedMsg{View: s.activeView} }, s.selectedReadCmd()), Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
	case "tabs.enter":
		s.FocusSearch()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "status.item.open":
		if s.narrow {
			s.detailMode = true
			s.rebuildActions()
		}
		s.FocusDetails()
		return ui.EventResult{Handled: true, Cmd: s.selectedReadCmd(), Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
	case "status.primary":
		if item, ok := s.Selected(); ok && item.PrimaryEnabled {
			return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return NavigateMsg{Item: item, Action: item.PrimaryAction} }}
		}
		return ui.EventResult{Handled: true}
	case "status.copy":
		if item, ok := s.Selected(); ok {
			item = s.copyItem(item)
			return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return CopySummaryMsg{Item: item} }}
		}
	case "status.more":
		if item, ok := s.Selected(); ok {
			item = s.copyItem(item)
			return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return MoreMsg{Item: item} }}
		}
	case "status.notice.read":
		if item, ok := s.Selected(); ok && item.NotificationID != "" {
			return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return MarkReadMsg{ID: item.NotificationID} }}
		}
	case "status.notice.dismiss":
		if item, ok := s.Selected(); ok && item.NotificationID != "" {
			return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return DismissMsg{ID: item.NotificationID} }}
		}
	case "status.close":
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return CloseMsg{} }}
	case "status.back":
		if s.narrow && s.detailMode {
			s.detailMode = false
			s.rebuildActions()
			s.FocusList()
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
	case ui.ActionSplitResize:
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	case "status.focus.details":
		s.FocusDetails()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "status.focus.utility":
		if s.utilityActions.Bounds().W > 0 {
			s.FocusUtilityActions()
		} else {
			s.FocusDetails()
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "status.focus.primary":
		s.FocusPrimaryActions()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "status.focus.views":
		s.focusID(ViewsID)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	if strings.HasPrefix(action.ID, "status.action:") {
		index, err := strconv.Atoi(strings.TrimPrefix(action.ID, "status.action:"))
		if item, ok := s.Selected(); err == nil && ok && index >= 0 && index < len(item.Actions) && item.Actions[index].Enabled {
			selectedAction := item.Actions[index].ID
			return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return NavigateMsg{Item: item, Action: selectedAction} }}
		}
		return ui.EventResult{Handled: true}
	}
	return ui.EventResult{Action: &action}
}

func (s *Surface) copyItem(item Item) Item {
	if item.Subtitle != "" {
		item.Lines = append([]DetailLine{{Value: item.Subtitle, Role: ui.ColorMuted}, {Blank: true}}, item.Lines...)
	}
	if len(item.Usage) == 0 {
		return item
	}
	selected := s.details.scopeByItem[item.ID]
	if selected == "" {
		selected = item.Usage[0].ID
	}
	for _, section := range item.Usage {
		if section.ID == selected {
			item.Lines = append(item.Lines, DetailLine{Blank: true}, DetailLine{Label: "Usage scope", Value: section.Label, Role: ui.ColorFg})
			item.Lines = append(item.Lines, section.Lines...)
			break
		}
	}
	return item
}

func (s *Surface) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Status overview"}
}

type searchControl struct {
	ui.Base
	field *ui.FilterField
	owner *Surface
}

func (s *searchControl) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return s.field.Measure(ctx, cs)
}
func (s *searchControl) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	s.field.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}
func (s *searchControl) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	s.field.Paint(buf, ox+s.Bounds().X, oy+s.Bounds().Y, ctx)
}
func (s *searchControl) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	result := s.field.Handle(ctx, evt)
	if evt.IsKey() && evt.Key.Code == tea.KeyTab {
		s.owner.FocusList()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	s.owner.query = s.field.Value()
	s.owner.syncItems(true)
	result.Invalidate |= ui.InvalidateLayout | ui.InvalidatePaint
	return result
}
func (s *searchControl) Semantics() ui.Semantics { return s.field.Semantics() }

type listControl struct {
	ui.Base
	list  *ui.List
	owner *Surface
}

func (l *listControl) Bounds() ui.Rect { return l.Base.Bounds() }
func (l *listControl) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return l.list.Measure(ctx, cs)
}
func (l *listControl) Layout(ctx ui.LayoutContext, r ui.Rect) {
	l.SetBounds(r)
	l.list.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}
func (l *listControl) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	l.list.Paint(buf, ox+l.Bounds().X, oy+l.Bounds().Y, ctx)
}
func (l *listControl) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	before := l.list.SelectedIndex()
	result := l.list.Handle(ctx, evt)
	if before != l.list.SelectedIndex() {
		l.owner.details.syncSelected()
		l.owner.rebuildActions()
		result.Invalidate |= ui.InvalidateLayout | ui.InvalidatePaint
		if readCmd := l.owner.selectedReadCmd(); readCmd != nil {
			result.Cmd = tea.Batch(result.Cmd, readCmd)
		}
	}
	if evt.IsKey() && evt.Key.Code == tea.KeyTab {
		if evt.Key.Mod.Contains(tea.ModShift) {
			l.owner.FocusSearch()
		} else if !l.owner.narrow {
			l.owner.focusID(DividerID)
		} else {
			l.owner.FocusDetails()
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	return result
}

func (s *Surface) selectedReadCmd() tea.Cmd {
	if s.narrow && !s.detailMode {
		return nil
	}
	item, ok := s.Selected()
	if !ok || item.NotificationID == "" || !item.NotificationUnread {
		return nil
	}
	id := item.NotificationID
	return func() tea.Msg { return MarkReadMsg{ID: id} }
}

func (s *Surface) DisplayedUnreadID() string {
	if s.narrow && !s.detailMode {
		return ""
	}
	item, ok := s.Selected()
	if !ok || !item.NotificationUnread {
		return ""
	}
	return item.NotificationID
}
func (l *listControl) Semantics() ui.Semantics { return l.list.Semantics() }

type detailControl struct {
	ui.Base
	owner       *Surface
	scroll      *ui.ScrollModel
	rawLines    []DetailLine
	lines       []DetailLine
	scopeModel  *ui.FormModel
	scopeState  *ui.FieldState
	scopeField  *scopeCycleControl
	scopeByItem map[string]string
	scopeRow    int
	rawScopeRow int
	scopeShown  bool
}

func newDetailControl(owner *Surface, tokens *ui.TokenSet) *detailControl {
	d := &detailControl{Base: ui.NewBase(DetailsID), owner: owner, scroll: ui.NewScrollModel(), scopeByItem: make(map[string]string), scopeRow: -1, rawScopeRow: -1}
	d.scopeModel = ui.NewFormModel()
	d.scopeState = ui.CycleFieldState("usage-scope", "Usage scope", []string{"agent"}, "agent", "")
	d.scopeModel.AddField(d.scopeState)
	d.scopeModel.OnChanged(func(string) {
		if item, ok := owner.Selected(); ok {
			d.scopeByItem[item.ID] = d.scopeState.Value.String()
			d.syncSelected()
		}
	})
	field := ui.NewBoundFormField(ui.NewID(DetailsID, "usage-scope-field"), d.scopeModel, d.scopeState, tokens)
	field.SetCompact(true)
	field.SetLabelWidth(14)
	field.SetSurfaceBackground(ui.ColorPopupBg)
	d.scopeField = &scopeCycleControl{Base: ui.NewBase(ui.NewID(DetailsID, "usage-scope")), owner: d, field: field}
	return d
}

func (d *detailControl) Children() []ui.Component {
	if d.scopeShown {
		return []ui.Component{d.scopeField}
	}
	return nil
}

func (d *detailControl) setTokens(tokens *ui.TokenSet) { d.scopeField.SetTokens(tokens) }

func (d *detailControl) syncSelected() {
	d.rawLines = nil
	d.scopeShown, d.scopeRow, d.rawScopeRow = false, -1, -1
	if item, ok := d.owner.Selected(); ok {
		heading := item.Label
		if item.Summary != "" {
			heading += "  " + item.Summary
		}
		d.rawLines = append(d.rawLines, DetailLine{Value: heading, Role: ui.ColorFg})
		if item.Subtitle != "" {
			d.rawLines = append(d.rawLines, DetailLine{Value: item.Subtitle, Role: ui.ColorMuted})
		}
		d.rawLines = append(d.rawLines, DetailLine{Blank: true})
		d.rawLines = append(d.rawLines, item.Lines...)
		if len(item.Usage) > 0 {
			d.rawLines = append(d.rawLines, DetailLine{Blank: true})
			selected := d.scopeByItem[item.ID]
			if selected == "" {
				selected = item.Usage[0].ID
			}
			if len(item.Usage) > 1 {
				d.scopeShown, d.rawScopeRow = true, len(d.rawLines)
				values := make([]string, 0, len(item.Usage))
				d.scopeState.Options = d.scopeState.Options[:0]
				for _, section := range item.Usage {
					values = append(values, section.ID)
					d.scopeState.Options = append(d.scopeState.Options, ui.FormOption{ID: section.ID, Label: section.Label})
				}
				if !contains(values, selected) {
					selected = item.Usage[0].ID
				}
				d.scopeState.Value, d.scopeState.Initial = ui.StringValue(selected), ui.StringValue(selected)
				d.rawLines = append(d.rawLines, DetailLine{Blank: true})
			} else {
				d.rawLines = append(d.rawLines, DetailLine{Value: "Usage", Role: ui.ColorAccent})
			}
			for _, section := range item.Usage {
				if section.ID == selected {
					d.rawLines = append(d.rawLines, section.Lines...)
					break
				}
			}
		}
		if !item.PrimaryEnabled && item.DisabledReason != "" {
			d.rawLines = append(d.rawLines, DetailLine{Blank: true}, DetailLine{Label: "Unavailable", Value: item.DisabledReason, Role: ui.ColorWarning})
		}
	}
	d.reflow(max(1, d.Bounds().W))
}
func (d *detailControl) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}
func (d *detailControl) Layout(_ ui.LayoutContext, r ui.Rect) {
	d.SetBounds(r)
	d.reflow(max(1, r.W))
	d.scroll.SetViewport(r.H)
	if d.scopeShown {
		y := d.scopeRow - d.scroll.Offset()
		if y >= 0 && y < r.H {
			d.scopeField.Layout(ui.LayoutContext{}, ui.Rect{X: 0, Y: y, W: r.W, H: 1})
		} else {
			d.scopeField.Layout(ui.LayoutContext{}, ui.Rect{})
		}
	} else {
		d.scopeField.Layout(ui.LayoutContext{}, ui.Rect{})
	}
}

func (d *detailControl) reflow(width int) {
	d.lines = d.lines[:0]
	d.scopeRow = -1
	labelW := d.labelWidth(width)
	for index, line := range d.rawLines {
		if index == d.rawScopeRow {
			d.scopeRow = len(d.lines)
			d.lines = append(d.lines, DetailLine{Blank: true})
			continue
		}
		if line.Blank || line.Value == "" {
			d.lines = append(d.lines, line)
			continue
		}
		valueWidth := width
		if line.Label != "" {
			valueWidth = max(1, width-labelW)
		}
		wrapped := rendering.HardWrap(line.Value, valueWidth)
		if len(wrapped) == 0 {
			wrapped = []string{""}
		}
		for part, value := range wrapped {
			visual := DetailLine{Value: value, Role: line.Role}
			if part == 0 {
				visual.Label = line.Label
			} else if line.Label != "" {
				visual.Value = strings.Repeat(" ", labelW) + value
			}
			d.lines = append(d.lines, visual)
		}
	}
	d.scroll.SetContent(len(d.lines))
}
func (d *detailControl) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	r := d.Bounds()
	bg := d.owner.tokens.Color(ui.ColorPopupBg)
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	start := d.scroll.Offset()
	labelW := d.labelWidth(r.W)
	for row := 0; row < r.H && start+row < len(d.lines); row++ {
		line := d.lines[start+row]
		if line.Blank || start+row == d.scopeRow {
			continue
		}
		x, y := ox+r.X, oy+r.Y+row
		role := line.Role
		if role == 0 {
			role = ui.ColorFg
		}
		if line.Label != "" {
			buf.WriteString(x, y, padVisual(rendering.TruncateVisual(line.Label, labelW-1, "…"), labelW), d.owner.tokens.Color(ui.ColorMuted), bg, 0)
			x += labelW
		}
		buf.WriteString(x, y, rendering.TruncateVisual(line.Value, max(0, r.W-(x-(ox+r.X))), "…"), d.owner.tokens.Color(role), bg, 0)
	}
}

func (d *detailControl) labelWidth(width int) int {
	longest := 0
	for _, line := range d.rawLines {
		longest = max(longest, rendering.VisibleWidth(line.Label))
	}
	return min(max(10, longest+2), min(16, max(10, width/2)))
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
func (d *detailControl) Handle(_ ui.EventContext, evt ui.Event) ui.EventResult {
	if !evt.IsKey() {
		return ui.EventResult{}
	}
	switch evt.Key.Code {
	case tea.KeyUp:
		d.scroll.ScrollBy(-1)
	case tea.KeyDown:
		d.scroll.ScrollBy(1)
	case tea.KeyPgUp:
		d.scroll.ScrollBy(-max(1, d.scroll.Viewport()))
	case tea.KeyPgDown:
		d.scroll.ScrollBy(max(1, d.scroll.Viewport()))
	case tea.KeyLeft, tea.KeyEsc:
		if d.owner.narrow {
			d.owner.detailMode = false
		}
		d.owner.FocusList()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidateFocus | ui.InvalidatePaint}
	case tea.KeyTab:
		if evt.Key.Mod.Contains(tea.ModShift) {
			if d.owner.narrow {
				d.owner.FocusList()
			} else {
				d.owner.focusID(DividerID)
			}
		} else if d.scopeShown {
			d.owner.focusID(d.scopeField.ID())
		} else if d.owner.utilityActions.Bounds().W > 0 {
			d.owner.FocusUtilityActions()
		} else {
			d.owner.FocusPrimaryActions()
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	default:
		return ui.EventResult{}
	}
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
}

type scopeCycleControl struct {
	ui.Base
	owner *detailControl
	field *ui.FormField
}

func (c *scopeCycleControl) SetTokens(tokens *ui.TokenSet) { c.field.SetTokens(tokens) }
func (c *scopeCycleControl) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return c.field.Measure(ctx, cs)
}
func (c *scopeCycleControl) Layout(ctx ui.LayoutContext, r ui.Rect) {
	c.SetBounds(r)
	c.field.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}
func (c *scopeCycleControl) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	c.field.Paint(buf, ox+c.Bounds().X, oy+c.Bounds().Y, ctx)
}
func (c *scopeCycleControl) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() && evt.Key.Code == tea.KeyTab {
		if evt.Key.Mod.Contains(tea.ModShift) {
			c.owner.owner.FocusDetails()
		} else if c.owner.owner.utilityActions.Bounds().W > 0 {
			c.owner.owner.FocusUtilityActions()
		} else {
			c.owner.owner.FocusPrimaryActions()
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	return c.field.Handle(ctx, evt)
}
func (c *scopeCycleControl) Semantics() ui.Semantics { return c.field.Semantics() }

type dividerControl struct {
	ui.Base
	owner *Surface
	inner *ui.SplitDivider
}

func (d *dividerControl) SetTokens(tokens *ui.TokenSet)      { d.inner.SetTokens(tokens) }
func (d *dividerControl) SetColorRole(role ui.SemanticColor) { d.inner.SetColorRole(role) }
func (d *dividerControl) SetCurrentFirst(width int)          { d.inner.SetCurrentFirst(width) }
func (d *dividerControl) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return d.inner.Measure(ctx, cs)
}
func (d *dividerControl) Layout(ctx ui.LayoutContext, r ui.Rect) {
	d.SetBounds(r)
	d.inner.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}
func (d *dividerControl) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	d.inner.Paint(buf, ox+d.Bounds().X, oy+d.Bounds().Y, ctx)
}
func (d *dividerControl) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() && evt.Key.Code == tea.KeyTab {
		if evt.Key.Mod.Contains(tea.ModShift) {
			d.owner.FocusList()
		} else {
			d.owner.FocusDetails()
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	return d.inner.Handle(ctx, evt)
}
func (d *dividerControl) Semantics() ui.Semantics { return d.inner.Semantics() }
func (d *detailControl) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Status details"}
}

func padVisual(value string, width int) string {
	valueWidth := rendering.VisibleWidth(value)
	if valueWidth >= width {
		return value
	}
	return value + strings.Repeat(" ", width-valueWidth)
}
