package viewmodel

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
	wm "crude/tui/wm"
)

// textClipboardHit pairs the text control a whole-text context menu edits with
// the component that must receive the edit. Composites (form fields, overlay
// editors, question write-ins) sync their own model only when an edit flows
// through their Handle, so Cut dispatches a synthetic Ctrl+X to the owner
// instead of mutating the inner editor behind its back.
type textClipboardHit struct {
	owner  ui.Component
	target ui.TextClipboardTarget
}

// textClipboardHitIn resolves the component a host hit-tested at a point in
// the host's coordinate space into a clipboard hit. Composites that hide their
// children from hit-testing (forms) resolve the control themselves through
// TextControlAtProvider.
func textClipboardHitIn(host *ui.Host, id ui.ComponentID, x, y int) (textClipboardHit, bool) {
	if host == nil {
		return textClipboardHit{}, false
	}
	c := host.Component(id)
	if c == nil {
		return textClipboardHit{}, false
	}
	if provider, ok := c.(ui.TextControlAtProvider); ok {
		rect, _ := host.ScreenRect(id)
		owner := provider.TextControlAt(x-rect.X, y-rect.Y)
		if owner == nil {
			return textClipboardHit{}, false
		}
		target := ui.TextClipboardTargetIn(owner)
		if target == nil {
			return textClipboardHit{}, false
		}
		return textClipboardHit{owner: owner, target: target}, true
	}
	target := ui.TextClipboardTargetIn(c)
	if target == nil {
		return textClipboardHit{}, false
	}
	return textClipboardHit{owner: c, target: target}, true
}

// textClipboardHitAt resolves the text control under a screen point for the
// surfaces other than the chat composer: the session deck panel (question deck
// write-ins) and the root shell host (tree filter, launcher search, activity
// filters). The composer builds its own menu because its draft carries
// attachment chips and its own paste semantics.
func (m *App) textClipboardHitAt(mouse tea.Mouse) (textClipboardHit, bool) {
	if hit, ok := m.deckTextClipboardHit(mouse); ok {
		return hit, true
	}
	if m.rootHost == nil {
		return textClipboardHit{}, false
	}
	return textClipboardHitIn(m.rootHost, m.rootHost.HitTest(mouse.X, mouse.Y), mouse.X, mouse.Y)
}

// deckTextClipboardHit hit-tests the session deck panel, whose host works in
// deck-local coordinates.
func (m *App) deckTextClipboardHit(mouse tea.Mouse) (textClipboardHit, bool) {
	proj := m.activeSessionDeck()
	if proj == nil || proj.deckHost == nil {
		return textClipboardHit{}, false
	}
	if m.sessionDeckRect.Empty() || !m.sessionDeckRect.Contains(mouse.X, mouse.Y) {
		return textClipboardHit{}, false
	}
	x, y := mouse.X-m.sessionDeckRect.X, mouse.Y-m.sessionDeckRect.Y
	return textClipboardHitIn(proj.deckHost, proj.deckHost.HitTest(x, y), x, y)
}

// windowTextClipboardHit hit-tests a floating window's content host, whose
// coordinates are the window body's local space.
func windowTextClipboardHit(fw *wm.FloatingWindow, mouse tea.Mouse) (textClipboardHit, bool) {
	if fw == nil {
		return textClipboardHit{}, false
	}
	w, ok := fw.Content.(*ui.WindowFrameWidget)
	if !ok || w.Host() == nil {
		return textClipboardHit{}, false
	}
	winX, winY := fw.Position()
	bounds := fw.ContentBounds()
	x, y := mouse.X-(winX+bounds.X), mouse.Y-(winY+bounds.Y)
	return textClipboardHitIn(w.Host(), w.Host().HitTest(x, y), x, y)
}

// textClipboardMenuSection builds the whole-text Copy/Cut items every text
// control offers from its context menu. Copy takes the entire value — the
// menu's subject is the control, not a selection — while the Ctrl+C / Ctrl+X
// chords keep their selection semantics.
func (m *App) textClipboardMenuSection(hit textClipboardHit) ui.ContextMenuSection {
	value := hit.target.TextValue()
	secret := hit.target.TextSecret()
	editable := hit.target.TextEditable() && !secret
	return ui.ContextMenuSection{
		SectionLabel: "Clipboard",
		Items: []ui.ContextMenuItem{
			{Label: "Copy", Detail: "Ctrl+C", Disabled: value == "" || secret, Action: func() {
				m.copyToClipboard(value)
			}},
			{Label: "Cut", Detail: "Ctrl+X", Disabled: value == "" || !editable, Action: func() {
				// The async request owns the clipboard write; the Ctrl+X
				// dispatch owns the edit, so the owning component updates its
				// model exactly as a key press would. Its copy command is
				// deliberately dropped so the clipboard is written once, and
				// an action it returns (form.changed) reaches the viewmodel —
				// popup-local action handlers are not re-entered from here.
				m.copyToClipboard(value)
				res := hit.owner.Handle(ui.EventContext{}, ui.KeyEvent(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl}))
				if res.Action != nil {
					m.handleHostAction(res.Action)
				}
			}},
		},
	}
}
