package activitylist

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const (
	ScreenID    ui.ComponentID = "activitylist"
	ListID      ui.ComponentID = "activitylist/list"
	ScrollbarID ui.ComponentID = "activitylist/scrollbar"
	DividerID   ui.ComponentID = "activitylist/divider"
	TitleID     ui.ComponentID = "activitylist/title"

	ActionActivate = "sidebar.activate"
)

// doubleClickWindow matches the picker screens' click-arming interval.
const doubleClickWindow = 500 * time.Millisecond

// listTop is the row the list starts on (row 0 is the pane heading).
const listTop = 1

const emptyStateText = "No items"

// Screen is the sidebar body for Agents, Terminals, and Git.
type Screen struct {
	ui.Base
	th        *theme.Theme
	tokens    *ui.TokenSet
	title     *paneTitle
	list      *sidebarList
	scrollbar *ui.ScrollbarControl
	split     *ui.SplitModel
	divider   *ui.SplitDivider
	heading   string
}

// paneTitle is the pane heading label. Pointer events pass through to the
// Screen so wheel over the heading still scrolls the list.
type paneTitle struct {
	*ui.Label
}

func (paneTitle) PointerPolicy() ui.PointerPolicy { return ui.PointerPolicyPassthrough }

func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	s := &Screen{
		Base:    ui.NewBase(ScreenID),
		th:      th,
		tokens:  tokens,
		heading: "Files",
	}
	s.title = &paneTitle{Label: ui.NewLabel(TitleID, strings.ToUpper(s.heading), th)}
	s.title.SetColorRole(ui.ColorMuted)
	s.list = newSidebarList(s, tokens)
	s.scrollbar = ui.NewScrollbarControl(ScrollbarID, s.list.inner.Scroll(), tokens)
	s.split = ui.NewSplitModel(ui.Horizontal)
	s.split.SetMinSizes(12, 0)
	s.divider = ui.NewSplitDivider(DividerID, s.split)
	s.divider.SetTokens(tokens)
	return s
}

func (s *Screen) SetHeading(h string) {
	s.heading = h
	if s.title != nil {
		s.title.SetText(strings.ToUpper(h))
	}
}

// SetItems replaces the list data. The shell calls this every frame with a
// freshly built slice, so identical content must be a no-op: selection and
// scroll state survive repaints. When the data really changed, the selection
// is re-matched by item ID and the viewport offset is preserved (clamped).
func (s *Screen) SetItems(items []ui.ListItem) {
	if s.list == nil {
		return
	}
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
	}
	return true
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
	if s.title != nil {
		s.title.SetTokens(ui.NewTokenSet(th))
	}
}

func (s *Screen) Children() []ui.Component {
	out := make([]ui.Component, 0, 4)
	if s.title != nil {
		out = append(out, s.title)
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
	return max(1, s.contentWidth(r)-2)
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
	if s.title != nil {
		s.title.Layout(ui.LayoutContext{}, ui.Rect{X: 1, Y: 0, W: max(1, contentW-2), H: 1})
	}
	listY := s.listTopRow(r)
	listH := max(1, r.H-listY)
	if s.list != nil {
		s.list.Layout(ui.LayoutContext{}, ui.Rect{X: 0, Y: listY, W: s.listWidth(r), H: listH})
	}
	if s.scrollbar != nil && contentW >= 1 {
		s.scrollbar.Layout(ui.LayoutContext{}, ui.Rect{X: contentW - 1, Y: listY, W: 1, H: listH})
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
	if s.list != nil && len(s.list.items()) == 0 {
		text := rendering.TruncateLine(emptyStateText, max(1, s.listWidth(r)-1))
		buf.WriteString(ox+r.X+1, oy+r.Y+s.listTopRow(r), text, muted, bg, 0)
	}
}

// Handle catches pointer events that miss every child: wheel anywhere in the
// pane scrolls the list, and the gap column between rows and scrollbar acts
// as scrollbar track (shelltree convention). Scrollbar drags that started in
// the gap keep routing to the scrollbar until release.
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
	case ui.EventMouseClick, ui.EventMouseMotion, ui.EventMouseRelease:
		if s.scrollbar != nil && (s.scrollbar.IsDragging() || ctx.LocalX >= s.listWidth(r)) {
			child := ctx
			child.LocalX = ctx.LocalX - (s.contentWidth(r) - 1)
			child.LocalY = ctx.LocalY - listY
			return s.scrollbar.Handle(child, evt)
		}
		if evt.Kind == ui.EventMouseClick && evt.MouseClick.Button == tea.MouseLeft {
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
	fg := tokens.Color(ui.ColorFg)
	bg := cell.NoColor
	attr := cell.Attrs(0)
	switch {
	case item.Disabled:
		fg = tokens.Color(ui.ColorMuted)
	case idx == sel && ctx.Focused:
		fg, bg, attr = tokens.TreeCursorFg(), tokens.TreeCursorBg(), cell.AttrBold
	case idx == sel:
		bg, attr = tokens.Color(ui.ColorSelection), cell.AttrBold
	case idx == l.hoverIdx && ctx.Hovered:
		bg = tokens.TreeHoverBg()
	}
	if !bg.IsDefault() {
		ui.FillRect(buf, 0, 0, ui.Rect{X: x, Y: y, W: w, H: 1}, cell.BlankCell(bg))
	}
	cx := x + 1
	remain := w - 2
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
		labelBudget = remain - detailW - 1
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
		if res.Action != nil && res.Action.ID == ActionActivate {
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
