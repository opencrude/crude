package shellchat

import (
	"bytes"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"crude/textsearch"
	"crude/tui/chatprojection"
	"crude/tui/rendering"
)

// findBudget bounds the time one frame spends counting matches. A pass that
// does not finish resumes on the next frame, so a very long history never
// stalls typing or scrolling.
const findBudget = 3 * time.Millisecond

// FindOptions are the filter toggles the find window offers. They mean the
// same thing here as in the editor: a case-sensitive, whole-word or regular
// expression search.
type FindOptions struct {
	CaseSensitive bool
	WholeWord     bool
	Regex         bool
}

// findSegment is the part of one match that sits on one painted line. A match
// that wraps onto the next line has one segment per line. from and to are byte
// offsets into the plain text of that line.
type findSegment struct {
	line, from, to int
	match          int
}

// chatFind owns text find over the rows a Screen shows. It reads exactly the
// lines that are painted, so a count and a highlight can never disagree with
// the screen, and it never renders anything itself.
//
// The cost is bounded by what changed. A row carries the render stamp of the
// renderer (MessageRow.Version); a row whose stamp matches the one it was
// counted at is skipped, so a streaming reply recounts one row, not the
// history. Counting reads each line once into reused buffers and allocates
// nothing per row. With find closed the Screen never calls into this type.
type chatFind struct {
	query  string
	opts   FindOptions
	needle []byte // query after folding; empty = no search
	re     *textsearch.Matcher
	err    string

	// Per-row results, parallel to the shown rows. stamps[i] is the row
	// Version counts[i] was computed for.
	stamps []uint64
	counts []int32
	total  int
	// waiting counts rows without rendered lines when the last pass ended.
	// Their text exists only after the renderer settles them.
	waiting int

	// A pass visits every row once. next is the first row it has not visited;
	// again asks for one more pass when rows changed behind it. counted says a
	// pass has finished, so the window can state a count instead of
	// "Searching…".
	next     int
	counted  bool
	again    bool
	counting int // rows without lines seen so far in the running pass
	source   uint64

	// cur is the match the user is on. The row is found again by ID, so rows
	// inserted before it do not move the selection.
	cur findCurrent
	// anchor is the global line the next automatic selection should be near.
	anchor    int
	selecting bool

	// gen changes whenever the needle or the counted rows are discarded, so
	// the cached segments of an older query are never reused.
	gen   uint64
	text  rowText
	paint paintCache
	// limit replaces findBudget when set; tests use it to force a pass to
	// span several frames.
	limit time.Duration
}

type findCurrent struct {
	ok    bool
	row   int
	id    string
	match int
}

type paintCache struct {
	row      int
	version  uint64
	gen      uint64
	segments []findSegment
}

func (c paintCache) holds(gen uint64, row int, version uint64) bool {
	return c.segments != nil && c.gen == gen && c.row == row && c.version == version
}

// rowText is the reusable scratch for one row: its lines joined into one
// string, plus where each line starts in it.
type rowText struct {
	raw    []byte
	orig   []byte // the painted text, lines joined with one space
	hay    []byte // orig lowercased (same length) for a case-insensitive search
	lines  []lineSpan
	starts []int // byte offset of every literal match in orig
	length int   // bytes of every literal match in orig
	spans  []findSpan
}

// findSpan is one match in the joined row text.
type findSpan struct{ start, end int }

// lineSpan maps a range of rowText.orig back to one painted line.
type lineSpan struct {
	line  int // line index within the row
	start int // offset of the line's first kept byte in orig
	trim  int // bytes of leading space removed from the plain text
	n     int // kept bytes
}

// querying reports whether a query is active, literal or regular expression.
func (f *chatFind) querying() bool { return len(f.needle) > 0 || f.re != nil }

// pending reports whether counting is unfinished for the shown rows.
func (f *chatFind) pending(rows int) bool { return f.querying() && f.next < rows }

// setQuery installs a new query and options, and discards every count. anchor
// is the global line the first selection should be near, or negative for the
// middle of the view.
func (f *chatFind) setQuery(query string, opts FindOptions, anchor int) {
	query = strings.TrimSpace(query)
	if query == f.query && opts == f.opts {
		return
	}
	f.anchor = anchor
	f.query, f.opts = query, opts
	f.needle, f.re, f.err = f.needle[:0], nil, ""
	switch {
	case query == "":
	case opts.Regex:
		matcher, err := textsearch.Compile(query, textsearch.Options{CaseSensitive: opts.CaseSensitive, WholeWord: opts.WholeWord, Regex: true})
		if err != nil {
			f.err = err.Error()
			break
		}
		f.re = matcher
	default:
		f.needle = appendPlainNeedle(f.needle, query, opts.CaseSensitive)
	}
	f.clear()
	f.selecting = f.querying()
}

// appendPlainNeedle appends the query bytes, folded unless the search is
// case-sensitive, so one byte-length-preserving fold covers both sides.
func appendPlainNeedle(dst []byte, query string, caseSensitive bool) []byte {
	if caseSensitive {
		return append(dst, query...)
	}
	return appendFolded(dst, []byte(query))
}

// clear drops counts and the selection, keeping the query text.
func (f *chatFind) clear() {
	clear(f.stamps)
	clear(f.counts)
	f.total, f.waiting, f.counting = 0, 0, 0
	f.next, f.again, f.counted = 0, false, false
	f.cur = findCurrent{}
	f.gen++
}

// stop ends the search and frees its working memory.
func (f *chatFind) stop() {
	*f = chatFind{gen: f.gen + 1}
}

// observe advances counting over rows. changed says the rows were replaced
// since the last call. It reports whether a pass finished with no selection
// yet wanted by a new query, which is the moment to pick and show one.
func (f *chatFind) observe(rows []chatprojection.MessageRow, source uint64, changed bool) (settled bool) {
	if !f.querying() {
		return false
	}
	if source != f.source {
		f.source = source
		f.clear()
		f.selecting = true
		changed = true
	}
	if changed {
		if f.next >= len(f.stamps) || f.next >= len(rows) {
			f.next = 0
		} else if f.next > 0 {
			f.again = true
		}
	}
	if len(f.stamps) != len(rows) {
		f.fit(len(rows))
	}
	if f.next >= len(rows) {
		return false
	}
	budget := f.limit
	if budget == 0 {
		budget = findBudget
	}
	deadline := time.Now().Add(budget)
	for f.next < len(rows) {
		i := f.next
		if f.stamps[i] != rows[i].Version {
			f.count(i, rows[i])
		}
		if rows[i].Version == 0 {
			f.counting++
		}
		f.next++
		if f.next&31 == 0 && time.Now().After(deadline) {
			break
		}
	}
	if f.next < len(rows) {
		return false
	}
	f.waiting, f.counting = f.counting, 0
	f.counted = true
	if f.again {
		f.again, f.next = false, 0
	}
	if f.selecting && f.total > 0 {
		f.selecting = false
		return true
	}
	return false
}

// fit resizes the per-row results to n rows, forgetting removed rows.
func (f *chatFind) fit(n int) {
	for i := n; i < len(f.counts); i++ {
		f.total -= int(f.counts[i])
	}
	old := min(len(f.stamps), n)
	f.stamps = slices.Grow(f.stamps[:old], n-old)[:n]
	f.counts = slices.Grow(f.counts[:old], n-old)[:n]
	// Spare capacity still holds the values of rows that were removed.
	clear(f.stamps[old:])
	clear(f.counts[old:])
}

// count recounts one row and records the stamp it was counted at.
func (f *chatFind) count(i int, row chatprojection.MessageRow) {
	f.total -= int(f.counts[i])
	n := 0
	if row.Version != 0 && (f.re != nil || len(f.needle) > 0) {
		f.text.flatten(row, !f.opts.CaseSensitive)
		n = f.text.find(f, f.needle)
	}
	f.counts[i], f.stamps[i] = int32(n), row.Version
	f.total += n
	if f.cur.ok && f.cur.row == i && f.cur.id == row.ID {
		if n == 0 {
			f.cur.ok = false
		} else {
			f.cur.match = min(f.cur.match, n-1)
		}
	}
}

// segments returns the match segments of one row, ordered by line. The last
// row asked for is cached, so painting every line of a row finds it once.
func (f *chatFind) segments(index int, row chatprojection.MessageRow) []findSegment {
	if !f.querying() || row.Version == 0 {
		return nil
	}
	if f.paint.holds(f.gen, index, row.Version) {
		return f.paint.segments
	}
	f.text.flatten(row, !f.opts.CaseSensitive)
	f.text.find(f, f.needle)
	segs := f.text.segments()
	if segs == nil {
		segs = []findSegment{}
	}
	f.paint = paintCache{row: index, version: row.Version, gen: f.gen, segments: segs}
	return segs
}

// rowOf resolves the selected row, or reports that it is gone.
func (f *chatFind) rowOf(rows []chatprojection.MessageRow) (int, bool) {
	if !f.cur.ok {
		return 0, false
	}
	if f.cur.row < len(rows) && rows[f.cur.row].ID == f.cur.id {
		return f.cur.row, true
	}
	for i := range rows {
		if rows[i].ID == f.cur.id {
			f.cur.row = i
			return i, true
		}
	}
	f.cur.ok = false
	return 0, false
}

// step moves the selection by one match in direction dir (+1 or -1) and wraps
// at both ends. It reports false when nothing matches.
func (f *chatFind) step(rows []chatprojection.MessageRow, dir int) bool {
	if f.total == 0 || len(f.counts) != len(rows) {
		return false
	}
	row, ok := f.rowOf(rows)
	if !ok {
		return false
	}
	match := f.cur.match + dir
	if match >= 0 && match < int(f.counts[row]) {
		f.cur.match = match
		return true
	}
	n := len(rows)
	for k := 1; k <= n; k++ {
		i := ((row+dir*k)%n + n) % n
		if f.counts[i] > 0 {
			f.cur = findCurrent{ok: true, row: i, id: rows[i].ID}
			if dir < 0 {
				f.cur.match = int(f.counts[i]) - 1
			}
			return true
		}
	}
	return false
}

// selectNear selects the match closest to the global line anchor. starts holds
// each row's first global line. Ties go to the earlier match.
func (f *chatFind) selectNear(rows []chatprojection.MessageRow, starts []int, anchor int) bool {
	if f.total == 0 || len(f.counts) != len(rows) || len(starts) != len(rows) {
		return false
	}
	at := sort.SearchInts(starts, anchor+1) - 1
	at = min(max(at, 0), len(rows)-1)
	row := -1
	for d := 0; d < len(rows) && row < 0; d++ {
		for _, i := range [2]int{at - d, at + d} {
			if i >= 0 && i < len(rows) && f.counts[i] > 0 {
				row = i
				break
			}
		}
	}
	if row < 0 {
		return false
	}
	best, bestDistance := 0, -1
	for _, seg := range f.segments(row, rows[row]) {
		if d := absInt(starts[row] + seg.line - anchor); bestDistance < 0 || d < bestDistance {
			best, bestDistance = seg.match, d
		}
	}
	f.cur = findCurrent{ok: true, row: row, id: rows[row].ID, match: best}
	return true
}

// ordinal is the 1-based position of the selection among all matches.
func (f *chatFind) ordinal() int {
	n := f.cur.match + 1
	for i := 0; i < f.cur.row && i < len(f.counts); i++ {
		n += int(f.counts[i])
	}
	return n
}

// flatten reads the plain text of every line of row into one string. Lines
// join with a single space, so a phrase that wraps still matches. A blank line
// is a paragraph break and never joins its neighbours.
func (t *rowText) flatten(row chatprojection.MessageRow, fold bool) {
	t.orig, t.lines = t.orig[:0], t.lines[:0]
	apart := false
	for i := range row.Lines {
		t.raw = appendPlain(t.raw[:0], row, i)
		lo, hi := trimBlank(t.raw)
		if lo >= hi {
			apart = true
			continue
		}
		if len(t.orig) > 0 {
			sep := byte(' ')
			if apart {
				sep = '\n'
			}
			t.orig = append(t.orig, sep)
		}
		apart = false
		start := len(t.orig)
		t.orig = append(t.orig, t.raw[lo:hi]...)
		t.lines = append(t.lines, lineSpan{line: i, start: start, trim: lo, n: hi - lo})
	}
	if !fold {
		t.hay = t.hay[:0]
		return
	}
	t.hay = appendFolded(t.hay[:0], t.orig)
}

// find records the start of every non-overlapping match of needle in the row
// and returns how many there are.
func (t *rowText) find(f *chatFind, needle []byte) int {
	// Both results are rebuilt for every row: a stale span from the row read
	// before would paint its match on this one.
	t.starts, t.spans, t.length = t.starts[:0], t.spans[:0], len(needle)
	if f.re != nil {
		// A regular expression may match different lengths, so a match is a
		// (start, end) pair here. This mode is explicit and never the default.
		for _, hit := range f.re.FindAll(string(t.orig), 0) {
			t.spans = append(t.spans, findSpan{start: hit.Start, end: hit.End})
		}
		return len(t.spans)
	}
	hay := t.orig
	if len(t.hay) == len(t.orig) {
		hay = t.hay
	}
	if len(needle) == 0 {
		return 0
	}
	for off := 0; off < len(hay); {
		i := bytes.Index(hay[off:], needle)
		if i < 0 {
			break
		}
		start := off + i
		if f.opts.WholeWord && !wholeWordAt(t.orig, start, start+len(needle)) {
			off = start + 1
			continue
		}
		t.starts = append(t.starts, start)
		off = start + len(needle)
	}
	return len(t.starts)
}

// spans returns the recorded matches as (start, end) pairs.
func (t *rowText) spansOf() []findSpan {
	if len(t.spans) > 0 || len(t.starts) == 0 {
		return t.spans
	}
	for _, start := range t.starts {
		t.spans = append(t.spans, findSpan{start: start, end: start + t.length})
	}
	return t.spans
}

// segments maps the recorded matches onto painted lines.
func (t *rowText) segments() []findSegment {
	var out []findSegment
	for match, span := range t.spansOf() {
		start, end := span.start, span.end
		first := sort.Search(len(t.lines), func(k int) bool { return t.lines[k].start+t.lines[k].n > start })
		for k := first; k < len(t.lines) && t.lines[k].start < end; k++ {
			ln := t.lines[k]
			from, to := max(start, ln.start), min(end, ln.start+ln.n)
			out = append(out, findSegment{line: ln.line, from: ln.trim + from - ln.start, to: ln.trim + to - ln.start, match: match})
		}
	}
	return out
}

// wholeWordAt reports whether [start,end) in text stands alone as a word, using
// the same character class the shared text search uses.
func wholeWordAt(text []byte, start, end int) bool {
	word := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) || r == '_' }
	if start > 0 {
		if r, _ := utf8.DecodeLastRune(text[:start]); word(r) {
			return false
		}
	}
	if end < len(text) {
		if r, _ := utf8.DecodeRune(text[end:]); word(r) {
			return false
		}
	}
	return true
}

// appendPlain appends the plain text of one line, the same text selection and
// copy read.
func appendPlain(dst []byte, row chatprojection.MessageRow, i int) []byte {
	if i < len(row.Native) {
		for _, span := range row.Native[i].Spans {
			dst = append(dst, span.Text...)
		}
		return dst
	}
	return append(dst, rendering.StripAnsi(row.Lines[i])...)
}

// linePlain is the plain text of one line, for converting byte offsets to
// screen columns.
func linePlain(row chatprojection.MessageRow, i int) string {
	return string(appendPlain(nil, row, i))
}

func trimBlank(b []byte) (lo, hi int) {
	hi = len(b)
	for lo < hi && (b[lo] == ' ' || b[lo] == '\t') {
		lo++
	}
	for hi > lo && (b[hi-1] == ' ' || b[hi-1] == '\t') {
		hi--
	}
	return lo, hi
}

// appendFolded appends src with letters lowercased. The result is exactly as
// long as src, so an offset in the folded text is an offset in the source. A
// letter whose lowercase form has another length stays as it is.
func appendFolded(dst, src []byte) []byte {
	at := len(dst)
	dst = slices.Grow(dst, len(src))[:at+len(src)]
	out := dst[at:]
	for i := 0; i < len(src); {
		c := src[i]
		if c < utf8.RuneSelf {
			if 'A' <= c && c <= 'Z' {
				c += 'a' - 'A'
			}
			out[i] = c
			i++
			continue
		}
		r, n := utf8.DecodeRune(src[i:])
		lower := unicode.ToLower(r)
		if r != utf8.RuneError && lower != r && utf8.RuneLen(lower) == n {
			utf8.EncodeRune(out[i:], lower)
		} else {
			copy(out[i:], src[i:i+n])
		}
		i += n
	}
	return dst
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// findStatusText is the window's counter text for a search that is still
// running; settled counts come from the shared frame's matchCountText.
func (f *chatFind) findStatusText() string {
	switch {
	case f.err != "":
		return ""
	case !f.querying():
		return ""
	case !f.counted:
		return "Searching…"
	case f.total == 0 && f.waiting > 0:
		return "No results · loading"
	case f.total > 0 && f.waiting > 0:
		if f.total == 1 {
			return "1 match · loading"
		}
		return fmt.Sprintf("%d matches · loading", f.total)
	}
	return ""
}
