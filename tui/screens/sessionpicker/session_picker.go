// Package sessionpicker provides the native past-sessions browser body.
package sessionpicker

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/sessiondb"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const (
	ScreenID    ui.ComponentID = "sessionpicker"
	TitleID     ui.ComponentID = "sessionpicker/title"
	SearchID    ui.ComponentID = "sessionpicker/search"
	ListID      ui.ComponentID = "sessionpicker/list"
	ScrollbarID ui.ComponentID = "sessionpicker/scrollbar"
	EmptyID     ui.ComponentID = "sessionpicker/empty"
	HintsID     ui.ComponentID = "sessionpicker/hints"
)

// Group is a date-bucketed batch of sessions for section headers.
type Group struct {
	Label    string
	Sessions []sessiondb.SessionMeta
}

// SelectMsg is emitted when the user resumes a session.
type SelectMsg struct {
	Target  *Screen
	Session sessiondb.SessionMeta
}

// CancelMsg is emitted when the picker is dismissed.
type CancelMsg struct{ Target *Screen }

// Screen is the Host-ready session picker: sectioned List + scrollbar + empty/hints.
type Screen struct {
	ui.Base
	th         *theme.Theme
	tokens     *ui.TokenSet
	sessions   []sessiondb.SessionMeta
	filtered   []sessiondb.SessionMeta
	filterOn   bool
	filterText string
	active     bool
	title      *ui.Label
	search     *filterSearch
	list       *pickerList
	scrollbar  *ui.ScrollbarControl
	empty      *ui.Label
	hints      *ui.Label
	focus      func(ui.ComponentID)
}

// New creates a closed session picker screen.
func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	s := &Screen{Base: ui.NewBase(ScreenID), th: th, tokens: tokens}

	s.title = ui.NewLabel(TitleID, "Past sessions", th)
	s.title.SetColorRole(ui.ColorFg)

	search := ui.NewFilterField(SearchID, tokens)
	search.SetPrompt("> ")
	search.SetPlaceholder("Filter sessions")
	search.SetSubmitAction("")
	search.OnChanged(s.applyFilterValue)
	s.search = &filterSearch{Base: ui.NewBase(SearchID), field: search, owner: s}

	list := ui.NewList(ListID, tokens)
	list.SetActivateAction("sessionpicker.resume")
	s.list = &pickerList{Base: ui.NewBase(ListID), list: list, owner: s, lastClick: -1}
	s.scrollbar = ui.NewScrollbarControl(ScrollbarID, list.Scroll(), tokens)

	s.empty = ui.NewLabel(EmptyID, "No sessions yet — chat first, then Ctrl+Alt+S to resume.", th)
	s.empty.SetColorRole(ui.ColorMuted)

	s.hints = ui.NewLabel(HintsID, "↑↓/jk navigate  / filter  Enter resume  Esc close", th)
	s.hints.SetColorRole(ui.ColorMuted)
	return s
}

// Open loads sessions and marks the picker active.
func (s *Screen) Open(sessions []sessiondb.SessionMeta) {
	s.sessions = append(s.sessions[:0], sessions...)
	s.filterOn = false
	s.filterText = ""
	if s.search != nil && s.search.field != nil {
		s.search.field.SetValue("")
	}
	s.active = true
	s.list.lastClick = -1
	s.list.lastClickTime = time.Time{}
	s.sync()
}

// Close deactivates the picker.
func (s *Screen) Close() { s.active = false }

// IsOpen reports whether the picker is active.
func (s *Screen) IsOpen() bool { return s.active }

// Sessions returns the flat session list.
func (s *Screen) Sessions() []sessiondb.SessionMeta { return s.sessions }

// Selected returns the currently highlighted session.
func (s *Screen) Selected() (sessiondb.SessionMeta, bool) {
	item, ok := s.list.list.Selected()
	if !ok {
		return sessiondb.SessionMeta{}, false
	}
	meta, ok := item.Payload.(sessiondb.SessionMeta)
	return meta, ok
}

// SelectedIndex returns the flat session index of the selection, or -1.
func (s *Screen) SelectedIndex() int {
	meta, ok := s.Selected()
	if !ok {
		return -1
	}
	for i, ses := range s.sessions {
		if ses.ID == meta.ID {
			return i
		}
	}
	return -1
}

// SetFocusHandler registers Host focus routing.
func (s *Screen) SetFocusHandler(fn func(ui.ComponentID)) { s.focus = fn }

// FocusList focuses the session list (or the screen when empty).
func (s *Screen) FocusList() {
	if len(s.sessions) == 0 {
		s.focusID(ScreenID)
		return
	}
	s.focusID(ListID)
}

func (s *Screen) focusID(id ui.ComponentID) {
	if s.focus != nil {
		s.focus(id)
	}
}

func (s *Screen) Children() []ui.Component {
	kids := []ui.Component{s.title, s.search}
	if len(s.sessions) == 0 || len(s.filtered) == 0 {
		kids = append(kids, s.empty)
	} else {
		kids = append(kids, s.list, s.scrollbar)
	}
	kids = append(kids, s.hints)
	return kids
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	s.title.SetTokens(tokens)
	s.search.SetTokens(tokens)
	s.list.SetTokens(tokens)
	s.scrollbar.SetTokens(tokens)
	s.empty.SetTokens(tokens)
	s.hints.SetTokens(tokens)
}

func (s *Screen) SetTheme(th *theme.Theme) {
	if th == nil {
		return
	}
	s.th = th
	s.SetTokens(ui.NewTokenSet(th))
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	s.title.Layout(ctx, ui.Rect{W: r.W, H: 1})
	listY := 2
	if s.filterOn && len(s.sessions) > 0 {
		s.search.Layout(ctx, ui.Rect{Y: 1, W: r.W, H: 1})
		listY = 2
	} else {
		// Keep search registered for focus, but give it an empty rect when idle.
		s.search.Layout(ctx, ui.Rect{Y: 1, W: 0, H: 0})
	}
	listH := max(1, r.H-listY-1)
	showEmpty := len(s.sessions) == 0 || len(s.filtered) == 0
	if showEmpty {
		s.empty.Layout(ctx, ui.Rect{Y: listY, W: r.W, H: listH})
	} else {
		s.list.Layout(ctx, ui.Rect{X: 0, Y: listY, W: max(1, r.W-1), H: listH})
		s.scrollbar.Layout(ctx, ui.Rect{X: max(0, r.W-1), Y: listY, W: 1, H: listH})
	}
	s.hints.Layout(ctx, ui.Rect{Y: max(0, r.H-1), W: r.W, H: 1})
}

func (s *Screen) Paint(_ *surface.CellBuffer, _, _ int, _ ui.PaintContext) {}

func (s *Screen) Handle(_ ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() {
		key := evt.Key
		switch {
		case key.Code == tea.KeyEsc:
			if s.filterOn {
				s.deactivateFilter()
				return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus | ui.InvalidateLayout}
			}
			return s.cancel()
		case key.String() == "/" && !s.filterOn && len(s.sessions) > 0:
			s.activateFilter()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus | ui.InvalidateLayout}
		}
	}
	return ui.EventResult{}
}

func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case "sessionpicker.resume":
		item, ok := action.Payload.(ui.ListItem)
		if !ok {
			return ui.EventResult{Handled: true}
		}
		meta, ok := item.Payload.(sessiondb.SessionMeta)
		if !ok || meta.ID == "" {
			return ui.EventResult{Handled: true}
		}
		return s.resume(meta)
	case "sessionpicker.cancel":
		return s.cancel()
	}
	return ui.EventResult{Action: &action}
}

func (s *Screen) Semantics() ui.Semantics {
	n := len(s.sessions)
	return ui.Semantics{Role: ui.RoleDialog, Label: "Session picker", Value: fmt.Sprintf("%d sessions", n)}
}

func (s *Screen) sync() {
	s.filtered = s.filterSessions(s.sessions, s.filterText)
	if len(s.sessions) == 0 {
		s.list.list.SetItems(nil)
		s.title.SetText("Past sessions")
		s.empty.SetText("No sessions yet — chat first, then Ctrl+Alt+S to resume.")
		return
	}
	if len(s.filtered) == 0 {
		s.list.list.SetItems(nil)
		s.title.SetText(fmt.Sprintf("Past sessions (%d)", len(s.sessions)))
		q := strings.TrimSpace(s.filterText)
		if q == "" {
			s.empty.SetText("No sessions to show.")
		} else {
			s.empty.SetText(fmt.Sprintf("No sessions match %q", q))
		}
		return
	}
	groups := BuildSessionGroups(s.filtered)
	items := make([]ui.ListItem, 0, len(s.filtered)+len(groups))
	for _, g := range groups {
		items = append(items, ui.ListItem{
			ID:     "header:" + g.Label,
			Label:  g.Label,
			Header: true,
		})
		for _, ses := range g.Sessions {
			items = append(items, ui.ListItem{
				ID:      ses.ID,
				Label:   sessionLabel(ses),
				Detail:  sessionDetail(ses),
				Payload: ses,
			})
		}
	}
	s.list.list.SetItems(items)
	s.title.SetText(fmt.Sprintf("Past sessions (%d)", len(s.sessions)))
}

func (s *Screen) activateFilter() {
	s.filterOn = true
	s.filterText = ""
	if s.search != nil && s.search.field != nil {
		s.search.field.SetValue("")
	}
	s.sync()
	s.focusID(SearchID)
}

func (s *Screen) deactivateFilter() {
	if !s.filterOn {
		return
	}
	s.filterOn = false
	s.filterText = ""
	if s.search != nil && s.search.field != nil {
		s.search.field.SetValue("")
	}
	s.sync()
	s.FocusList()
}

func (s *Screen) applyFilterValue(v string) {
	s.filterText = v
	s.sync()
}

func (s *Screen) filterSessions(all []sessiondb.SessionMeta, query string) []sessiondb.SessionMeta {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		out := make([]sessiondb.SessionMeta, len(all))
		copy(out, all)
		return out
	}
	var out []sessiondb.SessionMeta
	for _, ses := range all {
		hay := strings.ToLower(sessionLabel(ses) + " " + sessionDetail(ses) + " " + ses.ProjectName + " " + ses.Model + " " + ses.ID)
		if ses.FirstPrompt.Valid {
			hay += " " + strings.ToLower(ses.FirstPrompt.String)
		}
		if ses.LastPrompt.Valid {
			hay += " " + strings.ToLower(ses.LastPrompt.String)
		}
		if strings.Contains(hay, query) {
			out = append(out, ses)
		}
	}
	return out
}

func (s *Screen) resume(meta sessiondb.SessionMeta) ui.EventResult {
	return ui.EventResult{
		Handled: true,
		Cmd:     func() tea.Msg { return SelectMsg{Target: s, Session: meta} },
	}
}

func (s *Screen) cancel() ui.EventResult {
	return ui.EventResult{
		Handled: true,
		Cmd:     func() tea.Msg { return CancelMsg{Target: s} },
	}
}

// BuildSessionGroups buckets sessions by calendar day (today / yesterday / date).
// Sessions are assumed most-recent-first as returned by ListSessions.
func BuildSessionGroups(sessions []sessiondb.SessionMeta) []Group {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	yesterday := today.AddDate(0, 0, -1)

	var groups []Group
	var currentKey string
	var currentLabel string
	var currentSessions []sessiondb.SessionMeta

	for _, ses := range sessions {
		var key, label string
		if ses.CreatedAt > 0 {
			t := time.Unix(0, ses.CreatedAt)
			day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
			key = day.Format("2006-01-02")
			switch {
			case day.Equal(today):
				label = "today"
			case day.Equal(yesterday):
				label = "yesterday"
			default:
				label = day.Format("Mon Jan 2, 2006")
			}
		} else {
			key = "unknown"
			label = "unknown"
		}

		if key != currentKey {
			if len(currentSessions) > 0 {
				groups = append(groups, Group{Label: currentLabel, Sessions: currentSessions})
			}
			currentKey = key
			currentLabel = label
			currentSessions = nil
		}
		currentSessions = append(currentSessions, ses)
	}
	if len(currentSessions) > 0 {
		groups = append(groups, Group{Label: currentLabel, Sessions: currentSessions})
	}
	return groups
}

func sessionLabel(s sessiondb.SessionMeta) string {
	title := nullString(s.FirstPrompt)
	if title == "" {
		title = nullString(s.LastPrompt)
	}
	if title == "" {
		title = s.ProjectName
	}
	if title == "" {
		title = "untitled session"
	}
	title = strings.TrimSpace(strings.ReplaceAll(title, "\n", " "))
	return rendering.TruncateLine(title, 36)
}

func sessionDetail(s sessiondb.SessionMeta) string {
	when := ""
	if s.CreatedAt > 0 {
		when = time.Unix(0, s.CreatedAt).Format("15:04")
	}
	parts := []string{fmt.Sprintf("%d turns", s.TurnCount)}
	if s.Model != "" {
		parts = append(parts, s.Model)
	}
	if when != "" {
		parts = append(parts, when)
	}
	return strings.Join(parts, " · ")
}

func nullString(v sql.NullString) string {
	if v.Valid {
		return v.String
	}
	return ""
}

type pickerList struct {
	ui.Base
	list          *ui.List
	owner         *Screen
	lastClick     int
	lastClickTime time.Time
}

func (p *pickerList) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return p.list.Measure(ctx, cs)
}
func (p *pickerList) Layout(ctx ui.LayoutContext, r ui.Rect) {
	p.SetBounds(r)
	p.list.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}
func (p *pickerList) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	p.list.Paint(buf, ox+p.Bounds().X, oy+p.Bounds().Y, ctx)
}
func (p *pickerList) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if p.owner == nil {
		return p.list.Handle(ctx, evt)
	}
	if evt.IsKey() {
		key := evt.Key
		switch {
		case key.Code == tea.KeyEsc:
			if p.owner.filterOn {
				p.owner.deactivateFilter()
				return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus | ui.InvalidateLayout}
			}
			return p.owner.cancel()
		case key.String() == "/" && !p.owner.filterOn:
			p.owner.activateFilter()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus | ui.InvalidateLayout}
		case key.String() == "k":
			return p.list.Handle(ctx, ui.KeyEvent(tea.KeyPressMsg{Code: tea.KeyUp}))
		case key.String() == "j":
			return p.list.Handle(ctx, ui.KeyEvent(tea.KeyPressMsg{Code: tea.KeyDown}))
		}
	}
	result := p.list.Handle(ctx, evt)
	if evt.Kind == ui.EventMouseClick && evt.MouseClick.Button == tea.MouseLeft && result.Action != nil {
		index := p.list.SelectedIndex()
		now := time.Now()
		if p.lastClick != index || now.Sub(p.lastClickTime) > 500*time.Millisecond {
			// First click: select only (parity with legacy picker).
			p.lastClick, p.lastClickTime = index, now
			result.Action = nil
			return result
		}
		// Double-click: keep activate action → resume.
		p.lastClick, p.lastClickTime = -1, time.Time{}
	}
	return result
}
func (p *pickerList) Semantics() ui.Semantics       { return p.list.Semantics() }
func (p *pickerList) SetTokens(tokens *ui.TokenSet) { p.list.SetTokens(tokens) }

// filterSearch paints/handles the filter field and re-applies it to the list
// on every edit (FilterField.OnChanged, see New).
type filterSearch struct {
	ui.Base
	field *ui.FilterField
	owner *Screen
}

func (f *filterSearch) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return f.field.Measure(ctx, cs)
}
func (f *filterSearch) Layout(ctx ui.LayoutContext, r ui.Rect) {
	f.SetBounds(r)
	f.field.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}
func (f *filterSearch) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	f.field.Paint(buf, ox+f.Bounds().X, oy+f.Bounds().Y, ctx)
}
func (f *filterSearch) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() && evt.Key.Code == tea.KeyEsc {
		f.owner.deactivateFilter()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus | ui.InvalidateLayout}
	}
	return f.field.Handle(ctx, evt)
}
func (f *filterSearch) Semantics() ui.Semantics       { return f.field.Semantics() }
func (f *filterSearch) SetTokens(tokens *ui.TokenSet) { f.field.SetTokens(tokens) }

var _ ui.Component = (*Screen)(nil)
var _ ui.Parent = (*Screen)(nil)
var _ ui.ActionHandler = (*Screen)(nil)
