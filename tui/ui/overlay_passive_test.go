package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/theme"
)

func TestPassiveOverlayKeepsShellFocusAndUsesNormalPointerRouting(t *testing.T) {
	h, manager := newTestOverlayHost(t)
	th := theme.DefaultDark()
	input := NewTextField("draft", NewTokenSet(th))
	root := NewHost(th, input)
	root.Resize(80, 24)
	root.Layout()
	root.Focus(input.ID())
	h.SetFocusHost(root)
	policy := PopoverPolicy(true)
	policy.Passive = true
	fw := newStubWindow(string(OverlayPlanHover), 25, 8)
	fw.SetPosition(5, 5)
	h.Open(OverlayPlanHover, fw, policy, nil)
	if manager.Focused() != nil || root.Focused() != input.ID() || len(root.FocusScopes()) != 0 {
		t.Fatal("hover opening stole keyboard focus or saved a stale focus scope")
	}
	if manager.HandleMouseWheel(tea.MouseWheelMsg{X: 8, Y: 7, Button: tea.MouseWheelDown}) != fw {
		t.Fatal("passive surface cannot receive wheel input")
	}
	if manager.HandleMouse(tea.MouseClickMsg{X: 8, Y: 7, Button: tea.MouseLeft}) != fw || manager.Focused() != fw {
		t.Fatal("explicit click did not focus the reading surface")
	}
	h.Close(OverlayPlanHover)
	if manager.Focused() != nil || root.Focused() != input.ID() {
		t.Fatal("closing passive surface did not restore shell ownership")
	}
}
