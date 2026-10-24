package viewmodel

import "strings"

// invalidateClockContent keeps the ordinary idle presentation opportunities,
// but a composer's caret is not a mutation of the neighbouring transcript.
// These known screen subtrees own their backgrounds; arbitrary focused controls
// retain the conservative containing-region invalidation contract.
func (m *App) invalidateClockContent() {
	focused := m.rootHost.Focused()
	if m.wantsFastTick() {
		// Active tab icons, loading and reveal progress can live outside chat.
		m.rootHost.MarkComponentPaint(m.shell.mainPanes.ID())
	}
	if m.mainPanes != nil {
		for _, group := range m.mainPanes.groups {
			if group.view != nil && !group.deck.Empty() {
				// Decks can contain elapsed command clocks and nested controls.
				// Their existing containing-region owner remains conservative.
				m.rootHost.MarkComponentPaint(group.view.deck.ID())
			}
		}
		for _, group := range m.mainPanes.groups {
			view := group.view
			if view == nil {
				continue
			}
			if view.input != nil && focused == view.input.ID() {
				m.rootHost.MarkSubtreePaint(focused)
				return
			}
			if view.editor != nil && focused == view.editor.ID() {
				m.rootHost.MarkSubtreePaint(focused)
				return
			}
			if view.chat != nil && (focused == view.chat.ID() || strings.HasPrefix(string(focused), string(view.chat.ID())+"/")) {
				// configurePaneChat publishes authoritative row-clock generations.
				return
			}
		}
	}
	m.rootHost.MarkComponentPaint(focused)
}
