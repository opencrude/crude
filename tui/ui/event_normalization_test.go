package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
)

func TestNormalizeEventKinds(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.Msg
		want EventKind
	}{
		{"key", tea.KeyPressMsg{Code: tea.KeyEnter}, EventKey},
		{"click", tea.MouseClickMsg{Button: tea.MouseLeft}, EventMouseClick},
		{"motion", tea.MouseMotionMsg{}, EventMouseMotion},
		{"release", tea.MouseReleaseMsg{}, EventMouseRelease},
		{"wheel", tea.MouseWheelMsg{Button: tea.MouseWheelDown}, EventMouseWheel},
		{"paste-custom", tea.PasteMsg{Content: "x"}, EventCustom},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			evt := NormalizeEvent(tc.msg)
			if evt.Kind != tc.want {
				t.Fatalf("NormalizeEvent(%T).Kind=%v want %v", tc.msg, evt.Kind, tc.want)
			}
			if evt.ToMsg() != tc.msg {
				t.Fatalf("ToMsg round-trip mismatch for %T", tc.msg)
			}
		})
	}
}

// TestKeyAndMouseActivationEmitSameActionID is exit criterion #1 of Phase 5:
// keyboard and mouse activation of the same component must emit the same
// action ID. Both inputs flow through the Host boundary translation
// (Host.Handle tea.Msg → Event) and the Button switches on evt.Kind.
func TestKeyAndMouseActivationEmitSameActionID(t *testing.T) {
	th := theme.DefaultDark()
	btn := NewButton("activate", "Activate", "app.activate")
	h := NewHost(th, btn)
	h.Resize(20, 3)
	h.Layout()
	h.Focus(btn.ID())

	keyRes := h.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if keyRes.Action == nil || keyRes.Action.ID != "app.activate" {
		t.Fatalf("key activation action=%+v want app.activate", keyRes.Action)
	}

	// Button fills the host rect, so a left click anywhere inside activates.
	mouseRes := h.Handle(tea.MouseClickMsg{X: 1, Y: 0, Button: tea.MouseLeft})
	if mouseRes.Action == nil || mouseRes.Action.ID != "app.activate" {
		t.Fatalf("mouse activation action=%+v want app.activate", mouseRes.Action)
	}

	if keyRes.Action.ID != mouseRes.Action.ID {
		t.Fatalf("key action %q != mouse action %q", keyRes.Action.ID, mouseRes.Action.ID)
	}
}

// TestToggleButtonKeyAndMouseActivationSameActionID extends exit criterion #1
// to the second action-emitting interactive control.
func TestToggleButtonKeyAndMouseActivationSameActionID(t *testing.T) {
	tb := NewToggleButton("yolo", "YOLO", "safety.yolo", nil)
	if res := tb.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeySpace})); res.Action == nil || res.Action.ID != "safety.yolo" {
		t.Fatalf("key activation action=%+v", res.Action)
	}
	if res := tb.Handle(EventContext{}, MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft})); res.Action == nil || res.Action.ID != "safety.yolo" {
		t.Fatalf("mouse activation action=%+v", res.Action)
	}
}
