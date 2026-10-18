package editor

import (
	"crude/textsearch"
	"time"
)

// SearchOptions controls how a query is matched. Defaults are case-
// insensitive, substring (not whole word), and literal (not regex).
type SearchOptions struct {
	CaseSensitive bool
	WholeWord     bool
	Regex         bool
}

// SearchMatch is a single hit within the buffer. Start and End are
// line/col positions; the match is the half-open range [Start, End).
type SearchMatch struct {
	Start                  Position
	End                    Position
	Matched                string
	startOffset, endOffset int
	sourceMatch            textsearch.Match
}

// SearchState holds the live find/replace UI state for one tab. The
// editor widget reads Matches and CurrentIdx to render highlights and
// the floating popup reads Query, Replace, Options, and CurrentIdx to
// show its UI.
type SearchState struct {
	// UI text
	Query   string
	Replace string
	Options SearchOptions

	// Computed matches for the current Query + Options. Updated
	// whenever the user types a character in the find input. Matches
	// are in buffer order; CurrentIdx is an index into this slice.
	Matches     []SearchMatch
	CurrentIdx  int
	LastCompute time.Time

	// True when the search UI is open (Ctrl+F or Ctrl+H pressed).
	// The editor render path checks this to draw highlights.
	Open    bool
	Error   string
	matcher *textsearch.Matcher
}

// NewSearchState returns a fresh search state.
func NewSearchState() *SearchState {
	return &SearchState{
		CurrentIdx: -1,
	}
}

// HasMatches reports whether the last search found anything.
func (s *SearchState) HasMatches() bool {
	return len(s.Matches) > 0
}

// CurrentMatch returns the match under the cursor, or false if none.
func (s *SearchState) CurrentMatch() (SearchMatch, bool) {
	if s.CurrentIdx < 0 || s.CurrentIdx >= len(s.Matches) {
		return SearchMatch{}, false
	}
	return s.Matches[s.CurrentIdx], true
}

// Next advances the cursor to the next match (wrapping around).
func (s *SearchState) Next() {
	if len(s.Matches) == 0 {
		s.CurrentIdx = -1
		return
	}
	s.CurrentIdx = (s.CurrentIdx + 1) % len(s.Matches)
}

// Prev moves the cursor to the previous match (wrapping around).
func (s *SearchState) Prev() {
	if len(s.Matches) == 0 {
		s.CurrentIdx = -1
		return
	}
	s.CurrentIdx--
	if s.CurrentIdx < 0 {
		s.CurrentIdx = len(s.Matches) - 1
	}
}

// Reset clears matches and resets the cursor. Called when the query
// changes or the buffer mutates.
func (s *SearchState) Reset() {
	s.Matches = nil
	s.CurrentIdx = -1
}

// computeMatches populates Matches for the given buffer text. Returns
// the matches in source order.
//
// Literal queries are escaped before the same source-offset regexp scan.
// Unicode case folding must never change the byte offsets in the source.
// Whole-word boundaries classify original runes, including combining marks.
func (s *SearchState) computeMatches(bufText string) {
	s.Reset()
	s.Error = ""
	if s.Query == "" {
		return
	}

	matcher, err := textsearch.Compile(s.Query, textsearch.Options{CaseSensitive: s.Options.CaseSensitive, WholeWord: s.Options.WholeWord, Regex: s.Options.Regex})
	s.matcher = matcher
	if err != nil {
		s.Error = err.Error()
		return
	}
	for _, hit := range matcher.FindAll(bufText, 0) {
		start, end := offsetToLineCol(bufText, hit.Start, hit.End)
		s.Matches = append(s.Matches, SearchMatch{Start: start, End: end, Matched: bufText[hit.Start:hit.End], startOffset: hit.Start, endOffset: hit.End, sourceMatch: hit})
	}
}

// offsetToLineCol converts a byte offset pair (start, end) into
// (startPos, endPos) with visual columns (tab stops + grapheme widths).
func offsetToLineCol(text string, startOff, endOff int) (Position, Position) {
	startLine, startCol := ByteOffsetToLineCol(text, startOff, tabWidth)
	endLine, endCol := ByteOffsetToLineCol(text, endOff, tabWidth)
	return Position{Line: startLine, Col: startCol}, Position{Line: endLine, Col: endCol}
}

// Search returns the editor's SearchState. Callers can read Query,
// Options, Matches, CurrentIdx, etc. and call SetQuery/SetOptions to
// trigger a recompute.
func (e *Editor) Search() *SearchState {
	e.ensureSearchCurrent()
	return e.search
}

// SetQuery updates the search query and recomputes matches against the
// current buffer contents. The cursor is moved to the first match if
// there is one. Returns the new match count.
func (e *Editor) SetQuery(q string) int {
	if e.search == nil {
		e.search = NewSearchState()
	}
	e.search.Query = q
	e.search.Open = true
	e.recomputeMatches()
	return len(e.search.Matches)
}

// SetOptions updates match options and recomputes matches. The
// CurrentIdx is preserved as long as the new match list is at least
// that long.
func (e *Editor) SetOptions(opts SearchOptions) {
	if e.search == nil {
		e.search = NewSearchState()
	}
	e.search.Options = opts
	e.recomputeMatches()
}

// SetReplace updates the replacement string. Does not affect matches.
func (e *Editor) SetReplace(r string) {
	if e.search == nil {
		e.search = NewSearchState()
	}
	e.search.Replace = r
}

// ConfigureSearch publishes one query/options generation. Replacement edits do
// not re-scan or reset navigation. Opening selects the hit at/after the caret;
// typing never moves the document's insertion point or destroys its selection.
func (e *Editor) ConfigureSearch(query, replacement string, options SearchOptions) {
	if e.search == nil {
		e.search = NewSearchState()
	}
	changed := e.search.Query != query || e.search.Options != options || !e.search.Open
	e.search.Query, e.search.Replace, e.search.Options, e.search.Open = query, replacement, options, true
	if changed || e.searchRevision != e.Revision() {
		e.recomputeMatches()
		if changed && len(e.search.Matches) > 0 {
			e.search.CurrentIdx = 0
			for i, m := range e.search.Matches {
				if !positionBefore(m.End, e.cursor) {
					e.search.CurrentIdx = i
					break
				}
			}
		}
	}
}

// SetSearchOpen toggles whether the search UI is visible. When
// closed, highlights are not drawn.
func (e *Editor) SetSearchOpen(open bool) {
	if e.search == nil {
		e.search = NewSearchState()
	}
	e.search.Open = open
	if !open {
		e.search.Reset()
	}
}

// NextMatch jumps the editor cursor to the next match (and scrolls
// it into view). Wraps around. Returns the match count and whether
// we found one.
func (e *Editor) NextMatch() (int, bool) {
	e.ensureSearchCurrent()
	if e.search == nil || !e.search.HasMatches() {
		return 0, false
	}
	e.search.Next()
	if m, ok := e.search.CurrentMatch(); ok {
		e.SetCursorPosition(m.Start.Line, m.Start.Col)
		e.ScrollToCursor()
		return len(e.search.Matches), true
	}
	return 0, false
}

// PrevMatch jumps the editor cursor to the previous match.
func (e *Editor) PrevMatch() (int, bool) {
	e.ensureSearchCurrent()
	if e.search == nil || !e.search.HasMatches() {
		return 0, false
	}
	e.search.Prev()
	if m, ok := e.search.CurrentMatch(); ok {
		e.SetCursorPosition(m.Start.Line, m.Start.Col)
		e.ScrollToCursor()
		return len(e.search.Matches), true
	}
	return 0, false
}

// ReplaceCurrent replaces the match under the cursor with the
// replace string and re-runs the search. The cursor stays near the
// replacement. Returns the new match count.
func (e *Editor) ReplaceCurrent() int {
	if e.readOnly || e.DeferDocumentEdit(func() { e.ReplaceCurrent() }) {
		return 0
	}
	e.ensureSearchCurrent()
	if e.search == nil || e.search.CurrentIdx < 0 {
		return 0
	}
	if e.search.CurrentIdx >= len(e.search.Matches) {
		return 0
	}
	m := e.search.Matches[e.search.CurrentIdx]
	repl := e.search.matcher.Expand(e.Value(), m.sourceMatch, e.search.Replace)

	e.beginUndoGroup()
	defer e.endUndoGroup()
	cursor := e.cursor
	e.replaceSearchMatch(m, repl, cursor)
	e.cursor = e.buf.positionAtOffset(m.startOffset + len(repl))
	e.notifyContentChanged()
	e.markDocumentWidthAll()
	e.invalidateEditorCaches()

	// Advance past the replaced source, not back to match zero. In particular,
	// replacement text that itself matches must not trap repeated Replace.
	after := m.startOffset + len(repl)
	e.recomputeMatches()
	if len(e.search.Matches) > 0 {
		e.search.CurrentIdx = 0
		for i, hit := range e.search.Matches {
			if hit.startOffset >= after && (m.startOffset != m.endOffset || hit.startOffset > after) {
				e.search.CurrentIdx = i
				break
			}
		}
	}
	if m, ok := e.search.CurrentMatch(); ok {
		e.SetCursorPosition(m.Start.Line, m.Start.Col)
		e.ScrollToCursor()
	}
	return len(e.search.Matches)
}

// ReplaceAll replaces every match in the buffer. Returns the number
// of replacements actually made.
func (e *Editor) ReplaceAll() int {
	if e.readOnly || e.DeferDocumentEdit(func() { e.ReplaceAll() }) {
		return 0
	}
	e.ensureSearchCurrent()
	if e.search == nil || !e.search.HasMatches() {
		return 0
	}
	e.beginUndoGroup()
	defer e.endUndoGroup()
	cursor := e.cursor
	count := 0
	source := e.Value()
	// Iterate from the end so earlier offsets stay valid as we
	// mutate the buffer.
	for i := len(e.search.Matches) - 1; i >= 0; i-- {
		m := e.search.Matches[i]
		repl := e.search.matcher.Expand(source, m.sourceMatch, e.search.Replace)
		e.replaceSearchMatch(m, repl, cursor)
		count++
	}
	e.notifyContentChanged()
	e.markDocumentWidthAll()
	e.invalidateEditorCaches()
	e.clampCursorToLine()
	e.recomputeMatches()
	return count
}

// recomputeMatches re-runs the search against the current buffer.
func (e *Editor) recomputeMatches() {
	if e.search == nil {
		return
	}
	prevIdx := e.search.CurrentIdx
	text := string(e.buf.Bytes())
	e.search.computeMatches(text)
	e.searchRevision = e.Revision()
	e.search.LastCompute = time.Now()
	// Try to preserve the cursor position. If the new list is
	// shorter, clamp.
	if prevIdx >= len(e.search.Matches) {
		prevIdx = len(e.search.Matches) - 1
	}
	if prevIdx < 0 && len(e.search.Matches) > 0 {
		prevIdx = 0
	}
	e.search.CurrentIdx = prevIdx
}

func (e *Editor) ensureSearchCurrent() {
	if e.search != nil && e.search.Query != "" && e.searchRevision != e.Revision() {
		e.recomputeMatches()
	}
}

func (e *Editor) replaceSearchMatch(m SearchMatch, replacement string, cursor Position) {
	if m.Matched == replacement {
		return
	}
	if m.startOffset != m.endOffset {
		_, _ = e.buf.deleteOffsets(m.startOffset, m.endOffset)
		e.recordChange(Change{Type: ActDelete, Text: []byte(m.Matched), Offset: m.startOffset, CursorBefore: cursor})
	}
	if replacement != "" {
		_, _ = e.buf.insertAtOffset(m.startOffset, []byte(replacement))
		e.recordChange(Change{Type: ActInsert, Text: []byte(replacement), Offset: m.startOffset, CursorBefore: cursor})
	}
}
