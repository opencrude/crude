package findinfiles

import (
	"sort"
	"strings"

	"crude/fileio"
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
)

// rowKind distinguishes per-file header rows from match rows in the results
// list.
type rowKind uint8

const (
	rowHeader rowKind = iota
	rowMatch
)

// displayRow is one visible list row. Header rows carry the group's first
// result index so selecting a header still previews and opens something
// sensible.
type displayRow struct {
	kind   rowKind
	group  int
	result int
}

// resultGroup collects the result indices of one file, ordered for display.
type resultGroup struct {
	path string
	rel  string
	rows []int
}

// buildGroups groups results by file. Group order follows the first
// occurrence in the score-sorted result list; within a group, rows are
// ordered by line and column (filename-only rows first, since their line
// is zero).
func buildGroups(results []fileio.ProjectSearchMatch) []resultGroup {
	byPath := make(map[string]int)
	groups := make([]resultGroup, 0, 8)
	for i, r := range results {
		gi, ok := byPath[r.Path]
		if !ok {
			gi = len(groups)
			byPath[r.Path] = gi
			groups = append(groups, resultGroup{path: r.Path, rel: r.RelativePath})
		}
		groups[gi].rows = append(groups[gi].rows, i)
	}
	for gi := range groups {
		rows := groups[gi].rows
		sort.SliceStable(rows, func(a, b int) bool {
			ra, rb := results[rows[a]], results[rows[b]]
			if ra.Line != rb.Line {
				return ra.Line < rb.Line
			}
			return ra.Column < rb.Column
		})
	}
	return groups
}

// rebuildRows flattens groups into display rows, omitting the match rows of
// collapsed groups.
func (s *Screen) rebuildRows() {
	s.rows = s.rows[:0]
	for gi, g := range s.groups {
		if len(g.rows) == 0 {
			continue
		}
		s.rows = append(s.rows, displayRow{kind: rowHeader, group: gi, result: g.rows[0]})
		if s.collapsed[g.path] {
			continue
		}
		for _, ri := range g.rows {
			s.rows = append(s.rows, displayRow{kind: rowMatch, group: gi, result: ri})
		}
	}
}

// setResults replaces the flat result set and rebuilds groups, rows, and the
// initial selection (the first match row).
func (s *Screen) setResults(results []fileio.ProjectSearchMatch) {
	s.results = results
	s.collapsed = make(map[string]bool)
	s.groups = buildGroups(results)
	s.rebuildRows()
	if len(s.rows) == 0 {
		s.selRow = -1
		s.selected = -1
		s.previewSrc = nil
		return
	}
	s.selRow = s.firstMatchRow()
	s.selected = s.rows[s.selRow].result
}

// firstMatchRow returns the first match row index, falling back to row 0.
func (s *Screen) firstMatchRow() int {
	for i, row := range s.rows {
		if row.kind == rowMatch {
			return i
		}
	}
	return 0
}

// rowAt returns the display row at idx.
func (s *Screen) rowAt(idx int) (displayRow, bool) {
	if idx < 0 || idx >= len(s.rows) {
		return displayRow{}, false
	}
	return s.rows[idx], true
}

// headerRowOf returns the display index of a group's header row.
func (s *Screen) headerRowOf(group int) int {
	for i, row := range s.rows {
		if row.kind == rowHeader && row.group == group {
			return i
		}
	}
	return 0
}

// toggleGroup collapses or expands one file group and parks the selection on
// its header row.
func (s *Screen) toggleGroup(group int) {
	if group < 0 || group >= len(s.groups) {
		return
	}
	path := s.groups[group].path
	s.collapsed[path] = !s.collapsed[path]
	s.rebuildRows()
	s.selRow = s.headerRowOf(group)
	if row, ok := s.rowAt(s.selRow); ok {
		if row.result != s.selected {
			s.selected = row.result
			s.rebuildPreview()
		}
	}
	s.sync()
}

// GroupCollapsed reports whether the file group containing path is collapsed.
func (s *Screen) GroupCollapsed(path string) bool { return s.collapsed[path] }

// DisplayRowCount returns the number of visible list rows (headers plus
// expanded matches).
func (s *Screen) DisplayRowCount() int { return len(s.rows) }

// SelectedRow returns the selected display row index (-1 when empty).
func (s *Screen) SelectedRow() int { return s.selRow }

// SelectedRowIsHeader reports whether the selection sits on a group header.
func (s *Screen) SelectedRowIsHeader() bool {
	row, ok := s.rowAt(s.selRow)
	return ok && row.kind == rowHeader
}

// selectRow moves the selection to a display row, deriving the selected
// result (headers map to their group's first match) and refreshing the
// preview when it changed.
func (s *Screen) selectRow(idx int) {
	if len(s.rows) == 0 {
		s.selRow = -1
		s.selected = -1
		s.previewSrc = nil
		return
	}
	idx = max(0, min(idx, len(s.rows)-1))
	if idx == s.selRow {
		return
	}
	s.selRow = idx
	s.list.list.SelectIndex(idx)
	if res := s.rows[idx].result; res != s.selected {
		s.selected = res
		s.rebuildPreview()
	}
	s.sync()
}

// collapseAtSelection implements Left: a header collapses its group (or is a
// no-op when already collapsed); a match row jumps to its header.
func (s *Screen) collapseAtSelection() ui.EventResult {
	row, ok := s.rowAt(s.selRow)
	if !ok {
		return ui.EventResult{Handled: true}
	}
	if row.kind == rowHeader {
		if !s.collapsed[s.groups[row.group].path] {
			s.toggleGroup(row.group)
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	}
	s.selectRow(s.headerRowOf(row.group))
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
}

// expandAtSelection implements Right: a collapsed header expands; an expanded
// header steps into its first match row.
func (s *Screen) expandAtSelection() ui.EventResult {
	row, ok := s.rowAt(s.selRow)
	if !ok || row.kind != rowHeader {
		return ui.EventResult{Handled: true}
	}
	if s.collapsed[s.groups[row.group].path] {
		s.toggleGroup(row.group)
	} else if s.selRow+1 < len(s.rows) && s.rows[s.selRow+1].kind == rowMatch {
		s.selectRow(s.selRow + 1)
	}
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
}

// paintResultRow is the custom VirtualList row painter: group headers show a
// chevron, file icon, relative path, and match count; match rows show the
// line number and line text with every matched rune highlighted.
func (s *Screen) paintResultRow(buf *surface.CellBuffer, x, y, width int, item ui.VirtItem, _ int, st ui.ControlState, _ ui.PaintContext, tokens *ui.TokenSet) {
	row, ok := item.Payload.(displayRow)
	if !ok || tokens == nil || width < 4 {
		return
	}
	fg, bg := tokens.ListRowColors(st)
	muted := tokens.Color(ui.ColorMuted)
	matchFG := tokens.Color(ui.ColorAccent)
	matchAttr := cell.AttrBold
	if st == ui.StateFocused {
		muted = fg
		matchFG = fg
		matchAttr = cell.AttrUnderline
	}
	xMax := x + width

	if row.kind == rowHeader {
		g := s.groups[row.group]
		glyphs := tokens.Glyphs()
		chev := string(ui.FirstRune(glyphs.TreeExpanded, '▾'))
		if s.collapsed[g.path] {
			chev = string(ui.FirstRune(glyphs.TreeCollapsed, '▸'))
		}
		count := itoa(len(g.rows))
		countW := rendering.VisibleWidth(count)
		cx := writeClipped(buf, x, xMax, y, chev+" ", muted, bg, 0)
		if s.icons != nil {
			if icon := s.icons.ResolveIcon(baseName(g.rel), false, false, false).Icon; icon != "" {
				cx = writeClipped(buf, cx, xMax, y, icon+" ", fg, bg, 0)
			}
		}
		relMax := xMax - countW - 1
		cx = writeClipped(buf, cx, relMax, y, g.rel, fg, bg, cell.AttrBold)
		countX := xMax - countW
		if countX >= cx+1 {
			writeClipped(buf, countX, xMax, y, count, muted, bg, 0)
		}
		return
	}

	r := s.results[row.result]
	if r.NameOnly {
		text, positions := nameOnlyRowText(r)
		cx := writeClipped(buf, x, xMax, y, "      ", muted, bg, 0)
		writeHighlightedClipped(buf, cx, xMax, y, text, positions, fg, matchFG, bg, 0, matchAttr)
		return
	}
	lineNo := itoa(r.Line)
	for w := rendering.VisibleWidth(lineNo); w < 4; w++ {
		lineNo = " " + lineNo
	}
	cx := writeClipped(buf, x, xMax, y, lineNo+": ", muted, bg, 0)
	text, positions := trimLeadingWhitespace(strings.TrimRight(r.LineText, "\r\n"), r.TextMatches)
	writeHighlightedClipped(buf, cx, xMax, y, text, positions, fg, matchFG, bg, 0, matchAttr)
}

// nameOnlyRowText renders a filename-only match as its base name with the
// fuzzy positions shifted from RelativePath space into base-name space.
func nameOnlyRowText(r fileio.ProjectSearchMatch) (string, []int) {
	base := baseName(r.RelativePath)
	offset := len([]rune(r.RelativePath)) - len([]rune(base))
	positions := make([]int, 0, len(r.PathMatches))
	for _, p := range r.PathMatches {
		if p >= offset {
			positions = append(positions, p-offset)
		}
	}
	return base, positions
}

// baseName returns the final element of a slash-normalized relative path.
func baseName(rel string) string {
	if i := strings.LastIndexByte(rel, '/'); i >= 0 {
		return rel[i+1:]
	}
	return rel
}

// trimLeadingWhitespace strips indentation from a match line and shifts the
// match rune positions to stay aligned with the trimmed text.
func trimLeadingWhitespace(text string, positions []int) (string, []int) {
	runes := []rune(text)
	n := 0
	for n < len(runes) && (runes[n] == ' ' || runes[n] == '\t') {
		n++
	}
	if n == 0 {
		return text, positions
	}
	shifted := make([]int, 0, len(positions))
	for _, p := range positions {
		if p >= n {
			shifted = append(shifted, p-n)
		}
	}
	return string(runes[n:]), shifted
}

// writeClipped writes text left-to-right, stopping before xMax. Returns the
// next x position.
func writeClipped(buf *surface.CellBuffer, x, xMax, y int, text string, fg, bg cell.Color, attr cell.Attrs) int {
	for _, r := range text {
		w := rendering.VisibleWidth(string(r))
		if x+w > xMax {
			return x
		}
		x = buf.WriteString(x, y, string(r), fg, bg, attr)
	}
	return x
}

// writeHighlightedClipped is writeClipped with per-rune match highlighting.
// Positions are ascending rune indices into text.
func writeHighlightedClipped(buf *surface.CellBuffer, x, xMax, y int, text string, positions []int, fg, matchFG, bg cell.Color, attr, matchAttr cell.Attrs) int {
	pi := 0
	for ri, r := range []rune(text) {
		w := rendering.VisibleWidth(string(r))
		if x+w > xMax {
			return x
		}
		for pi < len(positions) && positions[pi] < ri {
			pi++
		}
		if pi < len(positions) && positions[pi] == ri {
			x = buf.WriteString(x, y, string(r), matchFG, bg, attr|matchAttr)
		} else {
			x = buf.WriteString(x, y, string(r), fg, bg, attr)
		}
	}
	return x
}

// itoa converts a non-negative int without fmt allocation overhead.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
