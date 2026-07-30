package editor

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

// syncScrollChrome updates the shared ScrollModel and ScrollbarControl layout
// from the current viewport. Paint and mouse drag both use this control.
func (e *Editor) syncScrollChrome(bodyH int) {
	if bodyH < 1 {
		bodyH = 1
	}
	lc := e.buf.LineCount()
	if e.scrollModel == nil {
		e.scrollModel = ui.NewScrollModel()
	}
	ui.SyncScrollModel(e.scrollModel, lc, bodyH, e.scrollY)
	if e.sbCtrl == nil {
		e.sbCtrl = ui.NewScrollbarControl("editor/scrollbar", e.scrollModel, e.tokens)
	} else {
		e.sbCtrl.SetScroll(e.scrollModel)
		e.sbCtrl.SetTokens(e.tokens)
	}
	e.sbCtrl.Layout(ui.LayoutContext{}, ui.Rect{X: 0, Y: 0, W: 1, H: bodyH})
}

// scrollbarColWidth is the reserved right-column width for shared chrome.
func (e *Editor) scrollbarColWidth() int {
	if e.tokens != nil {
		if w := e.tokens.Metrics().ScrollbarWidth; w > 0 {
			return w
		}
	}
	return 1
}

// paintScrollbar paints the shared ScrollbarControl into buf at column sbX
// for bodyH rows. Colors come from TokenSet when available.
func (e *Editor) paintScrollbar(buf *surface.CellBuffer, sbX, bodyH int) {
	if buf == nil || bodyH < 1 {
		return
	}
	e.syncScrollChrome(bodyH)
	if e.scrollModel == nil || e.scrollModel.MaxOffset() <= 0 {
		bg := e.cellColor(e.th.Colors.Bg)
		for y := 0; y < bodyH; y++ {
			buf.Set(sbX, y, cell.NewCell(' ', bg, bg, 0))
		}
		return
	}
	e.sbCtrl.Layout(ui.LayoutContext{}, ui.Rect{X: sbX, Y: 0, W: 1, H: bodyH})
	e.sbCtrl.Paint(buf, 0, 0, ui.PaintContext{})
}

// applyScrollbarOffset copies ScrollModel offset back into editor scrollY.
func (e *Editor) applyScrollbarOffset() {
	if e.scrollModel == nil {
		return
	}
	if off := e.scrollModel.Offset(); off != e.scrollY {
		e.scrollY = off
	}
}

// handleScrollbarEvent routes mouse input to ScrollbarControl using track-local Y.
func (e *Editor) handleScrollbarEvent(msg tea.Msg, localY int) bool {
	e.syncScrollChrome(e.bodyHeight())
	if e.sbCtrl == nil {
		return false
	}
	res := e.sbCtrl.Handle(ui.EventContext{LocalY: localY}, msg)
	if res.Handled {
		e.applyScrollbarOffset()
	}
	return res.Handled
}

// SetTokens attaches design-system tokens for chrome paint (scrollbar colors).
func (e *Editor) SetTokens(ts *ui.TokenSet) {
	e.tokens = ts
	if e.sbCtrl != nil {
		e.sbCtrl.SetTokens(ts)
	}
}

// SetTheme replaces the editor palette, rebuilds chroma styles, and clears
// line/chroma caches so the next paint cannot keep pre-theme colours.
func (e *Editor) SetTheme(th *theme.Theme) {
	if e == nil || th == nil {
		return
	}
	e.th = th
	e.initChroma()
	e.chromaCache = nil
	e.invalidateEditorCaches()
	if e.tokens != nil {
		e.tokens.Rebuild(th)
	} else {
		e.tokens = ui.NewTokenSet(th)
	}
	if e.sbCtrl != nil {
		e.sbCtrl.SetTokens(e.tokens)
	}
}

// Theme returns the editor's active theme pointer.
func (e *Editor) Theme() *theme.Theme {
	if e == nil {
		return nil
	}
	return e.th
}

// SetStatusChrome toggles the optional document footline inside the editor
// surface. The main IDE shell keeps this off — title, cursor, diagnostics
// counts, and actions live in shellstatus contextual help.
func (e *Editor) SetStatusChrome(on bool) {
	e.statusChrome = on
}

// StatusChrome reports whether the editor paints its own footline.
func (e *Editor) StatusChrome() bool { return e.statusChrome }

// ScrollModel exposes the shared scroll model for tests and adapters.
func (e *Editor) ScrollModel() *ui.ScrollModel {
	e.syncScrollChrome(e.bodyHeight())
	return e.scrollModel
}

// IsScrollbarDragging reports whether the thumb is being dragged.
func (e *Editor) IsScrollbarDragging() bool {
	return e.sbCtrl != nil && e.sbCtrl.IsDragging()
}

func (e *Editor) bodyHeight() int {
	h := e.viewH
	if e.statusChrome {
		h--
	}
	if h < 1 {
		h = 1
	}
	return h
}
