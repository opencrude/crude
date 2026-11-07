package shellchat

import (
	"strings"
	"testing"
	"time"

	"crude/tui/chatprojection"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

// findRow builds a rendered row. Each line is split into two spans mid-text,
// like bridged Markdown, so matching must read across span boundaries.
func findRow(id string, version uint64, lines ...string) chatprojection.MessageRow {
	native := make([]chatprojection.PaintLine, len(lines))
	for i, line := range lines {
		cut := len(line) / 2
		native[i] = chatprojection.PaintLine{Spans: []chatprojection.RowSpan{
			{Text: line[:cut], Role: ui.ColorFg}, {Text: line[cut:], Role: ui.ColorMuted},
		}}
	}
	return chatprojection.MessageRow{ID: id, Version: version, Lines: lines, Native: native}
}

func settle(t *testing.T, f *chatFind, rows []chatprojection.MessageRow) {
	t.Helper()
	for range 1000 {
		f.observe(rows, 1, false)
		if !f.pending(len(rows)) {
			return
		}
	}
	t.Fatal("find never finished counting")
}

func TestFindIgnoresCaseAndKeepsSourceOffsets(t *testing.T) {
	rows := []chatprojection.MessageRow{findRow("a", 1, "Voilà l'ÉCOLE du Zoo")}
	var f chatFind
	f.setQuery("école", FindOptions{}, -1)
	settle(t, &f, rows)
	if f.total != 1 {
		t.Fatalf("total=%d", f.total)
	}
	seg := f.segments(0, rows[0])[0]
	if got := rows[0].Lines[0][seg.from:seg.to]; got != "ÉCOLE" {
		t.Fatalf("highlight covers %q, want the original-case word", got)
	}
}

func TestFindHonorsTheCaseWholeWordAndRegexToggles(t *testing.T) {
	rows := []chatprojection.MessageRow{findRow("a", 1, "Cat cat catalog CAT")}
	cases := []struct {
		name  string
		query string
		opts  FindOptions
		want  int
	}{
		{"folded", "cat", FindOptions{}, 4},
		{"case", "cat", FindOptions{CaseSensitive: true}, 2},
		{"word", "cat", FindOptions{WholeWord: true}, 3},
		{"word and case", "cat", FindOptions{CaseSensitive: true, WholeWord: true}, 1},
		{"regex", "c.t", FindOptions{Regex: true}, 4},
		{"regex case", "C.T", FindOptions{Regex: true, CaseSensitive: true}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var f chatFind
			f.setQuery(tc.query, tc.opts, -1)
			settle(t, &f, rows)
			if f.total != tc.want {
				t.Fatalf("query %q with %+v found %d, want %d", tc.query, tc.opts, f.total, tc.want)
			}
		})
	}
}

func TestFindReportsAnInvalidRegularExpression(t *testing.T) {
	var f chatFind
	f.setQuery("a(", FindOptions{Regex: true}, -1)
	if f.err == "" {
		t.Fatal("an invalid regular expression must report an error")
	}
	settle(t, &f, []chatprojection.MessageRow{findRow("a", 1, "a(")})
	if f.total != 0 {
		t.Fatalf("an invalid expression matched %d rows", f.total)
	}
}

func TestFindMatchesAPhraseThatWrapsOntoTheNextLine(t *testing.T) {
	rows := []chatprojection.MessageRow{findRow("a", 1, "the quick brown", "    fox jumps over", "the lazy dog")}
	var f chatFind
	f.setQuery("brown fox", FindOptions{}, -1)
	settle(t, &f, rows)
	if f.total != 1 {
		t.Fatalf("a wrapped phrase must match once, got %d", f.total)
	}
	segs := f.segments(0, rows[0])
	if len(segs) != 2 || segs[0].line != 0 || segs[1].line != 1 {
		t.Fatalf("segments=%+v", segs)
	}
	if rows[0].Lines[0][segs[0].from:segs[0].to] != "brown" || rows[0].Lines[1][segs[1].from:segs[1].to] != "fox" {
		t.Fatalf("segments cover the wrong text: %+v", segs)
	}
}

func TestFindDoesNotJoinAcrossAParagraphBreak(t *testing.T) {
	rows := []chatprojection.MessageRow{findRow("a", 1, "alpha", "", "beta")}
	var f chatFind
	f.setQuery("alpha beta", FindOptions{}, -1)
	settle(t, &f, rows)
	if f.total != 0 {
		t.Fatalf("a blank line separates paragraphs, got %d matches", f.total)
	}
}

func TestFindCountsEveryNonOverlappingMatchInARow(t *testing.T) {
	rows := []chatprojection.MessageRow{findRow("a", 1, "aaaa aa"), findRow("b", 2, "no hit"), findRow("c", 3, "AA")}
	var f chatFind
	f.setQuery("aa", FindOptions{}, -1)
	settle(t, &f, rows)
	if f.total != 4 || f.counts[0] != 3 || f.counts[1] != 0 || f.counts[2] != 1 {
		t.Fatalf("total=%d counts=%v", f.total, f.counts)
	}
}

func TestFindRecountsOnlyRowsWhoseVersionChanged(t *testing.T) {
	rows := []chatprojection.MessageRow{findRow("a", 1, "needle"), findRow("b", 2, "needle")}
	var f chatFind
	f.setQuery("needle", FindOptions{}, -1)
	settle(t, &f, rows)
	// The streaming row is replaced under a new version; the other row's lines
	// change in place but keep their version, so they must not be read again.
	rows[0].Lines, rows[0].Native = []string{"gone"}, []chatprojection.PaintLine{{Spans: []chatprojection.RowSpan{{Text: "gone"}}}}
	rows[1] = findRow("b", 3, "needle needle")
	f.observe(rows, 1, true)
	for f.pending(len(rows)) {
		f.observe(rows, 1, false)
	}
	if f.counts[0] != 1 || f.counts[1] != 2 || f.total != 3 {
		t.Fatalf("counts=%v total=%d; unchanged version must be trusted, changed version recounted", f.counts, f.total)
	}
}

func TestFindResumesAnUnfinishedPassOnTheNextFrame(t *testing.T) {
	rows := make([]chatprojection.MessageRow, 500)
	for i := range rows {
		rows[i] = findRow(string(rune('A'+i%26))+strings.Repeat("x", i/26), uint64(i+1), "line with needle")
	}
	f := chatFind{limit: time.Nanosecond, clock: steppingClock()}
	f.setQuery("needle", FindOptions{}, -1)
	f.observe(rows, 1, false)
	if !f.pending(len(rows)) || f.total == 0 || f.total >= len(rows) {
		t.Fatalf("first frame should count part of the history: pending=%v total=%d", f.pending(len(rows)), f.total)
	}
	frames := 1
	for f.pending(len(rows)) {
		f.observe(rows, 1, false)
		frames++
	}
	if f.total != len(rows) || frames < 3 {
		t.Fatalf("total=%d after %d frames", f.total, frames)
	}
}

func TestFindRowsChangingMidPassAreRecountedByTheNextPass(t *testing.T) {
	rows := make([]chatprojection.MessageRow, 200)
	for i := range rows {
		rows[i] = findRow(string(rune(i+1)), uint64(i+1), "needle")
	}
	f := chatFind{limit: time.Nanosecond, clock: steppingClock()}
	f.setQuery("needle", FindOptions{}, -1)
	f.observe(rows, 1, false)
	rows[0] = findRow(rows[0].ID, 1000, "needle needle") // behind the cursor
	f.observe(rows, 1, true)
	for range 100 {
		f.observe(rows, 1, false)
	}
	if f.pending(len(rows)) || f.total != len(rows)+1 {
		t.Fatalf("pending=%v total=%d, want %d", f.pending(len(rows)), f.total, len(rows)+1)
	}
}

func TestFindStepWrapsInBothDirectionsAndKeepsItsRowWhenRowsShift(t *testing.T) {
	rows := []chatprojection.MessageRow{findRow("a", 1, "x"), findRow("b", 2, "hit hit"), findRow("c", 3, "x"), findRow("d", 4, "hit")}
	var f chatFind
	f.setQuery("hit", FindOptions{}, -1)
	settle(t, &f, rows)
	starts := []int{0, 1, 2, 3}
	if !f.selectNear(rows, starts, 0) || f.cur.id != "b" || f.cur.match != 0 {
		t.Fatalf("nearest to the top is the first match: %+v", f.cur)
	}
	want := []findCurrent{{ok: true, row: 1, id: "b", match: 1}, {ok: true, row: 3, id: "d"}, {ok: true, row: 1, id: "b"}}
	for i, w := range want {
		if !f.step(rows, 1) || f.cur != w {
			t.Fatalf("forward step %d: got %+v want %+v", i, f.cur, w)
		}
	}
	if !f.step(rows, -1) || f.cur.id != "d" {
		t.Fatalf("backward wrap: %+v", f.cur)
	}
	// A row inserted before the selection moves its index, not its identity.
	shifted := append([]chatprojection.MessageRow{findRow("z", 9, "x")}, rows...)
	if row, ok := f.rowOf(shifted); !ok || row != 4 {
		t.Fatalf("selection lost after an insert: row=%d ok=%v", row, ok)
	}
}

func TestFindSelectsTheMatchNearestTheAnchorAndPrefersTheEarlierOnATie(t *testing.T) {
	rows := []chatprojection.MessageRow{findRow("a", 1, "hit"), findRow("b", 2, "x"), findRow("c", 3, "hit")}
	var f chatFind
	f.setQuery("hit", FindOptions{}, -1)
	settle(t, &f, rows)
	if !f.selectNear(rows, []int{0, 1, 2}, 1) || f.cur.id != "a" {
		t.Fatalf("a tie must choose the earlier match: %+v", f.cur)
	}
	if !f.selectNear(rows, []int{0, 1, 2}, 2) || f.cur.id != "c" {
		t.Fatalf("nearest to line 2: %+v", f.cur)
	}
}

func TestFindStatusSpeaksOnlyWhileItLacksAPhase(t *testing.T) {
	rows := []chatprojection.MessageRow{findRow("a", 1, "one hit"), {ID: "b"}}
	f := chatFind{limit: time.Nanosecond, clock: steppingClock()}
	if f.findStatusText() != "" {
		t.Fatal("no query, no status")
	}
	f.setQuery("hit", FindOptions{}, -1)
	if got := f.findStatusText(); got != "Searching…" {
		t.Fatalf("status while counting: %q", got)
	}
	settle(t, &f, rows)
	if got := f.findStatusText(); got != "1 match · loading" {
		t.Fatalf("a row without rendered lines is still loading: %q", got)
	}
	f.setQuery("zzz", FindOptions{}, -1)
	settle(t, &f, rows)
	if got := f.findStatusText(); got != "No results · loading" {
		t.Fatalf("status=%q", got)
	}
	f.setQuery("hit", FindOptions{}, -1)
	settle(t, &f, []chatprojection.MessageRow{findRow("a", 1, "one hit")})
	if got := f.findStatusText(); got != "" {
		t.Fatalf("a settled count lets the window state it: %q", got)
	}
}

func TestFindDropsEverythingWhenTheRowSourceChanges(t *testing.T) {
	first := []chatprojection.MessageRow{findRow("a", 1, "hit")}
	second := []chatprojection.MessageRow{findRow("a", 1, "miss")}
	var f chatFind
	f.setQuery("hit", FindOptions{}, -1)
	f.observe(first, 1, false)
	for f.pending(1) {
		f.observe(first, 1, false)
	}
	if f.total != 1 {
		t.Fatal("setup")
	}
	// Same ID and version, different renderer: counts must not carry over.
	for range 3 {
		f.observe(second, 2, true)
	}
	if f.total != 0 {
		t.Fatalf("stale counts survived a source switch: %d", f.total)
	}
}

func TestFindForgetsRemovedRowsWhenTheTranscriptShrinksAndGrowsAgain(t *testing.T) {
	rows := []chatprojection.MessageRow{findRow("a", 1, "hit"), findRow("b", 2, "hit"), findRow("c", 3, "hit")}
	var f chatFind
	f.setQuery("hit", FindOptions{}, -1)
	settle(t, &f, rows)
	f.observe(rows[:1], 1, true)
	for f.pending(1) {
		f.observe(rows[:1], 1, false)
	}
	// Two new rows with no match must not inherit the counts of the removed ones.
	grown := []chatprojection.MessageRow{rows[0], findRow("x", 2, "miss"), findRow("y", 3, "miss")}
	f.observe(grown, 1, true)
	for f.pending(len(grown)) {
		f.observe(grown, 1, false)
	}
	if f.total != 1 || f.counts[1] != 0 || f.counts[2] != 0 {
		t.Fatalf("total=%d counts=%v, want only the first row to match", f.total, f.counts)
	}
}

// ---- Screen API ---------------------------------------------------------

type findFixture struct {
	t    *testing.T
	th   *theme.Theme
	s    *Screen
	host *ui.Host
	rows []chatprojection.MessageRow
	w, h int
	pad  int
}

func newFindFixture(t *testing.T, rows []chatprojection.MessageRow, viewport int) *findFixture {
	t.Helper()
	th := theme.DefaultDark()
	f := &findFixture{t: t, th: th, rows: rows, pad: th.Spacing.PadLR, h: viewport}
	f.s = New(th)
	compose := ComposeParams{InnerW: 40, PadLR: f.pad, Theme: th}
	f.w = ContentSurfaceWidth(compose.InnerW, f.pad, false)
	f.s.SetMessages(rows, PaintParams{Compose: compose})
	f.host = ui.NewHost(th, f.s)
	f.host.Resize(f.w, f.h)
	f.sync(true)
	return f
}

// sync mirrors one viewmodel frame: size the transcript and install the rows.
func (f *findFixture) sync(bottom bool) {
	contentH := 0
	for _, row := range f.rows {
		contentH += max(1, len(row.Lines))
	}
	f.s.SyncScrollMetrics(f.h-ActivityRows(f.h), contentH)
	if bottom {
		f.s.StickToBottom()
	}
	f.s.SetMessages(f.rows, PaintParams{Compose: ComposeParams{InnerW: 40, PadLR: f.pad, Theme: f.th}})
	f.host.MarkLayout()
	f.host.Layout()
}

func (f *findFixture) paint() *surface.CellBuffer {
	f.host.Layout()
	buf := surface.NewCellBuffer(f.w, f.h)
	f.host.Paint(buf)
	return buf
}

// rowOnScreen reports the screen row showing global line g, or -1. Chat
// content sticks to the bottom when it is shorter than the viewport.
func (f *findFixture) rowOnScreen(g int) int {
	scroll := f.s.ScrollModel()
	y := max(0, scroll.Viewport()-scroll.Content()) + g - scroll.Offset()
	if y < 0 || y >= scroll.Viewport() {
		return -1
	}
	return y
}

func (f *findFixture) cellAt(g, col int) (fg, bg string) {
	f.t.Helper()
	y := f.rowOnScreen(g)
	if y < 0 {
		f.t.Fatalf("line %d is not on screen (offset %d)", g, f.s.ScrollModel().Offset())
	}
	c := f.paint().Get(f.pad+col, y)
	return c.FG.ANSIFG(), c.BG.ANSIBG()
}

// search opens find with a query and runs the frames the shell would run
// until the count settles.
func (f *findFixture) search(query string, opts FindOptions) {
	f.t.Helper()
	f.s.OpenFind()
	f.s.SetFindQuery(query, opts)
	for i := 0; i < 2000 && f.s.find.pending(len(f.s.rows)); i++ {
		f.sync(false)
	}
	if f.s.find.pending(len(f.s.rows)) {
		f.t.Fatal("find never settled")
	}
	f.sync(false)
}

func longChat() []chatprojection.MessageRow {
	rows := make([]chatprojection.MessageRow, 30)
	for i := range rows {
		rows[i] = findRow(string(rune('a'+i)), uint64(i+1), "plain message line")
	}
	rows[3] = findRow("d", 4, "an old needle in history")
	rows[25] = findRow("z", 26, "a recent NEEDLE here")
	return rows
}

func TestFindSelectsTheMatchNearestTheViewAndDoesNotMoveIt(t *testing.T) {
	f := newFindFixture(t, longChat(), 10)
	before := f.s.ScrollModel().Offset()
	f.search("needle", FindOptions{})
	count, current := f.s.FindCounts()
	if count != 2 || current != 1 {
		t.Fatalf("count=%d current=%d, want the newest visible match selected", count, current)
	}
	if got := f.s.ScrollModel().Offset(); got != before {
		t.Fatalf("a match already in view must not scroll: %d -> %d", before, got)
	}
}

func TestFindHighlightsAllMatchesAndTheSelectedOneDifferently(t *testing.T) {
	f := newFindFixture(t, longChat(), 10)
	f.search("needle", FindOptions{})
	tokens := ui.NewTokenSet(f.th)
	current := tokens.FindMatchFill(true).ANSIBG()
	other := tokens.FindMatchFill(false).ANSIBG()
	if current == other {
		t.Fatal("the selected match and the other matches need different fills")
	}
	if _, bg := f.cellAt(25, len("a recent ")); bg != current {
		t.Fatalf("selected match fill %q, want %q", bg, current)
	}
	if _, bg := f.cellAt(25, 0); bg == current || bg == other {
		t.Fatal("text outside the match was highlighted")
	}
	f.s.StepFind(-1) // the older match, off screen
	if _, bg := f.cellAt(3, len("an old ")); bg != current {
		t.Fatalf("stepping must move the selected fill to the older match: %q", bg)
	}
}

func TestFindMarksOtherMatchesOnTheSameRowWithTheOtherFill(t *testing.T) {
	rows := []chatprojection.MessageRow{findRow("a", 1, "needle and needle again")}
	f := newFindFixture(t, rows, 4)
	f.search("needle", FindOptions{})
	tokens := ui.NewTokenSet(f.th)
	first, second := tokens.FindMatchFill(true).ANSIBG(), tokens.FindMatchFill(false).ANSIBG()
	_, bg1 := f.cellAt(0, 0)
	_, bg2 := f.cellAt(0, len("needle and "))
	if bg1 != first || bg2 != second {
		t.Fatalf("fills %q/%q, want the selected %q and the other %q", bg1, bg2, first, second)
	}
	f.s.StepFind(1)
	if _, bg := f.cellAt(0, 0); bg != second {
		t.Fatalf("the first match must become an ordinary match after stepping: %q", bg)
	}
	if _, bg := f.cellAt(0, len("needle and ")); bg != first {
		t.Fatalf("the second match must become the selected one: %q", bg)
	}
}

func TestFindRevealStopsFollowingNewMessages(t *testing.T) {
	f := newFindFixture(t, longChat(), 10)
	f.search("needle", FindOptions{})
	f.s.StepFind(-1)
	if got := f.s.ScrollModel().Offset(); got > 3 {
		t.Fatalf("the older match must be revealed, offset=%d", got)
	}
	if f.s.ScrollModel().AutoScroll() {
		t.Fatal("leaving the bottom must stop following new messages")
	}
}

func TestFindCloseRemovesEveryMarkAndFreesTheCounts(t *testing.T) {
	f := newFindFixture(t, longChat(), 10)
	f.search("needle", FindOptions{})
	tokens := ui.NewTokenSet(f.th)
	if _, bg := f.cellAt(25, len("a recent ")); bg != tokens.FindMatchFill(true).ANSIBG() {
		t.Fatal("setup: no mark")
	}
	f.s.CloseFind()
	f.sync(false)
	_, bg := f.cellAt(25, len("a recent "))
	if bg == tokens.FindMatchFill(true).ANSIBG() || bg == tokens.FindMatchFill(false).ANSIBG() {
		t.Fatalf("a mark outlived the search: %q", bg)
	}
	if f.s.find.querying() || f.s.find.counts != nil {
		t.Fatal("closing find must free its counts")
	}
}

func TestFindKeepsTheQueryForTheNextOpen(t *testing.T) {
	f := newFindFixture(t, longChat(), 10)
	f.search("needle", FindOptions{})
	f.s.CloseFind()
	f.search("needle", FindOptions{})
	if count, _ := f.s.FindCounts(); count != 2 {
		t.Fatalf("a retained query must be searched again, count=%d", count)
	}
}

func TestFindSelectionStaysVisibleOverAMatch(t *testing.T) {
	f := newFindFixture(t, longChat(), 10)
	f.search("needle", FindOptions{})
	y := f.rowOnScreen(25)
	start := f.pad + len("a recent ")
	f.s.selection.selAnchorLine, f.s.selection.selAnchorCol = 25, len("a recent ")
	f.s.selection.selEndLine, f.s.selection.selEndCol = 25, len("a recent needle")
	f.s.selection.selecting = true
	f.sync(false)
	tokens := ui.NewTokenSet(f.th)
	c := f.paint().Get(start, y)
	if c.BG != tokens.Color(ui.ColorTextSelection) {
		t.Fatalf("selection must win over the mark, got bg %v", c.BG)
	}
}

func TestFindFollowsRowsThatGrowWhileStreaming(t *testing.T) {
	f := newFindFixture(t, longChat(), 10)
	f.search("needle", FindOptions{})
	last := len(f.rows) - 1
	f.rows[last] = findRow(f.rows[last].ID, 500, "needle again", "and needle once more")
	f.sync(true)
	if count, _ := f.s.FindCounts(); count != 4 {
		t.Fatalf("a replaced row must be recounted, count=%d", count)
	}
}

func TestFindPendingExpiresWhenTheChatIsNoLongerObserved(t *testing.T) {
	rows := make([]chatprojection.MessageRow, 200)
	for i := range rows {
		rows[i] = findRow(string(rune(i+1)), uint64(i+1), "needle")
	}
	f := newFindFixture(t, rows, 10)
	f.s.find.limit, f.s.find.clock = time.Nanosecond, steppingClock()
	f.s.OpenFind()
	f.s.SetFindQuery("needle", FindOptions{})
	if !f.s.FindPending() {
		t.Fatal("a long history needs more frames to count")
	}
	f.s.findSeen = time.Now().Add(-2 * time.Second)
	if f.s.FindPending() {
		t.Fatal("a chat nobody renders must not keep the shell awake")
	}
}

// steppingClock advances one microsecond per reading, so a one-nanosecond
// budget always expires at the first check, on every platform timer.
func steppingClock() func() time.Time {
	t := time.Unix(0, 0)
	return func() time.Time {
		t = t.Add(time.Microsecond)
		return t
	}
}
