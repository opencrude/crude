//go:build cgo

package shellinput

import (
	"bytes"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"crude/internal/terminalvt"
	"crude/tui/compositor"
	"crude/tui/input"
	"crude/tui/theme"
	"crude/tui/ui"
)

// Check what a terminal displays, not the renderer’s own previous-frame cache.
func TestComposerIncrementalWideText(t *testing.T) {
	for _, term := range []string{"", "xterm-256color"} {
		t.Run("TERM="+term, func(t *testing.T) {
			const w, h = 100, 4
			c := compositor.NewCompositor(w, h)
			th := theme.DefaultDark()
			m := input.NewModel(th)
			s := New(th)
			s.Bind(&m)
			s.Layout(ui.LayoutContext{}, ui.Rect{W: w, H: 2})
			vt, err := terminalvt.New(terminalvt.Options{Cols: w, Rows: h})
			if err != nil {
				t.Fatal(err)
			}
			defer vt.Close()
			vt.Feed([]byte(ansi.SetModeUnicodeCore)) // same mode negotiated by Bubble Tea
			var out bytes.Buffer
			renderer := uv.NewTerminalRenderer(&out, []string{"TERM=" + term})
			renderer.SetColorProfile(colorprofile.TrueColor)
			renderer.SetFullscreen(true)
			renderer.SetTabStops(w)
			renderer.SetBackspace(true)
			grid := uv.NewScreenBuffer(w, h)
			check := func(want string, caret bool) {
				t.Helper()
				c.BeginFrame()
				s.Paint(c.Root().Buffer(), 0, 0, ui.PaintContext{Focused: true})
				c.Composite()
				c.PresentSurface().Draw(grid, grid.Bounds())
				renderer.Render(grid.RenderBuffer)
				if err := renderer.Flush(); err != nil {
					t.Fatal(err)
				}
				vt.Feed(out.Bytes())
				view, err := vt.Rows(terminalvt.RowRequest{Start: -1})
				if err != nil {
					t.Fatal(err)
				}
				var line strings.Builder
				var carets int
				for _, cell := range view.Cells[1] {
					if cell.Style.Inverse {
						if strings.TrimLeft(line.String(), " ") != want || cell.Text != " " {
							t.Fatalf("displaced caret after %q on %q; want it after %q", line.String(), cell.Text, want)
						}
						carets++
					}
					line.WriteString(cell.Text)
				}
				if m.Value() != want || strings.TrimSpace(line.String()) != want || carets > 1 || (carets == 1) != caret {
					t.Fatalf("draft %q, terminal row %q, reversed cells %d, want %q (caret=%v); frame %q", m.Value(), line.String(), carets, want, caret, out.String())
				}
				out.Reset()
			}
			want := "prefix"
			m.SetInput(want)
			check(want, true)
			for _, key := range []rune("😂😂b") {
				m, _, _ = m.Update(tea.KeyPressMsg{Code: key, Text: string(key)}, false, false)
				want += string(key)
				check(want, true)
			}
			const paste = "e\u0301☀️👩‍💻!"
			m, _, _ = m.Update(tea.PasteMsg{Content: paste}, false, false)
			want += paste
			check(want, true)
			m.SetSpinFrame(2800)
			check(want, false)
			m.SetSpinFrame(3200)
			check(want, true)
		})
	}
}

// Distinguish a pure insertion/deletion (which may shift the whole suffix)
// from changing its first glyph too. Exercise the actual first terminal column
// and both the ICH and IRM capability profiles.
func TestIncrementalWideTextBeforeSuffix(t *testing.T) {
	const w, h = 60, 3
	const suffix = "👩‍💻 shared wide and ASCII suffix"
	for _, term := range []string{"", "xterm-256color"} {
		t.Run("TERM="+term, func(t *testing.T) {
			vt, err := terminalvt.New(terminalvt.Options{Cols: w, Rows: h})
			if err != nil {
				t.Fatal(err)
			}
			defer vt.Close()
			vt.Feed([]byte(ansi.SetModeUnicodeCore))
			var out bytes.Buffer
			r := uv.NewTerminalRenderer(&out, []string{"TERM=" + term})
			r.SetFullscreen(true)
			grid := uv.NewScreenBuffer(w, h)
			grid.Method = ansi.GraphemeWidth
			for _, prefix := range []string{"", "😂", "😂😂", "a😂", "a", "😂", "", "ab", "a👩‍💻c", "a☀️", "a"} {
				want := prefix + suffix
				uv.NewStyledString(want).Draw(grid, grid.Bounds())
				r.Render(grid.RenderBuffer)
				if err := r.Flush(); err != nil {
					t.Fatal(err)
				}
				vt.Feed(out.Bytes())
				view, err := vt.Rows(terminalvt.RowRequest{Start: -1})
				if err != nil {
					t.Fatal(err)
				}
				var line strings.Builder
				for _, c := range view.Cells[0] {
					line.WriteString(c.Text)
				}
				if got := strings.TrimRight(line.String(), " "); got != want {
					t.Fatalf("row %q; want %q; frame %q", got, want, out.String())
				}
				out.Reset()
			}
		})
	}
}
