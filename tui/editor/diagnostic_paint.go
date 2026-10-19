package editor

// Diagnostic decorations can remain on unchanged source while the server
// analyses the next revision. They never authorize hover/navigation: those
// consumers still require diagnosticsRevision to match the source exactly.
// Touched lines are dropped, not shifted onto new text at old column offsets.
func (e *Editor) remapDiagnosticPaint() {
	if e.buf == nil {
		return
	}
	e.diagMu.Lock()
	defer e.diagMu.Unlock()
	revision, previous := e.Revision(), e.diagPaintRevision
	if e.diagPaintBuffer == e.buf && revision == previous {
		return
	}
	defer func() { e.diagPaintBuffer, e.diagPaintRevision = e.buf, revision }()
	if e.diagPaintBuffer != e.buf || revision < previous || revision-previous > uint64(len(e.buf.lineChanges)) {
		clear(e.diagByLine)
		return
	}
	for next := previous + 1; next <= revision; next++ {
		change := e.buf.lineChanges[next%uint64(len(e.buf.lineChanges))]
		if change.revision != next {
			clear(e.diagByLine)
			return
		}
		if change.delta == 0 {
			for line := range e.diagByLine {
				if line >= change.start && line <= change.end {
					delete(e.diagByLine, line)
				}
			}
			continue
		}
		if e.diagPaintScratch == nil {
			e.diagPaintScratch = make(map[int][]diagSpan, len(e.diagByLine))
		}
		clear(e.diagPaintScratch)
		for line, spans := range e.diagByLine {
			switch {
			case line < change.start:
				e.diagPaintScratch[line] = spans
			case line > change.end:
				e.diagPaintScratch[line+change.delta] = spans
			}
		}
		e.diagByLine, e.diagPaintScratch = e.diagPaintScratch, e.diagByLine
		clear(e.diagPaintScratch)
	}
}
