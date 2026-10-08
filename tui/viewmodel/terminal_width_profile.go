package viewmodel

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
	"crude/tui/ui"
)

func (m *App) applyTerminalWidthProfile(msg tea.TerminalWidthProfileMsg) {
	if m.Logger != nil {
		m.Logger.Info("TERM", "width_calibrated",
			"measured", msg.Measured,
			"corrections", msg.Corrections,
			"ambiguous_width", msg.AmbiguousWidth,
			"legacy_graphemes", msg.LegacyGraphemeClusters,
			"changed", msg.Changed,
		)
	}
	if msg.Measured == 0 {
		return
	}

	// Width is a layout primitive. Every retained presentation cache must cross
	// this barrier together before the renderer's forced clean redraw.
	m.cachedOutput = ""
	if m.rootHost != nil {
		m.rootHost.MarkLayout()
	}
	m.subPaneProjections.InvalidateWidthProfile()
	m.sessionProjections.InvalidateWidthProfile()
	for _, view := range m.gitReviews {
		view.screen.InvalidateWidthProfile()
	}
	for _, view := range m.localDiffs {
		view.screen.InvalidateWidthProfile()
	}

	seenEditors := make(map[*editor.Editor]struct{}, len(m.EditorState.Tabs)+1)
	if m.EditorState.Editor != nil {
		seenEditors[m.EditorState.Editor] = struct{}{}
		m.EditorState.Editor.InvalidateWidthProfile()
	}
	for _, tab := range m.EditorState.Tabs {
		if tab == nil || tab.Editor == nil {
			continue
		}
		if _, seen := seenEditors[tab.Editor]; seen {
			continue
		}
		seenEditors[tab.Editor] = struct{}{}
		tab.Editor.InvalidateWidthProfile()
	}

	if m.windowManager != nil {
		for _, window := range m.windowManager.Windows() {
			if window == nil {
				continue
			}
			if content, ok := window.Content.(interface{ Host() *ui.Host }); ok {
				if host := content.Host(); host != nil {
					host.MarkLayout()
				}
			}
			window.MarkContentDirty()
		}
	}
}
