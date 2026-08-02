// Package findinfiles provides the native Find in Files surface body.
package findinfiles

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/fileio"
	"crude/tui/cell"
	"crude/tui/editor"
	"crude/tui/filetree"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/widget"
)

const (
	ScreenID    ui.ComponentID = "find-in-files"
	QueryID     ui.ComponentID = "find-in-files/query"
	CaseID      ui.ComponentID = "find-in-files/case-sensitive"
	RegexID     ui.ComponentID = "find-in-files/regex"
	StatusID    ui.ComponentID = "find-in-files/status"
	ListID      ui.ComponentID = "find-in-files/list"
	ScrollbarID ui.ComponentID = "find-in-files/scrollbar"
	PreviewID   ui.ComponentID = "find-in-files/preview"
	HintsID     ui.ComponentID = "find-in-files/hints"
)

type SearchRequestMsg struct {
	Screen     *Screen
	Generation int
	Query      string
	Options    fileio.ProjectSearchOptions
}

type SearchCancelMsg struct {
	Screen *Screen
}

type OpenMsg struct {
	Screen *Screen
	Match  fileio.ProjectSearchMatch
}

type CloseMsg struct {
	Screen *Screen
}

type Screen struct {
	ui.Base
	th          *theme.Theme
	tokens      *ui.TokenSet
	root        string
	queryField  *findQuery
	caseToggle  *ui.ToggleButton
	regexToggle *ui.ToggleButton
	status      *ui.Label
	list        *findList
	scrollbar   *ui.ScrollbarControl
	preview     *previewPane
	hints       *ui.Label
	focus       func(ui.ComponentID)
	icons       *filetree.IconResolver

	results       []fileio.ProjectSearchMatch
	selected      int
	loading       bool
	errText       string
	duration      time.Duration
	generation    int
	previewSrc    *editor.Editor
	caseSensitive bool
	regex         bool
	resultH       int
	listY0        int
	previewSurf   *surface.Surface
}

func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	s := &Screen{Base: ui.NewBase(ScreenID), th: th, tokens: tokens, selected: -1, icons: filetree.NewIconResolver()}
	s.icons.ApplyTheme(&th.Colors.Tree)

	field := ui.NewSearchField(QueryID, tokens)
	field.SetPrompt(">")
	field.SetPlaceholder("Search file names and contents")
	field.SetSubmitAction("")
	s.queryField = &findQuery{Base: ui.NewBase(QueryID), field: field, owner: s}
	s.caseToggle = ui.NewToggleButton(CaseID, "Cc", "find.toggle-case", tokens)
	s.regexToggle = ui.NewToggleButton(RegexID, ".*", "find.toggle-regex", tokens)
	s.caseToggle.SetCancelAction("find.close")
	s.regexToggle.SetCancelAction("find.close")

	s.status = ui.NewLabel(StatusID, "", th)
	s.status.SetColorRole(ui.ColorMuted)

	list := ui.NewVirtualList(ListID, tokens)
	list.SetActivateAction("find.open")
	s.list = &findList{Base: ui.NewBase(ListID), list: list, owner: s, lastClick: -1}
	s.scrollbar = ui.NewScrollbarControl(ScrollbarID, list.Scroll(), tokens)

	s.preview = &previewPane{Base: ui.NewBase(PreviewID), owner: s}
	s.hints = ui.NewLabel(HintsID, "", th)
	s.hints.SetColorRole(ui.ColorMuted)
	return s
}

func (s *Screen) Root() string          { return s.root }
func (s *Screen) Query() string         { return s.queryField.field.Value() }
func (s *Screen) SetQuery(value string) { s.queryField.field.SetValue(value) }
func (s *Screen) Results() []fileio.ProjectSearchMatch {
	return s.results
}
func (s *Screen) Selected() int       { return s.selected }
func (s *Screen) Generation() int     { return s.generation }
func (s *Screen) CaseSensitive() bool { return s.caseSensitive }
func (s *Screen) Regex() bool         { return s.regex }

// SetSearchDebounce configures the shared query-field debounce interval.
func (s *Screen) SetSearchDebounce(delay time.Duration) { s.queryField.field.SetDebounce(delay) }

func (s *Screen) AcceptsSearch(screen *Screen, generation int) bool {
	return screen == s && generation == s.generation && strings.TrimSpace(s.Query()) != ""
}

func (s *Screen) Open(root string) {
	s.root = root
	s.queryField.field.Cancel()
	s.queryField.field.SetValue("")
	s.caseSensitive = false
	s.regex = false
	s.caseToggle.SetChecked(false)
	s.regexToggle.SetChecked(false)
	s.results = nil
	s.selected = -1
	s.previewSrc = nil
	s.loading = false
	s.errText = ""
	s.generation = 0
	s.duration = 0
	s.sync()
}

func (s *Screen) Close() {
	s.generation++
	s.loading = false
	s.queryField.field.Cancel()
}

func (s *Screen) SetFocusHandler(fn func(ui.ComponentID)) { s.focus = fn }
func (s *Screen) FocusQuery()                             { s.focusID(QueryID) }
func (s *Screen) focusID(id ui.ComponentID) {
	if s.focus != nil {
		s.focus(id)
	}
}

func (s *Screen) ApplySearchResults(generation int, results []fileio.ProjectSearchMatch, err error, duration time.Duration) {
	if generation != s.generation {
		return
	}
	s.loading = false
	s.duration = duration
	if err != nil {
		s.errText = err.Error()
		s.results = nil
		s.selected = -1
		s.previewSrc = nil
		s.sync()
		return
	}
	s.errText = ""
	s.results = results
	s.list.lastClick = -1
	s.list.lastClickTime = time.Time{}
	s.list.list.Scroll().ScrollTo(0)
	if len(s.results) == 0 {
		s.selected = -1
		s.previewSrc = nil
		s.sync()
		return
	}
	s.selected = 0
	s.sync()
	s.rebuildPreview()
}

// ScheduleSearch reschedules the current query after a non-text state change,
// such as a search-option toggle.
func (s *Screen) ScheduleSearch() tea.Cmd {
	if cancel := s.queryChanged(); cancel != nil {
		return cancel
	}
	return s.queryField.field.Schedule()
}

func (s *Screen) queryChanged() tea.Cmd {
	s.generation++
	s.loading = strings.TrimSpace(s.Query()) != ""
	s.errText = ""
	if strings.TrimSpace(s.Query()) == "" {
		s.results = nil
		s.selected = -1
		s.previewSrc = nil
		s.loading = false
		s.sync()
		return func() tea.Msg { return SearchCancelMsg{Screen: s} }
	}
	s.syncStatus()
	return nil
}

// HandleSearchDebounced converts an accepted shared SearchField notification
// into this screen's owner-tagged domain-search intent.
func (s *Screen) HandleSearchDebounced(msg ui.SearchDebouncedMsg) tea.Cmd {
	if !s.queryField.field.AcceptsDebounce(msg) || !s.AcceptsSearch(s, s.generation) {
		return nil
	}
	return func() tea.Msg {
		return SearchRequestMsg{
			Screen:     s,
			Generation: s.generation,
			Query:      s.Query(),
			Options: fileio.ProjectSearchOptions{
				CaseSensitive: s.caseSensitive,
				Regex:         s.regex,
			},
		}
	}
}

func (s *Screen) selectIndex(idx int) {
	if len(s.results) == 0 {
		s.selected = -1
		s.previewSrc = nil
		return
	}
	idx = max(0, min(idx, len(s.results)-1))
	if idx == s.selected {
		return
	}
	s.selected = idx
	s.list.list.SelectIndex(idx)
	s.rebuildPreview()
	s.sync()
}

func (s *Screen) rebuildPreview() {
	if s.selected < 0 || s.selected >= len(s.results) || s.results[s.selected].NameOnly {
		s.previewSrc = nil
		return
	}
	result := s.results[s.selected]
	preview := editor.NewPreview(s.th, result.Path, result.Context, result.ContextStart)
	localLine := result.Line - result.ContextStart
	if localLine < 0 {
		localLine = 0
	}
	lineRunes := []rune(result.LineText)
	startCol := rendering.RuneIdxToCol(lineRunes, result.Column)
	endCol := rendering.RuneIdxToCol(lineRunes, max(result.EndColumn, result.Column+1))
	preview.SetSelection(localLine, startCol, localLine, max(endCol, startCol+1))
	s.previewSrc = preview
}

func (s *Screen) sync() {
	s.syncList()
	s.syncStatus()
	s.syncHints()
}

func (s *Screen) syncList() {
	items := make([]ui.VirtItem, 0, len(s.results))
	for i, result := range s.results {
		icon := ""
		if s.icons != nil {
			icon = s.icons.ResolveIcon(filepath.Base(result.Path), false, false, false).Icon
		}
		label := result.RelativePath
		if !result.NameOnly {
			label += fmt.Sprintf(":%d", result.Line)
		}
		items = append(items, ui.VirtItem{
			ID:      fmt.Sprintf("%s-%d", result.Path, i),
			Height:  1,
			Label:   label,
			Icon:    icon,
			Detail:  strings.TrimRight(result.LineText, "\r\n"),
			Payload: i,
		})
	}
	s.list.list.SetItems(items)
	if s.selected >= 0 && s.selected < len(items) {
		s.list.list.SelectIndex(s.selected)
	}
}

func (s *Screen) syncStatus() {
	if s.loading {
		s.status.SetColorRole(ui.ColorAccent)
		s.status.SetText("searching")
		return
	}
	if s.errText != "" {
		s.status.SetColorRole(ui.ColorError)
		s.status.SetText("invalid query")
		return
	}
	s.status.SetColorRole(ui.ColorMuted)
	if s.Query() == "" {
		s.status.SetText(filepath.Base(s.root))
		return
	}
	files := make(map[string]struct{})
	for _, r := range s.results {
		files[r.Path] = struct{}{}
	}
	if s.duration > 0 {
		s.status.SetText(fmt.Sprintf("%d results · %d files · %dms", len(s.results), len(files), s.duration.Milliseconds()))
	} else {
		s.status.SetText(fmt.Sprintf("%d results · %d files", len(s.results), len(files)))
	}
}

func (s *Screen) syncHints() {
	s.hints.SetText("type to search  ·  Cc case-sensitive  ·  .* regex  ·  ↑↓ preview  ·  enter open  ·  double-click open  ·  esc close")
}

func (s *Screen) openSelected() tea.Cmd {
	if s.selected < 0 || s.selected >= len(s.results) {
		return nil
	}
	return func() tea.Msg { return OpenMsg{Screen: s, Match: s.results[s.selected]} }
}

func (s *Screen) Children() []ui.Component {
	return []ui.Component{s.caseToggle, s.regexToggle, s.queryField, s.status, s.list, s.scrollbar, s.preview, s.hints}
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	s.queryField.SetTokens(tokens)
	s.caseToggle.SetTokens(tokens)
	s.regexToggle.SetTokens(tokens)
	s.status.SetTokens(tokens)
	s.list.SetTokens(tokens)
	s.scrollbar.SetTokens(tokens)
	s.hints.SetTokens(tokens)
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	caseW := min(2, max(0, r.W))
	regexW := min(2, max(0, r.W-caseW-1))
	queryX := caseW + regexW
	if regexW > 0 {
		queryX++
	}
	s.caseToggle.Layout(ctx, ui.Rect{X: 0, Y: 0, W: caseW, H: 1})
	s.regexToggle.Layout(ctx, ui.Rect{X: caseW, Y: 0, W: regexW, H: 1})
	s.queryField.Layout(ctx, ui.Rect{X: queryX, Y: 0, W: max(1, r.W-queryX), H: 1})
	s.status.Layout(ctx, ui.Rect{X: 0, Y: 1, W: r.W, H: 1})

	s.listY0 = 2
	s.resultH = max(1, (r.H-s.listY0-1)*42/100)
	if s.resultH < 4 {
		s.resultH = min(4, max(1, r.H-s.listY0-1))
	}
	previewStart := s.listY0 + s.resultH
	previewH := max(0, r.H-previewStart-1)
	if previewH > 0 && previewH < 3 && r.H-s.listY0 >= 4 {
		s.resultH = max(1, r.H-s.listY0-2)
		previewStart = s.listY0 + s.resultH
		previewH = r.H - previewStart - 1
	}
	s.list.Layout(ctx, ui.Rect{X: 0, Y: s.listY0, W: max(1, r.W-1), H: s.resultH})
	s.scrollbar.Layout(ctx, ui.Rect{X: max(0, r.W-1), Y: s.listY0, W: 1, H: s.resultH})
	s.preview.Layout(ctx, ui.Rect{X: 0, Y: previewStart, W: r.W, H: max(0, previewH)})
	s.hints.Layout(ctx, ui.Rect{Y: max(0, r.H-1), W: r.W, H: 1})
}

func (s *Screen) Paint(buf *surface.CellBuffer, originX, originY int, _ ui.PaintContext) {
	r := s.Bounds()
	bg := cell.NoColor
	if s.tokens != nil {
		bg = s.tokens.Color(ui.ColorPopupBg)
	}
	if !bg.IsDefault() {
		ui.FillRect(buf, originX+r.X, originY+r.Y, r, cell.BlankCell(bg))
	}
}

func (s *Screen) Handle(_ ui.EventContext, _ tea.Msg) ui.EventResult { return ui.EventResult{} }
func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case "find.open":
		return ui.EventResult{Handled: true, Cmd: s.openSelected()}
	case "find.close":
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return CloseMsg{Screen: s} }}
	case "find.toggle-case":
		s.caseSensitive = !s.caseSensitive
		s.caseToggle.SetChecked(s.caseSensitive)
		return ui.EventResult{Handled: true, Cmd: s.ScheduleSearch(), Invalidate: ui.InvalidatePaint}
	case "find.toggle-regex":
		s.regex = !s.regex
		s.regexToggle.SetChecked(s.regex)
		return ui.EventResult{Handled: true, Cmd: s.ScheduleSearch(), Invalidate: ui.InvalidatePaint}
	}
	return ui.EventResult{Action: &action}
}
func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleDialog, Label: "Find in Files", Selected: len(s.results) > 0}
}

type findQuery struct {
	ui.Base
	field *ui.SearchField
	owner *Screen
}

func (q *findQuery) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return q.field.Measure(ctx, cs)
}
func (q *findQuery) Layout(ctx ui.LayoutContext, r ui.Rect) {
	q.SetBounds(r)
	q.field.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}
func (q *findQuery) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	q.field.Paint(buf, ox+q.Bounds().X, oy+q.Bounds().Y, ctx)
}
func (q *findQuery) Semantics() ui.Semantics       { return q.field.Semantics() }
func (q *findQuery) SetTokens(tokens *ui.TokenSet) { q.field.SetTokens(tokens) }
func (q *findQuery) Handle(ctx ui.EventContext, msg tea.Msg) ui.EventResult {
	if q.owner == nil {
		return q.field.Handle(ctx, msg)
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyEsc:
			return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return CloseMsg{Screen: q.owner} }}
		case tea.KeyEnter:
			return ui.EventResult{Handled: true, Cmd: q.owner.openSelected()}
		case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd:
			q.owner.focusID(ListID)
			result := q.owner.list.Handle(ui.EventContext{Focused: true}, msg)
			result.Invalidate |= ui.InvalidateFocus | ui.InvalidatePaint
			return result
		case tea.KeyTab:
			if len(q.owner.results) > 0 && !key.Mod.Contains(tea.ModShift) {
				q.owner.focusID(ListID)
			}
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		}
		if key.String() == "ctrl+r" {
			return q.owner.HandleAction(ctx, ui.Action{ID: "find.toggle-regex"})
		}
	}
	return q.applyFilter(ctx, msg)
}
func (q *findQuery) applyFilter(ctx ui.EventContext, msg tea.Msg) ui.EventResult {
	before := q.field.Value()
	result := q.field.Handle(ctx, msg)
	if q.field.Value() != before {
		result.Invalidate |= ui.InvalidateLayout | ui.InvalidatePaint
		result.Cmd = combineFindCmd(result.Cmd, q.owner.queryChanged())
	}
	return result
}

type findList struct {
	ui.Base
	list          *ui.VirtualList
	owner         *Screen
	lastClick     int
	lastClickTime time.Time
}

func (l *findList) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return l.list.Measure(ctx, cs)
}
func (l *findList) Layout(ctx ui.LayoutContext, r ui.Rect) {
	l.SetBounds(r)
	l.list.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}
func (l *findList) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	l.list.Paint(buf, ox+l.Bounds().X, oy+l.Bounds().Y, ctx)
}
func (l *findList) Handle(ctx ui.EventContext, msg tea.Msg) ui.EventResult {
	if l.owner == nil {
		return l.list.Handle(ctx, msg)
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
		if key.String() == "ctrl+r" {
			return l.owner.HandleAction(ctx, ui.Action{ID: "find.toggle-regex"})
		}
		switch key.Code {
		case tea.KeyEsc:
			return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return CloseMsg{Screen: l.owner} }}
		case tea.KeyEnter:
			return ui.EventResult{Handled: true, Cmd: l.owner.openSelected()}
		case tea.KeyTab:
			if key.Mod.Contains(tea.ModShift) {
				l.owner.focusID(QueryID)
			}
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd:
			old := l.list.SelectedIndex()
			result := l.list.Handle(ctx, msg)
			if l.list.SelectedIndex() != old {
				l.owner.selectIndex(l.list.SelectedIndex())
			}
			return result
		default:
			if key.Text != "" && !key.Mod.Contains(tea.ModCtrl) && !key.Mod.Contains(tea.ModAlt) {
				l.owner.focusID(QueryID)
				return l.owner.queryField.applyFilter(ui.EventContext{Focused: true}, msg)
			}
		}
	}
	if _, ok := msg.(tea.PasteMsg); ok {
		l.owner.focusID(QueryID)
		return l.owner.queryField.applyFilter(ui.EventContext{Focused: true}, msg)
	}
	result := l.list.Handle(ctx, msg)
	click, clicked := msg.(tea.MouseClickMsg)
	if clicked && click.Button == tea.MouseLeft && result.Action != nil {
		index := l.list.SelectedIndex()
		now := time.Now()
		if l.lastClick != index || now.Sub(l.lastClickTime) > 400*time.Millisecond {
			l.lastClick, l.lastClickTime = index, now
			result.Action = nil
			if index >= 0 && index < len(l.owner.results) {
				l.owner.selectIndex(index)
			}
		}
	}
	return result
}
func (l *findList) Semantics() ui.Semantics       { return l.list.Semantics() }
func (l *findList) SetTokens(tokens *ui.TokenSet) { l.list.SetTokens(tokens) }

func combineFindCmd(first, second tea.Cmd) tea.Cmd {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return tea.Batch(first, second)
}

type previewPane struct {
	ui.Base
	owner *Screen
}

func (p *previewPane) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}
func (p *previewPane) Layout(_ ui.LayoutContext, r ui.Rect) { p.SetBounds(r) }
func (p *previewPane) SetTokens(*ui.TokenSet)               {}
func (p *previewPane) Paint(buf *surface.CellBuffer, originX, originY int, _ ui.PaintContext) {
	r := p.Bounds()
	if r.Empty() {
		return
	}
	ed := p.owner.previewSrc
	if ed == nil {
		return
	}
	w, h := max(10, r.W), max(4, r.H)
	if p.owner.previewSurf == nil || p.owner.previewSurf.Width() < w || p.owner.previewSurf.Height() < h {
		p.owner.previewSurf = surface.NewSurface("fif-preview", w, h, cell.TransparentBlank())
	}
	p.owner.previewSurf.Buffer().Clear()
	wctx := &widget.RenderContext{Theme: p.owner.th}
	ed.Render(p.owner.previewSurf, wctx)
	surface.BlitOpaque(buf, originX+r.X, originY+r.Y, p.owner.previewSurf.Buffer())
}
func (p *previewPane) Handle(_ ui.EventContext, _ tea.Msg) ui.EventResult { return ui.EventResult{} }
func (p *previewPane) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Preview"}
}

var _ ui.Component = (*Screen)(nil)
var _ ui.Parent = (*Screen)(nil)
var _ ui.ActionHandler = (*Screen)(nil)
