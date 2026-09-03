package editor

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
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
	e.sbCtrl.Layout(ui.LayoutContext{}, ui.Rect{X: 0, Y: 0, W: e.scrollbarColWidth(), H: bodyH})
}

func (e *Editor) syncHorizontalScrollChrome(textW, bodyH int) bool {
	if textW < 1 || bodyH < 1 {
		e.hScrollbarVisible = false
		return false
	}
	maxWidth := e.documentVisualWidth()
	if e.hScrollModel == nil {
		e.hScrollModel = ui.NewScrollModel()
	}
	ui.SyncScrollModel(e.hScrollModel, maxWidth, textW, e.scrollX)
	e.scrollX = e.hScrollModel.Offset()
	if e.hSbCtrl == nil {
		e.hSbCtrl = ui.NewScrollbarControl("editor/h-scrollbar", e.hScrollModel, e.tokens)
		e.hSbCtrl.SetDirection(ui.Horizontal)
		e.hSbCtrl.SetPointerTargetSize(1)
	} else {
		e.hSbCtrl.SetScroll(e.hScrollModel)
		e.hSbCtrl.SetTokens(e.tokens)
	}
	e.hScrollbarVisible = e.hScrollModel.MaxOffset() > 0
	return e.hScrollbarVisible
}

func (e *Editor) documentVisualWidth() int {
	if e.buf == nil {
		return 0
	}
	revision := e.buf.Revision()
	count := e.buf.LineCount()
	if e.hWidthRevision != revision {
		e.hWidthAllDirty = true
		e.hWidthRevision = revision
	}
	if e.hWidthAllDirty || len(e.hLineWidths) != count {
		e.hLineWidths = make([]int, count)
		e.hDocumentWidth = 0
		for line := 0; line < count; line++ {
			width := e.measureDocumentLineWidth(line)
			e.hLineWidths[line] = width
			e.hDocumentWidth = max(e.hDocumentWidth, width)
		}
		e.hWidthAllDirty = false
		e.hWidthDirty = nil
		return e.hDocumentWidth
	}
	for line := range e.hWidthDirty {
		if line < 0 || line >= count {
			continue
		}
		old := e.hLineWidths[line]
		width := e.measureDocumentLineWidth(line)
		e.hLineWidths[line] = width
		if width > e.hDocumentWidth {
			e.hDocumentWidth = width
		} else if old == e.hDocumentWidth && width < old {
			e.hDocumentWidth = 0
			for _, candidate := range e.hLineWidths {
				e.hDocumentWidth = max(e.hDocumentWidth, candidate)
			}
		}
	}
	e.hWidthDirty = nil
	return e.hDocumentWidth
}

func (e *Editor) measureDocumentLineWidth(line int) int {
	e.hWidthMeasures++
	raw, err := e.buf.lineView(line)
	if err != nil {
		return 0
	}
	return rendering.VisibleWidth(ExpandTabs(string(raw), tabWidth))
}

func (e *Editor) markDocumentWidthChanged() {
	if e.buf == nil {
		return
	}
	revision := e.buf.Revision()
	if len(e.hLineWidths) != e.buf.LineCount() {
		e.hWidthAllDirty = true
	} else {
		if e.hWidthDirty == nil {
			e.hWidthDirty = make(map[int]bool)
		}
		e.hWidthDirty[e.cursor.Line] = true
	}
	e.hWidthRevision = revision
}
func (e *Editor) markDocumentWidthAll() {
	e.hWidthAllDirty = true
	if e.buf != nil {
		e.hWidthRevision = e.buf.Revision()
	}
	e.hWidthDirty = nil
}

func (e *Editor) paintHorizontalScrollbar(buf *surface.CellBuffer, x, y, width int) {
	if !e.hScrollbarVisible || e.hSbCtrl == nil || width < 1 {
		return
	}
	e.hSbCtrl.Layout(ui.LayoutContext{}, ui.Rect{X: x, Y: y, W: width, H: 1})
	e.hSbCtrl.Paint(buf, 0, 0, ui.PaintContext{Hovered: e.hScrollbarHovered})
}

// scrollbarColWidth is the reserved right-column width for shared chrome.
func (e *Editor) scrollbarColWidth() int {
	if e.tokens != nil {
		if w := e.tokens.Metrics().ScrollbarWidth; w > 0 {
			return w
		}
	}
	return ui.ScrollbarWidth
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
			for x := 0; x < e.scrollbarColWidth(); x++ {
				buf.Set(sbX+x, y, cell.NewCell(' ', bg, bg, 0))
			}
		}
		return
	}
	e.sbCtrl.Layout(ui.LayoutContext{}, ui.Rect{X: sbX, Y: 0, W: e.scrollbarColWidth(), H: bodyH})
	e.sbCtrl.Paint(buf, 0, 0, ui.PaintContext{Hovered: e.vScrollbarHovered})
}

func (e *Editor) refreshScrollbarHover(mouseX, mouseY int) {
	e.hScrollbarHovered = e.horizontalScrollbarHit(mouseX, mouseY)
	sw := e.gutterWidth()
	sbX := sw + max(1, e.viewW-sw-e.scrollbarColWidth())
	e.vScrollbarHovered = !e.hScrollbarHovered && e.verticalScrollbarHit(mouseX, mouseY, sbX)
}

func (e *Editor) SetPointerHovered(hovered bool) {
	if !hovered {
		e.vScrollbarHovered = false
		e.hScrollbarHovered = false
	}
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
	res := e.sbCtrl.Handle(ui.EventContext{LocalY: localY}, ui.NormalizeEvent(msg))
	if res.Handled {
		e.applyScrollbarOffset()
	}
	return res.Handled
}

func (e *Editor) handleHorizontalScrollbarEvent(msg tea.Msg, localX int) bool {
	if e.hSbCtrl == nil || e.hScrollModel == nil {
		return false
	}
	res := e.hSbCtrl.Handle(ui.EventContext{LocalX: localX}, ui.NormalizeEvent(msg))
	if res.Handled {
		e.scrollX = e.hScrollModel.Offset()
	}
	return res.Handled
}

// SetTokens attaches design-system tokens for chrome paint (scrollbar colors).
func (e *Editor) SetTokens(ts *ui.TokenSet) {
	e.tokens = ts
	if e.sbCtrl != nil {
		e.sbCtrl.SetTokens(ts)
	}
	if e.hSbCtrl != nil {
		e.hSbCtrl.SetTokens(ts)
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
	return (e.sbCtrl != nil && e.sbCtrl.IsDragging()) || (e.hSbCtrl != nil && e.hSbCtrl.IsDragging())
}

func (e *Editor) IsHorizontalScrollbarDragging() bool {
	return e.hSbCtrl != nil && e.hSbCtrl.IsDragging()
}

func (e *Editor) bodyHeight() int {
	h := e.viewH
	if e.statusChrome {
		h--
	}
	if e.hScrollbarVisible {
		h--
	}
	if h < 1 {
		h = 1
	}
	return h
}
