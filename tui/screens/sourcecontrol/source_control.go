// Package sourcecontrol provides the native Git activity sidebar.
package sourcecontrol

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/filetree"
	"crude/tui/rendering"
	"crude/tui/screens/panelchrome"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

const (
	ScreenID    ui.ComponentID = "sourcecontrol"
	FilterID    ui.ComponentID = "sourcecontrol/filter"
	ListID      ui.ComponentID = "sourcecontrol/list"
	ScrollbarID ui.ComponentID = "sourcecontrol/scrollbar"
	DividerID   ui.ComponentID = "sourcecontrol/divider"
	ActionsID   ui.ComponentID = "sourcecontrol/actions"

	ActionOpenChange = "sourcecontrol.open-change"
	ActionReview     = "sourcecontrol.review"
	ActionHistory    = "sourcecontrol.history"
	ActionBranches   = "sourcecontrol.branches"
	ActionOpenFile   = "sourcecontrol.open-file"
	ActionCopyPath   = "sourcecontrol.copy-path"
	ActionRefresh    = "sourcecontrol.refresh"
)

const (
	summaryRow = panelchrome.HeaderRows
	listTop    = panelchrome.HeaderRows + 1
)

type ItemKind uint8

const (
	ItemChange ItemKind = iota
)

// Item is the typed payload for one selectable source-control row.
type Item struct {
	Kind   ItemKind
	Change workspace.GitChange
}

type Screen struct {
	ui.Base
	th        *theme.Theme
	tokens    *ui.TokenSet
	snapshot  workspace.GitSnapshot
	filter    *filterField
	list      *ui.List
	listView  *changeList
	actions   *ui.ActionBar
	scrollbar *ui.ScrollbarControl
	split     *ui.SplitModel
	divider   *ui.SplitDivider
	icons     *filetree.IconResolver
	loading   bool
	errText   string
	focus     func(ui.ComponentID) bool
	selected  string
	collapsed map[string]bool
}

func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	s := &Screen{
		Base:      ui.NewBase(ScreenID),
		th:        th,
		tokens:    tokens,
		collapsed: make(map[string]bool),
		icons:     filetree.NewIconResolver(),
	}
	s.icons.ApplyTheme(&th.Colors.Tree)
	field := ui.NewFilterField(FilterID, tokens)
	field.SetPrompt(ui.SearchGlyphEnd(tokens))
	field.SetPromptPlacement(ui.PromptEnd)
	field.SetPlaceholder("Filter changes")
	field.SetSubmitAction("")
	field.OnChanged(func(string) { s.rebuild() })
	s.filter = &filterField{Base: ui.NewBase(FilterID), field: field, owner: s}
	s.list = ui.NewList(ListID, tokens)
	s.list.SetActivateAction("sourcecontrol.activate")
	s.list.SetDefaultRowBackground(ui.ColorBg)
	s.list.SetSelectionMarker(false)
	s.list.SetRowPainter(s.paintChange)
	s.listView = &changeList{Base: ui.NewBase(ListID), list: s.list, owner: s}
	s.actions = ui.NewActionBar(ActionsID, tokens,
		ui.ActionBarButton{Label: "Review", Action: ActionReview, Variant: ui.VariantSecondary, Enabled: true},
		ui.ActionBarButton{Label: "History", Action: ActionHistory, Variant: ui.VariantSecondary, Enabled: true},
		ui.ActionBarButton{Label: "Branches", Action: ActionBranches, Variant: ui.VariantSecondary, Enabled: true})
	s.actions.SetAlignEnd(false)
	s.actions.SetSurfaceBackground(ui.ColorBg)
	s.scrollbar = ui.NewScrollbarControl(ScrollbarID, s.list.Scroll(), tokens)
	s.split = ui.NewSplitModel(ui.Horizontal)
	s.split.SetMinSizes(12, 0)
	s.divider = ui.NewSplitDivider(DividerID, s.split)
	s.divider.SetTokens(tokens)
	s.rebuild()
	return s
}

func (s *Screen) SetFocusHandler(fn func(ui.ComponentID) bool) { s.focus = fn }

func (s *Screen) focusID(id ui.ComponentID) {
	if s.focus != nil {
		s.focus(id)
	}
}

func (s *Screen) SetSnapshot(snapshot workspace.GitSnapshot) {
	if current, ok := s.list.Selected(); ok && s.Filter() == "" {
		s.selected = current.ID
	}
	snapshot.Changes = append([]workspace.GitChange(nil), snapshot.Changes...)
	s.snapshot = snapshot
	s.loading = false
	s.errText = ""
	s.rebuild()
}

func (s *Screen) Snapshot() workspace.GitSnapshot { return s.snapshot }

func (s *Screen) SetLoading(loading bool) {
	s.loading = loading
	if loading {
		s.errText = ""
	}
}

func (s *Screen) SetError(err error) {
	s.loading = false
	if err == nil {
		s.errText = ""
	} else {
		s.errText = err.Error()
	}
	s.rebuild()
}

func (s *Screen) Filter() string { return s.filter.field.Value() }

func (s *Screen) ScrollModel() *ui.ScrollModel { return s.list.Scroll() }

func (screen *Screen) ViewState() (string, string, int, []string) {
	var collapsed []string
	for _, group := range []string{"Conflicts", "Staged", "Changes"} {
		if screen.collapsed[group] {
			collapsed = append(collapsed, group)
		}
	}
	return screen.Filter(), screen.selected, screen.list.Scroll().Offset(), collapsed
}

func (screen *Screen) RestoreViewState(filter, selected string, offset int, collapsed []string) {
	screen.selected = selected
	screen.collapsed = make(map[string]bool)
	for _, group := range collapsed {
		screen.collapsed[group] = true
	}
	screen.filter.field.SetValue(filter)
	screen.rebuild()
	screen.list.Scroll().ScrollTo(offset)
}

func (s *Screen) SetFilter(value string) {
	s.filter.field.SetValue(value)
	s.rebuild()
}

func (s *Screen) SetSplitMaxFirst(n int) {
	if s.split != nil {
		s.split.SetMaxFirst(n)
	}
}

func (s *Screen) SplitFirst() int {
	if s.split == nil {
		return 0
	}
	return s.split.FirstSize()
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	if s.filter != nil {
		s.filter.field.SetTokens(tokens)
	}
	if s.list != nil {
		s.list.SetTokens(tokens)
		s.list.SetDefaultRowBackground(ui.ColorBg)
	}
	if s.scrollbar != nil {
		s.scrollbar.SetTokens(tokens)
	}
	if s.divider != nil {
		s.divider.SetTokens(tokens)
	}
	if s.actions != nil {
		s.actions.SetTokens(tokens)
	}
}

func (s *Screen) SetTheme(th *theme.Theme) {
	if th == nil {
		return
	}
	s.th = th
	if s.icons != nil {
		s.icons.ApplyTheme(&th.Colors.Tree)
	}
	s.SetTokens(ui.NewTokenSet(th))
	s.rebuild()
}

func (s *Screen) Children() []ui.Component {
	return []ui.Component{s.filter, s.listView, s.scrollbar, s.actions, s.divider}
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	contentW := r.W
	if contentW > 1 {
		contentW--
	}
	fieldRect := s.headerTitle().FieldRect(panelchrome.BodyWidth(contentW))
	fieldRect.W = max(0, min(fieldRect.W, contentW-fieldRect.X))
	fieldRect.H = min(1, r.H)
	s.filter.Layout(ctx, fieldRect)
	actionWidth := max(0, contentW-panelchrome.PadLeft)
	if actionWidth < 35 {
		s.actions.SetButtons(ui.ActionBarButton{Label: "Review", Action: ActionReview, Variant: ui.VariantSecondary, Enabled: true}, ui.ActionBarButton{Label: "More", Action: "sourcecontrol.more", Variant: ui.VariantSecondary, Enabled: true})
	} else {
		s.actions.SetButtons(ui.ActionBarButton{Label: "Review", Action: ActionReview, Variant: ui.VariantSecondary, Enabled: true}, ui.ActionBarButton{Label: "History", Action: ActionHistory, Variant: ui.VariantSecondary, Enabled: true}, ui.ActionBarButton{Label: "Branches", Action: ActionBranches, Variant: ui.VariantSecondary, Enabled: true})
	}
	actionRows := min(1, max(0, r.H-listTop))
	listH := max(0, r.H-listTop-actionRows)
	barW := 0
	if s.scrollbar.NeededForViewport(listH) {
		barW = min(ui.ScrollbarWidth, contentW)
	}
	bodyW := panelchrome.BodyWidth(contentW)
	s.listView.Layout(ctx, ui.Rect{
		X: panelchrome.PadLeft, Y: listTop,
		W: max(0, bodyW-panelchrome.PadLeft), H: listH,
	})
	s.scrollbar.Layout(ctx, ui.Rect{X: max(0, contentW-barW), Y: listTop, W: barW, H: listH})
	s.actions.Layout(ctx, ui.Rect{X: panelchrome.PadLeft, Y: max(0, r.H-actionRows), W: max(0, contentW-panelchrome.PadLeft), H: actionRows})
	if s.split != nil && !s.split.Dragging() {
		s.split.SetFirstSize(max(0, r.W-1))
	}
	s.divider.SetCurrentFirst(max(0, r.W-1))
	s.divider.Layout(ctx, ui.Rect{X: max(0, r.W-1), W: 1, H: r.H})
}

func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	if buf == nil || s.tokens == nil {
		return
	}
	r := s.Bounds()
	bg := s.tokens.Color(ui.ColorBg)
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	contentW := r.W
	if contentW > 1 {
		contentW--
	}
	panelchrome.PaintTitle(buf, ox, oy, ui.Rect{X: r.X, Y: r.Y, W: panelchrome.BodyWidth(contentW), H: r.H}, s.tokens, s.headerTitle())
	if r.H > summaryRow {
		s.paintSummary(buf, ox+r.X+panelchrome.PadLeft, oy+r.Y+summaryRow, max(0, r.W-3))
	}
}

func (s *Screen) paintSummary(buf *surface.CellBuffer, x, y, width int) {
	text := s.summary()
	fg := s.tokens.Color(ui.ColorMuted)
	if s.errText != "" {
		text = s.errText
		fg = s.tokens.Color(ui.ColorError)
	} else if s.loading {
		text = "Refreshing repository…"
	} else if s.conflictCount() > 0 {
		fg = s.tokens.Color(ui.ColorWarning)
	}
	buf.WriteString(x, y, rendering.TruncateLine(text, width), fg, s.tokens.Color(ui.ColorBg), 0)
}

func (s *Screen) summary() string {
	counts := map[workspace.GitChangeArea]int{}
	for _, change := range s.snapshot.Changes {
		counts[change.Area]++
	}
	branch := s.branchBadge()
	if branch == "" {
		branch = "Repository"
	}
	parts := []string{branch}
	if n := counts[workspace.GitAreaConflict]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d conflicts", n))
	}
	if len(s.snapshot.Changes) == 0 {
		parts = append(parts, "Working tree clean")
	}
	return strings.Join(parts, " · ")
}

func (s *Screen) headerTitle() panelchrome.Title {
	return panelchrome.Title{
		Icon: sourceControlIcon(panelchrome.NerdFont(s.th)),
	}
}

func (s *Screen) branchBadge() string {
	branch := s.snapshot.Branch
	if branch == "" {
		return ""
	}
	if s.snapshot.Ahead > 0 || s.snapshot.Behind > 0 {
		branch += fmt.Sprintf(" ↑%d ↓%d", s.snapshot.Ahead, s.snapshot.Behind)
	}
	return branch
}

func (s *Screen) Handle(_ ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() && evt.Key.String() == "ctrl+r" {
		return ui.EventResult{
			Handled:    true,
			Action:     &ui.Action{ID: ActionRefresh},
			Invalidate: ui.InvalidatePaint,
		}
	}
	if evt.IsKey() {
		switch evt.Key.String() {
		case "ctrl+enter":
			return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionReview}}
		case "ctrl+h":
			return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionHistory}}
		case "ctrl+b":
			return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionBranches}}
		case "/":
			s.focusID(FilterID)
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		}
	}
	return ui.EventResult{}
}

func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	if action.ID != "sourcecontrol.activate" {
		return ui.EventResult{Action: &action}
	}
	row, ok := action.Payload.(ui.ListItem)
	if !ok {
		return ui.EventResult{Handled: true}
	}
	item, ok := row.Payload.(Item)
	if !ok {
		return ui.EventResult{Handled: true}
	}
	return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionOpenChange, Payload: item.Change}}
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Source Control", Value: s.snapshot.Branch}
}

func (s *Screen) ItemAtLocalY(localY int) (Item, bool) {
	row := localY - listTop
	if row < 0 || row >= s.list.Scroll().Viewport() {
		return Item{}, false
	}
	idx := s.list.Scroll().Offset() + row
	items := s.list.Items()
	if idx < 0 || idx >= len(items) || !items[idx].Selectable() {
		return Item{}, false
	}
	item, ok := items[idx].Payload.(Item)
	return item, ok
}

func (s *Screen) rebuild() {
	var rows []ui.ListItem
	if s.errText != "" {
		rows = append(rows, ui.ListItem{ID: "git-error", Label: "Repository unavailable", Detail: "!", Disabled: true})
	} else {
		rows = append(rows, s.changeRows()...)
	}
	offset := s.list.Scroll().Offset()
	s.list.SetItemsStable(rows)
	for position, row := range rows {
		if row.ID == s.selected && row.Selectable() {
			s.list.SelectIndex(position)
			break
		}
	}
	s.list.Scroll().ScrollTo(offset)
}

func (s *Screen) changeRows() []ui.ListItem {
	var rows []ui.ListItem
	groups := []struct {
		area  workspace.GitChangeArea
		label string
	}{
		{workspace.GitAreaConflict, "Conflicts"},
		{workspace.GitAreaStaged, "Staged"},
		{workspace.GitAreaWorking, "Changes"},
	}
	for _, group := range groups {
		var changes []workspace.GitChange
		for _, change := range s.snapshot.Changes {
			if (change.Area == group.area || group.area == workspace.GitAreaWorking && change.Area == workspace.GitAreaUntracked) && strings.Contains(strings.ToLower(change.Path), strings.ToLower(s.Filter())) {
				changes = append(changes, change)
			}
		}
		if len(changes) == 0 {
			if group.area != workspace.GitAreaConflict && s.Filter() == "" {
				rows = append(rows, ui.ListItem{ID: "empty:" + group.label, Label: group.label + " · none", Disabled: true})
			}
			continue
		}
		rows = append(rows, ui.ListItem{
			ID: "header:" + group.label, Label: mapDisclosure(s.collapsed[group.label]) + " " + group.label + " · " + strconv.Itoa(len(changes)),
			Detail: strconv.Itoa(len(changes)), Header: true,
		})
		if s.collapsed[group.label] {
			continue
		}
		for _, change := range changes {
			name := filepath.Base(change.Path)
			icon := ""
			if s.icons != nil {
				icon = s.icons.ResolveIcon(name, false, false, false).Icon
			}
			rows = append(rows, ui.ListItem{
				ID:    "change:" + strconv.Itoa(int(change.Area)) + ":" + change.Path,
				Label: name, Detail: change.Code, Icon: icon,
				Payload: Item{Kind: ItemChange, Change: change},
			})
		}
	}
	if len(rows) == 0 {
		rows = append(rows, ui.ListItem{ID: "no-matches", Label: "No matching changes", Disabled: true})
	}
	return rows
}

func (s *Screen) conflictCount() int {
	count := 0
	for _, change := range s.snapshot.Changes {
		if change.Area == workspace.GitAreaConflict {
			count++
		}
	}
	return count
}

func (s *Screen) paintChange(buf *surface.CellBuffer, x, y, width int, row ui.ListItem, state ui.ControlState, _ ui.PaintContext, tokens *ui.TokenSet) {
	fg := tokens.Color(ui.ColorFg)
	_, bg := tokens.ListRowColors(state)
	if state == ui.StateDefault || state == ui.StateDisabled {
		bg = tokens.Color(ui.ColorBg)
	}
	item, ok := row.Payload.(Item)
	if !ok {
		buf.WriteString(x, y, rendering.TruncateLine(row.Label, width), tokens.Color(ui.ColorMuted), bg, 0)
		return
	}
	attr := cell.Attrs(0)
	if state == ui.StateFocused || state == ui.StateSelected {
		attr = cell.AttrBold
	}
	status := tokens.Color(ui.ColorMuted)
	if item.Change.Area == workspace.GitAreaConflict {
		status = tokens.Color(ui.ColorWarning)
	}
	mark := row.Detail
	if item.Change.Area == workspace.GitAreaUntracked {
		mark = "U"
	}
	markW := min(width, rendering.VisibleWidth(mark))
	nameW := max(0, width-markW-2)
	name := rendering.TruncateLine(row.Label, nameW)
	buf.WriteString(x, y, name, fg, bg, attr)
	parent := filepath.ToSlash(filepath.Dir(item.Change.Path))
	remaining := nameW - rendering.VisibleWidth(name) - 2
	if parent != "." && remaining > 0 {
		buf.WriteString(x+rendering.VisibleWidth(name)+2, y, rendering.TruncateLine(parent, remaining), tokens.Color(ui.ColorMuted), bg, 0)
	}
	if markW > 0 {
		buf.WriteString(x+width-markW, y, mark, status, bg, 0)
	}
}

type filterField struct {
	ui.Base
	field *ui.FilterField
	owner *Screen
}

func (c *filterField) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return c.field.Measure(ctx, cs)
}
func (c *filterField) Layout(ctx ui.LayoutContext, r ui.Rect) {
	c.SetBounds(r)
	c.field.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}
func (c *filterField) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	c.field.Paint(buf, ox+c.Bounds().X, oy+c.Bounds().Y, ctx)
}
func (c *filterField) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() {
		if evt.Key.Code == tea.KeyDown || evt.Key.Code == tea.KeyTab || evt.Key.Code == tea.KeyEnter {
			c.owner.focusID(ListID)
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		}
	}
	return c.field.Handle(ctx, evt)
}
func (c *filterField) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleTextInput, Label: "Filter changes", Value: c.field.Value()}
}

func (c *filterField) TextEditor() ui.TextClipboardTarget { return c.field.TextEditor() }

type changeList struct {
	ui.Base
	list      *ui.List
	owner     *Screen
	lastClick string
	lastTime  time.Time
}

func (list *changeList) Layout(ctx ui.LayoutContext, bounds ui.Rect) {
	list.SetBounds(bounds)
	list.list.Layout(ctx, ui.Rect{W: bounds.W, H: bounds.H})
}

func (list *changeList) Paint(buf *surface.CellBuffer, originX, originY int, ctx ui.PaintContext) {
	list.list.Paint(buf, originX+list.Bounds().X, originY+list.Bounds().Y, ctx)
}

func (list *changeList) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.Kind == ui.EventMouseClick && evt.MouseClick.Button == tea.MouseLeft {
		index := list.list.Scroll().Offset() + ctx.LocalY
		rows := list.list.Items()
		if index >= 0 && index < len(rows) && strings.HasPrefix(rows[index].ID, "header:") {
			group := strings.TrimPrefix(rows[index].ID, "header:")
			list.owner.collapsed[group] = !list.owner.collapsed[group]
			list.owner.rebuild()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
		}
	}
	if evt.IsKey() {
		switch evt.Key.Code {
		case tea.KeyLeft, tea.KeyRight:
			if selected, exists := list.list.Selected(); exists {
				if item, valid := selected.Payload.(Item); valid {
					group := "Changes"
					switch item.Change.Area {
					case workspace.GitAreaStaged:
						group = "Staged"
					case workspace.GitAreaConflict:
						group = "Conflicts"
					}
					list.owner.collapsed[group] = evt.Key.Code == tea.KeyLeft
					list.owner.rebuild()
					return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
				}
			}
		case tea.KeyTab:
			list.owner.focusID(ActionsID)
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		}
	}
	result := list.list.Handle(ctx, evt)
	if selected, ok := list.list.Selected(); ok {
		list.owner.selected = selected.ID
		if evt.Kind == ui.EventMouseClick && result.Action != nil {
			now := time.Now()
			double := list.lastClick == selected.ID && now.Sub(list.lastTime) <= 400*time.Millisecond
			list.lastClick, list.lastTime = selected.ID, now
			if !double {
				result.Action = nil
			}
		}
	}
	return result
}

func mapDisclosure(collapsed bool) string {
	if collapsed {
		return "▸"
	}
	return "▾"
}

func (list *changeList) Semantics() ui.Semantics { return list.list.Semantics() }

func sourceControlIcon(nerd bool) string {
	if nerd {
		return "\uf418"
	}
	return "⑂"
}

var _ ui.Component = (*Screen)(nil)
var _ ui.Parent = (*Screen)(nil)
var _ ui.ActionHandler = (*Screen)(nil)
