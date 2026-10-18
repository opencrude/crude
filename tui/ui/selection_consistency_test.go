package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/surface"
	"crude/tui/theme"
)

func TestTextControlsKeyboardSelectionIsCaretBoundaryBased(t *testing.T) {
	for _, kind := range []string{"field", "area", "area-bridge"} {
		t.Run(kind, func(t *testing.T) {
			f := NewTextField("field", NewTokenSet(theme.DefaultDark()))
			a := NewTextArea("area", NewTokenSet(theme.DefaultDark()), true)
			f.SetValue("abcdef")
			f.Edit().SetCursor(3, false)
			a.SetValue("abcdef")
			a.SetCursorFlat(3)
			key := func(code rune, shift bool) {
				mod := tea.KeyMod(0)
				if shift {
					mod = tea.ModShift
				}
				msg := tea.KeyPressMsg{Code: code, Mod: mod}
				switch kind {
				case "field":
					f.Handle(EventContext{}, KeyEvent(msg))
				case "area":
					a.Handle(EventContext{}, KeyEvent(msg))
				default:
					a.Update(msg, false)
				}
			}
			selected := func() string {
				if kind == "field" {
					return f.SelectedText()
				}
				return a.SelectedText()
			}
			cursor := func() int {
				if kind == "field" {
					return f.Edit().Cursor()
				}
				return a.CursorFlat()
			}
			for _, step := range []struct {
				code rune
				want string
			}{{tea.KeyLeft, "c"}, {tea.KeyLeft, "bc"}, {tea.KeyRight, "c"}, {tea.KeyRight, ""}, {tea.KeyRight, "d"}} {
				key(step.code, true)
				if selected() != step.want {
					t.Fatalf("key %v selected %q want %q", step.code, selected(), step.want)
				}
			}
			key(tea.KeyLeft, false)
			if selected() != "" || cursor() != 3 {
				t.Fatalf("plain Left should collapse to 3, selection=%q cursor=%d", selected(), cursor())
			}
			key(tea.KeyLeft, true)
			key(tea.KeyLeft, true)
			key(tea.KeyRight, false)
			if selected() != "" || cursor() != 3 {
				t.Fatalf("plain Right should collapse to 3, selection=%q cursor=%d", selected(), cursor())
			}
		})
	}
}

func TestTextControlsWholeGraphemeSelectionAndDeletion(t *testing.T) {
	for _, cluster := range []string{"界", "e\u0301", "👩🏽‍💻", "🇺🇳"} {
		for _, kind := range []string{"field", "area"} {
			t.Run(kind+cluster, func(t *testing.T) {
				f := NewTextField("field", nil)
				a := NewTextArea("area", nil, true)
				value := "A" + cluster + "Z"
				f.SetValue(value)
				a.SetValue(value)
				f.Edit().SetCursor(1, false)
				a.SetCursorFlat(1)
				key := func(code rune, mod tea.KeyMod) {
					msg := tea.KeyPressMsg{Code: code, Mod: mod}
					if kind == "field" {
						f.Handle(EventContext{}, KeyEvent(msg))
					} else {
						a.Handle(EventContext{}, KeyEvent(msg))
					}
				}
				key(tea.KeyRight, tea.ModShift)
				selected := a.SelectedText()
				if kind == "field" {
					selected = f.SelectedText()
				}
				if selected != cluster {
					t.Fatalf("selected %q want whole %q", selected, cluster)
				}
				key(tea.KeyDelete, 0)
				got := a.Value()
				if kind == "field" {
					got = f.Value()
				}
				if got != "AZ" {
					t.Fatalf("selection delete=%q", got)
				}
				// Unselected deletion is equally atomic, and undo restores exact
				// content and boundaries rather than resurrecting a pointer gesture.
				f.SetValue(value)
				a.SetValue(value)
				f.Edit().SetCursor(1+len([]rune(cluster)), false)
				a.SetCursorFlat(1 + len([]rune(cluster)))
				key(tea.KeyBackspace, 0)
				got = a.Value()
				if kind == "field" {
					got = f.Value()
				}
				if got != "AZ" {
					t.Fatalf("plain backspace split cluster: %q", got)
				}
				key('z', tea.ModCtrl)
				got = a.Value()
				if kind == "field" {
					got = f.Value()
				}
				if got != value {
					t.Fatalf("undo lost cluster: %q", got)
				}
				key(tea.KeyLeft, tea.ModShift)
				selected = a.SelectedText()
				if kind == "field" {
					selected = f.SelectedText()
				}
				if selected != cluster {
					t.Fatalf("restored boundary selected %q", selected)
				}
			})
		}
	}
}

func TestTextControlsBackwardPointerIncludesPressedUnit(t *testing.T) {
	for _, kind := range []string{"field", "area"} {
		t.Run(kind, func(t *testing.T) {
			f := NewTextField("field", NewTokenSet(theme.DefaultDark()))
			a := NewTextArea("area", NewTokenSet(theme.DefaultDark()), true)
			var component Component = f
			if kind == "area" {
				component = a
			}
			f.SetValue("abcdef")
			a.SetValue("abcdef")
			host := NewHost(theme.DefaultDark(), component)
			host.Resize(30, 4)
			host.Layout()
			host.Handle(tea.MouseClickMsg{X: 3, Y: 0, Button: tea.MouseLeft})
			for _, step := range []struct {
				x    int
				want string
			}{{1, "bcd"}, {5, "de"}, {3, ""}, {1, "bcd"}} {
				host.Handle(tea.MouseMotionMsg{X: step.x, Y: 0, Button: tea.MouseLeft})
				got := a.SelectedText()
				if kind == "field" {
					got = f.SelectedText()
				}
				if got != step.want {
					t.Fatalf("drag to %d selected %q want %q", step.x, got, step.want)
				}
			}
			host.Handle(tea.MouseReleaseMsg{X: 1, Y: 0, Button: tea.MouseLeft})
			b := surface.NewCellBuffer(30, 4)
			host.Paint(b)
			selection := NewTokenSet(theme.DefaultDark()).Color(ColorTextSelection)
			for x := 1; x <= 3; x++ {
				if b.Get(x, 0).BG != selection {
					t.Fatalf("pressed/dragged cell %d not selected", x)
				}
			}
		})
	}
}

func TestTextControlsShiftClickAndPointerLifetime(t *testing.T) {
	for _, kind := range []string{"field", "area"} {
		t.Run(kind, func(t *testing.T) {
			f := NewTextField("field", nil)
			a := NewTextArea("area", nil, true)
			var component Component = f
			if kind == "area" {
				component = a
			}
			f.SetValue("abcdef")
			a.SetValue("abcdef")
			host := NewHost(theme.DefaultDark(), component)
			host.Resize(30, 3)
			host.Layout()
			selected := func() string {
				if kind == "field" {
					return f.SelectedText()
				}
				return a.SelectedText()
			}
			host.Handle(tea.MouseClickMsg{X: 3, Y: 0, Button: tea.MouseLeft})
			host.Handle(tea.MouseReleaseMsg{X: 3, Y: 0, Button: tea.MouseLeft})
			host.Handle(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModShift})
			host.Handle(tea.MouseClickMsg{X: 1, Y: 0, Button: tea.MouseLeft, Mod: tea.ModShift})
			host.Handle(tea.MouseReleaseMsg{X: 1, Y: 0, Button: tea.MouseLeft})
			if got := selected(); got != "bc" {
				t.Fatalf("Shift-click changed original anchor: %q", got)
			}
			host.Handle(tea.MouseClickMsg{X: 5, Y: 0, Button: tea.MouseLeft, Mod: tea.ModShift})
			host.Handle(tea.MouseReleaseMsg{X: 5, Y: 0, Button: tea.MouseLeft})
			if got := selected(); got != "de" {
				t.Fatalf("Shift-click reversal=%q", got)
			}
			host.Handle(tea.MouseMotionMsg{X: 0, Y: 0})
			if selected() != "de" {
				t.Fatal("unpressed hover changed retained range")
			}
			// A release contains an authoritative final position, even when transport
			// omitted all intermediate motion. A foreign button cannot finish capture.
			host.Handle(tea.KeyPressMsg{Code: tea.KeyHome})
			host.Handle(tea.MouseClickMsg{X: 3, Y: 0, Button: tea.MouseLeft})
			host.Handle(tea.MouseReleaseMsg{X: 1, Y: 0, Button: tea.MouseRight})
			if !host.HasPointerCapture() {
				t.Fatal("foreign release ended capture")
			}
			host.Handle(tea.MouseReleaseMsg{X: 1, Y: 0, Button: tea.MouseLeft})
			if selected() != "bcd" {
				t.Fatalf("final release ignored: %q", selected())
			}
		})
	}
}

func TestTextControlsDoubleClickDragKeepsWholeGraphemeWords(t *testing.T) {
	for _, kind := range []string{"field", "area"} {
		t.Run(kind, func(t *testing.T) {
			f := NewTextField("field", nil)
			a := NewTextArea("area", nil, true)
			var component Component = f
			if kind == "area" {
				component = a
			}
			f.SetValue("café beta gamma")
			a.SetValue("café beta gamma")
			host := NewHost(theme.DefaultDark(), component)
			host.Resize(40, 3)
			host.Layout()
			selected := func() string {
				if kind == "field" {
					return f.SelectedText()
				}
				return a.SelectedText()
			}
			for range 2 {
				host.Handle(tea.MouseClickMsg{X: 6, Y: 0, Button: tea.MouseLeft})
				host.Handle(tea.MouseReleaseMsg{X: 6, Y: 0, Button: tea.MouseLeft})
			}
			if selected() != "beta" {
				t.Fatalf("word click=%q", selected())
			}
			// Continue the second press as a held word gesture.
			host.Handle(tea.KeyPressMsg{Code: tea.KeyHome}) // end that click sequence
			for range 2 {
				host.Handle(tea.MouseClickMsg{X: 6, Y: 0, Button: tea.MouseLeft})
			}
			host.Handle(tea.MouseMotionMsg{X: 1, Y: 0, Button: tea.MouseLeft})
			if selected() != "café beta" {
				t.Fatalf("word-left drag=%q", selected())
			}
			host.Handle(tea.MouseMotionMsg{X: 12, Y: 0, Button: tea.MouseLeft})
			if selected() != "beta gamma" {
				t.Fatalf("word-reversal=%q", selected())
			}
		})
	}
}

func TestTextAreaKeyboardHoverAndAtomicPointerStaySeparate(t *testing.T) {
	a := NewTextArea("area", nil, true)
	a.SetValue("a @ref z")
	a.SetAtomicRanges([]AtomicRange{{Start: 2, End: 6}})
	h := NewHost(theme.DefaultDark(), a)
	h.Resize(30, 4)
	h.Layout()
	h.Handle(tea.MouseClickMsg{X: 4, Y: 0, Button: tea.MouseLeft})
	h.Handle(tea.MouseMotionMsg{X: 0, Y: 0, Button: tea.MouseLeft})
	h.Handle(tea.MouseReleaseMsg{X: 0, Y: 0, Button: tea.MouseLeft})
	if a.SelectedText() != "a @ref" {
		t.Fatalf("atomic press fragmented: %q", a.SelectedText())
	}
	a.SetValue("ab\ncd")
	a.SetCursorFlat(3)
	h.Handle(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModShift})
	if a.SelectedText() != "\n" || a.IsSelecting() {
		t.Fatalf("newline selection %q capture=%v", a.SelectedText(), a.IsSelecting())
	}
	h.Handle(tea.MouseReleaseMsg{X: 0, Y: 0, Button: tea.MouseLeft})
	if a.SelectedText() != "\n" {
		t.Fatal("unowned release changed keyboard range")
	}
}
