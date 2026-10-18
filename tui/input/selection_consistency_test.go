package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestComposerBridgeAndMountedSelectionAgree(t *testing.T) {
	for _, mounted := range []bool{false, true} {
		t.Run(map[bool]string{false: "bridge", true: "mounted"}[mounted], func(t *testing.T) {
			m := NewModel(theme.DefaultDark())
			m.SetWidth(60)
			m.SetInput("abcdef")
			m.LayoutShell(60, 5)
			pad, _ := m.inputGeometry(60)
			y := m.textAreaY()
			pointer := func(msg tea.MouseMsg) {
				if mounted {
					p := msg.Mouse()
					m.HandleShell(ui.EventContext{LocalX: p.X, LocalY: p.Y}, ui.NormalizeEvent(msg))
				} else {
					m, _, _ = m.Update(msg, false, false)
				}
			}
			pointer(tea.MouseClickMsg{X: pad + 3, Y: y, Button: tea.MouseLeft})
			pointer(tea.MouseMotionMsg{X: pad + 1, Y: y, Button: tea.MouseLeft})
			pointer(tea.MouseReleaseMsg{X: pad + 1, Y: y, Button: tea.MouseLeft})
			if m.SelectedText() != "bcd" || m.InputDragging() {
				t.Fatalf("drag %q capture=%v", m.SelectedText(), m.InputDragging())
			}
			m.SetInput("abcdef")
			m.SetCursorFlat(3)
			m, _, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModShift}, false, false)
			if m.SelectedText() != "c" || m.InputDragging() {
				t.Fatalf("keyboard %q capture=%v", m.SelectedText(), m.InputDragging())
			}
			// Unpressed motion must never mutate a keyboard selection.
			pointer(tea.MouseMotionMsg{X: pad + 5, Y: y})
			if m.SelectedText() != "c" {
				t.Fatal("hover extended keyboard selection")
			}
			m.SetInput("abcdef")
			pointer(tea.MouseClickMsg{X: pad + 3, Y: y, Button: tea.MouseLeft})
			pointer(tea.MouseMotionMsg{X: pad, Y: -5, Button: tea.MouseLeft})
			pointer(tea.MouseReleaseMsg{X: pad, Y: -5, Button: tea.MouseLeft})
			if m.InputDragging() {
				t.Fatal("outside release retained composer capture")
			}
		})
	}
}
