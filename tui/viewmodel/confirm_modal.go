package viewmodel

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
	"crude/tui/wm"
)

// openConfirmModal owns the common shell lifecycle of centered confirm frames.
func (m *App) openConfirmModal(id ui.OverlayID, frame *ui.ComponentWidget, activate func(int) tea.Cmd, closeFn func()) {
	w, h := frame.Measure(m.width, m.height)
	window := wm.NewFloatingWindow(string(id), "", frame, w, h,
		wm.WithDecoration(true), wm.WithMovable(true), wm.WithModal(true), wm.WithFocusable(true),
		wm.WithClosable(false), wm.WithResizable(false), wm.WithClickOutside(wm.ClickOutsideIgnore))
	wm.CenterOnScreen(window, m.width, m.height)
	policy := ui.ModalDialogPolicy()
	policy.Decorated, policy.Movable = true, true
	m.overlayHost.RegisterContentHandler(id, func(_ *ui.OverlayHost, fw *wm.FloatingWindow, x, y int, _ tea.Msg) tea.Cmd {
		return activate(confirmFrameButtonAt(fw, x, y))
	})
	m.overlayHost.RegisterHoverHandler(id, func(_ *ui.OverlayHost, fw *wm.FloatingWindow, x, y int, _ tea.MouseButton) {
		if widget, ok := fw.Content.(*ui.ComponentWidget); ok {
			ui.SetConfirmHover(widget, confirmFrameButtonAt(fw, x, y))
		}
	})
	if m.rootHost != nil {
		m.rootHost.CancelPointerCapture()
	}
	m.overlayHost.Open(id, window, policy, closeFn)
	m.forceFrame()
}

func (m *App) confirmModalInput(id ui.OverlayID, msg tea.KeyPressMsg, busy bool, activate func(int) tea.Cmd) (bool, tea.Cmd) {
	if m.overlayHost == nil || m.overlayHost.Top() == nil || m.overlayHost.Top().ID != id {
		return false, nil
	}
	frame := m.overlayHost.Window(id).Content.(*ui.ComponentWidget)
	if busy && msg.Code != tea.KeyEsc {
		return true, nil
	}
	switch msg.Code {
	case tea.KeyEsc:
		m.overlayHost.Close(id)
	case tea.KeyEnter:
		return true, activate(ui.ConfirmButtonIndexFromID(frame.Host().Focused()))
	case tea.KeyLeft, tea.KeyUp:
		ui.MoveConfirmFocus(frame, -1, 2)
	case tea.KeyRight, tea.KeyDown, tea.KeyTab:
		delta := 1
		if msg.Code == tea.KeyTab && msg.Mod.Contains(tea.ModShift) {
			delta = -1
		}
		ui.MoveConfirmFocus(frame, delta, 2)
	}
	m.forceFrame()
	return true, nil
}

func (m *App) refreshConfirmModal(id ui.OverlayID, frame *ui.ComponentWidget) {
	if window := m.overlayHost.Window(id); window != nil {
		if old, ok := window.Content.(*ui.ComponentWidget); ok {
			ui.SetConfirmFocus(frame, ui.ConfirmButtonIndexFromID(old.Host().Focused()))
		}
		w, h := frame.Measure(m.width, m.height)
		window.Content = frame
		frame.SetMarkContentDirty(window.MarkContentDirty)
		window.SetSize(w, h)
		window.MarkContentDirty()
		m.forceFrame()
	}
}

func disableConfirmButtons(frame *ui.ComponentWidget) {
	var disable func(ui.Component)
	disable = func(component ui.Component) {
		if button, ok := component.(*ui.Button); ok {
			button.SetDisabled(true)
		}
		if parent, ok := component.(ui.Parent); ok {
			for _, child := range parent.Children() {
				disable(child)
			}
		}
	}
	disable(frame.Root())
}
