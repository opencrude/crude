package editor

import (
	"errors"
	"fmt"

	"crude/lspedit"
	"crude/types"
)

type editorInlineLine struct {
	*inlineProjection
	clusters   []string
	widths     []int
	insertions []InlineInsertion
}

// The editor owns only a source-to-cell projection. The canonical coordinator
// owns provider/context evidence and originals; the shell owns request lifetime.
type editorInlayProjection struct {
	source   types.InsightSource
	buffer   *PieceTable
	revision uint64
	lines    map[int]*editorInlineLine
	maxWidth int
	runs     int
}

// SetInlayHints atomically prepares one exact-source range. It changes no text,
// caret, selection, history or savepoint. A valid UTF-16 position inside a source
// grapheme is unrenderable here: it is refused, never clamped to another anchor.
// The shell must already have checked the provider/context and its request owner.
func (e *Editor) SetInlayHints(set types.LSPInlaySet) error {
	if !e.MatchesInsightSource(set.Boundary.Source) || len(set.Hints) > inlineMaxInsertions {
		return errors.New("inlay hints do not identify this acknowledged editor source")
	}
	if set.Answer != types.LSPInlayHints && len(set.Hints) != 0 {
		return errors.New("negative inlay evidence contains labels")
	}
	if set.Answer != types.LSPInlayHints && set.Answer != types.LSPInlayNull &&
		set.Answer != types.LSPInlayEmpty && set.Answer != types.LSPInlayUnsupported {
		return errors.New("unknown inlay answer")
	}
	e.buf.ensureCache()
	byLine := make(map[int][]InlineInsertion)
	labelBytes := 0
	for index, hint := range set.Hints {
		line := hint.Position.Line
		if hint.Index != index || line < 0 || line >= e.buf.LineCount() {
			return errors.New("inlay hint has an invalid original row or source line")
		}
		raw, err := e.buf.lineView(line)
		if err != nil {
			return err
		}
		offset, err := lspedit.Offset(string(raw), 0, hint.Position.Character)
		if err != nil || e.buf.lineOffsets[line]+offset != hint.SourceByte {
			return errors.New("inlay source byte and UTF-16 position disagree")
		}
		labelBytes += len(hint.Label)
		if labelBytes > inlineMaxLabels {
			return errors.New("inlay display labels exceed 64 KiB")
		}
		byLine[line] = append(byLine[line], InlineInsertion{
			ByteOffset: offset, Index: index, Label: hint.Label,
			PaddingLeft: hint.PaddingLeft, PaddingRight: hint.PaddingRight,
		})
	}
	next := &editorInlayProjection{source: set.Boundary.Source, buffer: e.buf,
		revision: e.Revision(), lines: make(map[int]*editorInlineLine)}
	runs := 0
	for line, insertions := range byLine {
		raw, _ := e.buf.lineView(line)
		projection, err := buildInlineProjection(string(raw), insertions)
		if err != nil {
			return err
		}
		runs += len(projection.Runs)
		if runs > inlineMaxRuns {
			return fmt.Errorf("%w: combined inlay projection run bound exceeded", ErrInlinePlacement)
		}
		prepared := &editorInlineLine{inlineProjection: projection,
			clusters: make([]string, len(projection.Runs)), widths: make([]int, len(projection.Runs)),
			insertions: append([]InlineInsertion(nil), insertions...)}
		for i, run := range projection.Runs {
			prepared.clusters[i], prepared.widths[i] = run.Cluster, run.Width
		}
		next.lines[line] = prepared
		next.maxWidth = max(next.maxWidth, projection.DisplayWidth)
	}
	next.runs = runs
	visible := e.inlayCaretVisible()
	e.CancelPointerCapture()
	e.clearGoal()
	e.inlays = next
	e.pendingReveal = e.pendingReveal || visible
	return nil
}

func (e *Editor) ClearInlayHints() {
	if e.inlays == nil {
		return
	}
	visible := e.inlayCaretVisible()
	e.CancelPointerCapture()
	e.clearGoal()
	e.inlays = nil
	e.pendingReveal = e.pendingReveal || visible
}

func (e *Editor) inlayCaretVisible() bool {
	col := e.displayColumn(e.cursor.Line, e.cursor.Col)
	width := max(1, e.viewW-e.gutterWidth()-e.scrollbarColWidth())
	return e.cursor.Line >= e.scrollY && e.cursor.Line < e.scrollY+e.bodyHeight() &&
		col >= e.scrollX && col < e.scrollX+width
}

func (e *Editor) currentInlays() *editorInlayProjection {
	p := e.inlays
	if p != nil && (p.buffer != e.buf || p.revision != e.Revision() || !e.MatchesInsightSource(p.source)) {
		e.inlays = nil
		return nil
	}
	return p
}

func (e *Editor) inlineLine(line int) *editorInlineLine {
	var base *editorInlineLine
	if state := e.currentInlays(); state != nil {
		base = state.lines[line]
	}
	if ghost := e.currentGhost(); ghost != nil && ghost.caret.Line == line {
		return e.ghostProjection(ghost, base)
	}
	return base
}

func (e *Editor) displayColumn(line, sourceCol int) int {
	if p := e.inlineLine(line); p != nil {
		return p.sourceToDisplay(sourceCol)
	}
	return sourceCol
}

func (e *Editor) sourceColumn(line, displayCol int) int {
	if p := e.inlineLine(line); p != nil {
		return p.displayHit(displayCol).SourceCol
	}
	return displayCol
}

// InlayHintAt identifies only a painted label/padding cell, not a command or
// navigation target. Ordinary clicks still land at its exact source anchor.
func (e *Editor) InlayHintAt(x, y int) (int, bool) {
	if x < e.gutterWidth() || x >= e.viewW-e.scrollbarColWidth() || y < 0 || y >= e.bodyHeight() {
		return 0, false
	}
	if p := e.inlineLine(e.scrollY + y); p != nil {
		hit := p.displayHit(e.scrollX + x - e.gutterWidth())
		return hit.HintIndex, hit.HintIndex >= 0
	}
	return 0, false
}

func (e *Editor) inlineInsertionAt(x, y int) bool {
	if x < e.gutterWidth() || x >= e.viewW-e.scrollbarColWidth() || y < 0 || y >= e.bodyHeight() {
		return false
	}
	if p := e.inlineLine(e.scrollY + y); p != nil {
		return p.insertionAt(e.scrollX + x - e.gutterWidth())
	}
	return false
}
