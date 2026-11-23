package panelchrome

import (
	"fmt"
	"testing"

	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

// The image and Markdown toolbars show "[ icon ]" with the icon in the middle.
// A Nerd Font icon keeps one blank cell after it for its wide ink, so the ink
// has one blank cell on each side; a short row drops that cell, not the icon.
func TestViewerButtonsAreBracketedWithTheIconInTheMiddle(t *testing.T) {
	for _, tc := range []struct {
		nerd  bool
		width int
		want  string
	}{
		{false, 40, "[ + ]"},
		{true, 40, "[ \uf00e  ]"},
		{true, 15, "[ \uf00e ]"},
	} {
		t.Run(fmt.Sprintf("nerd=%t/width=%d", tc.nerd, tc.width), func(t *testing.T) {
			th := theme.DefaultDark()
			th.Tree.Config.UseNerdFont = &tc.nerd
			tokens := ui.NewTokenSet(th)
			buttons := make([]ui.Component, 3)
			for i := range buttons {
				b := NewViewerButton(ui.ComponentID(fmt.Sprint("b", i)), "pane", "act", "Zoom in", "+", "\uf00e", "+")
				b.SetTokens(tokens)
				buttons[i] = b
			}
			LayoutViewerActions(ui.LayoutContext{}, ui.Rect{W: tc.width, H: 1}, buttons)
			buf := surface.NewCellBuffer(tc.width, 1)
			for _, b := range buttons {
				b.Paint(buf, 0, 0, ui.PaintContext{})
			}
			end, gap := 0, 1
			if tc.width < 3*rendering.VisibleWidth(tc.want)+2 {
				gap = 0 // a short row drops the gaps first
			}
			for i, b := range buttons {
				r := b.Bounds()
				if i > 0 && r.X != end+gap {
					t.Fatalf("button %d starts at %d, want %d after a %d-cell gap", i, r.X, end+gap, gap)
				}
				if got := text(buf, r); got != tc.want {
					t.Fatalf("button %d painted %q, want %q", i, got, tc.want)
				}
				if buf.Get(r.X, 0).BG != tokens.Color(ui.ColorBg) {
					t.Fatalf("idle button %d is a raised chip, not the pane surface", i)
				}
				end = r.X + r.W
			}
			if end != tc.width {
				t.Fatalf("toolbar ends at %d, want the right edge %d", end, tc.width)
			}
			// The selected Markdown view stays marked after the pointer leaves.
			selected := buttons[0].(*ViewerButton)
			selected.SetSelected(true)
			selected.Paint(buf, 0, 0, ui.PaintContext{})
			r := selected.Bounds()
			if buf.Get(r.X, 0).BG != tokens.Color(ui.ColorSelection) || buf.Get(r.X+r.W-1, 0).BG != tokens.Color(ui.ColorSelection) || !selected.Semantics().Selected {
				t.Fatal("selected button is not marked across its brackets")
			}
		})
	}
}

func text(buf *surface.CellBuffer, r ui.Rect) string {
	out := ""
	for x := r.X; x < r.X+r.W; x++ {
		c := buf.Get(x, r.Y)
		if c.Content != "" {
			out += c.Content
		} else {
			out += string(c.Char)
		}
	}
	return out
}
