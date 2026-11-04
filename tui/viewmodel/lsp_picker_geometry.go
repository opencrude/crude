package viewmodel

import (
	"crude/tui/editor"
	"crude/tui/ui"
	"crude/tui/wm"
)

// The body owns intrinsic size, WindowFrame adds chrome, and the shared editor
// help geometry places the result. Neither picker keeps another geometry cache
// or a second caret/scroll projection.
func (m *App) placeEditorPicker(window *wm.FloatingWindow, frame *ui.WindowFrameWidget, ed *editor.Editor) {
	size := frame.Frame().Measure(ui.LayoutContext{}, ui.Constraints{
		MaxW: min(90, max(1, m.width-4)), MaxH: min(14, max(1, m.height-4)),
	})
	bounds, visible := m.editorHelpBounds(ed, size.W, size.H)
	if !visible {
		if w, h := window.Size(); w != size.W || h != size.H {
			window.SetSize(size.W, size.H)
		}
		wm.CenterOnScreen(window, m.width, m.height)
		return
	}
	if w, h := window.Size(); w != bounds.W || h != bounds.H {
		window.SetSize(bounds.W, bounds.H)
	}
	if x, y := window.Position(); x != bounds.X || y != bounds.Y {
		window.SetPosition(bounds.X, bounds.Y)
	}
}

func (m *App) positionEditorPickers() {
	if m.overlayHost == nil {
		return
	}
	if state := m.navigation; state != nil && !state.landing {
		if window := m.overlayHost.Window(navigationOverlay); window != nil {
			if frame, ok := window.Content.(*ui.WindowFrameWidget); ok {
				m.placeEditorPicker(window, frame, state.editor)
			}
		}
	}
	if state := m.codeActions; state != nil {
		if window := m.overlayHost.Window(codeActionsOverlay); window != nil {
			if frame, ok := window.Content.(*ui.WindowFrameWidget); ok {
				m.placeEditorPicker(window, frame, state.editor)
			}
		}
	}
}
