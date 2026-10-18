// Package workspaces provides the retained, searchable workspace switcher.
package workspaces

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/screens/activitybar"
	"crude/tui/screens/panelchrome"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

const (
	ScreenID      ui.ComponentID = "workspaces"
	SearchID      ui.ComponentID = "workspaces/search"
	ListID        ui.ComponentID = "workspaces/list"
	DividerID     ui.ComponentID = "workspaces/divider"
	ActionOpen                   = "workspaces.open"
	ActionNew                    = "workspaces.new"
	ActionRefresh                = "workspaces.refresh"
)

// Request binds an intent to the exact mounted screen, not a list index.
type Request struct {
	Screen *Screen
	Path   string
}
type State struct {
	Filter, Selection string
	Offset            int
}

type Screen struct {
	ui.Base
	tokens       *ui.TokenSet
	search       *searchField
	list         *workspaceList
	bar          *ui.ScrollbarControl
	divider      *ui.SplitDivider
	split        *ui.SplitModel
	icon         string
	current      string
	infos        []workspace.WorkspaceInfo
	loading      bool
	loadError    string
	switchStatus string
	focus        func(ui.ComponentID) bool
}

func New(th *theme.Theme) *Screen {
	s := &Screen{Base: ui.NewBase(ScreenID), tokens: ui.NewTokenSet(th), icon: activitybar.ViewWorkspaces.Icon(panelchrome.NerdFont(th))}
	f := ui.NewFilterField(SearchID, s.tokens)
	f.SetPlaceholder("Search workspaces")
	f.SetPrompt(ui.SearchGlyphEnd(s.tokens))
	f.SetPromptPlacement(ui.PromptEnd)
	f.SetSubmitAction("")
	f.OnChanged(func(string) {
		s.list.lastClickID, s.list.lastClickAt = "", time.Time{}
		s.filter()
		s.list.inner.SelectIndex(0)
		s.list.inner.Scroll().ScrollTo(0)
	})
	s.search = &searchField{Base: ui.NewBase(SearchID), owner: s, field: f}
	l := ui.NewVirtualList(ListID, s.tokens)
	l.SetSelectionAnchorsScroll(false)
	l.SetOpaqueRowFill(false)
	s.list = &workspaceList{Base: ui.NewBase(ListID), owner: s, inner: l}
	l.SetRowPainter(s.paintRow)
	s.bar = ui.NewScrollbarControl("workspaces/scrollbar", l.Scroll(), s.tokens)
	s.split = ui.NewSplitModel(ui.Horizontal)
	s.split.SetMinSizes(12, 0)
	s.divider = ui.NewSplitDivider(DividerID, s.split)
	s.SetTokens(s.tokens)
	return s
}
func (s *Screen) SetFocusHandler(f func(ui.ComponentID) bool) { s.focus = f }
func (s *Screen) focusID(id ui.ComponentID) {
	if s.focus != nil {
		s.focus(id)
	}
}
func (s *Screen) OwnsFocus(id ui.ComponentID) bool {
	return id == ScreenID || strings.HasPrefix(string(id), "workspaces/")
}
func (s *Screen) PreferredFocus() ui.ComponentID { return ListID }
func (s *Screen) Query() string                  { return s.search.field.Value() }
func (s *Screen) SetSplitMaxFirst(n int)         { s.split.SetMaxFirst(n) }
func (s *Screen) SetTokens(t *ui.TokenSet) {
	s.tokens = t
	s.search.field.SetTokens(t)
	s.search.field.SetPrompt(ui.SearchGlyphEnd(t))
	s.search.field.SetPromptColor(t.Color(ui.ColorAccent))
	s.search.field.SetBackgroundColor(t.Color(ui.ColorSubPaneBg))
	s.list.inner.SetTokens(t)
	s.bar.SetTokens(t)
	s.divider.SetTokens(t)
}
func (s *Screen) SetTheme(th *theme.Theme) {
	s.icon = activitybar.ViewWorkspaces.Icon(panelchrome.NerdFont(th))
	s.SetTokens(ui.NewTokenSet(th))
}
func (s *Screen) SetStatus(loading bool, err string) {
	s.loading, s.loadError = loading, err
}
func (s *Screen) SetSwitchStatus(status string) { s.switchStatus = status }
func (s *Screen) SetData(current string, infos []workspace.WorkspaceInfo) {
	s.list.lastClickID, s.list.lastClickAt = "", time.Time{}
	s.current = current
	s.infos = append([]workspace.WorkspaceInfo(nil), infos...)
	s.filter()
}
func (s *Screen) HasPath(path string) bool {
	for _, info := range s.infos {
		if info.ProjectDir == path {
			return true
		}
	}
	return false
}
func (s *Screen) ViewState() State {
	state := State{Filter: s.Query(), Offset: s.list.inner.Scroll().Offset()}
	if item, ok := s.list.inner.Selected(); ok {
		state.Selection = item.ID
	}
	return state
}
func (s *Screen) RestoreViewState(state State) {
	s.search.field.SetValue(state.Filter)
	s.filter()
	for i, item := range s.list.inner.Items() {
		if item.ID == state.Selection {
			s.list.inner.SelectIndex(i)
			break
		}
	}
	s.list.inner.Scroll().ScrollTo(state.Offset)
}
func (s *Screen) filter() {
	state := s.ViewState()
	query := strings.ToLower(strings.TrimSpace(s.Query()))
	items := make([]ui.VirtItem, 0, len(s.infos)+2)
	if query == "" {
		items = append(items, ui.VirtItem{ID: ActionNew, Height: 1, Icon: "+", Label: "New Workspace…"}, ui.VirtItem{ID: ActionRefresh, Height: 1, Icon: "↻", Label: "Refresh"})
	}
	for _, info := range s.infos {
		name := workspace.WorkspaceDisplayName(info)
		if query != "" && !strings.Contains(strings.ToLower(name+" "+info.ProjectDir), query) {
			continue
		}
		items = append(items, ui.VirtItem{ID: info.ProjectDir, Height: 3, Label: name, Detail: info.ProjectDir, Payload: info})
	}
	s.list.inner.SetItems(items)
	for i, item := range items {
		if item.ID == state.Selection {
			s.list.inner.SelectIndex(i)
			break
		}
	}
	s.list.inner.Scroll().ScrollTo(state.Offset)
}
func (s *Screen) Children() []ui.Component {
	return []ui.Component{s.search, s.list, s.bar, s.divider}
}
func (s *Screen) header() panelchrome.Title {
	return panelchrome.Title{Icon: s.icon, Badge: fmt.Sprint(len(s.infos))}
}
func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}
func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	w, h := max(0, r.W-1), max(0, r.H)
	rowW := panelchrome.BodyWidth(w)
	field := s.header().FieldRect(rowW)
	field.X = min(field.X, w)
	field.W, field.H = min(field.W, max(0, rowW-field.X)), min(field.H, h)
	s.search.Layout(ctx, field)
	listY := min(panelchrome.HeaderRows, h)
	listH := max(0, h-listY-1)
	barW := min(ui.ScrollbarWidth+1, w)
	s.list.Layout(ctx, ui.Rect{Y: listY, W: rowW, H: listH})
	s.bar.Layout(ctx, ui.Rect{X: w - barW, Y: listY, W: barW, H: listH})
	if !s.split.Dragging() {
		s.split.SetFirstSize(w)
	}
	s.divider.SetCurrentFirst(w)
	s.divider.Layout(ctx, ui.Rect{X: w, W: min(1, r.W), H: h})
}
func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	r := s.Bounds()
	if r.Empty() {
		return
	}
	t := s.tokens
	bg := t.Color(ui.ColorBg)
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	w := max(0, r.W-1)
	panelchrome.PaintTitle(buf, ox, oy, ui.Rect{X: r.X, Y: r.Y, W: panelchrome.BodyWidth(w), H: r.H}, t, s.header())
	status := "Enter opens · Right-click actions"
	role := ui.ColorMuted
	if s.switchStatus != "" {
		status = s.switchStatus
		role = ui.ColorWarning
	} else if s.loading {
		status = "Refreshing workspaces…"
	} else if s.loadError != "" {
		status = "Couldn’t refresh · " + s.loadError
		role = ui.ColorError
	}
	if (len(s.infos) == 0 || len(s.list.inner.Items()) == 0) && s.list.Bounds().H > 0 {
		text := "No recent workspaces"
		y := s.list.Bounds().Y
		if s.Query() == "" {
			y += 2
		}
		if s.Query() != "" {
			text = "No matching workspaces"
		}
		if y < s.list.Bounds().Y+s.list.Bounds().H {
			buf.WriteString(ox+r.X+min(1, w), oy+r.Y+y, rendering.TruncateVisual(text, max(0, w-2), "…"), t.Color(ui.ColorMuted), bg, 0)
		}
	}
	if r.H > 4 {
		buf.WriteString(ox+r.X+min(1, w), oy+r.Y+r.H-1, rendering.TruncateVisual(status, max(0, w-2), "…"), t.Color(role), bg, 0)
	}
}
func (s *Screen) paintRow(buf *surface.CellBuffer, x, y, w int, item ui.VirtItem, row int, state ui.ControlState, ctx ui.PaintContext, t *ui.TokenSet) {
	if row == 2 || w <= 0 {
		return
	}
	if state == ui.StateHovered && !ctx.Hovered {
		state = ui.StateDefault
	}
	if item.ID == ActionRefresh && s.loading {
		state = ui.StateDisabled
	}
	fg, bg, attr := panelchrome.RowColors(t, state)
	ui.FillRect(buf, 0, 0, ui.Rect{X: x, Y: y, W: w, H: 1}, cell.BlankCell(bg))
	text := item.Label
	if item.Icon != "" && w > 2 {
		buf.WriteString(x+1, y, item.Icon, t.Color(ui.ColorAccent), bg, 0)
		x += 2
		w -= 2
	}
	if row == 1 {
		text = item.Detail
		if state != ui.StateFocused {
			fg = t.Color(ui.ColorMuted)
		}
		attr = 0
	}
	if row == 0 && samePath(item.ID, s.current) {
		badge := "Current"
		if w > len(badge)+8 {
			buf.WriteString(x+w-len(badge)-1, y, badge, t.Color(ui.ColorAccent), bg, 0)
			w -= len(badge) + 2
		}
	}
	buf.WriteString(x+min(1, w), y, rendering.TruncateMiddleVisual(text, max(0, w-2), "…"), fg, bg, attr)
}
func samePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
func (s *Screen) ItemAt(x, y int) (workspace.WorkspaceInfo, bool) {
	r := s.list.Bounds()
	if !r.Contains(x, y) {
		return workspace.WorkspaceInfo{}, false
	}
	row := s.list.inner.Scroll().Offset() + y - r.Y
	i := s.list.inner.ItemIndexAtRow(row)
	items := s.list.inner.Items()
	if i < 0 || i >= len(items) || s.list.inner.ItemStartRow(i)+2 == row {
		return workspace.WorkspaceInfo{}, false
	}
	info, ok := items[i].Payload.(workspace.WorkspaceInfo)
	return info, ok
}
func (s *Screen) Handle(_ ui.EventContext, e ui.Event) ui.EventResult {
	if e.Kind == ui.EventMouseWheel {
		return s.list.inner.Handle(ui.EventContext{}, e)
	}
	return ui.EventResult{}
}
func (s *Screen) HandleAction(_ ui.EventContext, a ui.Action) ui.EventResult {
	if a.ID == ActionOpen {
		return ui.EventResult{Handled: true, Action: &a}
	}
	return ui.EventResult{Handled: true, Action: &ui.Action{ID: a.ID, Payload: Request{Screen: s}}}
}
func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Recent Workspaces", Value: fmt.Sprintf("%d workspaces", len(s.infos))}
}

type searchField struct {
	ui.Base
	owner *Screen
	field *ui.FilterField
}

func (f *searchField) TextEditor() ui.TextClipboardTarget { return f.field.TextEditor() }
func (f *searchField) Layout(ctx ui.LayoutContext, r ui.Rect) {
	f.SetBounds(r)
	f.field.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}
func (f *searchField) Paint(b *surface.CellBuffer, x, y int, c ui.PaintContext) {
	f.field.Paint(b, x+f.Bounds().X, y+f.Bounds().Y, c)
}
func (f *searchField) Semantics() ui.Semantics { return f.field.Semantics() }
func (f *searchField) Handle(ctx ui.EventContext, e ui.Event) ui.EventResult {
	if e.IsKey() {
		switch e.Key.Code {
		case tea.KeyDown, tea.KeyUp, tea.KeyPgDown, tea.KeyPgUp, tea.KeyHome, tea.KeyEnd, tea.KeyEnter:
			return f.owner.list.Handle(ctx, e)
		}
	}
	return f.field.Handle(ctx, e)
}

type workspaceList struct {
	ui.Base
	owner       *Screen
	inner       *ui.VirtualList
	lastClickID string
	lastClickAt time.Time
}

func (l *workspaceList) Layout(ctx ui.LayoutContext, r ui.Rect) {
	l.SetBounds(r)
	l.inner.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}
func (l *workspaceList) Paint(b *surface.CellBuffer, x, y int, c ui.PaintContext) {
	l.inner.Paint(b, x+l.Bounds().X, y+l.Bounds().Y, c)
}
func (l *workspaceList) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleList, Label: "Recent Workspaces"}
}
func (l *workspaceList) Handle(ctx ui.EventContext, e ui.Event) ui.EventResult {
	if e.IsKey() || e.Kind == ui.EventMouseWheel {
		l.lastClickID, l.lastClickAt = "", time.Time{}
	}
	if e.Kind == ui.EventMouseClick {
		if e.MouseClick.Button != tea.MouseLeft {
			return ui.EventResult{}
		}
		row := l.inner.Scroll().Offset() + ctx.LocalY
		i := l.inner.ItemIndexAtRow(row)
		if i < 0 || i >= len(l.inner.Items()) || row-l.inner.ItemStartRow(i) == 2 {
			return ui.EventResult{}
		}
		l.inner.SelectIndex(i)
		l.owner.focusID(ListID)
		item := l.inner.Items()[i]
		if item.ID == ActionNew || item.ID == ActionRefresh {
			return l.activate(item)
		}
		now := time.Now()
		if l.lastClickID != item.ID || now.Sub(l.lastClickAt) > 500*time.Millisecond {
			l.lastClickID, l.lastClickAt = item.ID, now
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
		l.lastClickID, l.lastClickAt = "", time.Time{}
		return l.activate(item)
	}
	res := l.inner.Handle(ctx, e)
	if e.IsKey() && res.Handled {
		l.inner.EnsureSelectedVisible()
	}
	if res.Action != nil {
		if item, ok := l.inner.Selected(); ok {
			return l.activate(item)
		}
	}
	if !res.Handled && (e.IsKey() || e.Kind == ui.EventCustom) {
		l.owner.focusID(SearchID)
		return l.owner.search.field.Handle(ctx, e)
	}
	return res
}

func (l *workspaceList) activate(item ui.VirtItem) ui.EventResult {
	id, path := ActionOpen, item.ID
	if item.ID == ActionNew || item.ID == ActionRefresh {
		id, path = item.ID, ""
	}
	if id == ActionRefresh && l.owner.loading {
		return ui.EventResult{Handled: true}
	}
	return ui.EventResult{Handled: true, Action: &ui.Action{ID: id, Payload: Request{Screen: l.owner, Path: path}}}
}
