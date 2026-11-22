package editor

import (
	"cmp"
	"slices"
	"sort"
	"strings"
	"unicode"

	"crude/text/anchor"
	"crude/tui/bidi"
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/types/conversation"
)

// ReviewComment is a display-only comment supplied by the editor's owner.
type ReviewComment struct {
	ID     string
	Anchor conversation.TextAnchor
	Body   string
	Status string
	Muted  bool
}

// ReviewCommentActivatedMsg asks the shell to open the selected comment.
type ReviewCommentActivatedMsg struct {
	Editor *Editor
	ID     string
}

const (
	maxReviewDocumentBytes = 1 << 20
	maxReviewCardRows      = 8
	reviewTextChanged      = "The commented text changed."
	reviewBar              = "▎"
)

type editorReview struct {
	comments []ReviewComment
	resolved *resolvedReview
	layout   *reviewLayout
	jumpID   string
	jumpAt   Position
	pending  bool
}

type resolvedReview struct {
	buffer   *PieceTable
	revision uint64
	cards    []resolvedReviewCard // source order, including unresolved hints
	index    map[string]int
	spans    []reviewSpan
	tabWidth int
}

type resolvedReviewCard struct {
	ReviewComment
	anchor.Result
	line int
}

type reviewSpan struct{ start, end Position }

type reviewLayout struct {
	source *resolvedReview
	width  int
	lines  map[int][]reviewCardRow
	cards  map[string]reviewRowRange
}

type reviewRowRange struct{ start, count int }

type reviewCardRow struct {
	id, body, status string
	muted            bool
}

// SetReviewComments replaces this view's review comments. Nil removes them.
func (e *Editor) SetReviewComments(comments []ReviewComment) {
	r := &e.review
	if slices.Equal(r.comments, comments) {
		return
	}
	*r = editorReview{comments: slices.Clone(comments)}
}

func (e *Editor) resolveReview() *resolvedReview {
	r := &e.review
	if len(r.comments) == 0 || !e.open || e.buf == nil {
		return nil
	}
	if p := r.resolved; p != nil && p.buffer == e.buf && p.revision == e.Revision() {
		return p
	}
	p := &resolvedReview{buffer: e.buf, revision: e.Revision()}
	r.resolved, r.layout, r.jumpID, r.pending = p, nil, "", false
	// The piece table already retains the current bytes. Check their size
	// before making the one string snapshot used by all the anchors.
	e.buf.ensureCache()
	if len(e.buf.cachedBytes) > maxReviewDocumentBytes {
		return p
	}
	text := string(e.buf.cachedBytes)
	p.cards = make([]resolvedReviewCard, 0, len(r.comments))
	for _, c := range r.comments {
		res := anchor.Resolve(text, c.Anchor)
		at := res.Start
		if res.Found && res.End > res.Start {
			at = res.End - 1
		}
		p.cards = append(p.cards, resolvedReviewCard{ReviewComment: c, Result: res, line: e.buf.lineAtOffset(at)})
	}
	slices.SortStableFunc(p.cards, func(a, b resolvedReviewCard) int {
		if n := cmp.Compare(a.Start, b.Start); n != 0 {
			return n
		}
		return cmp.Compare(a.End, b.End)
	})
	p.index = make(map[string]int, len(p.cards))
	for i, c := range p.cards {
		p.index[c.ID] = i
	}
	return p
}

func (e *Editor) currentReviewRows() *reviewLayout {
	p := e.resolveReview()
	if p == nil || len(p.cards) == 0 {
		return nil
	}
	width := e.textColumns()
	if rows := e.review.layout; rows != nil && rows.source == p && rows.width == width {
		return rows
	}
	rows := &reviewLayout{
		source: p, width: width,
		lines: make(map[int][]reviewCardRow), cards: make(map[string]reviewRowRange, len(p.cards)),
	}
	for _, c := range p.cards {
		status := c.Status
		if !c.Exact {
			if status != "" {
				status += " · "
			}
			status += reviewTextChanged
		}
		start := len(rows.lines[c.line])
		line := appendReviewCard(rows.lines[c.line], c.ReviewComment, status, width)
		rows.lines[c.line] = line
		rows.cards[c.ID] = reviewRowRange{start: start, count: len(line) - start}
	}
	e.review.layout = rows
	return rows
}

func (p *reviewLayout) lineRows(line int) []reviewCardRow {
	if p == nil {
		return nil
	}
	return p.lines[line]
}

func reviewPrefixWidth(width int) int {
	bar := rendering.VisibleWidth(reviewBar)
	if width > bar+1 {
		return bar + 1
	}
	return bar
}

func reviewLiteral(text string) string {
	// The comment is literal text, not ANSI or Markdown. Keep ordinary
	// directional text, but do not apply user-supplied bidi controls.
	return safeInlineLabel(strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Bidi_Control, r) {
			return '�'
		}
		return r
	}, text))
}

func appendReviewCard(dst []reviewCardRow, c ReviewComment, status string, width int) []reviewCardRow {
	available := width - reviewPrefixWidth(width)
	start := len(dst)
	add := func(body, status string) {
		dst = append(dst, reviewCardRow{id: c.ID, body: body, status: status, muted: c.Muted})
	}
	if available <= 0 {
		add("", "")
		return dst
	}
	overflow := false
	for para := range strings.SplitSeq(strings.ReplaceAll(c.Body, "\r\n", "\n"), "\n") {
		para = reviewLiteral(strings.ReplaceAll(para, "\t", "    "))
		for _, line := range rendering.WordWrap(para, available) {
			if len(dst)-start == maxReviewCardRows {
				overflow = true
				break
			}
			add(rendering.TruncateVisual(line, available, "…"), "")
		}
		if overflow {
			break
		}
	}
	status = reviewLiteral(status)
	last := &dst[len(dst)-1]
	if status == "" {
		if overflow {
			last.body = "…"
		}
		return dst
	}
	gap := ""
	if last.body != "" {
		gap = "  "
	}
	if !overflow && rendering.VisibleWidth(last.body+gap+status) <= available {
		last.status = gap + status
		return dst
	}
	if len(dst)-start == maxReviewCardRows && rendering.VisibleWidth("…  "+status) <= available {
		last.body, last.status = "…", "  "+status
		return dst
	}
	statusLines := rendering.WordWrap(status, available)
	// Leave room for the status on narrow panes. An ellipsis is always
	// the last body row, even if the muted status needs separate rows.
	if len(statusLines) >= maxReviewCardRows {
		statusLines = statusLines[:maxReviewCardRows-1]
		statusLines[len(statusLines)-1] = "…"
	}
	bodyLimit := maxReviewCardRows - len(statusLines)
	if overflow || len(dst)-start > bodyLimit {
		dst = dst[:start+min(len(dst)-start, bodyLimit)]
		dst[len(dst)-1].body = "…"
	}
	for _, line := range statusLines {
		add("", rendering.TruncateVisual(line, available, "…"))
	}
	return dst
}

func (e *Editor) reviewSpans() []reviewSpan {
	p := e.resolveReview()
	if p == nil || len(p.cards) == 0 {
		return nil
	}
	if p.tabWidth == e.TabWidth() {
		return p.spans
	}
	p.tabWidth = e.TabWidth()
	p.spans = p.spans[:0]
	for _, c := range p.cards {
		if !c.Found || c.Start == c.End {
			continue
		}
		start, end := e.buf.positionAtOffset(c.Start), e.buf.positionAtOffset(c.End)
		if e.buf.positionToOffset(end) != c.End {
			// Bounds may fall inside a grapheme. Highlight the whole unit.
			end = e.buf.nextPosition(end)
		}
		if n := len(p.spans); n > 0 && !positionBefore(p.spans[n-1].end, start) {
			if positionBefore(p.spans[n-1].end, end) {
				p.spans[n-1].end = end
			}
			continue
		}
		p.spans = append(p.spans, reviewSpan{start, end})
	}
	return p.spans
}

func reviewSpansAtLine(spans []reviewSpan, line int) []reviewSpan {
	for len(spans) > 0 && spans[0].end.Line < line {
		spans = spans[1:]
	}
	n := 0
	for n < len(spans) && spans[n].start.Line <= line {
		n++
	}
	return spans[:n]
}

func reviewContainsColumn(spans []reviewSpan, line, col int) bool {
	at := Position{Line: line, Col: col}
	for _, span := range spans {
		if positionBefore(at, span.start) {
			return false
		}
		if positionBefore(at, span.end) {
			return true
		}
	}
	return false
}

func (e *Editor) paintReviewRow(buf *surface.CellBuffer, y, left, right int, row visibleRow, gutter cell.Cell) {
	card := e.rows.review.lineRows(row.line)[row.reviewRow-1]
	bg := e.tokens.Color(ui.ColorSubPaneBg)
	fg, muted := e.tokens.Color(ui.ColorFg), e.tokens.Color(ui.ColorMuted)
	bar := e.tokens.Color(ui.ColorAccent)
	if card.muted {
		bar = muted
	}
	for x := 0; x < left; x++ {
		buf.Set(x, y, gutter)
	}
	for x := left; x < right; x++ {
		buf.Set(x, y, cell.BlankCell(bg))
	}
	buf.WriteLogical(left, y, reviewBar, bar, bg, 0)
	start := left + reviewPrefixWidth(right-left)
	x := buf.WriteLogical(start, y, card.body, fg, bg, 0)
	end := buf.WriteLogical(x, y, card.status, muted, bg, 0)
	if bidi.NeedsLayout(card.body) || bidi.NeedsLayout(card.status) {
		buf.ArrangeCells(start, y, end-start, bidi.LeftToRight)
	}
}

// ReviewCommentRange returns the current byte span. When a quote is missing,
// found is false and the offsets are the clamped position hint.
func (e *Editor) ReviewCommentRange(id string) (start, end int, found bool) {
	if p := e.resolveReview(); p != nil {
		if i, ok := p.index[id]; ok {
			c := p.cards[i]
			return c.Start, c.End, c.Found
		}
	}
	return 0, 0, false
}

// ReviewCommentAtCaret identifies a resolved range containing the caret.
func (e *Editor) ReviewCommentAtCaret() (string, bool) {
	p := e.resolveReview()
	if p == nil || len(p.cards) == 0 {
		return "", false
	}
	at := e.buf.positionToOffset(e.cursor)
	contains := func(c resolvedReviewCard) bool {
		return c.Found && at >= c.Start && (at < c.End || c.Start == c.End && at == c.Start)
	}
	if i, ok := p.index[e.review.jumpID]; ok && e.cursor == e.review.jumpAt && contains(p.cards[i]) {
		return p.cards[i].ID, true
	}
	for _, c := range p.cards {
		if contains(c) {
			return c.ID, true
		}
	}
	return "", false
}

// NextReviewComment moves to the next (or previous) comment, wrapping once.
func (e *Editor) NextReviewComment(forward bool) (string, bool) {
	p := e.resolveReview()
	if p == nil || len(p.cards) == 0 {
		return "", false
	}
	n := len(p.cards)
	i, repeated := p.index[e.review.jumpID]
	if repeated && e.cursor == e.review.jumpAt {
		if forward {
			i++
		} else {
			i--
		}
	} else {
		at := e.buf.positionToOffset(e.cursor)
		if forward {
			i = sort.Search(n, func(i int) bool { return p.cards[i].Start > at })
		} else {
			i = sort.Search(n, func(i int) bool { return p.cards[i].Start >= at }) - 1
		}
	}
	c := p.cards[(i+n)%n]
	e.revealReview(c)
	return c.ID, true
}

// RevealReviewComment moves the caret to the range start and shows the card.
func (e *Editor) RevealReviewComment(id string) bool {
	if p := e.resolveReview(); p != nil {
		if i, ok := p.index[id]; ok {
			e.revealReview(p.cards[i])
			return true
		}
	}
	return false
}

func (e *Editor) revealReview(c resolvedReviewCard) {
	pos := e.buf.positionAtOffset(c.Start)
	e.SetCursorPosition(pos.Line, pos.Col)
	e.landing = nil // the comment already marks its own range
	e.unfoldSource(c.line, false)
	e.ScrollToCursor()
	e.review.jumpID, e.review.jumpAt, e.review.pending = c.ID, e.cursor, true
	e.revealReviewRows()
}

func (e *Editor) revealReviewRows() {
	if !e.review.pending || e.viewW < 10 || e.viewH < 4 {
		return
	}
	p := e.resolveReview()
	if p == nil || !e.review.pending || e.review.jumpAt != e.cursor || !e.usesRows() {
		e.review.pending = false
		return
	}
	i, ok := p.index[e.review.jumpID]
	if !ok {
		e.review.pending = false
		return
	}
	c := p.cards[i]
	rows := e.prepareRows()
	at := rows.review.cards[c.ID]
	if at.count > 0 && rows.lines[c.line].count > 0 {
		h, top := e.bodyHeight(), e.scrollRow()
		start := rows.before(c.line) + rows.lines[c.line].textRows() + int(rows.lines[c.line].ghost) + at.start
		end := start + min(h, at.count)
		e.setScrollRow(min(min(start, max(top, end-h)), max(0, rows.total()-h)))
		e.syncScrollChrome(h)
	}
	// Cold wrapping preserves the intended card when the prepared index
	// replaces its temporary unwrapped rows.
	e.review.pending = e.wordWrap && !rows.wrapped && e.rowJob != nil
}
