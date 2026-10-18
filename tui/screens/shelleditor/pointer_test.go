package shelleditor_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
	"crude/tui/screens/shelleditor"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestMountedEditorPointerScrollUsesCaptureAndPaintsScrolledRows(t *testing.T) {
	th := theme.DefaultDark()
	ed := editor.NewPreview(th, "pointer.txt", strings.Repeat("alpha beta\n", 80), 1)
	t.Cleanup(ed.Close)
	screen := shelleditor.New(th)
	screen.Bind(ed)
	screen.SetPadding(2, 2)
	host := ui.NewHost(th, screen)
	host.Resize(40, 8)
	buf := surface.NewCellBuffer(40, 8)
	host.Paint(buf)
	host.Handle(tea.MouseClickMsg{X: 16, Y: 2, Button: tea.MouseLeft})
	res := host.Handle(tea.MouseMotionMsg{X: 16, Y: 12, Button: tea.MouseLeft})
	if res.Cmd == nil || !host.HasPointerCapture() {
		t.Fatal("mounted edge drag lost capture/timer")
	}
	msg := res.Cmd()
	for i := 0; i < 2; i++ {
		res = host.Deliver(screen.ID(), msg)
		if res.Cmd == nil {
			t.Fatal("stationary mounted drag did not repeat")
		}
		host.Paint(buf)
		msg = res.Cmd()
	}
	if ed.ScrollY() != 6 || !strings.Contains(shelleditor.PlainRow(buf, 0, 40), "7") {
		t.Fatalf("timer did not paint the scrolled viewport: offset=%d top=%q", ed.ScrollY(), shelleditor.PlainRow(buf, 0, 40))
	}
	host.CancelPointerCapture()
	host.Deliver(screen.ID(), msg)
	if ed.ScrollY() != 6 || ed.IsMouseDragging() {
		t.Fatal("cancelled mounted editor kept scrolling")
	}
}

func TestMountedEditorRebindRejectsOldGestureTick(t *testing.T) {
	th := theme.DefaultDark()
	a := editor.NewPreview(th, "a.txt", strings.Repeat("alpha\n", 80), 1)
	b := editor.NewPreview(th, "b.txt", strings.Repeat("bravo\n", 80), 1)
	t.Cleanup(a.Close)
	t.Cleanup(b.Close)
	screen := shelleditor.New(th)
	screen.Bind(a)
	host := ui.NewHost(th, screen)
	host.Resize(40, 8)
	host.Paint(surface.NewCellBuffer(40, 8))
	host.Handle(tea.MouseClickMsg{X: 16, Y: 2, Button: tea.MouseLeft})
	res := host.Handle(tea.MouseMotionMsg{X: 16, Y: 12, Button: tea.MouseLeft})
	msg := res.Cmd()
	screen.Bind(b)
	host.Deliver(screen.ID(), msg)
	if a.IsMouseDragging() || b.IsMouseDragging() || a.ScrollY() != 0 || b.ScrollY() != 0 {
		t.Fatal("rebind left old gesture live or delivered it to replacement editor")
	}
}
