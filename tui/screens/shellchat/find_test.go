package shellchat

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

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
	f.setQuery("école", -1)
	settle(t, &f, rows)
	if f.total != 1 {
		t.Fatalf("total=%d", f.total)
	}
	seg := f.segments(0, rows[0])[0]
	if got := rows[0].Lines[0][seg.from:seg.to]; got != "ÉCOLE" {
		t.Fatalf("highlight covers %q, want the original-case word", got)
	}
}

func TestFindMatchesAPhraseThatWrapsOntoTheNextLine(t *testing.T) {
	rows := []chatprojection.MessageRow{findRow("a", 1, "the quick brown", "    fox jumps over", "the lazy dog")}
	var f chatFind
	f.setQuery("brown fox", -1)
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
	f.setQuery("alpha beta", -1)
	settle(t, &f, rows)
	if f.total != 0 {
		t.Fatalf("a blank line separates paragraphs, got %d matches", f.total)
	}
}

func TestFindCountsEveryNonOverlappingMatchInARow(t *testing.T) {
	rows := []chatprojection.MessageRow{findRow("a", 1, "aaaa aa"), findRow("b", 2, "no hit"), findRow("c", 3, "AA")}
	var f chatFind
	f.setQuery("aa", -1)
	settle(t, &f, rows)
	if f.total != 4 || f.counts[0] != 3 || f.counts[1] != 0 || f.counts[2] != 1 {
		t.Fatalf("total=%d counts=%v", f.total, f.counts)
	}
}

func TestFindRecountsOnlyRowsWhoseVersionChanged(t *testing.T) {
	rows := []chatprojection.MessageRow{findRow("a", 1, "needle"), findRow("b", 2, "needle")}
	var f chatFind
	f.setQuery("needle", -1)
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
	f := chatFind{limit: time.Nanosecond}
	f.setQuery("needle", -1)
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
	f := chatFind{limit: time.Nanosecond}
	f.setQuery("needle", -1)
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
	f.setQuery("hit", -1)
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
	f.setQuery("hit", -1)
	settle(t, &f, rows)
	if !f.selectNear(rows, []int{0, 1, 2}, 1) || f.cur.id != "a" {
		t.Fatalf("a tie must choose the earlier match: %+v", f.cur)
	}
	if !f.selectNear(rows, []int{0, 1, 2}, 2) || f.cur.id != "c" {
		t.Fatalf("nearest to line 2: %+v", f.cur)
	}
}

func TestFindStatusTellsTheStateAndTheNextStep(t *testing.T) {
	rows := []chatprojection.MessageRow{findRow("a", 1, "one hit"), {ID: "b"}}
	f := chatFind{limit: time.Nanosecond}
	if f.status(2) != "" {
		t.Fatal("no query, no status")
	}
	f.setQuery("hit", -1)
	if f.status(2) != "Searching…" {
		t.Fatalf("status before counting: %q", f.status(2))
	}
	settle(t, &f, rows)
	if got := f.status(2); got != "1 match · loading" {
		t.Fatalf("a row without rendered lines is still loading: %q", got)
	}
	f.selectNear(rows, []int{0, 1}, 0)
	if got := f.status(2); got != "1 of 1 · loading" {
		t.Fatalf("status=%q", got)
	}
	f.setQuery("zzz", -1)
	settle(t, &f, rows)
	if got := f.status(2); got != "No matches yet · loading" {
		t.Fatalf("status=%q", got)
	}
	f.setQuery("hit", -1)
	rows[1] = findRow("b", 2, "nothing")
	settle(t, &f, rows)
	f.setQuery("zzz", -1)
	settle(t, &f, rows)
	if got := f.status(2); got != "No matches" {
		t.Fatalf("status=%q", got)
	}
}

func TestFindDropsEverythingWhenTheRowSourceChanges(t *testing.T) {
	first := []chatprojection.MessageRow{findRow("a", 1, "hit")}
	second := []chatprojection.MessageRow{findRow("a", 1, "miss")}
	var f chatFind
	f.setQuery("hit", -1)
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

// ---- Screen journey -------------------------------------------------------

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

// sync mirrors one viewmodel frame: size the transcript, install rows, lay out.
func (f *findFixture) sync(bottom bool) {
	contentH := 0
	for _, row := range f.rows {
		contentH += max(1, len(row.Lines))
	}
	f.s.SyncScrollMetrics(f.s.TranscriptRows(f.h), contentH)
	if bottom {
		f.s.StickToBottom()
	}
	f.s.SetMessages(f.rows, PaintParams{Compose: ComposeParams{InnerW: 40, PadLR: f.pad, Theme: f.th}})
	f.host.MarkLayout()
	f.host.Layout()
}

func (f *findFixture) key(text string, code rune) ui.EventResult {
	f.t.Helper()
	res := f.host.Handle(tea.KeyPressMsg{Code: code, Text: text})
	f.sync(false)
	return res
}

func (f *findFixture) typeQuery(query string) {
	f.t.Helper()
	for _, r := range query {
		f.key(string(r), r)
	}
}

func (f *findFixture) open() {
	f.t.Helper()
	f.s.OpenFind()
	f.host.MarkLayout()
	f.host.Layout()
	if !f.host.Focus(f.s.FindID()) {
		f.t.Fatal("the find bar did not mount")
	}
	f.sync(false)
}

func (f *findFixture) paint() *surface.CellBuffer {
	f.host.Layout()
	buf := surface.NewCellBuffer(f.w, f.h)
	f.host.Paint(buf)
	return buf
}

// rowOnScreen reports the screen row showing global line g, or -1.
func (f *findFixture) rowOnScreen(g int) int {
	y := g - f.s.ScrollModel().Offset()
	if y < 0 || y >= f.s.ScrollModel().Viewport() {
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

func longChat() []chatprojection.MessageRow {
	rows := make([]chatprojection.MessageRow, 30)
	for i := range rows {
		rows[i] = findRow(string(rune('a'+i)), uint64(i+1), "plain message line")
	}
	rows[3] = findRow("d", 4, "an old needle in history")
	rows[25] = findRow("z", 26, "a recent NEEDLE here")
	return rows
}

func TestFindOpensWithAMatchInViewWithoutMovingTheTranscript(t *testing.T) {
	f := newFindFixture(t, longChat(), 10)
	before := f.s.ScrollModel().Offset()
	f.open()
	if f.s.TranscriptRows(f.h) != f.h-1 {
		t.Fatalf("an open bar must take exactly one row, transcript has %d of %d", f.s.TranscriptRows(f.h), f.h)
	}
	f.typeQuery("needle")
	if !f.s.find.cur.ok || f.s.find.cur.id != "z" {
		t.Fatalf("the visible match must be selected first: %+v", f.s.find.cur)
	}
	if got := f.s.ScrollModel().Offset(); got != before+1 {
		t.Fatalf("a visible match must not scroll: offset %d -> %d (bar takes one bottom row)", before, got)
	}
	if got := f.s.find.status(len(f.rows)); got != "2 of 2" {
		t.Fatalf("status=%q", got)
	}
}

func TestFindHighlightsEveryMatchAndMarksTheCurrentOne(t *testing.T) {
	f := newFindFixture(t, longChat(), 10)
	f.open()
	f.typeQuery("needle")
	tokens := ui.NewTokenSet(f.th)
	_, bg := f.cellAt(25, len("a recent ")) // first letter of the match
	if bg != tokens.Color(ui.ColorAccent).ANSIBG() {
		t.Fatalf("current match background %q, want accent %q", bg, tokens.Color(ui.ColorAccent).ANSIBG())
	}
	if _, bgOutside := f.cellAt(25, 0); bgOutside == bg {
		t.Fatal("text outside the match was highlighted")
	}
	f.key("", tea.KeyUp) // previous = older match, scrolls to it
	_, other := f.cellAt(3, len("an old "))
	if other != tokens.Color(ui.ColorAccent).ANSIBG() {
		t.Fatalf("stepping must move the current mark to the older match: %q", other)
	}
	if f.s.find.cur.id != "d" {
		t.Fatalf("cur=%+v", f.s.find.cur)
	}
	if got := f.s.ScrollModel().Offset(); got > 3 {
		t.Fatalf("older match must be revealed, offset=%d", got)
	}
	if f.s.ScrollModel().AutoScroll() {
		t.Fatal("leaving the bottom must stop following new messages")
	}
}

func TestFindEnterAndShiftEnterStepThroughMatches(t *testing.T) {
	f := newFindFixture(t, longChat(), 10)
	f.open()
	f.typeQuery("needle")
	f.host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	f.sync(false)
	if f.s.find.cur.id != "d" {
		t.Fatalf("Enter must wrap to the first match: %+v", f.s.find.cur)
	}
	f.host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	f.sync(false)
	if f.s.find.cur.id != "z" {
		t.Fatalf("Shift+Enter must go back: %+v", f.s.find.cur)
	}
}

func TestFindEscClosesTheBarGivesBackItsRowAndRemovesEveryMark(t *testing.T) {
	f := newFindFixture(t, longChat(), 10)
	f.open()
	f.typeQuery("needle")
	tokens := ui.NewTokenSet(f.th)
	if _, bg := f.cellAt(25, len("a recent ")); bg != tokens.Color(ui.ColorAccent).ANSIBG() {
		t.Fatal("setup: no mark")
	}
	f.key("", tea.KeyEsc)
	if f.s.FindOpen() || f.s.TranscriptRows(f.h) != f.h {
		t.Fatalf("Esc must close the bar and return its row")
	}
	f.sync(false)
	_, bg := f.cellAt(25, len("a recent "))
	if bg == tokens.Color(ui.ColorAccent).ANSIBG() || bg == tokens.Color(ui.ColorWarning).ANSIBG() {
		t.Fatalf("a mark outlived the find bar: %q", bg)
	}
	if f.s.find.querying() || f.s.find.counts != nil {
		t.Fatal("closing find must free its counts")
	}
}

func TestFindKeepsTheTextForTheNextOpen(t *testing.T) {
	f := newFindFixture(t, longChat(), 10)
	f.open()
	f.typeQuery("needle")
	f.key("", tea.KeyEsc)
	f.open()
	f.sync(false)
	if got := f.s.findBar.field.Value(); got != "needle" {
		t.Fatalf("field=%q", got)
	}
	if f.s.find.total != 2 {
		t.Fatalf("a retained query must be searched again, total=%d", f.s.find.total)
	}
}

func TestFindSelectionStaysVisibleOverAMatch(t *testing.T) {
	f := newFindFixture(t, longChat(), 10)
	f.open()
	f.typeQuery("needle")
	y := f.rowOnScreen(25)
	start := f.pad + len("a recent ")
	f.s.selection.selAnchorLine, f.s.selection.selAnchorCol = 25, len("a recent ")
	f.s.selection.selEndLine, f.s.selection.selEndCol = 25, len("a recent needle")
	f.s.selection.selecting = true
	f.sync(false)
	tokens := ui.NewTokenSet(f.th)
	c := f.paint().Get(start, y)
	if c.BG != tokens.Color(ui.ColorTextSelection) {
		t.Fatalf("selection must win over the find mark, got bg %v", c.BG)
	}
}

func TestFindBarNeedsRoomAndNeverHidesTheLastTranscriptRows(t *testing.T) {
	f := newFindFixture(t, longChat(), 3)
	f.s.OpenFind()
	if f.s.FindRows(3) != 0 || f.s.TranscriptRows(3) != 3 {
		t.Fatalf("a 3-row chat has no room for the bar")
	}
	if f.s.FindRows(4) != 1 {
		t.Fatal("a 4-row chat keeps 3 rows for the transcript")
	}
}

func TestFindFollowsRowsThatGrowWhileStreaming(t *testing.T) {
	f := newFindFixture(t, longChat(), 10)
	f.open()
	f.typeQuery("needle")
	last := len(f.rows) - 1
	f.rows[last] = findRow(f.rows[last].ID, 500, "needle again", "and needle once more")
	f.sync(true)
	if f.s.find.total != 4 {
		t.Fatalf("a replaced row must be recounted, total=%d", f.s.find.total)
	}
}

func TestFindPendingExpiresWhenTheChatIsNoLongerObserved(t *testing.T) {
	rows := make([]chatprojection.MessageRow, 200)
	for i := range rows {
		rows[i] = findRow(string(rune(i+1)), uint64(i+1), "needle")
	}
	f := newFindFixture(t, rows, 10)
	f.s.find.limit = time.Nanosecond
	f.open()
	f.typeQuery("needle")
	if !f.s.FindPending() {
		t.Fatal("a long history needs more frames to count")
	}
	f.s.findSeen = time.Now().Add(-2 * time.Second)
	if f.s.FindPending() {
		t.Fatal("a chat nobody renders must not keep the shell awake")
	}
}

func TestFindForgetsRemovedRowsWhenTheTranscriptShrinksAndGrowsAgain(t *testing.T) {
	rows := []chatprojection.MessageRow{findRow("a", 1, "hit"), findRow("b", 2, "hit"), findRow("c", 3, "hit")}
	var f chatFind
	f.setQuery("hit", -1)
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

func TestFindBarPageKeysScrollTheTranscriptWhileTheFieldHasTheKeyboard(t *testing.T) {
	f := newFindFixture(t, longChat(), 10)
	f.open()
	f.typeQuery("needle")
	before := f.s.ScrollModel().Offset()
	f.host.Handle(tea.KeyPressMsg{Code: tea.KeyPgUp})
	f.sync(false)
	if got := f.s.ScrollModel().Offset(); got >= before {
		t.Fatalf("PgUp must scroll the transcript up: %d -> %d", before, got)
	}
	if f.host.Focused() != f.s.FindID() || f.s.findBar.field.Value() != "needle" {
		t.Fatal("scrolling must not move the keyboard out of the find field or change its text")
	}
}
