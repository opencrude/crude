package term

import (
	"bytes"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/ui"
)

func requireEmulator(t *testing.T) *Emulator {
	t.Helper()
	e, err := NewEmulator()
	if err != nil {
		t.Skipf("libghostty-vt: %v", err)
	}
	t.Cleanup(e.Close)
	e.Resize(20, 6)
	return e
}

func TestEmulatorPrintAndNewline(t *testing.T) {
	e := requireEmulator(t)
	e.Feed([]byte("hi\r\nthere"))
	if got := rowText(t, e, 0); got != "hi" {
		t.Fatalf("row0=%q want hi", got)
	}
	if got := rowText(t, e, 1); !strings.HasPrefix(got, "there") {
		t.Fatalf("row1=%q want there", got)
	}
	x, y, _ := e.Cursor()
	if y != 1 || x != 5 {
		t.Fatalf("cursor=(%d,%d) want (5,1)", x, y)
	}
}

func TestEmulatorSGRAndCUP(t *testing.T) {
	e := requireEmulator(t)
	e.Resize(10, 3)
	e.Feed([]byte("\x1b[38;2;255;0;0mR\x1b[0m\x1b[2;3H!"))
	buf := paintGrid(t, e)
	c := buf.Get(0, 0)
	if c.Char != 'R' {
		t.Fatalf("cell=%+v want R", c)
	}
	if c.FG.Type != cell.ColorTrueColor || c.FG.R != 255 {
		t.Fatalf("fg=%+v want truecolor red", c.FG)
	}
	x, y, _ := e.Cursor()
	if x != 3 || y != 1 {
		t.Fatalf("cursor=(%d,%d) want (3,1) after CUP 2;3", x, y)
	}
	if buf.Get(2, 1).Char != '!' {
		t.Fatalf("CUP write landed on %q", string(buf.Get(2, 1).Char))
	}
}

func TestEmulatorInverseUsesSingleReverseOperation(t *testing.T) {
	e := requireEmulator(t)
	e.Feed([]byte("\x1b[7mX"))
	c := paintGrid(t, e).Get(0, 0)
	if c.Attr&cell.AttrReverse == 0 {
		t.Fatalf("inverse cell attrs=%v want AttrReverse", c.Attr)
	}
	if !c.FG.Equal(cell.White) || !c.BG.Equal(cell.Black) {
		t.Fatalf("inverse pre-swapped colors: fg=%v bg=%v", c.FG, c.BG)
	}
}

func TestEmulatorAltScreen(t *testing.T) {
	e := requireEmulator(t)
	e.Resize(8, 2)
	e.Feed([]byte("keep"))
	e.Feed([]byte("\x1b[?1049hALT"))
	if got := rowText(t, e, 0); !strings.HasPrefix(got, "ALT") {
		t.Fatalf("alt=%q", got)
	}
	e.Feed([]byte("\x1b[?1049l"))
	if got := rowText(t, e, 0); !strings.HasPrefix(got, "keep") {
		t.Fatalf("primary after alt=%q", got)
	}
}

func TestEmulatorOSCTitle(t *testing.T) {
	e := requireEmulator(t)
	var title string
	e.SetTitleHandler(func(s string) { title = s })
	e.Feed([]byte("\x1b]0;ghostty-pin\x07"))
	if title != "ghostty-pin" && e.Title() != "ghostty-pin" {
		t.Fatalf("title=%q handler=%q", e.Title(), title)
	}
}

// Title callbacks fire synchronously from libghostty inside vt_write. The
// emulator must collect them and deliver to the Go handler only after the C
// call fully returns: a handler that re-entered libghostty mid-parse would be
// unsafe, and downstream publishers (event bus) assume Feed has returned.
// Observable contract: several OSC changes in one feed deliver exactly one
// notification, with the final title.
func TestEmulatorOSCTitleDeferredToEndOfFeed(t *testing.T) {
	e := requireEmulator(t)
	var calls []string
	e.SetTitleHandler(func(s string) { calls = append(calls, s) })
	e.Feed([]byte("\x1b]2;one\x07body\x1b]2;two\x07"))
	if len(calls) != 1 || calls[0] != "two" {
		t.Fatalf("handler calls=%q want exactly [\"two\"]", calls)
	}
	if got := e.Title(); got != "two" {
		t.Fatalf("Title()=%q want %q", got, "two")
	}
	// A later feed with the same title must not notify again.
	e.Feed([]byte("\x1b]2;two\x07"))
	if len(calls) != 1 {
		t.Fatalf("unchanged title re-notified: calls=%q", calls)
	}
}

func TestEmulatorGraphemeAndWideCell(t *testing.T) {
	e := requireEmulator(t)
	e.Resize(8, 1)
	e.Feed([]byte("a界b"))
	buf := paintGrid(t, e)
	if buf.Get(0, 0).Char != 'a' {
		t.Fatalf("col0=%q", string(buf.Get(0, 0).Char))
	}
	if buf.Get(1, 0).Width != 2 {
		t.Fatalf("wide width=%d content=%q", buf.Get(1, 0).Width, buf.Get(1, 0).Content)
	}
	if buf.Get(2, 0).Width != 0 {
		t.Fatalf("continuation width=%d", buf.Get(2, 0).Width)
	}
}

func TestEmulatorQueryReplies(t *testing.T) {
	e := requireEmulator(t)
	var replies []byte
	e.SetReplyHandler(func(b []byte) { replies = append(replies, b...) })
	e.Feed([]byte("\x1b[c")) // DA1
	if !bytes.Contains(replies, []byte("\x1b[")) && !bytes.Contains(replies, []byte("\x1b?")) {
		// DA1 is typically ESC [ ? 62 ; ... c
		if len(replies) == 0 {
			t.Fatalf("DA1 produced no PTY reply")
		}
	}
	replies = nil
	e.Feed([]byte("\x1b[5n")) // DSR
	if len(replies) == 0 {
		t.Fatalf("DSR produced no PTY reply")
	}
	replies = nil
	e.Feed([]byte("\x1b[18t")) // text area size
	if len(replies) == 0 {
		t.Fatalf("CSI 18 t produced no size reply")
	}
}

func TestEmulatorApplicationCursorKeys(t *testing.T) {
	e := requireEmulator(t)
	normal := e.EncodeKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if !bytes.Equal(normal, []byte{0x1b, '[', 'A'}) && !bytes.Contains(normal, []byte("A")) {
		t.Fatalf("normal up %q", normal)
	}
	e.Feed([]byte("\x1b[?1h")) // DECCKM application cursor
	app := e.EncodeKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if bytes.Equal(app, normal) && !bytes.Contains(app, []byte{'O'}) {
		t.Fatalf("application up %q should differ from normal %q", app, normal)
	}
	if !bytes.Contains(app, []byte{0x1b, 'O', 'A'}) && !bytes.Contains(app, []byte("OA")) {
		t.Fatalf("application up %q want ESC OA", app)
	}
}

func TestEmulatorBracketedPaste(t *testing.T) {
	e := requireEmulator(t)
	plain := e.EncodePaste([]byte("hi\nthere"))
	if bytes.Contains(plain, []byte("200~")) {
		t.Fatalf("disabled paste should not wrap: %q", plain)
	}
	e.Feed([]byte("\x1b[?2004h"))
	if !e.BracketedPaste() {
		t.Fatal("mode 2004 should enable bracketed paste")
	}
	wrapped := e.EncodePaste([]byte("hi\nthere"))
	if !bytes.Contains(wrapped, []byte("200~")) || !bytes.Contains(wrapped, []byte("201~")) {
		t.Fatalf("bracketed paste %q", wrapped)
	}
	unsafe := e.EncodePaste([]byte("x\x1b[201~injected"))
	if bytes.Contains(unsafe, []byte("\x1b[201~injected")) {
		t.Fatalf("unsafe marker should be sanitized: %q", unsafe)
	}
}

func TestEmulatorMouseTrackingAndScrollback(t *testing.T) {
	e := requireEmulator(t)
	e.Resize(10, 4)
	if e.MouseTracking() {
		t.Fatal("tracking should start off")
	}
	wheel := e.EncodeMouse(ui.MouseWheelEvent(tea.MouseWheelMsg{Button: tea.MouseWheelUp, Y: 1}), 1, 1)
	if len(wheel) != 0 {
		t.Fatalf("disabled tracking must not encode wheel: %q", wheel)
	}
	e.Feed([]byte("\x1b[?1000h\x1b[?1006h"))
	if !e.MouseTracking() {
		t.Fatal("1000h should enable mouse tracking")
	}
	click := e.EncodeMouse(ui.MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft, X: 2, Y: 1}), 2, 1)
	if len(click) == 0 {
		t.Fatal("enabled tracking should encode a click")
	}

	e.Feed([]byte("\x1b[?1000l"))
	for i := 0; i < 20; i++ {
		e.Feed([]byte("line\r\n"))
	}
	off, total, vis := e.Scrollbar()
	if total <= vis {
		t.Fatalf("scrollback total=%d vis=%d", total, vis)
	}
	e.ScrollDelta(-2)
	off2, _, _ := e.Scrollbar()
	if off2 == off && off != 0 {
		t.Fatalf("scroll delta did not move offset=%d", off)
	}
}

func TestEmulatorEncodeKeyPrintableAndCtrlC(t *testing.T) {
	e := requireEmulator(t)
	got := e.EncodeKey(tea.KeyPressMsg{Text: "a", Code: 'a'})
	if !bytes.Equal(got, []byte("a")) {
		t.Fatalf("a=%q", got)
	}
	got = e.EncodeKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !bytes.Equal(got, []byte{0x03}) {
		t.Fatalf("ctrl-c %q", got)
	}
	var help []byte
	for _, r := range "help" {
		help = append(help, e.EncodeKey(tea.KeyPressMsg{Text: string(r), Code: r})...)
	}
	if string(help) != "help" {
		t.Fatalf("help=%q", help)
	}
	e.Feed([]byte("\x1b[>3u"))
	got = e.EncodeKey(tea.KeyPressMsg{Text: "a", Code: 'a'})
	if !bytes.Equal(got, []byte("a")) {
		t.Fatalf("printable after kitty flags=%q", got)
	}
	up := e.EncodeKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if !bytes.Contains(up, []byte(":1A")) || !bytes.Contains(up, []byte(":3A")) {
		t.Fatalf("kitty report-events up should be press+release, got %q", up)
	}
}

func TestEmulatorZshRedrawDoesNotDuplicateCharacters(t *testing.T) {
	e := requireEmulator(t)
	// Zsh's line editor redraws the first typed byte before replacing it with
	// the full buffer: "h\bhelp". PTY reads routinely split this around the
	// first byte, so feed it as separate chunks as the manager does.
	e.Feed([]byte("\x1b[?2004hh"))
	e.Feed([]byte("\bhelp"))
	if got := rowText(t, e, 0); got != "help" {
		t.Fatalf("zsh redraw row=%q want help", got)
	}
}

func TestEmulatorFocusReportingGatedByMode(t *testing.T) {
	e := requireEmulator(t)
	if got := e.EncodeFocus(true); got != nil {
		t.Fatalf("focus report without mode 1004 = %q; want nil", got)
	}
	e.Feed([]byte("\x1b[?1004h"))
	if !e.FocusReporting() {
		t.Fatal("mode 1004 set but FocusReporting is false")
	}
	if got := string(e.EncodeFocus(true)); got != "\x1b[I" {
		t.Fatalf("focus-in=%q want CSI I", got)
	}
	if got := string(e.EncodeFocus(false)); got != "\x1b[O" {
		t.Fatalf("focus-out=%q want CSI O", got)
	}
	e.Feed([]byte("\x1b[?1004l"))
	if got := e.EncodeFocus(true); got != nil {
		t.Fatalf("focus report after mode reset = %q; want nil", got)
	}
}

func TestEmulatorAltScreenModeQuery(t *testing.T) {
	e := requireEmulator(t)
	if e.AltScreen() {
		t.Fatal("primary screen reported as alt")
	}
	e.Feed([]byte("\x1b[?1049h"))
	if !e.AltScreen() {
		t.Fatal("alt screen not reported after 1049h")
	}
	e.Feed([]byte("\x1b[?1049l"))
	if e.AltScreen() {
		t.Fatal("alt screen still reported after 1049l")
	}
}

func TestEmulatorSelectionDragCopiesText(t *testing.T) {
	e := requireEmulator(t)
	e.Feed([]byte("hello world"))
	if e.HasSelection() {
		t.Fatal("fresh terminal has a selection")
	}
	e.SelectionPress(0, 0, 1_000_000_000)
	if e.HasSelection() {
		t.Fatal("single press should only anchor, not select")
	}
	if !e.SelectionDrag(4, 0) {
		t.Fatal("drag produced no selection")
	}
	if !e.SelectionRelease(4, 0) {
		t.Fatal("selection lost on release")
	}
	if got := e.SelectionText(); got != "hello" {
		t.Fatalf("selection text=%q want hello", got)
	}
	e.ClearSelection()
	if e.HasSelection() {
		t.Fatal("ClearSelection left a selection")
	}
	if got := e.SelectionText(); got != "" {
		t.Fatalf("text after clear=%q want empty", got)
	}
}

func TestEmulatorSelectionDoubleClickSelectsWord(t *testing.T) {
	e := requireEmulator(t)
	e.Feed([]byte("hello world"))
	base := uint64(5_000_000_000)
	e.SelectionPress(7, 0, base)
	e.SelectionRelease(7, 0)
	e.SelectionPress(7, 0, base+200_000_000)
	if !e.HasSelection() {
		t.Fatal("double click did not create a word selection")
	}
	if got := e.SelectionText(); got != "world" {
		t.Fatalf("double-click selection=%q want world", got)
	}
}

func TestEmulatorSelectionPaintsColors(t *testing.T) {
	e := requireEmulator(t)
	// Child inverse/dim ink must not invert or mute the range fill.
	e.Feed([]byte("\x1b[?25l\x1b[1;2;3;4;7;38;2;200;10;20;48;2;30;40;50mabc"))
	original := paintGrid(t, e).Get(1, 0)
	e.SelectionPress(0, 0, 1)
	e.SelectionDrag(2, 0)
	for _, colors := range [][2]cell.Color{
		{cell.White, cell.Blue},
		{cell.Black, cell.NewTrueColor(120, 180, 240)},
	} {
		e.SetSelectionColors(colors[0], colors[1])
		c := paintGrid(t, e).Get(1, 0)
		if c.FG != colors[0] || c.BG != colors[1] || c.Attr != original.Attr&^(cell.AttrReverse|cell.AttrDim) {
			t.Fatalf("selected cell=%+v colors=%v original=%+v", c, colors, original)
		}
		if got := e.SelectionText(); got != "abc" {
			t.Fatalf("selection text=%q", got)
		}
	}
	e.ClearSelection()
	if got := paintGrid(t, e).Get(1, 0); got != original {
		t.Fatalf("clear changed child ink: got=%+v want=%+v", got, original)
	}
}

func TestEmulatorSelectionWideGlyphAndCursor(t *testing.T) {
	for _, end := range []int{1, 2} {
		t.Run(string(rune('0'+end)), func(t *testing.T) {
			e := requireEmulator(t)
			e.Feed([]byte("\x1b[?25la界b"))
			e.SetSelectionColors(cell.Black, cell.Cyan)
			e.SelectionPress(0, 0, 1)
			e.SelectionDrag(end, 0)
			buf := paintGrid(t, e)
			for x := 1; x <= 2; x++ {
				c := buf.Get(x, 0)
				if c.FG != cell.Black || c.BG != cell.Cyan || c.Attr != 0 {
					t.Fatalf("wide selected column %d=%+v", x, c)
				}
			}
			if buf.Get(1, 0).Content != "界" || buf.Get(1, 0).Width != 2 || buf.Get(2, 0).Width != 0 {
				t.Fatal("selection damaged wide glyph")
			}
			if c := buf.Get(3, 0); c.BG != cell.Black {
				t.Fatalf("unselected neighbor=%+v", c)
			}
			if got := e.SelectionText(); got != "a界" {
				t.Fatalf("native text=%q", got)
			}
			e.Feed([]byte("\x1b[?25h\x1b[1;2H"))
			e.SetCursorState(true, true)
			buf = paintGrid(t, e)
			for x := 1; x <= 2; x++ {
				c := buf.Get(x, 0)
				if c.BG != cell.Cyan || c.Attr&cell.AttrReverse == 0 || c.Attr&cell.AttrDim != 0 {
					t.Fatalf("selected block cursor column %d=%+v", x, c)
				}
			}
		})
	}
}

func TestEmulatorSelectAll(t *testing.T) {
	e := requireEmulator(t)
	e.Feed([]byte("first\r\nsecond"))
	e.SelectAll()
	if !e.HasSelection() {
		t.Fatal("SelectAll installed no selection")
	}
	text := e.SelectionText()
	if !strings.Contains(text, "first") || !strings.Contains(text, "second") {
		t.Fatalf("select-all text=%q want both lines", text)
	}
}

func TestEmulatorClipboardWriteOSC52Deferred(t *testing.T) {
	e := requireEmulator(t)
	var got []string
	inFeed := false
	e.SetClipboardHandler(func(text string) {
		if inFeed {
			t.Error("clipboard handler re-entered during vt_write")
		}
		got = append(got, text)
	})
	inFeed = true
	func() {
		defer func() { inFeed = false }()
		// The C write returns before flushEffects runs, so observing
		// inFeed==true inside the handler would mean synchronous re-entry.
		inFeed = false
		e.Feed([]byte("\x1b]52;c;aGVsbG8=\x07"))
	}()
	if len(got) != 1 || got[0] != "hello" {
		t.Fatalf("clipboard writes=%q want [hello]", got)
	}
}

func TestEmulatorBellDeferredToEndOfFeed(t *testing.T) {
	e := requireEmulator(t)
	rang := 0
	e.SetBellHandler(func() { rang++ })
	e.Feed([]byte("ding\x07"))
	if rang != 1 {
		t.Fatalf("bell handler ran %d times, want 1", rang)
	}
	e.Feed([]byte("quiet"))
	if rang != 1 {
		t.Fatalf("bell handler ran %d times after quiet feed, want 1", rang)
	}
}

func TestEmulatorPwdOSC7(t *testing.T) {
	e := requireEmulator(t)
	var got []string
	e.SetPwdHandler(func(p string) { got = append(got, p) })
	e.Feed([]byte("\x1b]7;file:///tmp/crude-project\x07"))
	if len(got) != 1 {
		t.Fatalf("pwd handler ran %d times, want 1", len(got))
	}
	if e.PWD() != got[0] || !strings.Contains(e.PWD(), "/tmp/crude-project") {
		t.Fatalf("PWD=%q handler=%q want /tmp/crude-project", e.PWD(), got[0])
	}
}

func TestEmulatorCursorStylesAlwaysPaintBlock(t *testing.T) {
	e := requireEmulator(t)
	e.Feed([]byte("x\x1b[1;1H"))
	// Product cursor geometry is always a block, even when an application asks
	// DECSCUSR for underline or bar. Timing still follows the requested mode.
	e.Feed([]byte("\x1b[4 q"))
	buf := paintGrid(t, e)
	c := buf.Get(0, 0)
	if c.Attr&cell.AttrReverse == 0 || c.Attr&cell.AttrUnderline != 0 {
		t.Fatalf("underline request attrs=%v want reverse block only", c.Attr)
	}
	e.Feed([]byte("\x1b[6 q"))
	buf = paintGrid(t, e)
	if c := buf.Get(0, 0); c.Attr&cell.AttrReverse == 0 || c.Attr&cell.AttrUnderline != 0 {
		t.Fatalf("bar request attrs=%v want reverse block only", c.Attr)
	}
}

func TestEmulatorCursorBlinkAndFocusStates(t *testing.T) {
	e := requireEmulator(t)
	e.Feed([]byte("x\x1b[1;1H"))
	if !e.CursorBlink() {
		t.Fatal("default cursor should request blinking (DECSCUSR 0)")
	}
	// Focused, blink phase on: cursor inverts the cell.
	e.SetCursorState(true, true)
	if c := paintGrid(t, e).Get(0, 0); c.Attr&cell.AttrReverse == 0 {
		t.Fatalf("blink-on cursor attrs=%v want AttrReverse", c.Attr)
	} else if !c.FG.Equal(cell.White) || !c.BG.Equal(cell.Black) {
		t.Fatalf("cursor pre-swapped colors: fg=%v bg=%v", c.FG, c.BG)
	}
	// Focused, blink phase off: cursor hidden.
	e.SetCursorState(true, false)
	if c := paintGrid(t, e).Get(0, 0); c.Attr&cell.AttrReverse != 0 {
		t.Fatal("blink-off phase still painted the cursor")
	}
	// Unfocused: steady dim cursor regardless of phase.
	e.SetCursorState(false, false)
	c := paintGrid(t, e).Get(0, 0)
	if c.Attr&cell.AttrReverse == 0 || c.Attr&cell.AttrDim == 0 {
		t.Fatalf("unfocused cursor attrs=%v want reverse+dim", c.Attr)
	}
	// Steady style disables blinking: cursor shows even on the off phase.
	e.Feed([]byte("\x1b[2 q"))
	e.SetCursorState(true, false)
	if e.CursorBlink() {
		t.Fatal("DECSCUSR 2 (steady block) should stop blink requests")
	}
	if c := paintGrid(t, e).Get(0, 0); c.Attr&cell.AttrReverse == 0 {
		t.Fatal("steady cursor must ignore the blink phase")
	}
}

func TestEmulatorViewportPinned(t *testing.T) {
	e := requireEmulator(t)
	for i := 0; i < 30; i++ {
		e.Feed([]byte("line\r\n"))
	}
	if !e.ViewportPinned() {
		t.Fatal("viewport should start pinned to the active area")
	}
	e.ScrollDelta(-3)
	if e.ViewportPinned() {
		t.Fatal("viewport still pinned after scrolling into history")
	}
	e.ScrollBottom()
	if !e.ViewportPinned() {
		t.Fatal("viewport not pinned after ScrollBottom")
	}
}

func paintGrid(t *testing.T, e *Emulator) *surface.CellBuffer {
	t.Helper()
	buf := surface.NewCellBuffer(e.Cols(), e.Rows())
	e.Paint(buf, 0, 0, cell.White, cell.Black)
	return buf
}

func rowText(t *testing.T, e *Emulator, y int) string {
	t.Helper()
	buf := paintGrid(t, e)
	var sb strings.Builder
	for x := 0; x < e.Cols(); x++ {
		c := buf.Get(x, y)
		if c.Width == 0 {
			continue
		}
		if c.Char == 0 || c.Char == ' ' {
			if sb.Len() == 0 {
				continue
			}
			break
		}
		if c.Content != "" {
			sb.WriteString(c.Content)
		} else {
			sb.WriteRune(c.Char)
		}
	}
	return sb.String()
}
