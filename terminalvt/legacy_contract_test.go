//go:build cgo

package terminalvt

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// These contracts formerly exercised the shell-owned parser. They deliberately
// use neutral input and detached rows, not a second presentation parser.
func TestLegacyRedrawAndCUP(t *testing.T) {
	e := newTestEngine(t, Options{Cols: 20, Rows: 6})
	e.Feed([]byte("\x1b[?2004hh"))
	e.Feed([]byte("\bhelp\r\nthere\x1b[2;3H!"))
	p, err := e.Rows(-1, 0)
	if err != nil {
		t.Fatal(err)
	}
	var first strings.Builder
	for _, c := range p.Cells[0] {
		first.WriteString(c.Text)
	}
	if strings.TrimSpace(first.String()) != "help" || p.Cells[1][2].Text != "!" || p.Cursor.X != 3 || p.Cursor.Y != 1 {
		t.Fatalf("split redraw/CUP: text=%q cursor=%+v row1=%+v", first.String(), p.Cursor, p.Cells[1])
	}
}

func TestLegacyApplicationCursorAndKittyKeys(t *testing.T) {
	e := newTestEngine(t, Options{})
	for _, tc := range []struct{ mode, want string }{{"", "\x1b[A"}, {"\x1b[?1h", "\x1bOA"}, {"\x1b[?1l", "\x1b[A"}} {
		e.Feed([]byte(tc.mode))
		if got := string(e.EncodeKey(Key{Code: KeyUp})); got != tc.want {
			t.Fatalf("mode=%q up=%q want=%q", tc.mode, got, tc.want)
		}
	}
	for _, mode := range []string{"", "\x1b[>3u"} {
		e.Feed([]byte(mode))
		var typed []byte
		for _, r := range "help界" {
			typed = append(typed, e.EncodeKey(Key{Code: r, Text: string(r)})...)
		}
		if string(typed) != "help界" {
			t.Fatalf("typed=%q mode=%q", typed, mode)
		}
		if got := e.EncodeKey(Key{Code: 'c', Mod: ModCtrl}); !bytes.Equal(got, []byte{3}) && mode == "" {
			t.Fatalf("ctrl-c=%q", got)
		}
	}
	up := string(e.EncodeKey(Key{Code: KeyUp}))
	if !strings.Contains(up, ":1A") || !strings.Contains(up, ":3A") {
		t.Fatalf("Kitty press+release=%q", up)
	}
}

func TestLegacyPasteAndFocusModeResets(t *testing.T) {
	e := newTestEngine(t, Options{})
	if got := e.EncodeFocus(true); len(got) != 0 {
		t.Fatalf("disabled focus=%q", got)
	}
	if got := e.EncodePaste([]byte("hello")); string(got) != "hello" {
		t.Fatalf("plain paste=%q", got)
	}
	e.Feed([]byte("\x1b[?2004h\x1b[?1004h"))
	if string(e.EncodeFocus(true)) != "\x1b[I" || string(e.EncodeFocus(false)) != "\x1b[O" {
		t.Fatal("focus encoding")
	}
	unsafe := []byte("x\x1b[201~injected")
	before := append([]byte(nil), unsafe...)
	got := e.EncodePaste(unsafe)
	if !bytes.HasPrefix(got, []byte("\x1b[200~")) || !bytes.HasSuffix(got, []byte("\x1b[201~")) || bytes.Contains(got, []byte("\x1b[201~injected")) {
		t.Fatalf("unsafe paste=%q", got)
	}
	if !bytes.Equal(unsafe, before) {
		t.Fatal("paste mutated caller")
	}
	e.Feed([]byte("\x1b[?2004l\x1b[?1004l"))
	p, err := e.Rows(-1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p.Modes.BracketedPaste || p.Modes.FocusReporting || len(e.EncodeFocus(true)) != 0 || string(e.EncodePaste([]byte("hello"))) != "hello" {
		t.Fatal("mode reset did not reach encoding/projection")
	}
}

func TestLegacyMouseAndAlternateScreenRestore(t *testing.T) {
	e := newTestEngine(t, Options{Cols: 8, Rows: 2})
	m := Mouse{Action: MousePress, Button: MouseWheelUp, X: 2, Y: 1}
	if len(e.EncodeMouse(m)) != 0 {
		t.Fatal("disabled mouse encoded")
	}
	e.Feed([]byte("keep\x1b[?1000h\x1b[?1006h"))
	if got := string(e.EncodeMouse(m)); got != "\x1b[<64;3;2M" {
		t.Fatalf("wheel=%q", got)
	}
	// Switching screens preserves the cursor column; explicitly position the
	// alternate application's first write instead of assuming an implicit CUP.
	e.Feed([]byte("\x1b[?1000l\x1b[?1049h\x1b[HALT"))
	p, err := e.Rows(-1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p.Modes.MouseTracking || !p.Modes.AltScreen || !strings.HasPrefix(text(p), "ALT") || len(e.EncodeMouse(m)) != 0 {
		t.Fatalf("alt=%+v text=%q", p.Modes, text(p))
	}
	e.Feed([]byte("\x1b[?1049l"))
	p, err = e.Rows(-1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p.Modes.AltScreen || !strings.HasPrefix(text(p), "keep") {
		t.Fatalf("primary=%q modes=%+v", text(p), p.Modes)
	}
}

func TestLegacyTrueColorAndCursorStyleProjection(t *testing.T) {
	e := newTestEngine(t, Options{Cols: 8, Rows: 2})
	e.Feed([]byte("\x1b[38;2;255;10;20mR\x1b[0m\x1b[H"))
	for _, tc := range []struct {
		request string
		blink   bool
	}{{"", true}, {"\x1b[4 q", false}, {"\x1b[6 q", false}, {"\x1b[3 q", true}, {"\x1b[5 q", true}, {"\x1b[2 q", false}} {
		e.Feed([]byte(tc.request))
		p, err := e.Rows(-1, 0)
		if err != nil {
			t.Fatal(err)
		}
		c := p.Cells[0][0]
		if c.Text != "R" || c.FG != (Color{Explicit: true, R: 255, G: 10, B: 20}) || c.Style.Underline || p.Cursor.Blink != tc.blink || !p.Cursor.Visible {
			t.Fatalf("DECSCUSR=%q cell=%+v cursor=%+v", tc.request, c, p.Cursor)
		}
	}
	e.Feed([]byte("\x1b[?25l"))
	p, err := e.Rows(-1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p.Cursor.Visible {
		t.Fatal("hidden cursor remained visible")
	}
}

func TestLegacyEffectsAreFeedLocalAndDetached(t *testing.T) {
	e := newTestEngine(t, Options{})
	fx := e.Feed([]byte("\x1b]2;one\a\x1b]2;two\a\x1b]7;file:///tmp/project\a\x1b]52;c;aGVsbG8=\a\a\x1b[5n"))
	if !fx.TitleChanged || fx.Title != "two" || !fx.CWDChanged || fx.CWD != "file:///tmp/project" || !fx.Bell || len(fx.Clipboard) != 1 || string(fx.Clipboard[0]) != "hello" || len(fx.Replies) != 1 || string(fx.Replies[0]) != "\x1b[0n" {
		t.Fatalf("effects=%+v", fx)
	}
	quiet := e.Feed([]byte("quiet"))
	if quiet.TitleChanged || quiet.CWDChanged || quiet.Bell || len(quiet.Clipboard) != 0 || len(quiet.Replies) != 0 {
		t.Fatalf("replayed effects=%+v", quiet)
	}
	e.Feed([]byte("\x1b]52;c;d29ybGQ=\a\x1b[5n"))
	if string(fx.Clipboard[0]) != "hello" || string(fx.Replies[0]) != "\x1b[0n" {
		t.Fatal("later feed changed prior effects")
	}
	read := e.Feed([]byte("\x1b]52;c;?\a"))
	if len(read.Clipboard) != 0 || len(read.Replies) != 0 {
		t.Fatalf("clipboard read unexpectedly serviced=%+v", read)
	}
}

func TestLegacySizeRepliesAndRejectedResize(t *testing.T) {
	e := newTestEngine(t, Options{Cols: 20, Rows: 6})
	for _, size := range [][2]int{{20, 6}, {12, 3}} {
		if err := e.Resize(size[0], size[1]); err != nil {
			t.Fatal(err)
		}
		fx := e.Feed([]byte("\x1b[18t\x1b[16t\x1b[14t"))
		want := "\x1b[8;6;20t\x1b[6;16;8t\x1b[4;96;160t"
		if size[0] == 12 {
			want = "\x1b[8;3;12t\x1b[6;16;8t\x1b[4;48;96t"
		}
		if got := string(bytes.Join(fx.Replies, nil)); got != want {
			t.Fatalf("size=%v replies=%q want=%q", size, got, want)
		}
	}
	if err := e.Resize(7, 1); !errors.Is(err, ErrBounds) {
		t.Fatalf("undersize=%v", err)
	}
	p, err := e.Rows(-1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p.Cols != 12 || p.GridRows != 3 {
		t.Fatalf("rejected resize committed: %+v", p)
	}
	if fx := e.Feed([]byte("\x1b[c")); len(fx.Replies) != 1 || !bytes.HasPrefix(fx.Replies[0], []byte("\x1b[?")) {
		t.Fatalf("DA1=%q", fx.Replies)
	}
}
