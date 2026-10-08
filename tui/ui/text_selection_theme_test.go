package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
)

func TestTextDragSelectionReadableAcrossRuntimeThemes(t *testing.T) {
	for _, area := range []bool{false, true} {
		var control Component
		if area {
			a := NewTextArea("text", NewTokenSet(theme.DefaultDark()), true)
			a.SetValue("abcdef")
			control = a
		} else {
			f := NewTextField("text", NewTokenSet(theme.DefaultDark()))
			f.SetValue("abcdef")
			control = f
		}
		h := NewHost(theme.DefaultDark(), control)
		h.Resize(20, 2)
		h.Layout()
		h.Handle(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})
		h.Handle(tea.MouseMotionMsg{X: 4, Y: 0, Button: tea.MouseLeft})
		for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight(), theme.DefaultMidnight(), theme.DefaultWarm(), theme.DefaultHighContrast()} {
			h.SetTheme(th)
			h.Layout()
			buf := surface.NewCellBuffer(20, 2)
			h.Paint(buf)
			selected, plain := buf.Get(1, 0), buf.Get(5, 0)
			if selected.Char != 'b' || plain.Char != 'f' {
				t.Fatalf("%s area=%v: drag moved text", th.Name, area)
			}
			if ratio := cell.ContrastRatio(selected.BG, plain.BG); ratio < 3 {
				t.Fatalf("%s area=%v: selection fill barely visible (%.2f:1)", th.Name, area, ratio)
			}
			if ratio := cell.ContrastRatio(selected.FG, selected.BG); ratio < 4.5 {
				t.Fatalf("%s area=%v: selected ink unreadable (%.2f:1)", th.Name, area, ratio)
			}
		}
		h.CancelPointerCapture()
	}
}
