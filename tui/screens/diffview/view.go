package diffview

import (
	tea "charm.land/bubbletea/v2"
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"fmt"
	"github.com/alecthomas/chroma/v2"
	"strings"
)

type Presentation string

const (
	Unified    Presentation = "unified"
	SideBySide Presentation = "side-by-side"
)

type point struct{ row, column int }
type Screen struct {
	ui.Base
	tokens                 *ui.TokenSet
	prepared               Prepared
	preferred, presented   Presentation
	split                  *ui.SplitModel
	divider                *ui.SplitDivider
	scroll                 *ui.ScrollModel
	scrollbar              *ui.ScrollbarControl
	before, after, unified *pane
	focus                  func(ui.ComponentID) bool
	query                  string
	hunkIndex, matchIndex  int
	search                 SearchProjection
	syntaxStyle            *chroma.Style
	syntaxInk              map[chroma.TokenType]cell.Color
	resolve                resolution
}

func New(th *theme.Theme) *Screen { return NewWithID(th, "diffview") }
func NewWithID(th *theme.Theme, id ui.ComponentID) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	s := &Screen{Base: ui.NewBase(id), tokens: ui.NewTokenSet(th), preferred: SideBySide, scroll: ui.NewScrollModel(), split: ui.NewSplitModel(ui.Horizontal), hunkIndex: -1, matchIndex: -1}
	s.split.SetMinSizes(20, 20)
	s.divider = ui.NewSplitDivider(ui.NewID(id, "divider"), s.split)
	s.scrollbar = ui.NewScrollbarControl(ui.NewID(id, "scrollbar"), s.scroll, s.tokens)
	s.scrollbar.SetPointerTargetSize(1)
	s.before = s.newPane("before", true, false)
	s.after = s.newPane("after", false, false)
	s.unified = s.newPane("unified", false, true)
	s.resolve.bar = newResolutionBar(id, s.tokens)
	s.SetTokens(s.tokens)
	return s
}
func (s *Screen) newPane(name string, before, unified bool) *pane {
	p := &pane{Base: ui.NewBase(ui.NewID(s.ID(), name)), owner: s, before: before, unified: unified, horizontal: ui.NewScrollModel()}
	p.bar = ui.NewScrollbarControl(ui.NewID(p.ID(), "horizontal"), p.horizontal, s.tokens)
	p.bar.SetDirection(ui.Horizontal)
	p.bar.SetPointerTargetSize(1)
	return p
}
func (s *Screen) SetPrepared(p Prepared) {
	s.prepared = p
	s.scroll.ScrollTo(0)
	s.hunkIndex = -1
	s.matchIndex = -1
	for _, pane := range []*pane{s.before, s.after, s.unified} {
		pane.CancelPointerCapture()
		pane.selected = false
		pane.horizontal.ScrollTo(0)
	}
	s.syncScroll()
	s.resetChoices()
	if s.resolve.enabled && s.hunkIndex >= 0 {
		// A resolution starts at its first change.
		s.revealChange()
	}
	s.SetQuery(s.query)
}
func (s *Screen) Comparison() Comparison                       { return s.prepared.Comparison }
func (s *Screen) HunkCount() int                               { return s.prepared.HunkCount() }
func (s *Screen) SetFocusHandler(fn func(ui.ComponentID) bool) { s.focus = fn }
func (s *Screen) DocumentID() ui.ComponentID {
	if s.presented == SideBySide {
		return s.after.ID()
	}
	return s.unified.ID()
}
func (s *Screen) SetPresentation(p Presentation) {
	if p == Unified || p == SideBySide {
		s.preferred = p
	}
}
func (s *Screen) PreferredPresentation() Presentation { return s.preferred }
func (s *Screen) Presentation() Presentation          { return s.presented }

// InvalidateWidthProfile publishes already-reflowed source geometry. The owner
// must run SourceGeometry ReflowCommands off Update before calling this method.
func (s *Screen) InvalidateWidthProfile() {
	s.prepared.beforeWidth, s.prepared.afterWidth = s.prepared.beforeSource.Width(), s.prepared.afterSource.Width()
	s.rebuildMatches()
}
func (s *Screen) SetTokens(t *ui.TokenSet) {
	if t == nil {
		return
	}
	if s.tokens == nil || s.syntaxStyle == nil || s.tokens.Theme() != t.Theme() || s.tokens.ColorVersion != t.ColorVersion {
		s.syntaxStyle = rendering.SourceStyle(t.Theme())
		s.syntaxInk = make(map[chroma.TokenType]cell.Color)
	}
	s.tokens = t
	s.divider.SetTokens(t)
	s.scrollbar.SetTokens(t)
	s.resolve.bar.SetTokens(t)
	for _, p := range []*pane{s.before, s.after, s.unified} {
		p.bar.SetTokens(t)
	}
}

func (s *Screen) sourceInk(kind chroma.TokenType) (cell.Color, bool) {
	if ink, ok := s.syntaxInk[kind]; ok {
		return ink, !ink.IsDefault()
	}
	ink := cell.NoColor
	if entry := s.syntaxStyle.Get(kind); entry.Colour.IsSet() {
		ink = rendering.LipglossColorToCell(entry.Colour.String())
	}
	s.syntaxInk[kind] = ink
	return ink, !ink.IsDefault()
}
func (s *Screen) SetTheme(th *theme.Theme) {
	if th != nil {
		s.SetTokens(ui.NewTokenSet(th))
	}
}
func (s *Screen) rows() []Row {
	if s.presented == SideBySide {
		return s.prepared.rows
	}
	return s.prepared.unified
}
func (s *Screen) syncScroll() { s.scroll.SetContent(len(s.rows())) }
func (s *Screen) Children() []ui.Component {
	children := []ui.Component{s.unified, s.scrollbar}
	if s.presented == SideBySide {
		children = []ui.Component{s.before, s.after, s.divider, s.scrollbar}
	}
	if s.resolve.enabled {
		children = append(children, s.resolve.bar)
	}
	return children
}

// top is the first pane row: a resolving comparison shows its decision bar
// above the panes.
func (s *Screen) top() int {
	if s.resolve.enabled {
		return min(1, s.Bounds().H)
	}
	return 0
}
func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	previous := s.presented
	oldRows := s.rows()
	oldOffset := s.scroll.Offset()
	anchor := Row{}
	if oldOffset < len(oldRows) {
		anchor = oldRows[oldOffset]
	}
	s.SetBounds(r)
	s.presented = Unified
	if s.preferred == SideBySide && r.W >= 60 {
		s.presented = SideBySide
	}
	top := s.top()
	s.resolve.bar.Layout(ctx, ui.Rect{})
	if top > 0 {
		s.resolve.bar.Layout(ctx, ui.Rect{W: r.W, H: top})
	}
	r = ui.Rect{X: r.X, Y: r.Y + top, W: r.W, H: max(0, r.H-top)}
	s.scroll.SetViewport(max(0, r.H-2))
	s.syncScroll()
	if previous != s.presented {
		for index, row := range s.rows() {
			if anchor.AfterLine > 0 && row.AfterLine == anchor.AfterLine || anchor.AfterLine == 0 && anchor.BeforeLine > 0 && row.BeforeLine == anchor.BeforeLine {
				s.scroll.ScrollTo(index)
				break
			}
		}
	}
	w := max(0, r.W-1)
	if s.presented == SideBySide {
		a, g, b := s.split.Resolve(w)
		s.before.Layout(ctx, ui.Rect{Y: top, W: a, H: r.H})
		s.divider.SetCurrentFirst(a)
		s.divider.Layout(ctx, ui.Rect{X: a, Y: top, W: g, H: r.H})
		s.after.Layout(ctx, ui.Rect{X: a + g, Y: top, W: b, H: r.H})
	} else {
		s.unified.Layout(ctx, ui.Rect{Y: top, W: w, H: r.H})
	}
	if previous != s.presented {
		s.rebuildMatches()
		if len(s.search.Matches) > 0 {
			s.search.Active = 0
			s.revealMatch()
		}
	}
	s.scrollbar.Layout(ctx, ui.Rect{X: w, Y: top + min(1, r.H), W: min(1, r.W), H: max(0, r.H-2)})
}
func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	ui.FillRect(buf, ox, oy, s.Bounds(), cell.BlankCell(s.tokens.Color(ui.ColorBg)))
}
func (s *Screen) Semantics() ui.Semantics {
	if s.resolve.enabled {
		return ui.Semantics{Role: ui.RoleGroup, Label: "Comparison with choices", Value: s.prepared.Comparison.Path, Description: s.ResolutionStatus()}
	}
	return ui.Semantics{Role: ui.RoleGroup, Label: "Read-only comparison", Value: s.prepared.Comparison.Path}
}
func (s *Screen) Handle(_ ui.EventContext, e ui.Event) ui.EventResult {
	if !e.IsKey() {
		return ui.EventResult{}
	}
	if s.resolutionKey(e.Key.String()) {
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	}
	switch e.Key.String() {
	case "alt+down":
		s.NavigateHunk(1)
	case "alt+up":
		s.NavigateHunk(-1)
	case "f3":
		s.NextMatch(false)
	case "shift+f3":
		s.NextMatch(true)
	default:
		return ui.EventResult{}
	}
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
}
func (s *Screen) HandleAction(_ ui.EventContext, a ui.Action) ui.EventResult {
	if s.resolve.enabled && s.resolutionAction(a.ID) {
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	}
	if a.ID == ui.ActionSplitResize {
		if resize, ok := a.Payload.(ui.SplitResize); ok && resize.ID == s.divider.ID() {
			w := max(1, s.Bounds().W-1)
			s.split.SetFraction(float64(resize.First) / float64(w))
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
		}
	}
	return ui.EventResult{Action: &a}
}
func (s *Screen) NavigateHunk(direction int) bool {
	rows := s.prepared.unifiedHunks
	if s.presented == SideBySide {
		rows = s.prepared.hunks
	}
	if len(rows) == 0 {
		return false
	}
	if s.hunkIndex < 0 {
		s.hunkIndex = 0
		if direction < 0 {
			s.hunkIndex = len(rows) - 1
		}
	} else {
		s.hunkIndex = (s.hunkIndex + direction + len(rows)) % len(rows)
	}
	s.revealChange()
	return true
}

// revealChange scrolls the current change into view with one row of context.
func (s *Screen) revealChange() {
	rows := s.prepared.unifiedHunks
	if s.presented == SideBySide {
		rows = s.prepared.hunks
	}
	if s.hunkIndex < 0 || s.hunkIndex >= len(rows) {
		return
	}
	s.scroll.ScrollTo(max(0, rows[s.hunkIndex]-1))
}

func (s *Screen) SetQuery(q string) {
	s.query = q
	s.rebuildMatches()
	if len(s.search.Matches) > 0 {
		s.search.Active = 0
		s.revealMatch()
	}
}
func (s *Screen) SearchStatus() string {
	return s.search.Status()
}
func (s *Screen) rebuildMatches() {
	s.matchIndex = -1
	if s.geometryStatus() != "" {
		s.search.Suspend()
		return
	}
	if s.query == "" {
		s.search.Reset("", nil)
		return
	}
	rows, presented := s.rows(), s.presented
	s.search.Reset(s.query, func(yield func(SearchLine) bool) {
		for i, row := range rows {
			if presented == SideBySide && row.BeforeLine > 0 {
				if !yield(SearchLine{Row: i, Side: 1, Text: row.Before}) {
					return
				}
			}
			if row.AfterLine > 0 {
				if !yield(SearchLine{Row: i, Side: 2, Text: row.After}) {
					return
				}
			} else if presented != SideBySide && row.BeforeLine > 0 {
				if !yield(SearchLine{Row: i, Side: 0, Text: row.Before}) {
					return
				}
			}
		}
	})
}

func (s *Screen) SearchCommand() tea.Cmd      { return s.search.Command(s.revealMatch) }
func (s *Screen) SearchDone() <-chan struct{} { return s.search.Done() }
func (s *Screen) StopSearch()                 { s.search.Cancel() }
func (s *Screen) WaitSearch()                 { s.search.Wait() }
func (s *Screen) matchPane(m SearchHit) *pane {
	if s.presented != SideBySide {
		return s.unified
	}
	if m.Side == 1 {
		return s.before
	}
	return s.after
}
func (s *Screen) revealMatch() {
	if s.search.Active < 0 || s.search.Active >= len(s.search.Matches) {
		return
	}
	m := s.search.Matches[s.search.Active]
	s.matchIndex = m.Row
	s.scroll.EnsureVisible(m.Row)
	s.matchPane(m).horizontal.EnsureVisible(m.End - 1)
	if m.End > s.matchPane(m).horizontal.Offset()+s.matchPane(m).horizontal.Viewport() || m.Start > s.matchPane(m).horizontal.Offset()+s.matchPane(m).horizontal.Viewport()/2 {
		s.matchPane(m).horizontal.ScrollTo(max(0, m.Start-1))
	}
	if m.Start < s.matchPane(m).horizontal.Offset() {
		s.matchPane(m).horizontal.ScrollTo(m.Start)
	}
}
func (s *Screen) NextMatch(previous bool) bool {
	if len(s.search.Matches) == 0 {
		return false
	}
	direction := 1
	if previous {
		direction = -1
	}
	if s.search.Active < 0 {
		s.search.Active = 0
		if previous {
			s.search.Active = len(s.search.Matches) - 1
		}
	} else {
		s.search.Active = (s.search.Active + direction + len(s.search.Matches)) % len(s.search.Matches)
	}
	s.revealMatch()
	return true
}

type pane struct {
	ui.Base
	owner                   *Screen
	before, unified         bool
	horizontal              *ui.ScrollModel
	bar                     *ui.ScrollbarControl
	start, end              point
	selected, dragging, all bool
}

func (p *pane) Children() []ui.Component { return []ui.Component{p.bar} }
func (p *pane) Layout(ctx ui.LayoutContext, r ui.Rect) {
	p.SetBounds(r)
	p.horizontal.SetViewport(max(0, r.W-p.gutter()))
	width := p.owner.prepared.afterWidth
	if p.before {
		width = p.owner.prepared.beforeWidth
	}
	if p.unified {
		width = max(width, p.owner.prepared.beforeWidth)
	}
	p.horizontal.SetContent(width)
	p.bar.Layout(ctx, ui.Rect{X: min(r.W, p.gutter()), Y: max(0, r.H-1), W: max(0, r.W-p.gutter()), H: min(1, r.H)})
}
func (p *pane) gutter() int {
	n := max(len(p.owner.prepared.rows), 1)
	width := len(fmt.Sprint(n)) + 2
	if p.unified {
		width = 2*len(fmt.Sprint(n)) + 4
	}
	return min(width+p.boxWidth(), p.Bounds().W)
}

// boxWidth is the gutter space of the choice boxes: one box in each split
// pane, the left and right boxes side by side in the unified pane.
func (p *pane) boxWidth() int {
	switch {
	case !p.owner.resolve.enabled:
		return 0
	case p.unified:
		return 2*boxWidth - 1 // "[x][ ] "
	}
	return boxWidth
}

// boxSides lists the sides of the boxes this pane shows, left first.
func (p *pane) boxSides() []bool {
	if p.unified {
		return []bool{true, false}
	}
	return []bool{p.before}
}

// paintBoxes draws the choice boxes of a change: [x] keeps that side. Open
// changes are in warning ink and the current change in accent ink.
func (p *pane) paintBoxes(buf *surface.CellBuffer, x, y, change int, bg cell.Color) {
	s := p.owner
	t := s.tokens
	choice := s.resolve.choices[change]
	role, attr := ui.ColorMuted, cell.Attrs(0)
	if choice == ChoiceNone {
		role = ui.ColorWarning
	}
	if change == s.hunkIndex {
		role, attr = ui.ColorAccent, cell.AttrBold
	}
	for _, left := range p.boxSides() {
		box := "[ ]"
		if choice != ChoiceNone && s.kept(change, left) {
			box = "[x]"
		}
		buf.WriteString(x, y, box, t.Color(role), bg, attr)
		x += boxWidth - 1
	}
}
func (p *pane) source(r Row) (string, int, ui.SemanticColor) {
	if p.unified {
		if r.AfterLine > 0 {
			return r.After, r.AfterLine, r.Kind
		}
		return r.Before, r.BeforeLine, r.Kind
	}
	if p.before {
		role := ui.ColorFg
		if r.Kind != ui.ColorFg {
			role = ui.ColorDiffDeletion
		}
		return r.Before, r.BeforeLine, role
	}
	role := ui.ColorFg
	if r.Kind != ui.ColorFg {
		role = ui.ColorDiffAddition
	}
	return r.After, r.AfterLine, role
}

func (p *pane) sourceLines(r Row) (SourceLines, int) {
	if p.before || p.unified && r.AfterLine == 0 {
		return p.owner.prepared.beforeSource, r.BeforeLine
	}
	return p.owner.prepared.afterSource, r.AfterLine
}
func (p *pane) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	r := p.Bounds()
	if r.Empty() {
		return
	}
	s := p.owner
	t := s.tokens
	bg := t.Color(ui.ColorBg)
	x, y := ox+r.X, oy+r.Y
	if status := s.geometryStatus(); status != "" {
		buf.WriteString(x, y, rendering.TruncateVisual(status, r.W, "…"), t.Color(ui.ColorMuted), bg, 0)
		return
	}
	label := s.prepared.Comparison.AfterLabel
	if p.before {
		label = s.prepared.Comparison.BeforeLabel
	}
	if p.unified {
		label = s.prepared.Comparison.BeforeLabel + " → " + s.prepared.Comparison.AfterLabel
	}
	buf.WriteString(x, y, rendering.TruncateVisual(label, r.W, "…"), t.Color(ui.ColorMuted), bg, cell.AttrBold)
	rows := s.rows()
	g := p.gutter()
	for local := 0; local < max(0, r.H-2); local++ {
		index := s.scroll.Offset() + local
		if index >= len(rows) {
			break
		}
		row := rows[index]
		_, line, role := p.source(row)
		rowBG := bg
		if line != 0 && role == ui.ColorDiffAddition {
			rowBG = t.Color(ui.ColorDiffAdditionBg)
		} else if line != 0 && role == ui.ColorDiffDeletion {
			rowBG = t.Color(ui.ColorDiffDeletionBg)
		}
		textAttr := cell.Attrs(0)
		change, boxes := s.firstChangeRow(index)
		if change >= 0 && !s.kept(change, p.before || p.unified && row.AfterLine == 0) {
			// A side the decision drops reads as removed text.
			rowBG, role, textAttr = bg, ui.ColorMuted, cell.AttrStrikethrough
		}
		buf.FillRect(surface.Rect{X: x, Y: y + local + 1, W: r.W, H: 1}, cell.BlankCell(rowBG))
		if s.resolve.enabled && boxes {
			p.paintBoxes(buf, x, y+local+1, change, rowBG)
		}
		if line == 0 {
			continue
		}
		box := p.boxWidth()
		number := fmt.Sprintf("%*d ", max(0, g-box-2), line)
		if !p.unified {
			mark := " "
			switch role {
			case ui.ColorDiffAddition:
				mark = "+"
			case ui.ColorDiffDeletion:
				mark = "-"
			}
			number += mark
		}
		if p.unified {
			mark := " "
			if role == ui.ColorDiffAddition {
				mark = "+"
			}
			if role == ui.ColorDiffDeletion {
				mark = "-"
			}
			old, next := "", ""
			if row.BeforeLine > 0 {
				old = fmt.Sprint(row.BeforeLine)
			}
			if row.AfterLine > 0 {
				next = fmt.Sprint(row.AfterLine)
			}
			nw := max(1, (g-box-4)/2)
			number = fmt.Sprintf("%-*s%s", g-box-1, fmt.Sprintf("%*s %*s", nw, old, nw, next), mark)
		}
		buf.WriteString(x+box, y+local+1, rendering.TruncateVisual(number, g-box, ""), t.Color(ui.ColorMuted), rowBG, 0)
		syntax := s.prepared.afterSyntax
		if p.before || p.unified && row.AfterLine == 0 {
			syntax = s.prepared.beforeSyntax
		}
		var spans []syntaxSpan
		if line <= len(syntax) {
			spans = syntax[line-1]
		}
		spanIndex := 0
		source, sourceNumber := p.sourceLines(row)
		cursor := source.cursor(sourceNumber, p.horizontal.Offset(), p.horizontal.Offset()+r.W-g)
		for unit, ok := cursor.next(); ok; unit, ok = cursor.next() {
			left := unit.column - p.horizontal.Offset()
			if left >= 0 && left+unit.width <= r.W-g {
				fg, fill := t.Color(role), rowBG
				for spanIndex < len(spans) && spans[spanIndex].end <= unit.offset {
					spanIndex++
				}
				if spanIndex < len(spans) {
					if ink, ok := s.sourceInk(spans[spanIndex].kind); ok {
						fg = ink
					}
				}
				sourceFG := fg
				for _, j := range s.search.ByRow[index] {
					m := s.search.Matches[j]
					if s.matchPane(m) == p && m.Row == index && unit.column < m.End && unit.column+unit.width > m.Start {
						fill = t.FindMatchFill(j == s.search.Active)
						fg = cell.ContrastInk(fill)
						break
					}
				}
				if textAttr != 0 {
					fg = t.Color(ui.ColorMuted)
				}
				if p.contains(index, unit.column, unit.column+unit.width) {
					fg, fill = t.TextSelectionColors(sourceFG)
				}
				buf.WriteString(x+g+left, y+local+1, unit.display, fg, fill, textAttr)
			}
		}
	}
}
func (p *pane) ordered() (point, point) {
	a, b := p.start, p.end
	if a.row > b.row || a.row == b.row && a.column > b.column {
		a, b = b, a
	}
	return a, b
}
func (p *pane) contains(row, start, end int) bool {
	if !p.selected {
		return false
	}
	if p.all {
		return true
	}
	a, b := p.ordered()
	return row >= a.row && row <= b.row && (row > a.row || end > a.column) && (row < b.row || start < b.column)
}

// SelectedText excludes absent alignment rows and painted line numbers. Source
// bytes (tabs, CRLF and missing final newlines) are preserved.
func (p *pane) SelectedText() string {
	if !p.selected {
		return ""
	}
	if p.all {
		c := p.owner.prepared.Comparison
		if !p.unified && p.before {
			return c.Before
		}
		if !p.unified {
			return c.After
		}
	}
	a, b := p.ordered()
	if p.all {
		a = point{}
		b = point{row: len(p.owner.rows()) - 1, column: 1 << 30}
	}
	var out strings.Builder
	for i := max(0, a.row); i <= b.row && i < len(p.owner.rows()); i++ {
		text, line, _ := p.source(p.owner.rows()[i])
		if line == 0 {
			continue
		}
		from, to := 0, 1<<30
		if i == a.row {
			from = a.column
		}
		if i == b.row {
			to = b.column
		}
		source, number := p.sourceLines(p.owner.rows()[i])
		for unit := range source.rangeUnits(number, from, to) {
			if unit.column+unit.width > from && unit.column < to {
				out.WriteString(unit.raw)
			}
		}
		if i < b.row || p.all {
			if strings.HasSuffix(text, "\r\n") {
				out.WriteString("\r\n")
			} else if strings.HasSuffix(text, "\n") {
				out.WriteByte('\n')
			}
		}
	}
	return out.String()
}
func (p *pane) pointer(ctx ui.EventContext) point {
	target := point{row: max(0, min(len(p.owner.rows())-1, p.owner.scroll.Offset()+max(0, ctx.LocalY-1))), column: max(0, ctx.LocalX-p.gutter()+p.horizontal.Offset())}
	return p.snap(target)
}
func (p *pane) snap(target point) point {
	rows := p.owner.rows()
	if len(rows) == 0 {
		return point{}
	}
	target.row = max(0, min(len(rows)-1, target.row))
	source, number := p.sourceLines(rows[target.row])
	for u := range source.rangeUnits(number, target.column, target.column+1) {
		if target.column < u.column+u.width {
			target.column = u.column
			return target
		}
	}
	target.column = source.LineWidth(number)
	return target
}
func (p *pane) extend(key string) {
	if len(p.owner.rows()) == 0 {
		return
	}
	if !p.selected || p.all {
		p.start = p.snap(point{row: p.owner.scroll.Offset()})
		p.end = p.start
	}
	p.selected = true
	p.all = false
	next := p.end
	switch key {
	case "shift+up":
		next.row--
	case "shift+down":
		next.row++
	case "shift+left":
		next.column--
	case "shift+right":
		source, number := p.sourceLines(p.owner.rows()[next.row])
		for u := range source.rangeUnits(number, next.column, int(^uint(0)>>1)) {
			if u.column >= next.column {
				next.column = u.column + u.width
				break
			}
		}
	}
	p.end = p.snap(next)
	p.owner.scroll.EnsureVisible(p.end.row)
	p.horizontal.EnsureVisible(p.end.column)
}
func (p *pane) CancelPointerCapture() {
	if p.dragging {
		p.selected = false
		p.all = false
	}
	p.dragging = false
}
func (p *pane) Handle(ctx ui.EventContext, e ui.Event) ui.EventResult {
	s := p.owner
	if s.geometryStatus() != "" {
		return ui.EventResult{Handled: true}
	}
	result := ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	if e.IsKey() {
		if ui.IsCopyChord(e.Key) {
			if !p.selected {
				return ui.EventResult{}
			}
			result.Cmd = rendering.CopyToClipboardCmd(p.SelectedText())
			return result
		}
		switch e.Key.String() {
		case "shift+left", "shift+right", "shift+up", "shift+down":
			if len(s.rows()) == 0 {
				return result
			}
			p.extend(e.Key.String())
			return result
		case "ctrl+a":
			p.all = true
			p.selected = true
			return result
		case "esc":
			if !p.selected {
				return ui.EventResult{}
			}
			p.CancelPointerCapture()
			p.selected = false
			p.all = false
			return result
		case "up":
			s.scroll.ScrollBy(-1)
		case "down":
			s.scroll.ScrollBy(1)
		case "pgup":
			s.scroll.ScrollBy(-s.scroll.Viewport())
		case "pgdown":
			s.scroll.ScrollBy(s.scroll.Viewport())
		case "home", "ctrl+home":
			s.scroll.ScrollTo(0)
		case "end", "ctrl+end":
			s.scroll.ScrollTo(s.scroll.MaxOffset())
		case "left":
			p.horizontal.ScrollBy(-4)
		case "right":
			p.horizontal.ScrollBy(4)
		default:
			return ui.EventResult{}
		}
		return result
	}
	switch e.Kind {
	case ui.EventMouseWheel:
		switch e.MouseWheel.Button {
		case tea.MouseWheelLeft:
			p.horizontal.ScrollBy(-4)
		case tea.MouseWheelRight:
			p.horizontal.ScrollBy(4)
		case tea.MouseWheelUp:
			if e.MouseWheel.Mod.Contains(tea.ModShift) {
				p.horizontal.ScrollBy(-4)
			} else {
				s.scroll.ScrollBy(-3)
			}
		case tea.MouseWheelDown:
			if e.MouseWheel.Mod.Contains(tea.ModShift) {
				p.horizontal.ScrollBy(4)
			} else {
				s.scroll.ScrollBy(3)
			}
		}
	case ui.EventMouseClick:
		if e.MouseClick.Button != tea.MouseLeft || ctx.LocalY < 1 {
			return ui.EventResult{}
		}
		if s.resolve.enabled {
			row := s.scroll.Offset() + ctx.LocalY - 1
			if change, boxes := s.firstChangeRow(row); boxes && ctx.LocalX < p.boxWidth() {
				left := p.before
				if p.unified {
					left = ctx.LocalX < boxWidth-1
				}
				s.toggle(change, left)
				return result
			}
			if change := s.changeAt(row); change >= 0 && row < len(s.rows()) {
				s.hunkIndex = change
			}
		}
		p.start = p.pointer(ctx)
		p.end = p.start
		p.dragging = true
		p.selected = true
		p.all = false
	case ui.EventMouseMotion:
		if !p.dragging {
			return ui.EventResult{}
		}
		p.end = p.pointer(ctx)
		if ctx.LocalY < 1 {
			s.scroll.ScrollBy(-1)
		} else if ctx.LocalY >= p.Bounds().H-1 {
			s.scroll.ScrollBy(1)
		}
	case ui.EventMouseRelease:
		if !p.dragging {
			return ui.EventResult{}
		}
		p.end = p.pointer(ctx)
		p.dragging = false
		text := p.SelectedText()
		p.selected = false
		if text != "" {
			result.Cmd = rendering.CopyToClipboardCmd(text)
		}
	default:
		return ui.EventResult{}
	}
	return result
}
func (p *pane) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleTextInput, Label: "Read-only comparison text", Description: "Select and copy source text; synchronized vertical scrolling"}
}

func (s *Screen) ScrollOffset() int { return s.scroll.Offset() }
