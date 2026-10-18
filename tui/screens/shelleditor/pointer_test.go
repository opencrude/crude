package shelleditor_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
	"crude/tui/screens/shelleditor"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestMountedEditorBackwardDragIncludesPressedCharacterAndRetainsPaint(t *testing.T) {
	for _, size := range []struct {
		name string
		w, h int
	}{{"desktop", 80, 20}, {"constrained", 24, 6}} {
		t.Run(size.name, func(t *testing.T) {
			th := theme.DefaultDark()
			path := filepath.Join(t.TempDir(), "anchor.txt")
			if err := os.WriteFile(path, []byte("abcdef"), 0o600); err != nil {
				t.Fatal(err)
			}
			ed := editor.New(th)
			if err := ed.Open(path); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(ed.Close)
			screen := shelleditor.New(th)
			screen.Bind(ed)
			screen.SetPadding(2, 2)
			screen.SetFocused(true)
			host := ui.NewHost(th, screen)
			host.Resize(size.w, size.h)
			buf := surface.NewCellBuffer(size.w, size.h)
			host.Paint(buf)
			textX := -1
			for x := 0; x < buf.Width(); x++ {
				if buf.Get(x, 0).Char == 'a' {
					textX = x
					break
				}
			}
			if textX < 0 {
				t.Fatal("mounted source text was not painted")
			}
			host.Handle(tea.MouseClickMsg{X: textX + 3, Y: 0, Button: tea.MouseLeft})
			if !host.HasPointerCapture() || ed.HasSelection() {
				t.Fatal("plain press must capture with a collapsed insertion caret")
			}
			host.Handle(tea.MouseMotionMsg{X: textX + 2, Y: 0, Button: tea.MouseLeft})
			host.Paint(buf)
			if got := ed.SelectedText(); got != "cd" {
				t.Errorf("held mounted selection = %q, want cd", got)
			}
			selectionBG := ui.NewTokenSet(th).Color(ui.ColorTextSelection)
			if buf.Get(textX+3, 0).BG != selectionBG || buf.Get(textX+4, 0).BG == selectionBG {
				t.Error("held drag must paint pressed d selected and following e unselected")
			}
			// Release supplies a new endpoint even without an intervening motion.
			res := host.Handle(tea.MouseReleaseMsg{X: textX + 1, Y: 0, Button: tea.MouseLeft})
			host.Paint(buf)
			if res.Cmd != nil || host.HasPointerCapture() || ed.IsMouseDragging() || ed.SelectedText() != "bcd" {
				t.Fatalf("release lost retained editable range/capture: selected=%q, captured=%v", ed.SelectedText(), host.HasPointerCapture())
			}
			for col := 0; col < 6; col++ {
				if got, want := buf.Get(textX+col, 0).BG == selectionBG, col >= 1 && col < 4; got != want {
					t.Errorf("mounted cell %d selection fill=%v, want %v", col, got, want)
				}
			}
			// Product keyboard routing calls the same Editor owner after release.
			ed.HandleEvent(tea.KeyPressMsg{Code: 'X', Text: "X"})
			host.Paint(buf)
			if got := ed.Value(); got != "aXef" || ed.HasSelection() {
				t.Fatalf("typing must replace exactly the retained source range, got %q", got)
			}
			if !strings.Contains(shelleditor.PlainRow(buf, 0, size.w), "aXef") {
				t.Fatal("replacement bytes did not reach mounted paint")
			}
		})
	}
}

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
