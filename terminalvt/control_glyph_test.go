//go:build cgo

package terminalvt_test

import (
	"reflect"
	"strings"
	"testing"

	"crude/terminalvt"
	"crude/tui/compositor"
	"crude/tui/termview"
	"crude/tui/theme"
	"crude/tui/ui"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// Expectations come from the pinned Ghostty stream's ground/non-ground
// distinction, not from the projection normalizer: ground stores CAN/SUB;
// non-ground CAN/SUB abort the sequence without printing a cell. Unsafe stored
// codepoints must appear as U+FFFD, never as executable host-terminal bytes.
func TestEngineControlGlyphProjection(t *testing.T) {
	for _, tc := range []struct {
		name  string
		parts []string
		want  string
	}{
		{"CAN", []string{"\x18"}, "�"},
		{"SUB", []string{"\x1a"}, "�"},
		{"DLE", []string{"\x10"}, "�"},
		{"DEL", []string{"\x7f"}, "�"},
		{"raw-C1", []string{"\x9b"}, "�"},
		{"UTF8-C1", []string{"\xc2", "\x9b"}, "�"},
		{"cancel-split-CSI", []string{"\x1b[31", "\x18", "Z"}, "Z"},
		{"substitute-split-CSI", []string{"\x1b[31", "\x1a", "Z"}, "Z"},
		{"cancel-split-OSC", []string{"\x1b]2;hidden", "\x18", "Z"}, "Z"},
		{"replace-split-ESC", []string{"\x1b[31", "\x1b", "[0mZ"}, "Z"},
		{"malformed-UTF8-CAN", []string{"\xc2", "\x18"}, "��"},
		{"safe-graphemes", []string{"\x1b[?2027h", "é👩‍💻♻️"}, "é👩‍💻♻️"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			makeEngine := func() *terminalvt.Engine {
				e, err := terminalvt.New(terminalvt.Options{Cols: 16, Rows: 2})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(e.Close)
				return e
			}
			stream, direct := makeEngine(), makeEngine()
			var encoder terminalvt.RowEncoder
			var decoder terminalvt.RowDecoder
			read := func() terminalvt.Projection {
				t.Helper()
				p, err := stream.StreamRows(terminalvt.RowRequest{Start: -1})
				if err != nil {
					t.Fatal(err)
				}
				q, err := direct.Rows(terminalvt.RowRequest{Start: -1})
				if err != nil {
					t.Fatal(err)
				}
				if len(p.Cells) != 2 || len(p.Wrapped) != 2 || p.Cursor != q.Cursor || !reflect.DeepEqual(p.Cells, q.Cells) || !reflect.DeepEqual(p.Wrapped, q.Wrapped) {
					t.Fatalf("stream/direct projection diverged: stream=%+v direct=%+v", p, q)
				}
				for i, row := range p.Cells {
					packed := encoder.Append(nil, row, p.Wrapped[i])
					decoder.Styles = encoder.Definitions
					got, wrap, rest, err := decoder.Decode(nil, packed)
					if err != nil {
						t.Fatalf("native row cannot be published: %v first=%+v", err, row[0])
					}
					if len(row) != 16 || len(rest) != 0 || wrap != p.Wrapped[i] || !reflect.DeepEqual(got, row) {
						t.Fatal("published row lost cells, widths, styles or wrap")
					}
					for _, c := range row {
						for _, r := range c.Text {
							if r < 0x20 || r >= 0x7f && r <= 0x9f {
								t.Fatalf("executable control U+%04X in detached cell", r)
							}
						}
					}
				}
				return p
			}
			read() // The minimized fuzz failure requires an already published blank grid.
			for _, part := range tc.parts {
				stream.Feed([]byte(part))
				direct.Feed([]byte(part))
				read()
			}
			p := read() // Also exercise unchanged dirty-row reuse.
			var line strings.Builder
			for _, c := range p.Cells[0] {
				line.WriteString(c.Text)
			}
			if strings.TrimRight(line.String(), " ") != tc.want {
				t.Fatalf("visible text=%q want=%q", line.String(), tc.want)
			}
			if p.Cursor.Y != 0 || p.Cursor.X != ansi.StringWidth(tc.want) {
				t.Fatalf("cursor=%+v want X=%d Y=0", p.Cursor, ansi.StringWidth(tc.want))
			}
			// Selection and both compositor output paths consume the same safe
			// detached cells. No codec-only repair may leave direct copy/paint raw.
			var view termview.View
			view.Install(p, 1)
			view.SelectionPress(0, 0, 1)
			view.SelectionDrag(p.Cursor.X-1, 0)
			if copied, ok := view.SelectionText(); !ok || copied != tc.want {
				t.Fatalf("copy=%q ok=%t want=%q", copied, ok, tc.want)
			}
			view.ClearSelection()
			c := compositor.NewCompositor(16, 2)
			c.BeginFrame()
			view.Paint(c.Root().Buffer(), 0, 0, 16, 2, ui.NewTokenSet(theme.DefaultDark()), false, false)
			painted := ansi.Strip(compositor.GenerateANSI(c.Root().Buffer()))
			for _, r := range painted {
				if r != '\n' && (r < 0x20 || r >= 0x7f && r <= 0x9f) {
					t.Fatalf("paint emitted executable text U+%04X", r)
				}
			}
			if !strings.HasPrefix(painted, tc.want) {
				t.Fatalf("ANSI visible output=%q want prefix=%q", painted, tc.want)
			}
			c.Composite()
			out := uv.NewScreenBuffer(16, 2)
			c.PresentSurface().Draw(out, out.Bounds())
			for x, cell := range p.Cells[0] {
				if cell.Width > 0 && (out.CellAt(x, 0).Content != cell.Text || out.CellAt(x, 0).Width != int(cell.Width)) {
					t.Fatalf("direct UV cell %d=%+v want=%+v", x, out.CellAt(x, 0), cell)
				}
			}
			// A subsequent publication still works after the former fatal cell.
			stream.Feed([]byte("!"))
			p, err := stream.StreamRows(terminalvt.RowRequest{Start: -1})
			if err != nil || p.Cells[0][p.Cursor.X-1].Text != "!" {
				t.Fatalf("publication did not resume: err=%v projection=%+v", err, p)
			}
		})
	}
}

func TestEngineControlGlyphHistoryAndSynchronizedFrames(t *testing.T) {
	e, err := terminalvt.New(terminalvt.Options{Cols: 16, Rows: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	e.Feed([]byte("\x1b[31;1m\x18\x1b[0m"))
	check := func(p terminalvt.Projection, err error, synchronized bool) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		if p.Synchronized != synchronized || len(p.Cells) == 0 {
			t.Fatalf("unusable frame: %+v", p)
		}
		c := p.Cells[0][0]
		if c.Text != "�" || c.Width != 1 || !c.Style.Bold || !c.FG.Explicit {
			t.Fatalf("control replacement lost glyph, width or style: %+v", c)
		}
		var encoder terminalvt.RowEncoder
		packed := encoder.Append(nil, p.Cells[0], p.Wrapped[0])
		decoder := terminalvt.RowDecoder{Styles: encoder.Definitions}
		got, _, _, err := decoder.Decode(nil, packed)
		if err != nil || !reflect.DeepEqual(got, p.Cells[0]) {
			t.Fatalf("replacement cannot be published intact: %v", err)
		}
	}
	// The synchronized snapshot is first exported only after live state changes.
	e.Feed([]byte("\x1b[?2026h\x1b[2J\x1b[Hhidden\x1a"))
	p, err := e.StreamRows(terminalvt.RowRequest{Start: -1})
	check(p, err, true)
	p, err = e.Rows(terminalvt.RowRequest{Start: -1})
	check(p, err, true)
	e.Feed([]byte("\x1b[?2026l\x1b[2J\x1b[H\x1b[31;1m\x18\x1b[0m\r\nsecond\r\nthird"))
	p, err = e.Rows(terminalvt.RowRequest{Start: 0, Count: 1})
	check(p, err, false)
	p, err = e.StreamRows(terminalvt.RowRequest{Start: 0, Count: 1})
	check(p, err, false)
}
