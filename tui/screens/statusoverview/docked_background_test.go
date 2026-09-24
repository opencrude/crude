package statusoverview

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestDockedRowsNeverReintroduceTextBackgrounds(t *testing.T) {
	for _, th := range theme.AllPresets() {
		t.Run(th.Name, func(t *testing.T) {
			screen := NewDocked(th, testProjection())
			host := ui.NewHost(th, screen)
			host.Resize(80, 24)
			host.Layout()
			screen.Select("agent:one")
			for _, focused := range []bool{false, true} {
				if focused {
					host.Focus(ListID)
				} else {
					host.Focus(SearchID)
				}
				host.MarkLayout()
				host.Layout()
				host.Handle(tea.MouseMotionMsg{X: 8, Y: screen.list.Bounds().Y + 1})
				buf := surface.NewCellBuffer(80, 24)
				host.Paint(buf)
				ground := ui.NewTokenSet(th).Color(ui.ColorBg)
				for y := screen.list.Bounds().Y; y < 24; y++ {
					for x := 0; x < 80; x++ {
						if !buf.Get(x, y).BG.Equal(ground) {
							t.Fatalf("focused=%v cell %d,%d has a text/selection slab: %+v", focused, x, y, buf.Get(x, y))
						}
					}
				}
			}
		})
	}
}
