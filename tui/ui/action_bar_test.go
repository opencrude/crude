package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
)

func TestActionBarRoutesOnlyFocusedAndHitButtons(t *testing.T) {
	bar := NewActionBar("actions", NewTokenSet(theme.DefaultDark()),
		ActionBarButton{ID: "primary", Label: "Primary", Action: "primary", Enabled: false},
		ActionBarButton{ID: "cancel", Label: "Cancel", Action: "cancel", Enabled: true},
	)
	host := NewHost(theme.DefaultDark(), bar)
	host.Resize(40, 1)
	host.Layout()
	host.Focus(bar.ID())
	if result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}); result.Action != nil || result.Cmd != nil {
		t.Fatalf("disabled focused button activated another button: %#v", result)
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyRight})
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result.Action == nil || result.Action.ID != "cancel" {
		t.Fatalf("focused cancel result=%#v", result)
	}

	result = host.Handle(tea.MouseClickMsg{X: 32, Y: 0, Button: tea.MouseLeft})
	if result.Action == nil || result.Action.ID != "cancel" {
		t.Fatalf("cancel click result=%#v", result)
	}
}

func TestActionBarSetSameLabelRetainsButtonBounds(t *testing.T) {
	bar := NewActionBar("actions", NewTokenSet(theme.DefaultDark()),
		ActionBarButton{Label: "Open Workspace", Action: "open", Enabled: true},
		ActionBarButton{Label: "Cancel", Action: "cancel", Enabled: true})
	bar.Layout(LayoutContext{}, Rect{X: 2, Y: 1, W: 42, H: 1})
	for frame := 0; frame < 20; frame++ {
		if !bar.SetLabel("open", "Open Workspace") {
			t.Fatalf("frame %d did not find stable action", frame)
		}
		if bounds := bar.buttons[0].button.Bounds(); bounds.W == 0 || bounds.H == 0 {
			t.Fatalf("frame %d dropped button bounds: %+v", frame, bounds)
		}
	}
	if !bar.SetLabel("open", "Attach Files") || bar.buttons[0].button.Bounds().W == 0 {
		t.Fatal("changing a button label lost its hit bounds")
	}
}

func TestActionBarAlignStartPacksFromLeft(t *testing.T) {
	bar := NewActionBar("actions", NewTokenSet(theme.DefaultDark()),
		ActionBarButton{ID: "reset", Label: "Reset section", Action: "reset", Variant: VariantDanger, Enabled: true},
	)
	bar.SetAlignEnd(false)
	bar.Layout(LayoutContext{}, Rect{W: 40, H: 1})
	if bar.buttons[0].button.Bounds().X != 0 {
		t.Fatalf("start-aligned button x=%d want 0", bar.buttons[0].button.Bounds().X)
	}
}

func TestActionBarUpReturnsToForm(t *testing.T) {
	bar := NewActionBar("actions", NewTokenSet(theme.DefaultDark()),
		ActionBarButton{ID: "save", Label: "Save", Action: "save", Enabled: true},
		ActionBarButton{ID: "cancel", Label: "Cancel", Action: "cancel", Enabled: true},
	)
	res := bar.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyUp}))
	if res.Action == nil || res.Action.ID != "actionbar.exit.backward" {
		t.Fatalf("Up on action bar result=%#v", res)
	}
	res = bar.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyDown}))
	if res.Action == nil || res.Action.ID != "actionbar.exit.forward" {
		t.Fatalf("Down on action bar result=%#v", res)
	}
}

func TestDockedActionBarPacksFromLeftWithRule(t *testing.T) {
	bar := NewDockedActionBar("actions", NewTokenSet(theme.DefaultDark()),
		ActionBarButton{ID: "save", Label: "Save", Action: "save", Variant: VariantPrimary, Enabled: true},
	)
	if bar.PreferredHeight() != 2 {
		t.Fatalf("docked action bar height=%d, want 2", bar.PreferredHeight())
	}
	bar.Layout(LayoutContext{}, Rect{W: 40, H: 2})
	if bar.buttons[0].button.Bounds().X != 0 || bar.buttons[0].button.Bounds().Y != 1 {
		t.Fatalf("docked button bounds=%v, want x=0 y=1", bar.buttons[0].button.Bounds())
	}
}

func TestActionBarRuleFullBleedSpansParentRow(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	paint := func(bar *ActionBar) *surface.CellBuffer {
		bar.Layout(LayoutContext{}, Rect{W: 40, H: 2})
		buf := surface.NewCellBuffer(100, 2)
		bar.Paint(buf, 0, 0, PaintContext{})
		return buf
	}
	bleeded := NewDockedActionBar("actions", tokens,
		ActionBarButton{ID: "save", Label: "Save", Action: "save", Enabled: true},
	)
	bleeded.SetRuleBleed(0, 100)
	buf := paint(bleeded)
	if got := buf.Get(99, 0).Char; got != '─' {
		t.Fatalf("full-bleed rule stopped before the parent row end: cell=%q", got)
	}
	if got := bleedWithBufEdge(buf); got != 100 {
		t.Fatalf("full-bleed rule width=%d, want 100", got)
	}

	// An offset bleed starts offsetX columns LEFT of the bar origin: the
	// rule runs from the parent's left edge to width-offsetX columns right
	// of the bar, i.e. edge to edge across the deck row.
	bleeded.SetRuleBleed(3, 100)
	buf = paint(bleeded)
	if got := buf.Get(0, 0).Char; got != '─' {
		t.Fatalf("offset bleed must start left of the bar origin: cell=%q", got)
	}
	if got := buf.Get(97, 0).Char; got == '─' {
		t.Fatal("offset bleed painted past the parent row end")
	}

	bounded := NewActionBar("actions", tokens,
		ActionBarButton{ID: "save", Label: "Save", Action: "save", Enabled: true},
	)
	bounded.SetShowRule(true)
	bounded.SetRuleBleed(0, 100)
	buf = paint(bounded)
	if got := buf.Get(41, 0).Char; got == '─' {
		t.Fatal("bounded rule expanded despite full-bleed being disabled")
	}
}

func bleedWithBufEdge(buf *surface.CellBuffer) int {
	width := 0
	for x := 0; x < buf.Width(); x++ {
		if buf.Get(x, 0).Char == '─' {
			width = x + 1
		}
	}
	return width
}
