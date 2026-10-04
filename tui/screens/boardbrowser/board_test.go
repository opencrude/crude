package boardbrowser

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestDockLayoutAndInteraction(t *testing.T) {
	screen := New(theme.DefaultDark())
	screen.SetDockHeaderInset(9)
	host := ui.NewHost(theme.DefaultDark(), screen)
	for _, width := range []int{1, 8, 24, 100} {
		for _, height := range []int{1, 3, 9, 18} {
			t.Run(fmt.Sprintf("%dx%d", width, height), func(t *testing.T) {
				host.Resize(width, height)
				host.Layout()
				if got := screen.tabs.Bounds(); got.W != max(0, width-9) {
					t.Fatalf("tabs = %#v", got)
				}
				for _, child := range screen.Children() {
					b := child.Bounds()
					if b.X < 0 || b.Y < 0 || b.W < 0 || b.H < 0 || b.X+b.W > width || b.Y+b.H > height {
						t.Fatalf("%s outside slot: %#v", child.ID(), b)
					}
				}
				buffer := surface.NewCellBuffer(width+4, height+4)
				buffer.Fill(cell.NewCell('~', cell.NoColor, cell.NoColor, 0))
				screen.Layout(ui.LayoutContext{}, ui.Rect{X: 2, Y: 2, W: width, H: height})
				screen.Paint(buffer, 0, 0, ui.PaintContext{})
				for _, child := range screen.Children() {
					child.Paint(buffer, 2, 2, ui.PaintContext{})
				}
				for y := 0; y < height+4; y++ {
					for x := 0; x < width+4; x++ {
						if x >= 2 && x < width+2 && y >= 2 && y < height+2 {
							continue
						}
						if buffer.Get(x, y).Char != '~' {
							t.Fatalf("paint escaped at %d,%d", x, y)
						}
					}
				}
			})
		}
	}
	host.Resize(100, 18)
	host.MarkLayout()
	host.Layout()
	if filter, actions := screen.filter.Bounds(), screen.bar.Bounds(); filter.Y != ui.StripHeight || filter.Y != actions.Y || filter.X+filter.W+2 > actions.X || screen.table.Bounds().W < 90 {
		t.Fatalf("filter=%#v actions=%#v body=%#v", filter, actions, screen.table.Bounds())
	}
	buffer := surface.NewCellBuffer(100, 18)
	host.Paint(buffer)
	if !buffer.Get(99, 1).BG.Equal(ui.NewTokenSet(theme.DefaultDark()).Color(ui.ColorBg)) {
		t.Fatal("board baseline uses popup fill instead of the dock surface")
	}
	result := host.Handle(tea.MouseClickMsg{X: screen.bar.Bounds().X + 3, Y: screen.bar.Bounds().Y, Button: tea.MouseLeft})
	if !result.Handled || result.Action == nil || result.Action.ID != "board.refresh" {
		t.Fatalf("board action click did not request refresh: %#v", result)
	}
	host.Focus(screen.filter.ID())
	host.Handle(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if screen.filter.Value() != "q" {
		t.Fatal("aligned board filter did not retain text editing")
	}
	host.Focus(screen.tabs.ID())
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if host.Focused() != screen.filter.ID() {
		t.Fatal("board tab Enter did not move focus to search")
	}
	host.Resize(24, 18)
	host.Layout()
	if !screen.moreActions || screen.overflowActions.Bounds().Y != screen.filter.Bounds().Y || screen.filter.Bounds().W <= 0 {
		t.Fatalf("narrow More/filter = %#v / %#v", screen.overflowActions.Bounds(), screen.filter.Bounds())
	}
	button := screen.overflowActions.Bounds()
	host.Handle(tea.MouseClickMsg{X: button.X + 3, Y: button.Y, Button: tea.MouseLeft})
	if !screen.actionsMenu || host.Focused() != screen.narrowActions.ID() {
		t.Fatal("narrow More did not focus the accessible action list")
	}
	if len(screen.narrowActions.Items()) != len(screen.actionSpecs)+1 {
		t.Fatal("More menu did not retain all actions and Back")
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	if screen.actionsMenu || host.Focused() != screen.filter.ID() || screen.filter.Value() != "q" {
		t.Fatal("closing More did not restore board search and query")
	}
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "tabs.activate", Payload: "agents"})
	if screen.page != "agents" {
		t.Fatal("workspace agent tab did not activate")
	}
}
