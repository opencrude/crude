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

// SessionStatus is the lifecycle state shown on a session row.
type SessionStatus uint8

const (
	// StatusTerminated: no live agent owns the session (stopped/closed).
	StatusTerminated SessionStatus = iota
	// StatusWorking: a live agent for the session is running a turn.
	StatusWorking
	// StatusIdle: a live agent owns the session but is between turns.
	StatusIdle
	// StatusMinimized: live, but its window tab was closed (minimized).
	StatusMinimized
)

func (s SessionStatus) String() string {
	switch s {
	case StatusWorking:
		return "working"
	case StatusIdle:
		return "idle"
	case StatusMinimized:
		return "minimized"
	default:
		return "terminated"
	}
}

// LiveInfo carries runtime details for one session, supplied by the
// viewmodel/work boundary: lifecycle status and sub-agent count.
type LiveInfo struct {
	Status    SessionStatus
	SubAgents int
}

// Merge keeps the strongest status seen for a session with several live
// nodes: working wins over minimized, minimized over idle, anything over
// terminated. The larger sub-agent count wins (persisted vs live tree).
func (i LiveInfo) Merge(other LiveInfo) LiveInfo {
	out := i
	if other.Status > i.Status {
		out.Status = other.Status
	}
	if other.SubAgents > i.SubAgents {
		out.SubAgents = other.SubAgents
	}
	return out
}

// SelectMsg is emitted when the user resumes a session.
type SelectMsg struct {
	Target  *Screen
	Session sessiondb.SessionMeta
}

// CancelMsg is emitted when the picker is dismissed.
type CancelMsg struct{ Target *Screen }

// Screen is the Host-ready session picker: search + sectioned List +
// scrollbar + empty/hints. The search row is always visible so filtering is
// discoverable without a hidden "/" shortcut.
type Screen struct {
	ui.Base
	th         *theme.Theme
	tokens     *ui.TokenSet
	sessions   []sessiondb.SessionMeta
	filtered   []sessiondb.SessionMeta
	live       map[string]LiveInfo
	spinFrame  int
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
	search.SetPrompt(ui.SearchGlyphEnd(tokens))
	search.SetPromptPlacement(ui.PromptEnd)
	search.SetPlaceholder("Type to search sessions…")
	search.SetSubmitAction("")
	search.OnChanged(s.applyFilterValue)
	s.search = &filterSearch{Base: ui.NewBase(SearchID), field: search, owner: s}

	list := ui.NewList(ListID, tokens)
	list.SetActivateAction("sessionpicker.resume")
	s.list = &pickerList{Base: ui.NewBase(ListID), list: list, owner: s, lastClick: -1}
	s.scrollbar = ui.NewScrollbarControl(ScrollbarID, list.Scroll(), tokens)
	s.scrollbar.SetSurfaceBackground(ui.ColorPopupBg)

	s.empty = ui.NewLabel(EmptyID, "No sessions yet — chat first, then Ctrl+Alt+S to resume.", th)
	s.empty.SetColorRole(ui.ColorMuted)

	s.hints = ui.NewLabel(HintsID, "type to filter · ↑↓/jk sessions · Enter resume · Esc close", th)
	s.hints.SetColorRole(ui.ColorMuted)
	return s
}

// Open loads sessions and marks the picker active. Runtime details reset;
// the caller refreshes them via SetLiveInfo right after.
func (s *Screen) Open(sessions []sessiondb.SessionMeta) {
	s.sessions = append(s.sessions[:0], sessions...)
	s.live = nil
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

// SetLiveInfo supplies per-session runtime details (status, sub-agent
// counts). The viewmodel refreshes it on open and on ticks while running
// agents change state. Rows resync with the selection preserved.
func (s *Screen) SetLiveInfo(live map[string]LiveInfo) {
	s.live = live
	s.sync()
}

// NeedsAnimation reports whether any listed session is running a turn, so
// the host keeps repainting the spinner icon on ticks.
func (s *Screen) NeedsAnimation() bool {
	for _, info := range s.live {
		if info.Status == StatusWorking {
			return true
		}
	}
	return false
}

// SetSpinFrame advances the running-spinner icon. Rows rebuild only on
// change; stopped sessions keep static icons and never animate.
func (s *Screen) SetSpinFrame(frame int) {
	if ui.ActivityDot(frame) == ui.ActivityDot(s.spinFrame) || !s.NeedsAnimation() {
		return
	}
	s.spinFrame = frame
	s.sync()
}

func (s *Screen) rowIcon(id string) string {
	if s.live == nil {
		return ""
	}
	switch s.live[id].Status {
	case StatusWorking:
		return ui.ActivityDot(s.spinFrame)
	case StatusIdle:
		return "●"
	case StatusMinimized:
		return "▪"
	default:
		return "○"
	}
}

// SetFocusHandler registers Host focus routing.
func (s *Screen) SetFocusHandler(fn func(ui.ComponentID)) { s.focus = fn }

// FocusSearch focuses the search input so typing filters immediately (the
// always-visible row). With no sessions there is nothing to search and focus
// lands on the screen itself.
func (s *Screen) FocusSearch() {
	if len(s.sessions) == 0 {
		s.focusID(ScreenID)
		return
	}
	s.focusID(SearchID)
}

// FocusList focuses the session list (or the screen when empty).
func (s *Screen) FocusList() {
	if len(s.sessions) == 0 {
		s.focusID(ScreenID)
		return
	}
	s.focusID(ListID)
}

// handoffToList moves focus to the session list and replays the navigation
// key, so the same keypress that leaves the search field also advances the
// list cursor.
func (s *Screen) handoffToList(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	s.FocusList()
	res := s.list.Handle(ctx, evt)
	res.Handled = true
	res.Invalidate |= ui.InvalidatePaint | ui.InvalidateFocus | ui.InvalidateLayout
	return res
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
	if s.search != nil && s.search.field != nil {
		s.search.field.SetPrompt(ui.SearchGlyphEnd(tokens))
		s.search.field.SetPromptPlacement(ui.PromptEnd)
	}
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
	// Search row is permanent: always give it a real rect so users can see
	// (and click) the field without knowing a keyboard shortcut.
	s.search.Layout(ctx, ui.Rect{Y: 1, W: r.W, H: 1})
	listY := 2
	listH := max(1, r.H-listY-1)
	showEmpty := len(s.sessions) == 0 || len(s.filtered) == 0
	if showEmpty {
		s.empty.Layout(ctx, ui.Rect{Y: listY, W: r.W, H: listH})
	} else {
		barW := 0
		if s.scrollbar.NeededForViewport(listH) {
			barW = min(ui.ScrollbarWidth, r.W)
		}
		s.list.Layout(ctx, ui.Rect{X: 0, Y: listY, W: max(1, r.W-barW), H: listH})
		s.scrollbar.Layout(ctx, ui.Rect{X: max(0, r.W-barW), Y: listY, W: barW, H: listH})
	}
	s.hints.Layout(ctx, ui.Rect{Y: max(0, r.H-1), W: r.W, H: 1})
}

func (s *Screen) Paint(_ *surface.CellBuffer, _, _ int, _ ui.PaintContext) {}

func (s *Screen) Handle(_ ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() {
		key := evt.Key
		switch {
		case key.Code == tea.KeyEsc:
			return s.cancel()
		case key.String() == "/":
			s.FocusSearch()
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
	query := strings.TrimSpace(s.filterText)
	if len(s.sessions) == 0 {
		s.list.list.SetItems(nil)
		s.title.SetText("Past sessions")
		s.empty.SetText("No sessions yet — chat first, then Ctrl+Alt+S to resume.")
		return
	}
	if len(s.filtered) == 0 {
		s.list.list.SetItems(nil)
		s.title.SetText(s.titleText(query))
		if query == "" {
			s.empty.SetText("No sessions to show.")
		} else {
			s.empty.SetText(fmt.Sprintf("No sessions match %q", query))
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
				Icon:    s.rowIcon(ses.ID),
				Detail:  s.sessionDetail(ses),
				Payload: ses,
			})
		}
	}
	// SetItemsStable keeps the user's selection AND scroll offset anchored
	// across resyncs (spinner ticks rebuild every row ~10x/second while any
	// agent is working; SetItems would snap both back to the top).
	s.list.list.SetItemsStable(items)
	s.title.SetText(s.titleText(query))
}

// titleText shows matched/total while a query narrows the list.
func (s *Screen) titleText(query string) string {
	if strings.TrimSpace(query) == "" {
		return fmt.Sprintf("Past sessions (%d)", len(s.sessions))
	}
	return fmt.Sprintf("Past sessions (%d/%d)", len(s.filtered), len(s.sessions))
}

func (s *Screen) applyFilterValue(v string) {
	s.filterText = v
	s.sync()
}

// filterSessions keeps sessions whose searchable text contains EVERY
// whitespace-separated term (case-insensitive AND matching), so "beta claude"
// narrows by title word and model together.
func (s *Screen) filterSessions(all []sessiondb.SessionMeta, query string) []sessiondb.SessionMeta {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		out := make([]sessiondb.SessionMeta, len(all))
		copy(out, all)
		return out
	}
	terms := strings.Fields(query)
	var out []sessiondb.SessionMeta
	for _, ses := range all {
		hay := strings.ToLower(sessionLabel(ses) + " " + s.sessionDetail(ses) + " " + ses.ProjectName + " " + ses.Model + " " + ses.ID)
		if ses.FirstPrompt.Valid {
			hay += " " + strings.ToLower(ses.FirstPrompt.String)
		}
		if ses.LastPrompt.Valid {
			hay += " " + strings.ToLower(ses.LastPrompt.String)
		}
		matched := true
		for _, term := range terms {
			if !strings.Contains(hay, term) {
				matched = false
				break
			}
		}
		if matched {
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
	if s.AgentName.String != "" {
		title = s.AgentName.String + " — " + title
	}
	return rendering.TruncateLine(title, 36)
}

// sessionDetail composes the right-aligned row metadata: user prompts, model
// rounds, sub-agent
// count, lifecycle status, model, and start time.
func (s *Screen) sessionDetail(ses sessiondb.SessionMeta) string {
	when := ""
	if ses.CreatedAt > 0 {
		when = time.Unix(0, ses.CreatedAt).Format("15:04")
	}
	promptLabel := "prompts"
	if ses.TurnCount == 1 {
		promptLabel = "prompt"
	}
	parts := []string{fmt.Sprintf("%d %s", ses.TurnCount, promptLabel)}
	if ses.RoundCount > 0 {
		roundLabel := "rounds"
		if ses.RoundCount == 1 {
			roundLabel = "round"
		}
		parts = append(parts, fmt.Sprintf("%d %s", ses.RoundCount, roundLabel))
	}
	if info := s.live[ses.ID]; info.SubAgents > 0 {
		label := "sub-agents"
		if info.SubAgents == 1 {
			label = "sub-agent"
		}
		parts = append(parts, fmt.Sprintf("%d %s", info.SubAgents, label))
	}
	// The status word appears once runtime info was supplied; without it the
	// picker keeps its plain past-sessions look.
	if s.live != nil {
		parts = append(parts, s.live[ses.ID].Status.String())
	}
	if ses.Model != "" {
		parts = append(parts, ses.Model)
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
			return p.owner.cancel()
		case key.String() == "/":
			p.owner.FocusSearch()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus | ui.InvalidateLayout}
		case key.Code == tea.KeyUp || key.String() == "k":
			if p.atTopSelectable() {
				// First row already selected: hand focus back to search.
				p.owner.FocusSearch()
				return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus | ui.InvalidateLayout}
			}
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

// atTopSelectable reports whether the cursor sits on the first selectable row
// so an upward step can cycle focus back to the search field.
func (p *pickerList) atTopSelectable() bool {
	items := p.list.Items()
	for i := range items {
		if items[i].Selectable() {
			return p.list.SelectedIndex() == i
		}
	}
	return false
}
func (p *pickerList) Semantics() ui.Semantics       { return p.list.Semantics() }
func (p *pickerList) SetTokens(tokens *ui.TokenSet) { p.list.SetTokens(tokens) }

// filterSearch paints/handles the always-visible search field and re-applies
// it to the list on every edit (FilterField.OnChanged, see New).
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
	if evt.IsKey() {
		switch evt.Key.Code {
		case tea.KeyDown, tea.KeyPgDown:
			// Leave the field and enter the list in one keypress.
			return f.owner.handoffToList(ctx, evt)
		case tea.KeyUp, tea.KeyPgUp:
			// The field is the top boundary; swallow upward navigation here
			// so it is not eaten by the text editor as a cursor move.
			return ui.EventResult{Handled: true}
		case tea.KeyEnter:
			// Palette-style: Enter activates whatever the list has selected.
			if meta, ok := f.owner.Selected(); ok {
				return f.owner.resume(meta)
			}
			return ui.EventResult{Handled: true}
		case tea.KeyEsc:
			res := f.field.Handle(ctx, evt) // clears a non-empty query first
			if !res.Handled {
				return f.owner.cancel()
			}
			return ui.EventResult{Handled: true, Invalidate: res.Invalidate | ui.InvalidateLayout | ui.InvalidatePaint}
		}
	}
	return f.field.Handle(ctx, evt)
}
func (f *filterSearch) Semantics() ui.Semantics       { return f.field.Semantics() }
func (f *filterSearch) SetTokens(tokens *ui.TokenSet) { f.field.SetTokens(tokens) }

var _ ui.Component = (*Screen)(nil)
var _ ui.Parent = (*Screen)(nil)
var _ ui.ActionHandler = (*Screen)(nil)
