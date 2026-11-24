package editor

import (
	"bytes"
	"context"
	"path/filepath"

	"crude/tui/theme"
)

// PreviewDocument owns an immutable text snapshot and its scroll geometry.
// Build it on the file worker. Mounting or moving to another hit does not
// copy the file, split it into strings, measure every line or open a watcher.
type PreviewDocument struct {
	table    *PieceTable
	widths   []int
	widest   int
	folds    foldState
	language *languageFacts
}

// PreparePreview takes ownership of decoded text and indexes the complete file.
func PreparePreview(ctx context.Context, path string, text []byte, tabWidth int) (*PreviewDocument, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pt := NewPieceTable()
	pt.original = text
	pt.cachedBytes = text
	pt.pieces[0].length = len(text)
	pt.cacheValid = true
	pt.revision = 1
	if tabWidth > 0 {
		pt.tabSize = tabWidth
	}
	p := &PreviewDocument{table: pt}
	for start := 0; ; {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := bytes.IndexByte(text[start:], '\n')
		last := end < 0
		if last {
			end = len(text)
		} else {
			end += start
		}
		pt.lineOffsets = append(pt.lineOffsets, start)
		line := text[start:end]
		if !last {
			line = bytes.TrimSuffix(line, []byte{'\r'})
		}
		w := LineVisualWidth(line, pt.tabSize)
		p.widths = append(p.widths, w)
		p.widest = max(p.widest, w)
		if last {
			break
		}
		start = end + 1
	}
	// Prime the same editor fold index used by paint, off the Update goroutine.
	view := Editor{buf: pt, filePath: path}
	view.refreshFolds()
	p.folds, p.language = view.folds, view.language
	return p, ctx.Err()
}

// CapturePreview captures this revision without exporting its bytes on Update.
func (b *Buffer) CapturePreview(path string) func(context.Context) (*PreviewDocument, error) {
	s, tab := b.buf.snapshotText(), b.buf.tabSize
	return func(ctx context.Context) (*PreviewDocument, error) {
		data := make([]byte, 0, s.size)
		walk := s.cursor(false)
		for len(data) < s.size {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			span := walk.span()
			data = append(data, span...)
			walk.offset += len(span)
		}
		return PreparePreview(ctx, path, data, tab)
	}
}

// Revision reports the current text revision without copying the buffer.
func (b *Buffer) Revision() uint64 { return b.buf.Revision() }

// LineText returns a source line from the immutable preview, without a terminator.
func (p *PreviewDocument) LineText(line int) string {
	raw, _ := p.table.lineView(line)
	return string(raw)
}

// LineCount returns the number of source lines, including a trailing blank line.
func (p *PreviewDocument) LineCount() int { return p.table.LineCount() }

// TabWidth is the width used by the prepared source geometry.
func (p *PreviewDocument) TabWidth() int { return p.table.tabSize }

// Open mounts one read-only view. Syntax starts only after the owning screen
// accepts the worker result, never for a discarded selection.
func (p *PreviewDocument) Open(th *theme.Theme, path string) *Editor {
	e := New(th)
	e.readOnly = true
	e.open = true
	e.title = filepath.Base(path)
	e.filePath = path
	e.buf = p.table
	e.folds, e.language = p.folds, p.language
	e.buffer = &Buffer{ID: e.buffer.ID, Path: path, Title: e.title, buf: p.table}
	e.hLineWidths = p.widths
	e.hDocumentWidth = p.widest
	e.hWidthRevision = e.Revision()
	e.cursorBlink.MarkActivity()
	e.initChroma()
	return e
}

// DoubleClickPosition returns the original source cell of a word press. The
// selection's end may differ for a tab, a bidi run or a wide grapheme.
func (e *Editor) DoubleClickPosition() (Position, bool) {
	return e.lastClickPos, e.clickCount == 2 && e.drag.kind == pointerWord && !e.drag.moved
}
