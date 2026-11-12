package editor

// TabWidth is shared by every view of one source table. It is presentation,
// not source identity, an edit generation, or a reason to restart syntax.
func (e *Editor) TabWidth() int {
	if e == nil || e.buf == nil {
		return DefaultTabWidth
	}
	return e.buf.tabSize
}

func (e *Editor) setTabWidth(width int) {
	if e.buf == nil || width == e.buf.tabSize {
		return
	}
	type viewGeometry struct {
		view       *Editor
		state      editorViewState
		landing    [2]int
		hasLanding bool
	}
	var views []viewGeometry
	old := e.buf.tabSize
	e.eachView(func(v *Editor) {
		state := viewGeometry{view: v, state: v.captureViewState()}
		if v.landing != nil {
			state.landing = [2]int{v.buf.positionToOffset(v.landing.Start), v.buf.positionToOffset(v.landing.End)}
			state.hasLanding = true
		}
		state.state.ScrollX = reprojectColumn(v.LineText(v.cursor.Line), v.scrollX, old, width)
		views = append(views, state)
	})
	e.buf.tabSize = width
	e.buf.indentationRevision = 0
	for _, saved := range views {
		v := saved.view
		v.stopPointerSelection()
		v.CancelPointerCapture()
		v.lineRunesCacheValid = false
		v.lineRunesCache = nil
		v.lineRunesCacheWidth = 0
		v.lineGeomCache = nil
		clear(v.chromaLineCache)
		clear(v.chromaLineScratch)
		v.markDocumentWidthAll()
		v.restoreViewState(saved.state)
		v.resetSecondaryGoals()
		v.ClearHover()
		v.link = nil
		if saved.hasLanding {
			v.landing.Start = v.buf.positionAtOffset(saved.landing[0])
			v.landing.End = v.buf.positionAtOffset(saved.landing[1])
		}
		if v.search != nil {
			for i := range v.search.Matches {
				m := &v.search.Matches[i]
				m.Start.Col = reprojectColumn(v.LineText(m.Start.Line), m.Start.Col, old, width)
				m.End.Col = reprojectColumn(v.LineText(m.End.Line), m.End.Col, old, width)
			}
		}
		v.reprojectDiagnosticColumns(old, width)
		v.reprojectInlineGeometry()
		v.pendingReveal = true
	}
}

func reprojectColumn(raw string, col, oldWidth, newWidth int) int {
	oldEnd := LineVisualWidth(raw, oldWidth)
	if col > oldEnd {
		return LineVisualWidth(raw, newWidth) + col - oldEnd
	}
	return ByteOffsetToVisualCol(raw, VisualColToByteOffset(raw, col, oldWidth), newWidth)
}

// Retained diagnostic ink may be provisional while canonical typing catches up.
// Reproject its current source columns, not older raw-provider coordinates.
func (e *Editor) reprojectDiagnosticColumns(oldWidth, newWidth int) {
	e.diagMu.Lock()
	defer e.diagMu.Unlock()
	for i := range e.diagnostics {
		d := &e.diagnostics[i]
		d.Col = reprojectColumn(e.LineText(d.Line), d.Col, oldWidth, newWidth)
		d.EndCol = reprojectColumn(e.LineText(d.EndLine), d.EndCol, oldWidth, newWidth)
	}
	for line, spans := range e.diagByLine {
		raw := e.LineText(line)
		for i := range spans {
			spans[i].startCol = reprojectColumn(raw, spans[i].startCol, oldWidth, newWidth)
			spans[i].endCol = reprojectColumn(raw, spans[i].endCol, oldWidth, newWidth)
		}
	}
}

func (e *Editor) reprojectInlineGeometry() {
	if p := e.currentInlays(); p != nil {
		byLine := make(map[int][]InlineInsertion, len(p.lines))
		for line, projection := range p.lines {
			byLine[line] = projection.insertions
		}
		next, err := e.prepareInlayProjection(p.source, byLine)
		if err != nil {
			e.inlays = nil
		} else {
			e.inlays = next
		}
	}
	if g := e.ghost; g != nil {
		g.caret = e.buf.positionAtOffset(g.anchor)
		g.base, g.line = nil, nil
	}
}
