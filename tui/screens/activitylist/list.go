package activitylist

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/screens/panelchrome"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const (
	ScreenID    ui.ComponentID = "activitylist"
	ListID      ui.ComponentID = "activitylist/list"
	FilterID    ui.ComponentID = "activitylist/filter"
	ScrollbarID ui.ComponentID = "activitylist/scrollbar"
	DividerID   ui.ComponentID = "activitylist/divider"

	ActionActivate = "sidebar.activate"
)

// doubleClickWindow matches the picker screens' click-arming interval.
const doubleClickWindow = 500 * time.Millisecond

// listTop is the first scrollable row after the icon+search header.
const listTop = panelchrome.HeaderRows

const emptyStateText = "No items"

// Screen is the sidebar body for Agents, Terminals, and Git.
type Screen struct {
	ui.Base
	th         *theme.Theme
	tokens     *ui.TokenSet
	filter     *activityFilterField
	list       *sidebarList
	scrollbar  *ui.ScrollbarControl
	split      *ui.SplitModel
	divider    *ui.SplitDivider
	heading    string
	icon       string
	badge      string
	sourceData []ui.ListItem
}

func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	s := &Screen{
		Base:    ui.NewBase(ScreenID),
		th:      th,
		tokens:  tokens,
		heading: "Agents",
		icon:    "◈",
	}
	s.filter = &activityFilterField{Base: ui.NewBase(FilterID), owner: s, field: ui.NewFilterField(FilterID, tokens)}
	s.filter.SetPrompt(ui.SearchGlyphEnd(tokens))
	s.filter.SetPromptPlacement(ui.PromptEnd)
	s.filter.SetPlaceholder("Search agents")
	s.filter.SetBackgroundColor(tokens.Color(ui.ColorSubPaneBg))
	s.filter.SetPromptColor(tokens.Color(ui.ColorAccent))
	s.filter.OnChanged(func(string) { s.applyFilter() })
	s.list = newSidebarList(s, tokens)
	s.scrollbar = ui.NewScrollbarControl(ScrollbarID, s.list.inner.Scroll(), tokens)
	s.split = ui.NewSplitModel(ui.Horizontal)
	s.split.SetMinSizes(12, 0)
	s.divider = ui.NewSplitDivider(DividerID, s.split)
	s.divider.SetTokens(tokens)
	return s
}

type activityFilterField struct {
	ui.Base
	owner *Screen
	field *ui.FilterField
}

func (f *activityFilterField) SetPrompt(value string) { f.field.SetPrompt(value) }
func (f *activityFilterField) SetPromptPlacement(value ui.PromptPlacement) {
	f.field.SetPromptPlacement(value)
}
func (f *activityFilterField) SetPlaceholder(value string)         { f.field.SetPlaceholder(value) }
func (f *activityFilterField) SetBackgroundColor(value cell.Color) { f.field.SetBackgroundColor(value) }
func (f *activityFilterField) SetPromptColor(value cell.Color)     { f.field.SetPromptColor(value) }
func (f *activityFilterField) OnChanged(fn func(string))           { f.field.OnChanged(fn) }
func (f *activityFilterField) Value() string                       { return f.field.Value() }
func (f *activityFilterField) SetTokens(tokens *ui.TokenSet)       { f.field.SetTokens(tokens) }
func (f *activityFilterField) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return f.field.Measure(ctx, cs)
}
func (f *activityFilterField) Layout(ctx ui.LayoutContext, r ui.Rect) {
	f.SetBounds(r)
	f.field.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}
func (f *activityFilterField) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	f.field.Paint(buf, ox+f.Bounds().X, oy+f.Bounds().Y, ctx)
}
func (f *activityFilterField) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() && f.owner != nil && f.owner.list != nil {
		switch evt.Key.Code {
		case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd, tea.KeyEnter:
			return f.owner.list.Handle(ctx, evt)
		}
	}
	return f.field.Handle(ctx, evt)
}
func (f *activityFilterField) Semantics() ui.Semantics { return f.field.Semantics() }

func (s *Screen) SetHeading(h string) {
	s.heading = h
	if s.filter != nil {
		s.filter.SetPlaceholder("Search " + strings.ToLower(h))
	}
}

func (s *Screen) SetIcon(icon string) { s.icon = icon }

func (s *Screen) SetBadge(badge string) { s.badge = badge }

type ViewState struct {
	Filter, Selection string
	Offset            int
}

func (screen *Screen) ViewState() ViewState {
	state := ViewState{Filter: screen.filter.Value(), Offset: screen.list.inner.Scroll().Offset()}
	if selected, ok := screen.list.inner.Selected(); ok {
		state.Selection = selected.ID
	}
	return state
}

func (screen *Screen) RestoreViewState(state ViewState) {
	screen.filter.field.SetValue(state.Filter)
	screen.applyFilter()
	for index, item := range screen.list.inner.Items() {
		if item.ID == state.Selection {
			screen.list.inner.SelectIndex(index)
			break
		}
	}
	screen.list.inner.Scroll().ScrollTo(state.Offset)
}

func (s *Screen) headerTitle() panelchrome.Title {
	return panelchrome.Title{Icon: s.icon, Badge: s.badge}
}

// SetItems replaces the unfiltered source data. The shell calls this every
// frame, so identical content is a no-op and selection/scroll survive paint.
func (s *Screen) SetItems(items []ui.ListItem) {
	if s.list == nil {
		return
	}
	if itemsEquivalent(s.sourceData, items) {
		return
	}
	// Do not reuse the backing array: ui.List retains the visible slice. If
	// sourceData overwrites that array before selection is captured, the
	// selected ID silently changes to whichever new row occupies the index.
	s.sourceData = append([]ui.ListItem(nil), items...)
	s.applyFilter()
}

func (s *Screen) applyFilter() {
	query := ""
	if s.filter != nil {
		query = strings.TrimSpace(strings.ToLower(s.filter.Value()))
	}
	items := s.sourceData
	if query != "" {
		items = make([]ui.ListItem, 0, len(s.sourceData))
		for _, item := range s.sourceData {
			if !item.Selectable() {
				continue
			}
			if fuzzyContains(item.Label, query) || fuzzyContains(item.Detail, query) {
				items = append(items, item)
			}
		}
	}
	s.applyVisibleItems(items)
}

func (s *Screen) applyVisibleItems(items []ui.ListItem) {
	inner := s.list.inner
	if itemsEquivalent(inner.Items(), items) {
		return
	}
	prevID := ""
	if it, ok := inner.Selected(); ok && it.Selectable() {
		prevID = it.ID
	}
	prevOffset := inner.Scroll().Offset()
	inner.SetItems(items)
	if prevID != "" {
		for i := range items {
			if items[i].ID == prevID && items[i].Selectable() {
				inner.Selection().SetIndex(i)
				break
			}
		}
	}
	inner.Scroll().ScrollTo(prevOffset)
}

// itemsEquivalent reports whether two item slices carry the same visible
// data. Payload-bearing items never compare equal so stale payloads cannot
// be retained by the skip path.
func itemsEquivalent(a, b []ui.ListItem) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.Payload != nil || y.Payload != nil {
			return false
		}
		if x.ID != y.ID || x.Label != y.Label || x.Detail != y.Detail || x.Icon != y.Icon {
			return false
		}
		if x.Disabled != y.Disabled || x.Header != y.Header || x.Separator != y.Separator || x.Spacer != y.Spacer {
			return false
		}
		if x.Command != y.Command {
			return false
		}
		if x.Depth != y.Depth || x.TreeLast != y.TreeLast {
			return false
		}
	}
	return true
}

func fuzzyContains(value, query string) bool {
	value = strings.ToLower(value)
	query = strings.ToLower(query)
	if strings.Contains(value, query) {
		return true
	}
	queryRunes := []rune(query)
	qi := 0
	for _, r := range value {
		if qi < len(queryRunes) && r == queryRunes[qi] {
			qi++
		}
	}
	return qi == len(queryRunes)
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
		s.filter.SetTokens(tokens)
		s.filter.SetPrompt(ui.SearchGlyphEnd(tokens))
		s.filter.SetPromptPlacement(ui.PromptEnd)
		s.filter.SetBackgroundColor(tokens.Color(ui.ColorSubPaneBg))
		s.filter.SetPromptColor(tokens.Color(ui.ColorAccent))
	}
	if s.list != nil {
		s.list.SetTokens(tokens)
	}
	if s.scrollbar != nil {
		s.scrollbar.SetTokens(tokens)
	}
	if s.divider != nil {
		s.divider.SetTokens(tokens)
	}
}

func (s *Screen) SetTheme(th *theme.Theme) {
	if th == nil {
		return
	}
	s.th = th
	s.SetTokens(ui.NewTokenSet(th))
}

func (s *Screen) Children() []ui.Component {
	out := make([]ui.Component, 0, 4)
	if s.filter != nil {
		out = append(out, s.filter)
	}
	if s.list != nil {
		out = append(out, s.list)
	}
	if s.scrollbar != nil {
		out = append(out, s.scrollbar)
	}
	if s.divider != nil {
		out = append(out, s.divider)
	}
	return out
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

// contentWidth is the pane width left of the divider column.
func (s *Screen) contentWidth(r ui.Rect) int {
	if r.W > 1 {
		return r.W - 1
	}
	return r.W
}

// listWidth mirrors the file tree's row width: one gap column between rows
// and the scrollbar (filetree.SidebarContentWidth convention).
func (s *Screen) listWidth(r ui.Rect) int {
	return panelchrome.BodyWidth(s.contentWidth(r))
}

func (s *Screen) listTopRow(r ui.Rect) int {
	if r.H <= listTop {
		return 0
	}
	return listTop
}

func (s *Screen) Layout(_ ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	contentW := s.contentWidth(r)
	if s.filter != nil {
		s.filter.Layout(ui.LayoutContext{}, s.headerTitle().FieldRect(s.listWidth(r)))
	}
	listY := s.listTopRow(r)
	listH := max(1, r.H-listY)
	if s.list != nil {
		s.list.Layout(ui.LayoutContext{}, ui.Rect{X: 0, Y: listY, W: s.listWidth(r), H: listH})
	}
	if s.scrollbar != nil && contentW >= 1 {
		barW := min(ui.ScrollbarWidth+1, contentW)
		s.scrollbar.Layout(ui.LayoutContext{}, ui.Rect{X: contentW - barW, Y: listY, W: barW, H: listH})
	}
	if s.divider != nil && r.W > 0 {
		if s.split != nil && !s.split.Dragging() {
			s.split.SetFirstSize(r.W - 1)
		}
		s.divider.SetCurrentFirst(r.W - 1)
		s.divider.Layout(ui.LayoutContext{}, ui.Rect{X: r.W - 1, Y: 0, W: 1, H: r.H})
	}
}

func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	r := s.Bounds()
	if r.Empty() || buf == nil {
		return
	}
	bg := cell.NoColor
	muted := cell.NoColor
	if s.tokens != nil {
		bg = s.tokens.Color(ui.ColorBg)
		muted = s.tokens.Color(ui.ColorMuted)
	}
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	body := ui.Rect{X: r.X, Y: r.Y, W: s.listWidth(r), H: r.H}
	panelchrome.PaintTitle(buf, ox, oy, body, s.tokens, s.headerTitle())
	if s.list != nil && len(s.list.items()) == 0 {
		text := emptyStateText
		if s.filter != nil && strings.TrimSpace(s.filter.Value()) != "" {
			text = "No matches"
		}
		text = rendering.TruncateLine(text, max(1, s.listWidth(r)-1))
		buf.WriteString(ox+r.X+1, oy+r.Y+s.listTopRow(r), text, muted, bg, 0)
	}
}

// Query returns the current panel filter.
func (s *Screen) Query() string {
	if s == nil || s.filter == nil {
		return ""
	}
	return s.filter.Value()
}

// ItemAtLocalY resolves a panel-local row to its visible list item. Chrome,
// scrollbar, and empty rows return false.
func (s *Screen) ItemAtLocalY(localY int) (ui.ListItem, bool) {
	if s == nil || s.list == nil {
		return ui.ListItem{}, false
	}
	row := localY - s.listTopRow(s.Bounds())
	if row < 0 || row >= s.list.inner.Scroll().Viewport() {
		return ui.ListItem{}, false
	}
	idx := s.list.inner.Scroll().Offset() + row
	items := s.list.inner.Items()
	if idx < 0 || idx >= len(items) || !items[idx].Selectable() {
		return ui.ListItem{}, false
	}
	return items[idx], true
}

// SelectedItem returns the currently selected visible item.
func (s *Screen) SelectedItem() (ui.ListItem, bool) {
	if s == nil || s.list == nil {
		return ui.ListItem{}, false
	}
	return s.list.inner.Selected()
}

// Handle catches pointer events that miss every child. Wheel anywhere in the
// pane scrolls the list; scrollbar hit slop and capture stay Host-owned.
func (s *Screen) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	r := s.Bounds()
	listY := s.listTopRow(r)
	switch evt.Kind {
	case ui.EventMouseWheel:
		if s.list == nil {
			return ui.EventResult{}
		}
		child := ctx
		child.LocalY = ctx.LocalY - listY
		return s.list.Handle(child, evt)
	case ui.EventMouseClick:
		if evt.MouseClick.Button == tea.MouseLeft {
			return ui.EventResult{Handled: true}
		}
	}
	return ui.EventResult{}
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: s.heading}
}

// sidebarList wraps ui.List with sidebar policy: file-tree row styling,
// select-on-click with activate-on-double-click (picker convention), hover
// tracking gated on host hover, and a viewport that survives the shell's
// per-frame relayout (no EnsureVisible re-anchoring in Layout).
type sidebarList struct {
	ui.Base
	owner       *Screen
	inner       *ui.List
	hoverIdx    int
	lastClickID string
	lastClickAt time.Time
}

func newSidebarList(owner *Screen, tokens *ui.TokenSet) *sidebarList {
	inner := ui.NewList(ListID+"/inner", tokens)
	inner.SetActivateAction(ActionActivate)
	return &sidebarList{
		Base:     ui.NewBase(ListID),
		owner:    owner,
		inner:    inner,
		hoverIdx: -1,
	}
}

func (l *sidebarList) items() []ui.ListItem { return l.inner.Items() }

func (l *sidebarList) SetTokens(ts *ui.TokenSet) { l.inner.SetTokens(ts) }

func (l *sidebarList) tokens() *ui.TokenSet {
	if l.owner != nil {
		return l.owner.tokens
	}
	return nil
}

func (l *sidebarList) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

// Layout deliberately avoids ui.List.Layout: the root host relayouts every
// frame and List.Layout re-anchors the viewport to the selection
// (EnsureVisible), which would snap wheel scrolling back each frame.
// Keyboard navigation still follows the selection via Handle.
func (l *sidebarList) Layout(_ ui.LayoutContext, r ui.Rect) {
	l.SetBounds(r)
	l.inner.Scroll().SetViewport(r.H)
}

func (l *sidebarList) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	r := l.Bounds()
	if r.Empty() || buf == nil {
		return
	}
	items := l.inner.Items()
	off := l.inner.Scroll().Offset()
	vh := l.inner.Scroll().Viewport()
	sel := l.inner.SelectedIndex()
	tokens := l.tokens()
	for row := 0; row < vh && row < r.H; row++ {
		idx := off + row
		if idx < 0 || idx >= len(items) {
			break
		}
		l.paintRow(buf, ox+r.X, oy+r.Y+row, r.W, items[idx], idx, sel, ctx, tokens)
	}
}

func (l *sidebarList) paintRow(buf *surface.CellBuffer, x, y, w int, item ui.ListItem, idx, sel int, ctx ui.PaintContext, tokens *ui.TokenSet) {
	if item.Spacer {
		return
	}
	if item.Separator {
		line := strings.Repeat(string(ui.FirstRune(tokens.Glyphs().BorderH, '─')), max(0, w))
		buf.WriteString(x, y, line, tokens.Color(ui.ColorBorder), cell.NoColor, 0)
		return
	}
	if item.Header {
		label := rendering.TruncateLine(item.Label, max(1, w-2))
		buf.WriteString(x+1, y, label, tokens.Color(ui.ColorMuted), cell.NoColor, cell.AttrBold)
		return
	}
	state := ui.StateDefault
	switch {
	case item.Disabled:
		state = ui.StateDisabled
	case idx == sel && ctx.Focused:
		state = ui.StateFocused
	case idx == sel:
		state = ui.StateSelected
	case idx == l.hoverIdx && ctx.Hovered:
		state = ui.StateHovered
	}
	fg, bg, attr := panelchrome.RowColors(tokens, state)
	if !bg.IsDefault() {
		ui.FillRect(buf, 0, 0, ui.Rect{X: x, Y: y, W: w, H: 1}, cell.BlankCell(bg))
	}
	cx := x + 1
	remain := w - 2
	if item.Depth > 0 && remain > 2 {
		var branch strings.Builder
		for depth := 1; depth < item.Depth; depth++ {
			branch.WriteString("│  ")
		}
		if item.TreeLast {
			branch.WriteString("└─ ")
		} else {
			branch.WriteString("├─ ")
		}
		prefix := rendering.TruncateVisual(branch.String(), remain, "")
		buf.WriteString(cx, y, prefix, tokens.TreeIndentFg(), bg, 0)
		pw := rendering.VisibleWidth(prefix)
		cx += pw
		remain -= pw
	}
	if item.Icon != "" && remain > 2 {
		iconFG := tokens.Color(ui.ColorAccent)
		if item.Disabled {
			iconFG = fg
		}
		icon := item.Icon + " "
		buf.WriteString(cx, y, icon, iconFG, bg, 0)
		iw := rendering.VisibleWidth(icon)
		cx += iw
		remain -= iw
	}
	detail := item.Detail
	detailW := 0
	if detail != "" {
		detailW = rendering.VisibleWidth(detail)
		if detailW+2 > remain {
			detail, detailW = "", 0
		}
	}
	labelBudget := remain
	if detailW > 0 {
		labelBudget = remain - detailW - ui.TableMinGap
	}
	label := rendering.TruncateVisual(item.Label, max(1, labelBudget), "…")
	buf.WriteString(cx, y, label, fg, bg, attr)
	if detailW > 0 {
		buf.WriteString(x+w-1-detailW, y, detail, detailColor(item, tokens), bg, 0)
	}
}

// detailColor maps well-known status details to semantic colors: git status
// letters use the file tree's git palette so the Git view matches tree
// badges; agent/terminal lifecycle states use success/error.
func detailColor(item ui.ListItem, tokens *ui.TokenSet) cell.Color {
	if strings.HasPrefix(item.ID, "git:") {
		switch item.Detail {
		case "A":
			return tokens.TreeGitAdded()
		case "D", "!":
			return tokens.TreeGitDeleted()
		case "U":
			return tokens.TreeGitUntracked()
		case "M", "R":
			return tokens.TreeGitModified()
		}
	}
	switch item.Detail {
	case "running":
		return tokens.Color(ui.ColorSuccess)
	case "exited":
		return tokens.Color(ui.ColorError)
	}
	return tokens.Color(ui.ColorMuted)
}

func (l *sidebarList) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	switch evt.Kind {
	case ui.EventMouseMotion:
		idx := l.rowAt(ctx.LocalY)
		if idx != l.hoverIdx {
			l.hoverIdx = idx
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
		return ui.EventResult{Handled: true}
	case ui.EventMouseWheel:
		res := l.inner.Handle(ctx, evt)
		l.hoverIdx = l.rowAt(ctx.LocalY)
		return res
	case ui.EventMouseClick:
		if evt.MouseClick.Button != tea.MouseLeft {
			return ui.EventResult{}
		}
		res := l.inner.Handle(ctx, evt)
		if res.Action != nil && res.Action.ID == ActionActivate && !activateIsCommand(res.Action) {
			// Resource rows (agents, terminals, changed files) arm on the
			// first click and open on the second, matching the pickers.
			// Command rows are buttons and must fire immediately.
			id := activateID(res.Action)
			now := time.Now()
			if id == "" || l.lastClickID != id || now.Sub(l.lastClickAt) > doubleClickWindow {
				l.lastClickID, l.lastClickAt = id, now
				res.Action = nil
			} else {
				l.lastClickID, l.lastClickAt = "", time.Time{}
			}
		}
		return res
	default:
		return l.inner.Handle(ctx, evt)
	}
}

func activateID(a *ui.Action) string {
	item, ok := a.Payload.(ui.ListItem)
	if !ok {
		return ""
	}
	return item.ID
}

func activateIsCommand(a *ui.Action) bool {
	item, ok := a.Payload.(ui.ListItem)
	return ok && item.Command
}

// rowAt maps a list-local row to a selectable item index, or -1.
func (l *sidebarList) rowAt(localY int) int {
	if localY < 0 || localY >= l.inner.Scroll().Viewport() {
		return -1
	}
	idx := l.inner.Scroll().Offset() + localY
	items := l.inner.Items()
	if idx < 0 || idx >= len(items) || !items[idx].Selectable() {
		return -1
	}
	return idx
}

func (l *sidebarList) Semantics() ui.Semantics { return l.inner.Semantics() }

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
