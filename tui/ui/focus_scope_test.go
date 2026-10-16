package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
	"crude/tui/wm"
)

// focusProbe is a leaf component that records every key it receives, so tests
// can assert exactly which component got keyboard focus after a key press.
type focusProbe struct {
	Base
	keys []string
}

func (p *focusProbe) Handle(_ EventContext, evt Event) EventResult {
	if evt.IsKey() {
		p.keys = append(p.keys, evt.Key.String())
		return EventResult{Handled: true}
	}
	return EventResult{}
}

// focusProbeTree builds a host whose root mounts editor/popover/dialog as
// siblings. The dialog is itself a parent so tests can focus a component deep
// inside the dialog's subtree (dialog/confirm).
func focusProbeTree(t *testing.T) *Host {
	t.Helper()
	editor := &focusProbe{Base: NewBase("editor")}
	popover := &focusProbe{Base: NewBase("popover")}
	body := &focusProbe{Base: NewBase("dialog/body")}
	confirm := &focusProbe{Base: NewBase("dialog/confirm")}
	dialog := NewStack("dialog", Vertical, 0, body, confirm)
	root := NewStack("root", Vertical, 1, editor, popover, dialog)
	host := NewHost(theme.DefaultDark(), root)
	host.Resize(80, 24)
	host.Layout()
	return host
}

func TestHost_FocusScopePushPopRestoresExactFocus(t *testing.T) {
	host := focusProbeTree(t)
	if !host.Focus("editor") {
		t.Fatal("focus editor")
	}

	// Push a non-modal scope: focus moves to the boundary, saving the editor.
	host.PushFocusScope("popover", false)
	if got := host.Focused(); got != "popover" {
		t.Fatalf("after push focus=%q want %q", got, "popover")
	}
	if got := host.FocusScopes(); len(got) != 1 || got[0] != "popover" {
		t.Fatalf("scopes=%v want [popover]", got)
	}

	// A modal dialog opens over the popover.
	host.PushFocusScope("dialog", true)
	if got := host.Focused(); got != "dialog" {
		t.Fatalf("after second push focus=%q want %q", got, "dialog")
	}
	if got := host.FocusScopes(); len(got) != 2 || got[0] != "popover" || got[1] != "dialog" {
		t.Fatalf("scopes=%v want [popover dialog]", got)
	}

	// Pop the modal: the EXACT prior focus (the popover) is restored.
	if got := host.PopFocusScope(); got != "popover" {
		t.Fatalf("pop returned %q want %q", got, "popover")
	}
	if got := host.Focused(); got != "popover" {
		t.Fatalf("after pop focus=%q want %q", got, "popover")
	}

	// Pop the popover: the EXACT prior focus (the editor) is restored.
	if got := host.PopFocusScope(); got != "editor" {
		t.Fatalf("pop returned %q want %q", got, "editor")
	}
	if got := host.Focused(); got != "editor" {
		t.Fatalf("after pop focus=%q want %q", got, "editor")
	}
	if len(host.FocusScopes()) != 0 {
		t.Fatalf("scopes=%v want empty", host.FocusScopes())
	}
}

func TestHost_ModalScopeTrapsFocusAndKeys(t *testing.T) {
	host := focusProbeTree(t)
	if !host.Focus("editor") {
		t.Fatal("focus editor")
	}

	// Push a modal scope on the dialog: focus moves to the boundary.
	host.PushFocusScope("dialog", true)
	if got := host.Focused(); got != "dialog" {
		t.Fatalf("focus=%q want %q", got, "dialog")
	}
	if !host.FocusScopeModal() {
		t.Fatal("top scope should be modal")
	}

	// Focus may not leave the modal subtree, not to the editor (prior
	// focus) and not to a sibling popover.
	if host.Focus("editor") {
		t.Fatal("modal scope must reject focus to the editor")
	}
	if host.Focus("popover") {
		t.Fatal("modal scope must reject focus to a sibling")
	}
	if got := host.Focused(); got != "dialog" {
		t.Fatalf("focused must stay inside the modal, got %q", got)
	}
	if !host.FocusWithinScope("dialog/confirm") {
		t.Fatal("dialog/confirm is inside the dialog scope")
	}
	if host.FocusWithinScope("editor") {
		t.Fatal("editor is outside the dialog scope")
	}

	// A key press dispatches only to the modal subtree.
	if !host.Focus("dialog/confirm") {
		t.Fatal("focus dialog/confirm (inside the modal scope)")
	}
	editor := host.registry["editor"].(*focusProbe)
	confirm := host.registry["dialog/confirm"].(*focusProbe)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyTab})
	if len(confirm.keys) != 1 {
		t.Fatalf("modal subtree must receive the key, got %v", confirm.keys)
	}
	if len(editor.keys) != 0 {
		t.Fatalf("key escaped the modal: editor received %v", editor.keys)
	}
}

func TestOverlayHost_NestedOpenCloseRestoresExactFocusPath(t *testing.T) {
	host := focusProbeTree(t)
	overlay := NewOverlayHost(wm.NewWindowManager())
	overlay.SetFocusHost(host)

	// Start focused on the editor.
	if !host.Focus("editor") {
		t.Fatal("focus editor")
	}

	// Open a popover over the editor: the scope captures the editor focus.
	fwPop := newStubWindow("popover", 10, 3, wm.WithFocusable(true))
	overlay.Open(OverlayID("popover"), fwPop, PopoverPolicy(false), nil)
	if got := host.Focused(); got != "popover" {
		t.Fatalf("after popover open focus=%q want %q", got, "popover")
	}
	if got := host.FocusScopes(); len(got) != 1 || got[0] != "popover" {
		t.Fatalf("scopes=%v want [popover]", got)
	}

	// Open a modal dialog over the popover.
	fwDlg := newStubWindow("dialog", 10, 3, wm.WithFocusable(true))
	overlay.Open(OverlayID("dialog"), fwDlg, ModalDialogPolicy(), nil)
	if got := host.Focused(); got != "dialog" {
		t.Fatalf("after dialog open focus=%q want %q", got, "dialog")
	}
	if got := host.FocusScopes(); len(got) != 2 || got[0] != "popover" || got[1] != "dialog" {
		t.Fatalf("scopes=%v want [popover dialog]", got)
	}

	// Close the dialog: focus returns to the popover, the EXACT prior focus.
	overlay.Close(OverlayID("dialog"))
	if got := host.Focused(); got != "popover" {
		t.Fatalf("after dialog close focus=%q want %q", got, "popover")
	}
	if got := host.FocusScopes(); len(got) != 1 || got[0] != "popover" {
		t.Fatalf("scopes=%v want [popover]", got)
	}

	// Close the popover: focus returns to the editor, the EXACT prior focus.
	overlay.Close(OverlayID("popover"))
	if got := host.Focused(); got != "editor" {
		t.Fatalf("after popover close focus=%q want %q", got, "editor")
	}
	if got := host.FocusScopes(); len(got) != 0 {
		t.Fatalf("scopes=%v want empty", got)
	}
}

func TestOverlayHost_CloseAllRestoresFirstScopeFocus(t *testing.T) {
	host := focusProbeTree(t)
	overlay := NewOverlayHost(wm.NewWindowManager())
	overlay.SetFocusHost(host)
	if !host.Focus("editor") {
		t.Fatal("focus editor")
	}

	overlay.Open(OverlayID("popover"), newStubWindow("popover", 10, 3, wm.WithFocusable(true)), PopoverPolicy(false), nil)
	overlay.Open(OverlayID("dialog"), newStubWindow("dialog", 10, 3, wm.WithFocusable(true)), ModalDialogPolicy(), nil)
	if got := host.FocusScopes(); len(got) != 2 {
		t.Fatalf("scopes=%v want 2", got)
	}

	overlay.CloseAll()
	if got := host.Focused(); got != "editor" {
		t.Fatalf("after CloseAll focus=%q want %q (focus before any overlay opened)", got, "editor")
	}
	if got := host.FocusScopes(); len(got) != 0 {
		t.Fatalf("scopes=%v want empty", got)
	}
}

func TestOverlayHost_NonTopCloseDropsScopeWithoutRestore(t *testing.T) {
	host := focusProbeTree(t)
	overlay := NewOverlayHost(wm.NewWindowManager())
	overlay.SetFocusHost(host)
	if !host.Focus("editor") {
		t.Fatal("focus editor")
	}

	overlay.Open(OverlayID("popover"), newStubWindow("popover", 10, 3, wm.WithFocusable(true)), PopoverPolicy(false), nil)
	overlay.Open(OverlayID("dialog"), newStubWindow("dialog", 10, 3, wm.WithFocusable(true)), ModalDialogPolicy(), nil)
	if got := host.Focused(); got != "dialog" {
		t.Fatalf("top overlay focus=%q want %q", got, "dialog")
	}

	// Closing the MIDDLE overlay drops its scope without clobbering the top
	// overlay's focus.
	overlay.Close(OverlayID("popover"))
	if got := host.Focused(); got != "dialog" {
		t.Fatalf("top overlay must keep focus after a non-top close, got %q", got)
	}
	if got := host.FocusScopes(); len(got) != 1 || got[0] != "dialog" {
		t.Fatalf("scopes=%v want [dialog]", got)
	}

	// Closing the remaining (now top) overlay restores the focus that existed
	// before the REMOVED popover opened: the editor, not the gone popover.
	overlay.Close(OverlayID("dialog"))
	if got := host.Focused(); got != "editor" {
		t.Fatalf("after final close focus=%q want %q", got, "editor")
	}
}

func TestHost_FocusScopeReplaceOverlayPreservesPriorFocus(t *testing.T) {
	host := focusProbeTree(t)
	overlay := NewOverlayHost(wm.NewWindowManager())
	overlay.SetFocusHost(host)
	if !host.Focus("editor") {
		t.Fatal("focus editor")
	}

	// Open then RE-OPEN the same overlay (Open closes the existing one first).
	overlay.Open(OverlayID("popover"), newStubWindow("popover", 10, 3, wm.WithFocusable(true)), PopoverPolicy(false), nil)
	overlay.Open(OverlayID("popover"), newStubWindow("popover", 10, 3, wm.WithFocusable(true)), PopoverPolicy(false), nil)
	if got := host.FocusScopes(); len(got) != 1 || got[0] != "popover" {
		t.Fatalf("scopes=%v want single [popover]", got)
	}

	overlay.Close(OverlayID("popover"))
	if got := host.Focused(); got != "editor" {
		t.Fatalf("after replace+close focus=%q want %q", got, "editor")
	}
}
