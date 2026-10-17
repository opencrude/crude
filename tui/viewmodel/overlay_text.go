package viewmodel

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
	"crude/tui/wm"
)

// routeOverlayText delivers a key press or bracketed paste to a text field of
// a ComponentWidget overlay (find/replace, command palette, tree prompt)
// through the shared ui.TextField contract: editing, selection, undo/redo,
// Ctrl+C / Ctrl+X / Ctrl+V and bracketed paste behave exactly as in every
// other text control. The field is bound to the overlay's retained TextEdit
// (ui.TextField.BindEdit), so there is no mirrored copy to drift.
//
// It reports whether the value changed so the owner can re-derive (matches,
// filtered rows) and returns the field's command plus, for Ctrl+V, the
// asynchronous clipboard read whose text deliver re-enters through the
// owner's own paste path (owner-keyed: it targets the initiating field).
func (m *App) routeOverlayText(fw *wm.FloatingWindow, field *ui.TextField, msg tea.Msg, deliver func(string) tea.Cmd) (bool, tea.Cmd) {
	if fw == nil || field == nil {
		return false, nil
	}
	cw, ok := fw.Content.(*ui.ComponentWidget)
	if !ok || cw == nil || cw.Host() == nil {
		return false, nil
	}
	host := cw.Host()
	host.Focus(field.ID())
	before := field.Value()
	// Delivered to the field itself, never bubbled: keys the field declines
	// (Up/Down) belong to the owner's navigation, not to frame focus moves.
	res := field.Handle(ui.EventContext{Theme: host.Theme(), Focused: true}, ui.NormalizeEvent(msg))
	host.MarkPaint()
	fw.MarkContentDirty()
	cmd := res.Cmd
	if res.Action != nil && res.Action.ID == ui.ActionTextPaste {
		target, _ := res.Action.Payload.(ui.ClipboardPasteTarget)
		cmd = tea.Batch(cmd, m.beginNativeClipboardPaste(target, fw.ID, deliver))
	}
	return field.Value() != before, cmd
}

// overlayFieldPointer forwards a popup-local press on a text field to the
// overlay's Host, so click placement, Shift+click extension, double/triple
// click selection and captured drag selection come from ui.TextField. Later
// motion and the release reach the same field through window pointer capture
// (capturingWindow / forwardEventToWindowContent).
func overlayFieldPointer(fw *wm.FloatingWindow, localX, localY int, msg tea.Msg) bool {
	if fw == nil {
		return false
	}
	cw, ok := fw.Content.(*ui.ComponentWidget)
	if !ok || cw == nil || cw.Host() == nil {
		return false
	}
	click := tea.MouseClickMsg{X: localX, Y: localY, Button: tea.MouseLeft}
	if original, ok := msg.(tea.MouseClickMsg); ok {
		click.Button, click.Mod = original.Button, original.Mod
	}
	w, h := fw.Size()
	cw.Host().Resize(w, h)
	cw.Host().Layout()
	res := cw.Host().Handle(click)
	fw.MarkContentDirty()
	return res.Handled
}
