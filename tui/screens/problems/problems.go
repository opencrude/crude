// Package problems is the Problems pane: every problem a language server or
// project check reported, grouped by file and ordered so the worst comes
// first, with one short language status and its next step.
//
// The screen owns presentation, selection and copy only. The owner supplies a
// Model and handles OpenMsg and ActionMsg; nothing here reads files or talks
// to a language server.
package problems

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
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
	ScreenID ui.ComponentID = "problems"
	FilterID ui.ComponentID = "problems/filter"
	ListID   ui.ComponentID = "problems/list"
	ScrollID ui.ComponentID = "problems/scroll"

	// margin is the left and right inset every docked surface uses.
	margin = 2
	// listTop is the first list row: the header row, then its rule.
	listTop = 2
)

// Severity orders problems: errors first.
type Severity uint8

const (
	Error Severity = iota
	Warning
	Information
	Hint
)

// Problem is one reported problem. Line and Col are 1-based.
type Problem struct {
	Path     string
	Folder   string // workspace-relative folder; empty at the root
	Line     int
	Col      int
	Severity Severity
	Message  string
	Source   string // the producer, for example "compiler"
	// Note is a short reason the problem cannot be opened now. Empty when
	// Go to problem works.
	Note string
	// Ref is the owner's reference, returned unchanged in OpenMsg.
	Ref any
}

// Status is the one language status line and its next step.
type Status struct {
	Text        string
	Attention   bool
	Action      string
	ActionLabel string
}

// RefreshAction is the ActionMsg of the Refresh link: restart the language
// servers so that every one checks the project again. A server can keep a
// wrong result after the files are fixed; only a new server process drops it.
const RefreshAction = "problems:refresh"

const refreshLabel = "Refresh"

// Model is everything the pane shows.
type Model struct {
	Problems []Problem
	// Checked is false until any check produced results.
	Checked bool
	Status  Status
	// Refresh shows the Refresh link. It is off while language servers cannot
	// run, because the status action is then the next step.
	Refresh bool
}

// OpenMsg asks the owner to show the problem in its file.
type OpenMsg struct{ Problem Problem }

// ActionMsg asks the owner to run the status action.
type ActionMsg struct{ Action string }

type group struct {
	path, name, folder string
	problems           []Problem
	counts             [4]int
	worst              Severity
}

// row is a file row (problem < 0) or a problem row.
type row struct {
	group, problem int
}

// Screen is the Problems pane.
type Screen struct {
	ui.Base
	tokens    *ui.TokenSet
	filter    *ui.FilterField
	list      *list
	scrollbar *ui.ScrollbarControl
	model     Model
	groups    []group
	rows      []row
	collapsed map[string]bool
	query     string
	inset     int
	link      ui.Rect // the status action, screen-local
	refresh   ui.Rect // the Refresh link, screen-local
	// band puts the language status on its own row above the list when it
	// does not fit beside the filter and counts; it is never cut short there.
	band  bool
	focus func(ui.ComponentID)
}

func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	ts := ui.NewTokenSet(th)
	s := &Screen{Base: ui.NewBase(ScreenID), tokens: ts, collapsed: map[string]bool{}}
	s.filter = ui.NewFilterField(FilterID, ts)
	s.filter.SetPlaceholder("Filter problems")
	s.filter.SetPromptPlacement(ui.PromptEnd)
	s.filter.SetSubmitAction("")
	s.filter.OnChanged(func(value string) {
		s.query = strings.ToLower(strings.TrimSpace(value))
		s.rebuild()
	})
	s.list = &list{Base: ui.NewBase(ListID), owner: s, scroll: ui.NewScrollModel(), hover: -1}
	s.scrollbar = ui.NewScrollbarControl(ScrollID, s.list.scroll, ts)
	s.scrollbar.SetSurfaceBackground(ui.ColorBg)
	s.SetTokens(ts)
	return s
}

func (s *Screen) SetTokens(ts *ui.TokenSet) {
	if ts == nil {
		return
	}
	s.tokens = ts
	s.filter.SetTokens(ts)
	s.filter.SetPrompt(ui.SearchGlyphEnd(ts))
	s.filter.SetPromptColor(ts.Color(ui.ColorAccent))
	s.scrollbar.SetTokens(ts)
}

func (s *Screen) SetTheme(th *theme.Theme) {
	if th != nil {
		s.SetTokens(ui.NewTokenSet(th))
	}
}

// SetFocusHandler lets Tab and Down move keys between the filter and the
// list through the owner's Host.
func (s *Screen) SetFocusHandler(fn func(ui.ComponentID)) { s.focus = fn }

func (s *Screen) focusID(id ui.ComponentID) ui.EventResult {
	if s.focus != nil {
		s.focus(id)
	}
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
}

// SetDockHeaderInset keeps the header clear of the dock's own buttons.
func (s *Screen) SetDockHeaderInset(width int) {
	s.inset = max(0, width)
	s.updateBand()
}

// PreferredFocus puts keys on the list: arrows move, Ctrl+C copies.
func (s *Screen) PreferredFocus() ui.ComponentID { return ListID }

// SetModel replaces the problems. The selected problem stays selected when it
// is still reported.
func (s *Screen) SetModel(model Model) {
	key, ok := s.list.cursorKey()
	s.model = model
	s.groups = buildGroups(model.Problems)
	s.rebuild()
	if ok {
		s.list.selectKey(key)
	}
}

// Model returns the shown model.
func (s *Screen) Model() Model { return s.model }

// buildGroups orders files by their worst problem, then path; problems in a
// file by severity, then line and column.
func buildGroups(problems []Problem) []group {
	byPath := map[string]int{}
	var groups []group
	for _, problem := range problems {
		index, ok := byPath[problem.Path]
		if !ok {
			index = len(groups)
			byPath[problem.Path] = index
			groups = append(groups, group{path: problem.Path, name: filepath.Base(problem.Path), folder: problem.Folder, worst: Hint})
		}
		g := &groups[index]
		g.problems = append(g.problems, problem)
		g.counts[min(int(problem.Severity), 3)]++
		g.worst = min(g.worst, problem.Severity)
	}
	for i := range groups {
		sort.SliceStable(groups[i].problems, func(a, b int) bool {
			x, y := groups[i].problems[a], groups[i].problems[b]
			if x.Severity != y.Severity {
				return x.Severity < y.Severity
			}
			if x.Line != y.Line {
				return x.Line < y.Line
			}
			return x.Col < y.Col
		})
	}
	sort.SliceStable(groups, func(a, b int) bool {
		if groups[a].worst != groups[b].worst {
			return groups[a].worst < groups[b].worst
		}
		return groups[a].path < groups[b].path
	})
	return groups
}

func (s *Screen) matches(g group, p Problem) bool {
	if s.query == "" {
		return true
	}
	text := strings.ToLower(g.name + " " + g.folder + " " + p.Message + " " + p.Source)
	return strings.Contains(text, s.query)
}

func (s *Screen) rebuild() {
	s.rows = s.rows[:0]
	for gi, g := range s.groups {
		start := len(s.rows)
		s.rows = append(s.rows, row{group: gi, problem: -1})
		shown := 0
		for pi, p := range g.problems {
			if !s.matches(g, p) {
				continue
			}
			shown++
			if !s.collapsed[g.path] {
				s.rows = append(s.rows, row{group: gi, problem: pi})
			}
		}
		if shown == 0 {
			s.rows = s.rows[:start]
		}
	}
	s.updateBand()
	s.list.clamp()
}

// updateBand decides from the current width and model whether the status
// shares the header row. Layout and every model or filter change call it, so
// paint and pointer hits always agree.
func (s *Screen) updateBand() {
	width := s.Bounds().W
	left, right := s.headerSpan(width)
	need := s.countsWidth() + s.statusWidth()
	if s.statusWidth() > 0 {
		need += 3
	}
	s.band = len(s.statusLinks()) > 0 && width > 0 && left+need > right
	s.list.top = 0
	if s.band {
		s.list.top = 1
	}
	s.list.scroll.SetViewport(max(0, s.list.Bounds().H-s.list.top))
}

func (s *Screen) headerSpan(width int) (left, right int) {
	return margin + filterWidth(width) + 2, width - max(margin, s.inset+1)
}

func (s *Screen) countsWidth() int {
	w := 0
	for i, part := range s.countParts(s.shownProblems()) {
		if i > 0 {
			w += 2
		}
		w += rendering.VisibleWidth(part.text)
	}
	return w
}

type statusLink struct{ label, action string }

// statusLinks are the header links: Refresh, then the status action.
func (s *Screen) statusLinks() []statusLink {
	var links []statusLink
	if s.model.Refresh {
		links = append(links, statusLink{refreshLabel, RefreshAction})
	}
	if s.model.Status.ActionLabel != "" {
		links = append(links, statusLink{s.model.Status.ActionLabel, s.model.Status.Action})
	}
	return links
}

func linksWidth(links []statusLink) int {
	w := 0
	for i, link := range links {
		if i > 0 {
			w += 3
		}
		w += rendering.VisibleWidth(link.label)
	}
	return w
}

// statusWidth is the status text and its links, joined by " · ".
func (s *Screen) statusWidth() int {
	links := s.statusLinks()
	if len(links) == 0 {
		return 0
	}
	w := linksWidth(links)
	if status := s.model.Status.Text; status != "" {
		w += rendering.VisibleWidth(status) + 3
	}
	return w
}

// linkAt is the action of the header link at a screen-local point.
func (s *Screen) linkAt(x, y int) (string, bool) {
	switch {
	case s.refresh.W > 0 && s.refresh.Contains(x, y):
		return RefreshAction, true
	case s.link.W > 0 && s.link.Contains(x, y):
		return s.model.Status.Action, true
	}
	return "", false
}

// shownProblems are the problems that pass the filter, in display order.
func (s *Screen) shownProblems() []Problem {
	var out []Problem
	for _, g := range s.groups {
		for _, p := range g.problems {
			if s.matches(g, p) {
				out = append(out, p)
			}
		}
	}
	return out
}

func (s *Screen) Children() []ui.Component {
	children := []ui.Component{s.filter, s.list}
	if s.scrollbar.Needed() {
		children = append(children, s.scrollbar)
	}
	return children
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

// filterWidth leaves the rest of the header row to the counts and status.
func filterWidth(width int) int {
	return min(max(20, width/3), 48, max(0, width-2*margin))
}

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	innerW := max(0, r.W-2*margin)
	s.filter.Layout(ctx, ui.Rect{X: margin, W: filterWidth(r.W), H: min(1, r.H)})
	listH := max(0, r.H-listTop)
	s.list.scroll.SetContent(len(s.rows))
	s.list.scroll.Clamp()
	listW := innerW
	if s.scrollbar.NeededForViewport(listH) && listW > 2 {
		listW -= 2
		s.scrollbar.Layout(ctx, ui.Rect{X: margin + listW, Y: listTop, W: 2, H: listH})
	} else {
		s.scrollbar.Layout(ctx, ui.Rect{})
	}
	s.list.Layout(ctx, ui.Rect{X: margin, Y: listTop, W: listW, H: listH})
	s.updateBand()
	s.list.scroll.Clamp()
}

func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	r := s.Bounds()
	if r.Empty() {
		return
	}
	t := s.tokens
	bg := t.Color(ui.ColorBg)
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	if r.H > 1 {
		rule := ui.FirstRune(t.Glyphs().BorderH, '-')
		for x := margin; x < r.W-margin; x++ {
			buf.Set(ox+r.X+x, oy+r.Y+1, cell.NewCell(rule, t.Color(ui.ColorBorder), bg, 0))
		}
	}
	s.paintHeader(buf, ox+r.X, oy+r.Y, r.W)
}

// paintHeader writes the counts after the filter and, when it fits, the
// language status at the right edge. Otherwise the status takes the first
// list row in full.
func (s *Screen) paintHeader(buf *surface.CellBuffer, x, y, width int) {
	t := s.tokens
	bg := t.Color(ui.ColorBg)
	left, right := s.headerSpan(width)
	s.link, s.refresh = ui.Rect{}, ui.Rect{}
	if right <= left {
		return
	}
	cx := left
	for _, part := range s.countParts(s.shownProblems()) {
		w := rendering.VisibleWidth(part.text)
		if cx+w > right {
			break
		}
		buf.WriteString(x+cx, y, part.text, t.Color(part.role), bg, 0)
		cx += w + 2
	}
	if s.band {
		lr := s.list.Bounds()
		s.paintStatus(buf, x, y+lr.Y, lr.X, lr.X+lr.W, lr.Y)
		return
	}
	s.paintStatus(buf, x, y, right-s.statusWidth(), right, 0)
}

// paintStatus writes "status · Refresh · Action" ending at right, or from left
// when it is on its own row, where only the status text is shortened. row is
// the screen-local row of the links.
func (s *Screen) paintStatus(buf *surface.CellBuffer, x, y, left, right, row int) {
	t := s.tokens
	bg := t.Color(ui.ColorBg)
	links := s.statusLinks()
	if len(links) == 0 {
		return
	}
	role := ui.ColorMuted
	if s.model.Status.Attention {
		role = ui.ColorWarning
	}
	cx := max(left, right-s.statusWidth())
	if s.band {
		cx = left
	}
	if status := s.model.Status.Text; status != "" {
		text := status
		if s.band {
			text = rendering.TruncateVisual(status, max(0, right-left-linksWidth(links)-3), "…")
		}
		buf.WriteString(x+cx, y, text, t.Color(role), bg, 0)
		cx += rendering.VisibleWidth(text)
		buf.WriteString(x+cx+1, y, "·", t.Color(ui.ColorMuted), bg, 0)
		cx += 3
	}
	for i, link := range links {
		if i > 0 {
			buf.WriteString(x+cx+1, y, "·", t.Color(ui.ColorMuted), bg, 0)
			cx += 3
		}
		w := rendering.VisibleWidth(link.label)
		rect := ui.Rect{X: cx, Y: row, W: w, H: 1}
		if link.action == RefreshAction {
			s.refresh = rect
		} else {
			s.link = rect
		}
		buf.WriteString(x+cx, y, link.label, t.Color(ui.ColorAccent2), bg, cell.AttrUnderline)
		cx += w
	}
}

type countPart struct {
	text string
	role ui.SemanticColor
}

// countParts is the header summary: only kinds that are present, or one
// plain sentence when there are none.
func (s *Screen) countParts(problems []Problem) []countPart {
	var counts [4]int
	for _, p := range problems {
		counts[min(int(p.Severity), 3)]++
	}
	var parts []countPart
	for severity, count := range counts {
		if count > 0 {
			parts = append(parts, countPart{severityGlyph(Severity(severity)) + " " + plural(count, severityNoun(Severity(severity))), severityRole(Severity(severity))})
		}
	}
	if len(parts) == 0 {
		switch {
		case s.query != "" && len(s.model.Problems) > 0:
			parts = append(parts, countPart{"No problems match the filter.", ui.ColorMuted})
		case !s.model.Checked:
			parts = append(parts, countPart{"No results yet.", ui.ColorMuted})
		default:
			parts = append(parts, countPart{"No problems.", ui.ColorMuted})
		}
	}
	return parts
}

func plural(n int, noun string) string {
	if n == 1 || noun == "information" {
		return strconv.Itoa(n) + " " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

func severityNoun(s Severity) string {
	return [...]string{"error", "warning", "information", "hint"}[min(int(s), 3)]
}

func severityGlyph(s Severity) string {
	return ui.DiagnosticGlyph(int(s) + 1) // Problems is zero-based; LSP starts at one.
}

func severityRole(s Severity) ui.SemanticColor {
	return ui.DiagnosticColor(int(s) + 1)
}

func (s *Screen) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.Kind == ui.EventMouseClick && evt.MouseClick.Button == tea.MouseLeft {
		if action, ok := s.linkAt(ctx.LocalX, ctx.LocalY); ok {
			return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return ActionMsg{Action: action} }}
		}
	}
	// Keys the filter does not use reach here: Tab and Down go to the list.
	if evt.IsKey() && (evt.Key.Code == tea.KeyTab || evt.Key.Code == tea.KeyDown) {
		return s.focusID(ListID)
	}
	return ui.EventResult{}
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Problems"}
}

// Line is the copy text of one problem: path:line:col, severity, message and
// source, tab-free so it pastes cleanly anywhere.
func Line(p Problem) string {
	text := fmt.Sprintf("%s:%d:%d  %s  %s", p.Path, p.Line, p.Col, severityNoun(p.Severity), p.Message)
	if p.Source != "" {
		text += "  (" + p.Source + ")"
	}
	return text
}

func linesText(problems []Problem) string {
	lines := make([]string, len(problems))
	for i, p := range problems {
		lines[i] = Line(p)
	}
	return strings.Join(lines, "\n")
}

// SelectionText is the copy text of the selected rows. A file row stands for
// every problem in that file.
func (s *Screen) SelectionText() string { return linesText(s.list.selectedProblems()) }

// AllText is the copy text of every shown problem.
func (s *Screen) AllText() string { return linesText(s.shownProblems()) }

// Selected returns the problem under the cursor, if the cursor is on one.
func (s *Screen) Selected() (Problem, bool) { return s.list.cursorProblem() }

// SelectAt prepares a context menu at a screen-local point: the row under the
// pointer becomes the selection unless it is already part of it. It reports
// whether a row is there.
func (s *Screen) SelectAt(localX, localY int) bool {
	lr := s.list.Bounds()
	if !lr.Contains(localX, localY) {
		return false
	}
	if localY-lr.Y < s.list.top {
		return false
	}
	index := s.list.scroll.Offset() + localY - lr.Y - s.list.top
	if index < 0 || index >= len(s.rows) {
		return false
	}
	if !s.list.inSelection(index) {
		s.list.cursor, s.list.anchor = index, index
	}
	return true
}

// OpenSelected asks the owner to open the problem under the cursor.
func (s *Screen) OpenSelected() tea.Cmd {
	problem, ok := s.list.cursorProblem()
	if !ok || problem.Note != "" {
		return nil
	}
	return func() tea.Msg { return OpenMsg{Problem: problem} }
}

// list is the scrolled problem list. Selection is a row range from anchor to
// cursor; a drag or Shift+arrow extends it, and Ctrl+C copies it.
type list struct {
	ui.Base
	owner        *Screen
	scroll       *ui.ScrollModel
	top          int // rows above the first problem: the status band
	cursor       int
	anchor       int
	hover        int
	press        int
	dragged      bool
	pressing     bool
	lastClickKey string
	lastClickAt  time.Time
}

func (l *list) clamp() {
	n := len(l.owner.rows)
	l.cursor = min(max(0, l.cursor), max(0, n-1))
	l.anchor = min(max(0, l.anchor), max(0, n-1))
	if l.hover >= n {
		l.hover = -1
	}
	l.scroll.SetContent(n)
	l.scroll.Clamp()
}

func (l *list) rowKey(index int) string {
	if index < 0 || index >= len(l.owner.rows) {
		return ""
	}
	r := l.owner.rows[index]
	g := l.owner.groups[r.group]
	if r.problem < 0 {
		return g.path
	}
	p := g.problems[r.problem]
	return fmt.Sprintf("%s\x00%d\x00%d\x00%s", g.path, p.Line, p.Col, p.Message)
}

func (l *list) cursorKey() (string, bool) {
	key := l.rowKey(l.cursor)
	return key, key != ""
}

func (l *list) selectKey(key string) {
	for index := range l.owner.rows {
		if l.rowKey(index) == key {
			l.cursor, l.anchor = index, index
			return
		}
	}
}

func (l *list) inSelection(index int) bool {
	lo, hi := min(l.anchor, l.cursor), max(l.anchor, l.cursor)
	return index >= lo && index <= hi
}

func (l *list) cursorProblem() (Problem, bool) {
	if l.cursor < 0 || l.cursor >= len(l.owner.rows) {
		return Problem{}, false
	}
	r := l.owner.rows[l.cursor]
	if r.problem < 0 {
		return Problem{}, false
	}
	return l.owner.groups[r.group].problems[r.problem], true
}

func (l *list) selectedProblems() []Problem {
	if len(l.owner.rows) == 0 {
		return nil
	}
	lo, hi := min(l.anchor, l.cursor), max(l.anchor, l.cursor)
	seen := map[[2]int]bool{}
	var out []Problem
	add := func(gi, pi int) {
		if key := [2]int{gi, pi}; !seen[key] {
			seen[key] = true
			out = append(out, l.owner.groups[gi].problems[pi])
		}
	}
	for index := lo; index <= hi && index < len(l.owner.rows); index++ {
		r := l.owner.rows[index]
		g := l.owner.groups[r.group]
		if r.problem >= 0 {
			add(r.group, r.problem)
			continue
		}
		for pi, p := range g.problems {
			if l.owner.matches(g, p) {
				add(r.group, pi)
			}
		}
	}
	return out
}

func (l *list) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (l *list) move(index int, extend bool) ui.EventResult {
	if len(l.owner.rows) == 0 {
		return ui.EventResult{Handled: true}
	}
	l.cursor = min(max(0, index), len(l.owner.rows)-1)
	if !extend {
		l.anchor = l.cursor
	}
	l.scroll.EnsureVisible(l.cursor)
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
}

func (l *list) toggle(index int, open bool) ui.EventResult {
	if index < 0 || index >= len(l.owner.rows) {
		return ui.EventResult{Handled: true}
	}
	path := l.owner.groups[l.owner.rows[index].group].path
	if l.owner.collapsed[path] == !open {
		return ui.EventResult{Handled: true}
	}
	l.owner.collapsed[path] = !open
	l.owner.rebuild()
	l.selectKey(path)
	l.scroll.EnsureVisible(l.cursor)
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
}

// activate opens a problem row and folds or unfolds a file row.
func (l *list) activate(index int) ui.EventResult {
	if index < 0 || index >= len(l.owner.rows) {
		return ui.EventResult{Handled: true}
	}
	r := l.owner.rows[index]
	if r.problem < 0 {
		return l.toggle(index, l.owner.collapsed[l.owner.groups[r.group].path])
	}
	return ui.EventResult{Handled: true, Cmd: l.owner.OpenSelected()}
}

func (l *list) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	// y is the row within the problem rows; the status band sits above them.
	y := ctx.LocalY - l.top
	switch evt.Kind {
	case ui.EventKey:
		return l.handleKey(evt)
	case ui.EventMouseMotion:
		index := l.scroll.Offset() + y
		if l.pressing {
			// A drag selects a row range and scrolls at the edges.
			if y < 0 {
				l.scroll.ScrollBy(-1)
			} else if y >= l.scroll.Viewport() {
				l.scroll.ScrollBy(1)
			}
			index = min(max(0, l.scroll.Offset()+y), len(l.owner.rows)-1)
			if index != l.press {
				l.dragged = true
			}
			l.cursor = max(0, index)
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
		if index < 0 || index >= len(l.owner.rows) || y < 0 || y >= l.scroll.Viewport() {
			index = -1
		}
		if index != l.hover {
			l.hover = index
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
		return ui.EventResult{Handled: true}
	case ui.EventMouseClick:
		if evt.MouseClick.Button != tea.MouseLeft {
			return ui.EventResult{}
		}
		if y < 0 && !l.pressing {
			// The status band: only its links act.
			lr := l.Bounds()
			if action, ok := l.owner.linkAt(lr.X+ctx.LocalX, lr.Y+ctx.LocalY); ok {
				return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return ActionMsg{Action: action} }}
			}
			return ui.EventResult{Handled: true}
		}
		index := l.scroll.Offset() + y
		if index < 0 || index >= len(l.owner.rows) {
			return ui.EventResult{Handled: true}
		}
		if evt.MouseClick.Mod.Contains(tea.ModShift) {
			l.cursor = index
		} else {
			l.cursor, l.anchor = index, index
		}
		l.press, l.pressing, l.dragged = index, true, false
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus}
	case ui.EventMouseRelease:
		if !l.pressing {
			return ui.EventResult{}
		}
		l.pressing = false
		if l.dragged || l.anchor != l.cursor || y < 0 || y >= l.scroll.Viewport() || l.scroll.Offset()+y != l.press {
			l.lastClickKey, l.lastClickAt = "", time.Time{}
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
		if l.owner.rows[l.cursor].problem < 0 {
			l.lastClickKey, l.lastClickAt = "", time.Time{}
			return l.activate(l.cursor)
		}
		key, now := l.rowKey(l.cursor), time.Now()
		if key != "" && key == l.lastClickKey && now.Sub(l.lastClickAt) <= 500*time.Millisecond {
			l.lastClickKey, l.lastClickAt = "", time.Time{}
			return l.activate(l.cursor)
		}
		l.lastClickKey, l.lastClickAt = key, now
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	case ui.EventMouseWheel:
		switch evt.MouseWheel.Button {
		case tea.MouseWheelUp:
			l.scroll.ScrollBy(-3)
		case tea.MouseWheelDown:
			l.scroll.ScrollBy(3)
		default:
			return ui.EventResult{}
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	}
	return ui.EventResult{}
}

// CancelPointerCapture ends a drag that lost the pointer; the range stays.
func (l *list) CancelPointerCapture() { l.pressing = false }

func (l *list) handleKey(evt ui.Event) ui.EventResult {
	key := evt.Key
	shift := key.Mod.Contains(tea.ModShift)
	if key.Mod.Contains(tea.ModCtrl) {
		switch key.Code {
		case 'c':
			text := l.owner.SelectionText()
			if text == "" {
				return ui.EventResult{Handled: true}
			}
			return ui.EventResult{Handled: true, Cmd: rendering.CopyToClipboardCmd(text)}
		case 'a':
			if len(l.owner.rows) > 0 {
				l.anchor, l.cursor = 0, len(l.owner.rows)-1
			}
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
		return ui.EventResult{}
	}
	switch key.Code {
	case tea.KeyUp:
		return l.move(l.cursor-1, shift)
	case tea.KeyDown:
		return l.move(l.cursor+1, shift)
	case tea.KeyPgUp:
		return l.move(l.cursor-max(1, l.scroll.Viewport()-1), shift)
	case tea.KeyPgDown:
		return l.move(l.cursor+max(1, l.scroll.Viewport()-1), shift)
	case tea.KeyHome:
		return l.move(0, shift)
	case tea.KeyEnd:
		return l.move(len(l.owner.rows)-1, shift)
	case tea.KeyEnter, tea.KeySpace:
		return l.activate(l.cursor)
	case tea.KeyRight:
		return l.toggle(l.cursor, true)
	case tea.KeyLeft:
		if l.cursor >= 0 && l.cursor < len(l.owner.rows) {
			if r := l.owner.rows[l.cursor]; r.problem >= 0 {
				// Left from a problem goes to its file row.
				l.selectKey(l.owner.groups[r.group].path)
				l.scroll.EnsureVisible(l.cursor)
				return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
			}
		}
		return l.toggle(l.cursor, false)
	case tea.KeyTab:
		return l.owner.focusID(FilterID)
	case tea.KeyEsc:
		if l.anchor != l.cursor {
			l.anchor = l.cursor
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
	}
	return ui.EventResult{}
}

// columns are the right-aligned widths shared by every visible row, so the
// tags and positions line up.
type columns struct{ tag, position int }

func (l *list) columns(width int) columns {
	var c columns
	end := min(len(l.owner.rows), l.scroll.Offset()+l.scroll.Viewport())
	for index := l.scroll.Offset(); index < end; index++ {
		r := l.owner.rows[index]
		if r.problem < 0 {
			continue
		}
		p := l.owner.groups[r.group].problems[r.problem]
		c.tag = max(c.tag, rendering.VisibleWidth(problemTag(p)))
		c.position = max(c.position, rendering.VisibleWidth(position(p)))
	}
	c.tag = min(c.tag, 20)
	if width < 60 {
		c.tag = 0
	}
	return c
}

func problemTag(p Problem) string {
	if p.Note != "" {
		return p.Note
	}
	return p.Source
}

func position(p Problem) string { return fmt.Sprintf("%d:%d", p.Line, p.Col) }

func (l *list) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	r := l.Bounds()
	t := l.owner.tokens
	if r.Empty() || t == nil {
		return
	}
	x0, y0 := ox+r.X, oy+r.Y
	bg := t.Color(ui.ColorBg)
	if len(l.owner.rows) == 0 {
		return
	}
	cols := l.columns(r.W)
	end := min(len(l.owner.rows), l.scroll.Offset()+r.H-l.top)
	for index := l.scroll.Offset(); index < end; index++ {
		y := y0 + l.top + index - l.scroll.Offset()
		rowBg := bg
		switch {
		case l.inSelection(index) && (ctx.Focused || index == l.cursor):
			rowBg = t.Color(ui.ColorSelection)
		case index == l.hover:
			rowBg = t.Color(ui.ColorHover)
		}
		ui.FillRect(buf, 0, 0, ui.Rect{X: x0, Y: y, W: r.W, H: 1}, cell.BlankCell(rowBg))
		attr := cell.Attrs(0)
		if index == l.cursor && ctx.Focused {
			attr = cell.AttrBold
		}
		row := l.owner.rows[index]
		g := l.owner.groups[row.group]
		if row.problem < 0 {
			l.paintFile(buf, x0, y, r.W, g, rowBg, attr)
			continue
		}
		l.paintProblem(buf, x0, y, r.W, g.problems[row.problem], cols, rowBg, attr)
	}
}

// File rows: chevron, name, folder; counts at the right edge.
func (l *list) paintFile(buf *surface.CellBuffer, x, y, width int, g group, bg cell.Color, attr cell.Attrs) {
	t := l.owner.tokens
	glyphs := t.Glyphs()
	chevron := ui.FirstRune(glyphs.TreeExpanded, '▾')
	if l.owner.collapsed[g.path] {
		chevron = ui.FirstRune(glyphs.TreeCollapsed, '▸')
	}
	buf.Set(x, y, cell.NewCell(chevron, t.Color(ui.ColorMuted), bg, 0))
	var counts []countPart
	countsW := 0
	for severity, count := range g.counts {
		if count > 0 {
			text := severityGlyph(Severity(severity)) + " " + strconv.Itoa(count)
			counts = append(counts, countPart{text, severityRole(Severity(severity))})
			countsW += rendering.VisibleWidth(text) + 2
		}
	}
	nameX := x + 2
	room := max(0, width-2-countsW)
	name := rendering.TruncateVisual(g.name, room, "…")
	buf.WriteString(nameX, y, name, t.Color(ui.ColorFg), bg, attr|cell.AttrBold)
	if folder := g.folder; folder != "" {
		folderX := nameX + rendering.VisibleWidth(name) + 2
		if folderRoom := x + width - countsW - folderX; folderRoom > 3 {
			buf.WriteString(folderX, y, rendering.TruncateVisual(folder, folderRoom, "…"), t.Color(ui.ColorMuted), bg, 0)
		}
	}
	cx := x + width - countsW + 2
	for _, part := range counts {
		if cx >= x {
			buf.WriteString(cx, y, part.text, t.Color(part.role), bg, 0)
		}
		cx += rendering.VisibleWidth(part.text) + 2
	}
}

// Problem rows: severity glyph and message under the file name; the tag and
// position columns are right-aligned and shared by every visible row.
func (l *list) paintProblem(buf *surface.CellBuffer, x, y, width int, p Problem, cols columns, bg cell.Color, attr cell.Attrs) {
	t := l.owner.tokens
	glyphX := x + 2
	buf.WriteString(glyphX, y, severityGlyph(p.Severity), t.Color(severityRole(p.Severity)), bg, 0)
	posX := x + width - cols.position
	pos := position(p)
	buf.WriteString(posX+cols.position-rendering.VisibleWidth(pos), y, pos, t.Color(ui.ColorMuted), bg, 0)
	tagX := posX
	if cols.tag > 0 {
		tagX = posX - 2 - cols.tag
		tag := rendering.TruncateVisual(problemTag(p), cols.tag, "…")
		role := ui.ColorMuted
		if p.Note != "" {
			role = ui.ColorWarning
		}
		buf.WriteString(tagX, y, tag, t.Color(role), bg, 0)
	}
	messageX := glyphX + 2
	room := max(0, tagX-2-messageX)
	ink := ui.ColorFg
	if p.Note != "" {
		ink = ui.ColorMuted
	}
	message := strings.Join(strings.Fields(p.Message), " ")
	buf.WriteString(messageX, y, rendering.TruncateVisual(message, room, "…"), t.Color(ink), bg, attr)
}

func (l *list) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleList, Label: "Problems"}
}
