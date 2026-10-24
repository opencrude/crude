package viewmodel

import (
	"crude/terminalvt"
	"crude/tui/ui"
	"crude/workspace"
)

// Prediction is a presentation-only overlay offered by a network terminal
// replica. It never mutates input, selection authority or the authoritative grid.
func (m *App) predictTerminalKey(id string, key terminalvt.Key) {
	service, ok := m.ws.Terminals().(interface {
		PredictiveKey(string, terminalvt.Key) (workspace.TerminalProjection, bool)
	})
	if !ok {
		return
	}
	p, ok := service.PredictiveKey(id, key)
	if !ok {
		return
	}
	if screen := m.termScreens[id]; screen != nil {
		screen.SetProjection(p)
		m.rootHost.MarkComponentPaint(screen.ID())
	}
	if fw := m.overlayHost.Window(ui.TerminalOverlayID(id)); fw != nil {
		fw.MarkContentDirty()
	}
}
