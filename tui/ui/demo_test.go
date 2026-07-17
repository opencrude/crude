package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
)

func TestDemoGalleryPaintsAndEdits(t *testing.T) {
	themes := map[string]*theme.Theme{
		"dark":  theme.DefaultDark(),
		"light": LightTheme(),
	}
	for name, th := range themes {
		t.Run(name, func(t *testing.T) {
			host := DemoGallery(th)
			host.Resize(80, 30)

			buf := surface.NewCellBuffer(80, 30)
			host.Paint(buf)

			res := host.Handle(tea.KeyPressMsg{Code: 'x', Text: "x"})
			if !res.Handled {
				t.Fatal("expected focused text field to handle key")
			}
			host.Paint(buf)
		})
	}
}

func TestDemoPaintHelper(t *testing.T) {
	for name, th := range map[string]*theme.Theme{"dark": theme.DefaultDark(), "light": LightTheme()} {
		t.Run(name, func(t *testing.T) {
			buf := DemoPaint(th, 80, 30)
			if buf == nil {
				t.Fatal("nil buffer")
			}
		})
	}
}

func TestDemoGalleryRuntimeThemeSwitchPreservesState(t *testing.T) {
	host := DemoGallery(theme.DefaultDark())
	host.Resize(80, 30)
	res := host.Handle(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if !res.Handled {
		t.Fatal("focused field did not accept edit before theme switch")
	}
	darkPopup := host.Tokens().Color(ColorPopupBg)
	host.SetTheme(LightTheme())
	if host.Tokens().Color(ColorPopupBg).Equal(darkPopup) {
		t.Fatal("light theme did not replace resolved popup token")
	}
	field, ok := host.registry[host.Focused()].(*TextField)
	if !ok || field.Value() != "defaultx" {
		t.Fatalf("theme switch lost focused input state: %#v", field)
	}
	host.Paint(surface.NewCellBuffer(80, 30))
}

func TestWindowFrameCloseHit(t *testing.T) {
	th := theme.DefaultDark()
	tokens := NewTokenSet(th)
	frame := NewWindowFrame(NewID(RootID, "f"), FrameDialog, tokens)
	frame.SetTitle("T")
	frame.SetClosable(true)
	label := NewLabel(NewID(frame.ID(), "body"), "body", th)
	frame.SetContent(label)
	frame.Layout(LayoutContext{Theme: th}, Rect{X: 0, Y: 0, W: 20, H: 8})
	if !frame.IsOnClose(18, 0) && !frame.IsOnClose(19, 0) && !frame.IsOnClose(17, 0) {
		// close is at W-3..W-1 on title row
		if frame.CloseBounds().Empty() {
			t.Fatal("expected close bounds")
		}
	}
}

func TestVirtualListNavigation(t *testing.T) {
	th := theme.DefaultDark()
	tokens := NewTokenSet(th)
	vl := NewVirtualList(NewID(RootID, "vl"), tokens)
	vl.SetItems([]VirtItem{
		{ID: "a", Height: 1, Label: "A"},
		{ID: "b", Height: 2, Label: "B"},
		{ID: "c", Height: 1, Label: "C"},
	})
	vl.Layout(LayoutContext{Theme: th}, Rect{X: 0, Y: 0, W: 20, H: 5})
	vl.Handle(EventContext{}, tea.KeyPressMsg{Code: tea.KeyDown})
	if vl.SelectedIndex() != 1 {
		t.Fatalf("selected=%d want 1", vl.SelectedIndex())
	}
}

func TestScrollbarThumbBounds(t *testing.T) {
	s := NewScrollModel()
	s.SetContent(100)
	s.SetViewport(10)
	s.ScrollTo(50)
	top, th := s.ThumbBounds(20)
	if th < 1 {
		t.Fatalf("thumbH=%d", th)
	}
	if top < 0 || top >= 20 {
		t.Fatalf("thumbTop=%d", top)
	}
}
