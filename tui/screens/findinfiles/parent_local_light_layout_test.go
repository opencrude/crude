package findinfiles

import (
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestFindInFilesPaintUsesParentLocalOriginInLightTheme(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode Mode
	}{
		{name: "find", mode: ModeFind},
		{name: "replace", mode: ModeReplace},
	} {
		t.Run(tc.name, func(t *testing.T) {
			th := theme.DefaultLight()
			screen := New(th)
			screen.mode = tc.mode
			bounds := ui.Rect{X: 6, Y: 4, W: 5, H: 4}
			screen.Layout(ui.LayoutContext{}, bounds)

			sentinel := cell.NewTrueColor(1, 2, 3)
			buf := surface.NewCellBuffer(24, 18)
			buf.Fill(cell.BlankCell(sentinel))
			const originX, originY = 2, 1
			screen.Paint(buf, originX, originY, ui.PaintContext{Theme: th})

			tokens := ui.NewTokenSet(th)
			wantBar := tokens.Color(ui.ColorPopupSubtleBg)
			wantBody := tokens.Color(ui.ColorPopupBg)
			x0, y0 := originX+bounds.X, originY+bounds.Y
			if got := buf.Get(x0, y0).BG; !got.Equal(wantBar) {
				t.Fatalf("toolbar BG=%+v, want light-theme subtle BG %+v", got, wantBar)
			}
			toolbarH := 1
			if tc.mode == ModeReplace {
				toolbarH = 2
			}
			if got := buf.Get(x0+1, y0+toolbarH).BG; !got.Equal(wantBody) {
				t.Fatalf("body BG=%+v, want light-theme popup BG %+v", got, wantBody)
			}
			if got := buf.Get(x0-1, y0+toolbarH).BG; !got.Equal(sentinel) {
				t.Fatalf("paint escaped left parent-local bound: BG=%+v", got)
			}

			// The former bug translated the origin and then passed bounds that
			// translated it a second time, filling this disjoint rectangle.
			doubleX := originX + 2*bounds.X
			doubleY := originY + 2*bounds.Y + 1
			if got := buf.Get(doubleX, doubleY).BG; !got.Equal(sentinel) {
				t.Fatalf("double-origin leak at (%d,%d): BG=%+v", doubleX, doubleY, got)
			}
		})
	}
}
