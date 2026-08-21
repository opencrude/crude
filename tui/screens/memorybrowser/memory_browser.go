// Package memorybrowser provides the workspace memory browser body: search
// over durable memories with pin/forget actions and a provenance detail pane.
package memorybrowser

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/memory"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const (
	ScreenID    ui.ComponentID = "memorybrowser"
	TitleID     ui.ComponentID = "memorybrowser/title"
	SearchID    ui.ComponentID = "memorybrowser/search"
	ListID      ui.ComponentID = "memorybrowser/list"
	ScrollbarID ui.ComponentID = "memorybrowser/scrollbar"
	EmptyID     ui.ComponentID = "memorybrowser/empty"
	HintsID     ui.ComponentID = "memorybrowser/hints"
)

const detailHeight = 6

// PinMsg asks the viewmodel to toggle a memory's pinned flag.
type PinMsg struct {
	Target *Screen
	ID     string
	Pinned bool
}

// ForgetMsg asks the viewmodel to supersede (forget) a memory.
type ForgetMsg struct {
	Target *Screen
	ID     string
}

// CancelMsg is emitted when the browser is dismissed.
type CancelMsg struct{ Target *Screen }

// Screen is the Host-ready memory browser: search field + list + detail.
type Screen struct {
	ui.Base
	th      *theme.Theme
	tokens  *ui.TokenSet
	records []memory.ScoredRecord
	// searchFn runs the workspace query (fast local SQLite; synchronous).
	searchFn func(query string) []memory.ScoredRecord

	title     *ui.Label
	search    *searchField
	list      *browserList
	scrollbar *ui.ScrollbarControl
	empty     *ui.Label
	hints     *ui.Label

	detailRect ui.Rect
	focus      func(ui.ComponentID)
}

// New creates the browser screen.
func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	s := &Screen{Base: ui.NewBase(ScreenID), th: th, tokens: tokens}

	s.title = ui.NewLabel(TitleID, "Workspace Memory", th)
	s.title.SetColorRole(ui.ColorFg)

	field := ui.NewFilterField(SearchID, tokens)
	field.SetPrompt(ui.SearchGlyphEnd(tokens))
	field.SetPromptPlacement(ui.PromptEnd)
	field.SetPlaceholder("Search memories (identifiers, paths, topics)")
	field.SetSubmitAction("")
	field.OnChanged(s.applyQuery)
	s.search = &searchField{Base: ui.NewBase(SearchID), field: field, owner: s}

	list := ui.NewList(ListID, tokens)
	list.SetActivateAction("memorybrowser.activate")
	s.list = &browserList{Base: ui.NewBase(ListID), list: list, owner: s}
	s.scrollbar = ui.NewScrollbarControl(ScrollbarID, list.Scroll(), tokens)

	s.empty = ui.NewLabel(EmptyID, "No memories yet — the agent saves them as you work.", th)
	s.empty.SetColorRole(ui.ColorMuted)

	s.hints = ui.NewLabel(HintsID, "↑↓/jk navigate  / search  p pin  d forget  Esc close", th)
	s.hints.SetColorRole(ui.ColorMuted)
	return s
}

// SetSearchFn wires the workspace query used on every search edit/refresh.
func (s *Screen) SetSearchFn(fn func(query string) []memory.ScoredRecord) {
	s.searchFn = fn
}

// Open (re)loads results for the current query.
func (s *Screen) Open() {
	if s.search != nil && s.search.field != nil {
		s.search.field.SetValue("")
	}
	s.Refresh()
}

// Refresh re-runs the current query (after pin/forget mutations).
func (s *Screen) Refresh() {
	query := ""
	if s.search != nil && s.search.field != nil {
		query = s.search.field.Value()
	}
	if s.searchFn != nil {
		s.records = s.searchFn(query)
	}
	s.sync()
}

// SetFocusHandler registers Host focus routing.
func (s *Screen) SetFocusHandler(fn func(ui.ComponentID)) { s.focus = fn }

// FocusSearch focuses the query field.
func (s *Screen) FocusSearch() {
	if s.focus != nil {
		s.focus(SearchID)
	}
}

// FocusList focuses the results list.
func (s *Screen) FocusList() {
	if s.focus == nil {
		return
	}
	if len(s.records) == 0 {
		s.focus(SearchID)
		return
	}
	s.focus(ListID)
}

// Selected returns the highlighted record.
func (s *Screen) Selected() (memory.ScoredRecord, bool) {
	item, ok := s.list.list.Selected()
	if !ok {
		return memory.ScoredRecord{}, false
	}
	record, ok := item.Payload.(memory.ScoredRecord)
	return record, ok
}

func (s *Screen) applyQuery(string) { s.Refresh() }

func (s *Screen) sync() {
	items := make([]ui.ListItem, 0, len(s.records))
	for _, record := range s.records {
		items = append(items, ui.ListItem{
			ID:      record.ID,
			Label:   recordLabel(record.Record),
			Detail:  recordDetail(record.Record),
			Payload: record,
		})
	}
	s.list.list.SetItems(items)
	s.title.SetText(fmt.Sprintf("Workspace Memory (%d)", len(s.records)))
	if len(s.records) == 0 {
		query := ""
		if s.search != nil && s.search.field != nil {
			query = strings.TrimSpace(s.search.field.Value())
		}
		if query == "" {
			s.empty.SetText("No memories yet — the agent saves them as you work.")
		} else {
			s.empty.SetText(fmt.Sprintf("No memories match %q", query))
		}
	}
}

func (s *Screen) Children() []ui.Component {
	kids := []ui.Component{s.title, s.search}
	if len(s.records) == 0 {
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
	s.search.Layout(ctx, ui.Rect{Y: 1, W: r.W, H: 1})
	detailH := detailHeight
	if r.H < 14 {
		detailH = max(2, r.H/3)
	}
	listY := 3
	listH := max(1, r.H-listY-detailH-1)
	if len(s.records) == 0 {
		s.empty.Layout(ctx, ui.Rect{Y: listY, W: r.W, H: listH})
	} else {
		s.list.Layout(ctx, ui.Rect{X: 0, Y: listY, W: max(1, r.W-1), H: listH})
		s.scrollbar.Layout(ctx, ui.Rect{X: max(0, r.W-1), Y: listY, W: 1, H: listH})
	}
	s.detailRect = ui.Rect{X: 0, Y: listY + listH, W: r.W, H: detailH}
	s.hints.Layout(ctx, ui.Rect{Y: max(0, r.H-1), W: r.W, H: 1})
}

// Paint draws the detail pane for the selected record: content, scope, and
// provenance (the audit trail seam).
func (s *Screen) Paint(buf *surface.CellBuffer, originX, originY int, _ ui.PaintContext) {
	if s.detailRect.Empty() || len(s.records) == 0 {
		return
	}
	record, ok := s.Selected()
	if !ok {
		return
	}
	fg := s.tokens.Color(ui.ColorFg)
	muted := s.tokens.Color(ui.ColorMuted)
	b := s.Bounds()
	x := originX + b.X + s.detailRect.X
	y := originY + b.Y + s.detailRect.Y
	width := s.detailRect.W

	lines := rendering.WrapText(record.Content, max(1, width))
	contentRows := s.detailRect.H - 2
	if contentRows < 1 {
		contentRows = 1
	}
	row := 0
	buf.WriteString(x, y+row, rendering.TruncateLine(strings.Repeat("─", max(0, width)), width), muted, s.tokens.Color(ui.ColorBg), 0)
	row++
	for _, line := range lines {
		if row > contentRows {
			break
		}
		buf.WriteString(x, y+row, rendering.TruncateLine(line, width), fg, s.tokens.Color(ui.ColorBg), 0)
		row++
	}
	meta := provenanceLine(record.Record)
	if row < s.detailRect.H {
		buf.WriteString(x, y+s.detailRect.H-1, rendering.TruncateLine(meta, width), muted, s.tokens.Color(ui.ColorBg), 0)
	}
}

func (s *Screen) Handle(_ ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() && evt.Key.Code == tea.KeyEsc {
		return s.cancel()
	}
	return ui.EventResult{}
}

func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	if action.ID == "memorybrowser.activate" {
		// Enter on a row: no-op for now (detail pane already shows content).
		return ui.EventResult{Handled: true}
	}
	return ui.EventResult{Action: &action}
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{
		Role: ui.RoleDialog, Label: "Workspace memory browser",
		Value: fmt.Sprintf("%d memories", len(s.records)),
	}
}

func (s *Screen) cancel() ui.EventResult {
	return ui.EventResult{
		Handled: true,
		Cmd:     func() tea.Msg { return CancelMsg{Target: s} },
	}
}

func (s *Screen) pinSelected() ui.EventResult {
	record, ok := s.Selected()
	if !ok {
		return ui.EventResult{Handled: true}
	}
	return ui.EventResult{
		Handled: true,
		Cmd: func() tea.Msg {
			return PinMsg{Target: s, ID: record.ID, Pinned: !record.Pinned}
		},
	}
}

func (s *Screen) forgetSelected() ui.EventResult {
	record, ok := s.Selected()
	if !ok {
		return ui.EventResult{Handled: true}
	}
	return ui.EventResult{
		Handled: true,
		Cmd:     func() tea.Msg { return ForgetMsg{Target: s, ID: record.ID} },
	}
}

func recordLabel(record memory.Record) string {
	marker := "  "
	if record.Pinned {
		marker = "* "
	}
	title := strings.TrimSpace(strings.ReplaceAll(record.Title, "\n", " "))
	return rendering.TruncateLine(marker+title, 48)
}

func recordDetail(record memory.Record) string {
	parts := []string{record.Kind, fmt.Sprintf("imp %d", record.Importance)}
	if record.UseCount > 0 {
		parts = append(parts, fmt.Sprintf("used %d×", record.UseCount))
	}
	if record.Status == memory.StatusArchived {
		parts = append(parts, "archived")
	}
	parts = append(parts, formatAge(record.UpdatedAt), record.Source)
	return strings.Join(parts, " · ")
}

func provenanceLine(record memory.Record) string {
	parts := []string{"id " + record.ID}
	if record.Provenance != "" {
		parts = append(parts, record.Provenance)
	} else if record.SourceSessionID != "" {
		parts = append(parts, "session:"+record.SourceSessionID)
	}
	if len(record.ScopePaths) > 0 {
		parts = append(parts, "paths: "+strings.Join(record.ScopePaths, ", "))
	}
	return strings.Join(parts, " · ")
}

func formatAge(unixNano int64) string {
	if unixNano <= 0 {
		return "unknown"
	}
	age := time.Since(time.Unix(0, unixNano))
	switch {
	case age < time.Hour:
		return fmt.Sprintf("%dm ago", int(age.Minutes()))
	case age < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(age.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(age.Hours()/24))
	}
}

// browserList wraps the list to add pin/forget/nav keys.
type browserList struct {
	ui.Base
	list  *ui.List
	owner *Screen
}

func (p *browserList) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return p.list.Measure(ctx, cs)
}
func (p *browserList) Layout(ctx ui.LayoutContext, r ui.Rect) {
	p.SetBounds(r)
	p.list.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}
func (p *browserList) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	p.list.Paint(buf, ox+p.Bounds().X, oy+p.Bounds().Y, ctx)
}
func (p *browserList) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if p.owner != nil && evt.IsKey() {
		key := evt.Key
		switch {
		case key.Code == tea.KeyEsc:
			return p.owner.cancel()
		case key.String() == "/":
			p.owner.FocusSearch()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus}
		case key.String() == "p":
			return p.owner.pinSelected()
		case key.String() == "d" || key.Code == tea.KeyDelete:
			return p.owner.forgetSelected()
		case key.String() == "k":
			return p.list.Handle(ctx, ui.KeyEvent(tea.KeyPressMsg{Code: tea.KeyUp}))
		case key.String() == "j":
			return p.list.Handle(ctx, ui.KeyEvent(tea.KeyPressMsg{Code: tea.KeyDown}))
		}
	}
	result := p.list.Handle(ctx, evt)
	// Selection changes repaint the detail pane.
	result.Invalidate |= ui.InvalidatePaint
	return result
}
func (p *browserList) Semantics() ui.Semantics       { return p.list.Semantics() }
func (p *browserList) SetTokens(tokens *ui.TokenSet) { p.list.SetTokens(tokens) }

// searchField wraps the filter field: Esc/Down move focus to the list.
type searchField struct {
	ui.Base
	field *ui.FilterField
	owner *Screen
}

func (f *searchField) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return f.field.Measure(ctx, cs)
}
func (f *searchField) Layout(ctx ui.LayoutContext, r ui.Rect) {
	f.SetBounds(r)
	f.field.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}
func (f *searchField) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	f.field.Paint(buf, ox+f.Bounds().X, oy+f.Bounds().Y, ctx)
}
func (f *searchField) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() {
		switch evt.Key.Code {
		case tea.KeyEsc:
			return f.owner.cancel()
		case tea.KeyDown, tea.KeyEnter:
			f.owner.FocusList()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus}
		}
	}
	return f.field.Handle(ctx, evt)
}
func (f *searchField) Semantics() ui.Semantics       { return f.field.Semantics() }
func (f *searchField) SetTokens(tokens *ui.TokenSet) { f.field.SetTokens(tokens) }

var _ ui.Component = (*Screen)(nil)
var _ ui.Parent = (*Screen)(nil)
var _ ui.ActionHandler = (*Screen)(nil)
