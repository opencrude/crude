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
	UtilityActionsID ui.ComponentID = "status-overview/actions/utility"
	PrimaryActionsID ui.ComponentID = "status-overview/actions/primary"
	ListScrollID     ui.ComponentID = "status-overview/list-scroll"
)

const expandedDetailIndent = 4

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

type statusRow struct {
	ItemID       string
	Detail       bool
	Line         DetailLine
	Continuation bool
}

type Surface struct {
	ui.Base
	tokens         *ui.TokenSet
	views          *ui.Tabs
	search         *searchControl
	list           *listControl
	utilityActions *ui.ActionBar
	primaryActions *ui.ActionBar
	listScroll     *ui.ScrollbarControl
	focus          func(ui.ComponentID)
	projection     Projection
	activeView     ViewID
	query          string
	items          map[string]Item
	expanded       map[string]bool
	listWidth      int
}

func New(th *theme.Theme, projection Projection) *Surface {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	s := &Surface{
		Base:     ui.NewBase(SurfaceID),
		tokens:   tokens,
		items:    make(map[string]Item),
		expanded: make(map[string]bool),
	}
	s.views = ui.NewTabs(ViewsID, tokens)
	s.views.SetItems(viewItems(), string(ViewTabs))
	s.search = &searchControl{Base: ui.NewBase(SearchID), owner: s, field: ui.NewFilterField(SearchID, tokens)}
	s.search.field.SetPrompt(ui.SearchGlyphEnd(tokens))
	s.search.field.SetPromptPlacement(ui.PromptEnd)
	s.search.field.SetPlaceholder("Search status")
	s.search.field.SetSubmitAction("")
	s.search.field.SetBackgroundColor(tokens.Color(ui.ColorPopupSubtleBg))
	s.search.field.SetPromptColor(tokens.Color(ui.ColorAccent))
	s.list = &listControl{Base: ui.NewBase(ListID), owner: s, list: ui.NewList(ListID, tokens)}
	s.list.list.SetActivateAction("status.item.toggle")
	s.list.list.SetRowPainter(s.paintStatusRow)
	s.listScroll = ui.NewScrollbarControl(ListScrollID, s.list.list.Scroll(), tokens)
	s.listScroll.SetSurfaceBackground(ui.ColorPopupBg)
	s.utilityActions = ui.NewActionBar(UtilityActionsID, tokens)
	s.utilityActions.SetAlignEnd(false)
	s.utilityActions.SetExitActions("status.focus.primary", "status.focus.list")
	s.primaryActions = ui.NewActionBar(PrimaryActionsID, tokens)
	s.primaryActions.SetExitActions("status.focus.views", "status.focus.utility")
	s.SetProjection(projection)
	return s
}

func viewItems() []ui.TabItem {
	return []ui.TabItem{
		{ID: string(ViewTabs), Label: "Tabs"},
		{ID: string(ViewWorkspace), Label: "Workspace"},
		{ID: string(ViewActivity), Label: "Activity"},
		{ID: string(ViewNotifications), Label: "Notifications"},
	}
}

func (s *Surface) Children() []ui.Component {
	children := []ui.Component{s.views, s.search, s.list}
	if s.listScroll.Needed() {
		children = append(children, s.listScroll)
	}
	return append(children, s.utilityActions, s.primaryActions)
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
	s.utilityActions.SetTokens(tokens)
	s.primaryActions.SetTokens(tokens)
	s.rebuildActions()
}

func (s *Surface) SetFocusHandler(fn func(ui.ComponentID)) { s.focus = fn }
func (s *Surface) FocusSearch()                            { s.focusID(SearchID) }
func (s *Surface) FocusList()                              { s.focusID(ListID) }
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
	s.views.SetItems(viewItems(), string(s.activeView))
	s.syncItems(true)
}

func (s *Surface) ActiveView() ViewID { return s.activeView }

func (s *Surface) Select(id string) bool {
	for index, row := range s.list.list.Items() {
		if row.ID != id || !s.list.list.SelectIndex(index) {
			continue
		}
		s.expanded[id] = true
		s.syncItems(true)
		return true
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

func (s *Surface) IsExpanded(id string) bool { return s.expanded[id] }
func (s *Surface) ListBounds() ui.Rect       { return s.list.Bounds() }

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
		if query != "" && !strings.Contains(strings.ToLower(searchText(item)), query) {
			continue
		}
		s.items[item.ID] = item
		rows = append(rows, ui.ListItem{ID: item.ID, Label: item.Label, Detail: item.Summary, Icon: item.Icon, Payload: statusRow{ItemID: item.ID}})
		if s.expanded[item.ID] {
			rows = s.appendExpandedRows(rows, item)
		}
	}
	if stable {
		s.list.list.SetItemsStable(rows)
	} else {
		s.list.list.SetItems(rows)
	}
	s.rebuildActions()
}

func searchText(item Item) string {
	var text strings.Builder
	text.WriteString(item.Label)
	text.WriteByte(' ')
	text.WriteString(item.Summary)
	text.WriteByte(' ')
	text.WriteString(item.Subtitle)
	for _, line := range item.Lines {
		text.WriteByte(' ')
		text.WriteString(line.Label)
		text.WriteByte(' ')
		text.WriteString(line.Value)
	}
	for _, section := range item.Usage {
		text.WriteByte(' ')
		text.WriteString(section.Label)
		for _, line := range section.Lines {
			text.WriteByte(' ')
			text.WriteString(line.Label)
			text.WriteByte(' ')
			text.WriteString(line.Value)
		}
	}
	return text.String()
}

func (s *Surface) appendExpandedRows(rows []ui.ListItem, item Item) []ui.ListItem {
	lines := make([]DetailLine, 0, len(item.Lines)+8)
	if item.Subtitle != "" {
		lines = append(lines, DetailLine{Value: item.Subtitle, Role: ui.ColorMuted})
	}
	if len(item.Lines) > 0 {
		if len(lines) > 0 {
			lines = append(lines, DetailLine{Blank: true})
		}
		lines = append(lines, item.Lines...)
	}
	for _, section := range item.Usage {
		if len(lines) > 0 {
			lines = append(lines, DetailLine{Blank: true})
		}
		lines = append(lines, DetailLine{Value: section.Label, Role: ui.ColorAccent})
		lines = append(lines, section.Lines...)
	}
	if !item.PrimaryEnabled && item.DisabledReason != "" {
		if len(lines) > 0 {
			lines = append(lines, DetailLine{Blank: true})
		}
		lines = append(lines, DetailLine{Label: "Unavailable", Value: item.DisabledReason, Role: ui.ColorWarning})
	}
	if len(lines) == 0 {
		lines = append(lines, DetailLine{Value: "No additional status information.", Role: ui.ColorMuted})
	}
	width := max(1, s.listWidth-expandedDetailIndent)
	labelW := expandedLabelWidth(width)
	detailIndex := 0
	for _, line := range lines {
		if line.Blank {
			rows = append(rows, ui.ListItem{ID: item.ID + "/detail/blank/" + strconv.Itoa(detailIndex), Spacer: true})
			detailIndex++
			continue
		}
		valueWidth := width
		if line.Label != "" {
			valueWidth = max(1, width-labelW)
		}
		parts := rendering.HardWrap(line.Value, valueWidth)
		if len(parts) == 0 {
			parts = []string{""}
		}
		for partIndex, value := range parts {
			visual := line
			visual.Value = value
			if partIndex > 0 {
				visual.Label = ""
			}
			rows = append(rows, ui.ListItem{
				ID:       item.ID + "/detail/" + strconv.Itoa(detailIndex),
				Disabled: true,
				Payload:  statusRow{ItemID: item.ID, Detail: true, Line: visual, Continuation: partIndex > 0 && line.Label != ""},
			})
			detailIndex++
		}
	}
	return append(rows, ui.ListItem{ID: item.ID + "/detail/end", Spacer: true})
}

func (s *Surface) toggleSelected(expandOnly, collapseOnly bool) (bool, tea.Cmd) {
	item, ok := s.Selected()
	if !ok {
		return false, nil
	}
	wasExpanded := s.expanded[item.ID]
	switch {
	case expandOnly && wasExpanded:
		return false, nil
	case collapseOnly && !wasExpanded:
		return false, nil
	default:
		s.expanded[item.ID] = !wasExpanded
	}
	s.syncItems(true)
	if !wasExpanded {
		return true, s.selectedReadCmd()
	}
	return true, nil
}

func (s *Surface) rebuildActions() {
	item, ok := s.Selected()
	utility := make([]ui.ActionBarButton, 0, 2)
	if ok {
		utility = append(utility, ui.ActionBarButton{ID: "status-overview/copy", Label: "Copy summary", Action: "status.copy", Variant: ui.VariantSecondary, Enabled: true})
		if item.NotificationID != "" || len(item.Actions) > 0 {
			utility = append(utility, ui.ActionBarButton{ID: "status-overview/more", Label: "More", Action: "status.more", Variant: ui.VariantSecondary, Enabled: true})
		}
	}
	primary := make([]ui.ActionBarButton, 0, 2)
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

func (s *Surface) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
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
	s.utilityActions.Layout(ctx, ui.Rect{X: innerX, Y: actionY, W: utilityW, H: 1})
	contentH := max(1, ruleY-topY)
	listH := max(1, contentH-2)
	s.search.Layout(ctx, ui.Rect{X: innerX, Y: topY, W: innerW, H: 1})
	listW := innerW
	showScroll := s.listScroll.NeededForViewport(listH)
	if showScroll && listW > 2 {
		listW -= 2
	}
	if listW != s.listWidth {
		s.listWidth = listW
		s.syncItems(true)
	}
	s.list.Layout(ctx, ui.Rect{X: innerX, Y: topY + 2, W: listW, H: listH})
	if showScroll && innerW > 2 {
		s.listScroll.Layout(ctx, ui.Rect{X: innerX + innerW - 2, Y: topY + 2, W: 2, H: listH})
	} else {
		s.listScroll.Layout(ctx, ui.Rect{})
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
	y := oy + r.Y + s.search.Bounds().Y + 1
	for x := ox + r.X + 2; x < ox+r.X+2+s.search.Bounds().W; x++ {
		buf.Set(x, y, cell.NewCell(ui.FirstRune(s.tokens.Glyphs().BorderH, '-'), divider, bg, 0))
	}
}

func (s *Surface) paintStatusRow(buf *surface.CellBuffer, x, y, width int, item ui.ListItem, state ui.ControlState, _ ui.PaintContext, tokens *ui.TokenSet) {
	payload, ok := item.Payload.(statusRow)
	if !ok {
		return
	}
	_, bg := tokens.ListRowColors(state)
	if payload.Detail {
		role := payload.Line.Role
		if role == 0 {
			role = ui.ColorFg
		}
		labelW := expandedLabelWidth(max(1, width-expandedDetailIndent))
		valueX := x + expandedDetailIndent
		if payload.Continuation {
			valueX += labelW
		} else if payload.Line.Label != "" {
			label := rendering.TruncateVisual(payload.Line.Label, max(1, labelW-1), "…")
			buf.WriteString(valueX, y, padVisual(label, labelW), tokens.Color(ui.ColorMuted), bg, 0)
			valueX += labelW
		}
		buf.WriteString(valueX, y, rendering.TruncateVisual(payload.Line.Value, max(0, x+width-valueX), "…"), tokens.Color(role), bg, 0)
		return
	}
	fg, _ := tokens.ListRowColors(state)
	fg, muted := tokens.MenuInk(state, fg)
	attr := cell.Attrs(0)
	if state == ui.StateFocused || state == ui.StateSelected {
		attr = cell.AttrBold
	}
	chevron := string(ui.FirstRune(tokens.Glyphs().TreeCollapsed, '▸'))
	if s.expanded[payload.ItemID] {
		chevron = string(ui.FirstRune(tokens.Glyphs().TreeExpanded, '▾'))
	}
	prefix := strings.Repeat(" ", max(0, tokens.FormIndent())) + chevron + " "
	if item.Icon != "" {
		prefix += item.Icon + " "
	}
	buf.WriteString(x, y, prefix, fg, bg, attr)
	contentX := x + rendering.VisibleWidth(prefix)
	contentW := max(0, width-rendering.VisibleWidth(prefix))
	label := item.Label
	if statusItem, exists := s.items[payload.ItemID]; exists && statusItem.Active {
		label = "• " + label
	}
	paintStatusMainLine(buf, contentX, y, contentW, label, item.Detail, fg, muted, bg, attr)
}

func paintStatusMainLine(buf *surface.CellBuffer, x, y, width int, label, detail string, fg, muted, bg cell.Color, attr cell.Attrs) {
	if width <= 0 {
		return
	}
	if detail == "" {
		buf.WriteString(x, y, rendering.TruncateVisual(label, width, "…"), fg, bg, attr)
		return
	}
	detailW := min(rendering.VisibleWidth(detail), max(0, width/2))
	labelW := max(0, width-detailW-2)
	if labelW == 0 {
		buf.WriteString(x, y, rendering.TruncateVisual(detail, width, "…"), muted, bg, attr)
		return
	}
	buf.WriteString(x, y, rendering.TruncateVisual(label, labelW, "…"), fg, bg, attr)
	detailX := x + width - detailW
	buf.WriteString(detailX, y, rendering.TruncateVisual(detail, detailW, "…"), muted, bg, attr)
}

func expandedLabelWidth(width int) int {
	return min(16, max(10, width/4))
}

func (s *Surface) Handle(_ ui.EventContext, evt ui.Event) ui.EventResult {
	if !evt.IsKey() {
		return ui.EventResult{}
	}
	switch evt.Key.Code {
	case tea.KeyEsc:
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return CloseMsg{} }}
	case tea.KeyTab:
		if evt.Key.Mod.Contains(tea.ModShift) {
			s.FocusPrimaryActions()
		} else {
			s.FocusSearch()
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
			s.search.field.SetValue("")
			s.syncItems(false)
			s.FocusSearch()
		}
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return ViewChangedMsg{View: s.activeView} }, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
	case "tabs.enter":
		s.FocusSearch()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "status.item.toggle":
		changed, cmd := s.toggleSelected(false, false)
		invalidate := ui.InvalidatePaint
		if changed {
			invalidate |= ui.InvalidateLayout
		}
		return ui.EventResult{Handled: true, Cmd: cmd, Invalidate: invalidate}
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
	case "status.focus.utility":
		if s.utilityActions.Bounds().W > 0 {
			s.FocusUtilityActions()
		} else {
			s.FocusList()
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "status.focus.primary":
		s.FocusPrimaryActions()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "status.focus.views":
		s.focusID(ViewsID)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "status.focus.list":
		s.FocusList()
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
	lines := make([]DetailLine, 0, len(item.Lines)+8)
	if item.Subtitle != "" {
		lines = append(lines, DetailLine{Value: item.Subtitle, Role: ui.ColorMuted}, DetailLine{Blank: true})
	}
	lines = append(lines, item.Lines...)
	for _, section := range item.Usage {
		lines = append(lines, DetailLine{Blank: true}, DetailLine{Value: section.Label, Role: ui.ColorAccent})
		lines = append(lines, section.Lines...)
	}
	item.Lines = lines
	return item
}

func (s *Surface) selectedReadCmd() tea.Cmd {
	item, ok := s.Selected()
	if !ok || !s.expanded[item.ID] || item.NotificationID == "" || !item.NotificationUnread {
		return nil
	}
	id := item.NotificationID
	return func() tea.Msg { return MarkReadMsg{ID: id} }
}

func (s *Surface) DisplayedUnreadID() string {
	item, ok := s.Selected()
	if !ok || !s.expanded[item.ID] || !item.NotificationUnread {
		return ""
	}
	return item.NotificationID
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
	if evt.IsKey() {
		switch evt.Key.Code {
		case tea.KeyRight:
			if changed, cmd := l.owner.toggleSelected(true, false); changed {
				return ui.EventResult{Handled: true, Cmd: cmd, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
			}
		case tea.KeyLeft:
			if changed, cmd := l.owner.toggleSelected(false, true); changed {
				return ui.EventResult{Handled: true, Cmd: cmd, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
			}
		case tea.KeySpace:
			changed, cmd := l.owner.toggleSelected(false, false)
			if changed {
				return ui.EventResult{Handled: true, Cmd: cmd, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
			}
		}
	}
	before := l.list.SelectedIndex()
	result := l.list.Handle(ctx, evt)
	if before != l.list.SelectedIndex() {
		l.owner.rebuildActions()
		result.Invalidate |= ui.InvalidateLayout | ui.InvalidatePaint
	}
	if evt.IsKey() && evt.Key.Code == tea.KeyTab {
		if evt.Key.Mod.Contains(tea.ModShift) {
			l.owner.FocusSearch()
		} else if l.owner.utilityActions.Bounds().W > 0 {
			l.owner.FocusUtilityActions()
		} else {
			l.owner.FocusPrimaryActions()
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	return result
}
func (l *listControl) Semantics() ui.Semantics { return l.list.Semantics() }

func padVisual(value string, width int) string {
	valueWidth := rendering.VisibleWidth(value)
	if valueWidth >= width {
		return value
	}
	return value + strings.Repeat(" ", width-valueWidth)
}
