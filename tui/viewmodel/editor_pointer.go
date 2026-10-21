package viewmodel

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
	"crude/tui/ui"
)

// Repeating selection work re-enters through its mounted capture owner, not
// keyboard focus or the selected editor alias. The engine then checks the exact
// editor/gesture epoch. This also reaches read-only previews in window Hosts.
func (m *App) deliverPointerScroll(msg editor.PointerScrollMsg) tea.Cmd {
	var cmds []tea.Cmd
	var modal *ui.OverlayEntry
	if m.overlayHost != nil {
		modal = m.overlayHost.Top()
	}
	if m.rootHost != nil {
		m.rootHost.Layout()
		if id, _, ok := m.rootHost.CapturedPointer(); ok {
			if m.sessionDeckModal() || modal != nil && modal.Policy.Modal {
				m.rootHost.CancelPointerCapture()
			} else {
				res := m.rootHost.Deliver(id, msg)
				cmds = append(cmds, res.Cmd)
			}
		}
	}
	if m.windowManager != nil {
		for _, fw := range m.windowManager.Windows() {
			host := windowContentHost(fw)
			if host == nil {
				continue
			}
			host.Layout()
			if id, _, ok := host.CapturedPointer(); ok {
				if !fw.Visible() || modal != nil && modal.Policy.Modal && modal.Window != fw {
					host.CancelPointerCapture()
					continue
				}
				res := host.Deliver(id, msg)
				if res.Handled {
					fw.MarkContentDirty()
				}
				cmds = append(cmds, res.Cmd)
			}
		}
	}
	return tea.Batch(cmds...)
}

func (m *App) cancelPointerCaptures() {
	m.setWheelHost(nil)
	if m.rootHost != nil {
		m.rootHost.ResetWheelGesture()
		m.rootHost.CancelPointerCapture()
	}
	if m.windowManager != nil {
		for _, fw := range m.windowManager.Windows() {
			if host := windowContentHost(fw); host != nil {
				host.ResetWheelGesture()
				host.CancelPointerCapture()
			}
		}
	}
}
