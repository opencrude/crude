package tea

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
)

// FuzzDirectSurfaceTerminalOutput exercises the actual terminal writer, unlike
// the grid-only resize/conflation target. The reference takes literal SGR/OSC
// input, never Surface.String, Cell.String, Style.String, or a surface-derived
// ANSI export. Both paths deliberately share the production UV terminal diff:
// byte parity is a handoff regression oracle, not an independent VT emulator.
func FuzzDirectSurfaceTerminalOutput(f *testing.F) {
	// Minimized close/start regression: a fresh UV terminal diff must repaint
	// the retained direct snapshot even when its cells have not changed.
	f.Add([]byte("0"))
	f.Add([]byte{20, 8, 255, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12})
	f.Add([]byte{0, 0, 0, 1, 20, 8, 0, 255, 3, 5, 4, 0, 0, 0})
	f.Add([]byte{1, 1, 127, 255, 2, 1, 0, 0, 4, 0, 0, 0, 31, 7})
	f.Add([]byte{2, 8, 64, 0, 4, 255, 0, 0, 0, 3, 0, 0, 0, 255})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 4096 {
			data = data[:4096]
		}
		input := surfaceTransferInput{data: data}
		var directOut, referenceOut bytes.Buffer
		direct := newCursedRenderer(&directOut, []string{"TERM=xterm-256color"}, 1, 1)
		reference := newCursedRenderer(&referenceOut, []string{"TERM=xterm-256color"}, 1, 1)
		direct.setColorProfile(colorprofile.TrueColor)
		reference.setColorProfile(colorprofile.TrueColor)
		src := uv.NewScreenBuffer(1, 1)
		var want uv.ScreenBuffer
		var wantCursor *Cursor

		checkOutput := func(step int, phase string) {
			t.Helper()
			if !bytes.Equal(directOut.Bytes(), referenceOut.Bytes()) {
				t.Fatalf("step %d %s terminal bytes differ:\ndirect %q\nreference %q", step, phase, directOut.Bytes(), referenceOut.Bytes())
			}
			directOut.Reset()
			referenceOut.Reset()
		}
		flush := func(step int) {
			t.Helper()
			if err := direct.flush(false); err != nil {
				t.Fatal(err)
			}
			if err := reference.flush(false); err != nil {
				t.Fatal(err)
			}
			checkOutput(step, "flush")
			assertDirectGrid(t, direct.cellbuf, reference.cellbuf)
			assertDirectGrid(t, direct.cellbuf, want)
			if (direct.lastView.Cursor == nil) != (wantCursor == nil) ||
				(wantCursor != nil && *direct.lastView.Cursor != *wantCursor) {
				t.Fatalf("step %d cursor retained mutable producer metadata", step)
			}
		}

		for step := range 8 {
			w, h := int(input.next()%21), int(input.next()%9)
			flags := input.next()
			alt, empty := flags&1 != 0, flags&2 != 0
			// Inline empty surfaces have zero rows while an empty legacy string
			// has one. That intentional API difference is not a parity failure.
			if h == 0 || empty {
				alt = true
			}
			direct.resize(w, h)
			reference.resize(w, h)
			direct.setSyncdUpdates(flags&4 != 0)
			reference.setSyncdUpdates(flags&4 != 0)
			src.Resize(w, h)
			src.Clear()
			if empty {
				src.Resize(0, 0)
			}
			literal := surfaceTransferFrame(src, &input)
			want = uv.NewScreenBuffer(w, h)
			src.Draw(want, want.Bounds())
			if empty || h == 0 {
				// Nonempty literal keeps the legacy alt-screen viewport's height
				// even for a completely blank direct frame, including width zero.
				literal = " "
			}
			v := NewViewFromSurface(src)
			v.Content = "must not render: stale legacy content"
			v.AltScreen = alt
			v.ReportFocus = flags&8 != 0
			v.DisableBracketedPasteMode = flags&16 != 0
			v.MouseMode = MouseMode((flags >> 5) % 3)
			v.WindowTitle = fmt.Sprintf("surface-%d", input.next()%4)
			v.IconName = v.WindowTitle
			wantCursor = nil
			if flags&128 != 0 && w > 0 && h > 0 {
				v.Cursor = NewCursor(int(input.next())%w, int(input.next())%h)
				v.Cursor.Shape = CursorShape(input.next() % 3)
				v.Cursor.Blink = input.next()&1 != 0
				cursor := *v.Cursor
				wantCursor = &cursor
			}
			ref := NewView(literal)
			ref.AltScreen, ref.ReportFocus = v.AltScreen, v.ReportFocus
			ref.DisableBracketedPasteMode, ref.MouseMode = v.DisableBracketedPasteMode, v.MouseMode
			ref.WindowTitle, ref.IconName, ref.Cursor = v.WindowTitle, v.IconName, wantCursor
			direct.render(v)
			reference.render(ref)
			// Reuse the exact same surface and cursor immediately after handoff.
			// Neither the pending nor last presented frame may alias these values.
			src.Clear()
			if w > 0 && h > 0 && !empty {
				src.SetCell(0, 0, &uv.Cell{Content: "!", Width: 1, Style: uv.Style{Bg: uv.RGBColor(255, 0, 255)}})
			}
			if v.Cursor != nil {
				v.Cursor.X, v.Cursor.Y = w+10, h+10
				v.Cursor.Blink = !v.Cursor.Blink
			}
			// Omitted flushes conflate complete snapshots; always consume the
			// final frame so every input checks actual writer output.
			if flags&64 != 0 && step != 7 {
				continue
			}
			flush(step)
			// A second flush cannot leak the poisoned source or stale legacy
			// content even when the direct source pointer has not changed.
			flush(step)
			if flags&32 != 0 {
				if err := direct.close(); err != nil {
					t.Fatal(err)
				}
				if err := reference.close(); err != nil {
					t.Fatal(err)
				}
				checkOutput(step, "close")
				direct.start()
				reference.start()
				flush(step)
			}
		}
	})
}

type surfaceTransferInput struct {
	data []byte
	pos  int
}

func (in *surfaceTransferInput) next() byte {
	if len(in.data) == 0 {
		return 0
	}
	b := in.data[in.pos%len(in.data)]
	in.pos++
	return b
}

// Build cells from scalar inputs and encode the reference with literal ANSI,
// rather than asking any production cell/style serializer for the expectation.
func surfaceTransferFrame(dst uv.ScreenBuffer, in *surfaceTransferInput) string {
	var literal strings.Builder
	glyphs := [...]struct {
		text  string
		width int
	}{{" ", 1}, {"A", 1}, {"z", 1}, {"e\u0301", 1}, {"界", 2}, {"👩‍💻", 2}, {"🇯🇵", 2}}
	for y := 0; y < dst.Height(); y++ {
		if y > 0 {
			literal.WriteByte('\n')
		}
		for x := 0; x < dst.Width(); {
			glyph := glyphs[int(in.next())%len(glyphs)]
			if glyph.width > dst.Width()-x {
				glyph = glyphs[0]
			}
			attrs, underline := in.next(), in.next()%6
			fg, fgANSI := surfaceTransferColor(in, false)
			bg, bgANSI := surfaceTransferColor(in, true)
			cell := uv.Cell{Content: glyph.text, Width: glyph.width, Style: uv.Style{
				Attrs: attrs, Underline: uv.Underline(underline), Fg: fg, Bg: bg,
			}}
			literal.WriteString("\x1b[0m\x1b[")
			literal.WriteString(fgANSI + ";" + bgANSI)
			for bit, code := range [...]string{"1", "2", "3", "5", "6", "7", "8", "9"} {
				if attrs&(1<<bit) != 0 {
					literal.WriteString(";" + code)
				}
			}
			if underline != 0 {
				fmt.Fprintf(&literal, ";4:%d", underline)
			}
			literal.WriteByte('m')
			if in.next()&1 != 0 {
				cell.Link = uv.Link{URL: "https://example.org/surface", Params: "id=fuzz"}
				literal.WriteString("\x1b]8;id=fuzz;https://example.org/surface\x1b\\")
			}
			dst.SetCell(x, y, &cell)
			literal.WriteString(glyph.text)
			literal.WriteString("\x1b]8;;\x1b\\\x1b[0m")
			x += glyph.width
		}
	}
	// Zero-width rows still need a nonempty legacy string to retain alt height.
	if literal.Len() == 0 {
		literal.WriteString("\x1b[0m")
	}
	return literal.String()
}

func surfaceTransferColor(in *surfaceTransferInput, background bool) (uv.Color, string) {
	kind, a, b, c := in.next()%5, in.next(), in.next(), in.next()
	base, extended := 30, 38
	if background {
		base, extended = 40, 48
	}
	switch kind {
	case 0:
		return 0, fmt.Sprint(base + 9)
	case 1:
		index := int(a % 16)
		code := base + index
		if index >= 8 {
			code = base + 60 + index - 8
		}
		return uv.BasicColor(uint8(index)), fmt.Sprint(code)
	case 2:
		return uv.IndexedColor(a), fmt.Sprintf("%d;5;%d", extended, a)
	case 3:
		return uv.RGBColor(a, b, c), fmt.Sprintf("%d;2;%d;%d;%d", extended, a, b, c)
	default:
		// RGB black is not the default, basic black, or indexed black.
		return uv.RGBColor(0, 0, 0), fmt.Sprintf("%d;2;0;0;0", extended)
	}
}
