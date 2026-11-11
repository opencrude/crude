package editor

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
)

type rowJob struct {
	done                 chan struct{}
	cancel               context.CancelFunc
	buffer               *PieceTable
	revision, generation uint64
	tabWidth             int
	delivering           bool
	result               editorRows
	err                  error
}

// RowsReadyMsg is admitted through the same retained-editor job drain as syntax.
// It never selects a tab, replaces source, or moves a source caret.
type RowsReadyMsg struct {
	editor *Editor
	job    *rowJob
}

func (m RowsReadyMsg) Apply() {
	if m.editor != nil && m.editor.rowJob == m.job {
		m.editor.finishRows()
	}
}
func (m RowsReadyMsg) Editor() *Editor { return m.editor }

func (e *Editor) cancelRows() {
	e.rowGeneration++
	if e.rowJob != nil {
		e.rowJob.cancel()
	}
}
func (e *Editor) StopRows() { e.cancelRows(); e.rowsStopped = true }
func (e *Editor) RowsDone() <-chan struct{} {
	if e.rowJob == nil {
		return nil
	}
	return e.rowJob.done
}
func (e *Editor) RowsCommand() tea.Cmd {
	e.finishRows()
	if e.wordWrap && e.rows.needsCold(e) {
		e.startRows()
	}
	job := e.rowJob
	if job == nil || job.delivering {
		return nil
	}
	job.delivering = true
	return func() tea.Msg { <-job.done; return RowsReadyMsg{e, job} }
}
func (e *Editor) startRows() {
	if !e.open || !e.wordWrap || e.rowsStopped {
		return
	}
	if old := e.rowJob; old != nil {
		if old.buffer != e.buf || old.revision != e.Revision() || old.tabWidth != e.TabWidth() {
			old.cancel()
		}
		return // one owned job; its completion permits the exact replacement
	}
	source := e.buf.snapshotText()
	ctx, cancel := context.WithCancel(context.Background())
	job := &rowJob{done: make(chan struct{}), cancel: cancel, buffer: e.buf, revision: e.Revision(), generation: e.rowGeneration, tabWidth: e.TabWidth()}
	e.rowJob = job
	go func() {
		defer close(job.done)
		defer cancel()
		// Source-sized CPU work shares the existing bounded editor worker pool.
		select {
		case syntaxWorkers <- struct{}{}:
			defer func() { <-syntaxWorkers }()
		case <-ctx.Done():
			job.err = ctx.Err()
			return
		}
		document, err := syntaxSource(ctx, source)
		if err != nil {
			job.err = err
			return
		}
		job.result, job.err = buildSourceRows(ctx, document.text, document.lines, job.tabWidth)
	}()
}
func (e *Editor) finishRows() {
	job := e.rowJob
	if job == nil {
		return
	}
	select {
	case <-job.done:
	default:
		return
	}
	e.rowJob = nil
	current := e.open && e.wordWrap && !e.rowsStopped && job.buffer == e.buf && job.revision == e.Revision() && job.tabWidth == e.TabWidth() && job.generation == e.rowGeneration
	if current && job.err == nil {
		job.result.buffer, job.result.revision, job.result.tabWidth = job.buffer, job.revision, job.tabWidth
		e.rows = job.result
		e.wrapOffset = 0
		e.wrapBefore = false
		e.pendingReveal = true
	}
}

func buildSourceRows(ctx context.Context, text string, offsets []int, tabWidth int) (editorRows, error) {
	p := editorRows{wrapped: true, ghostLine: -1}
	p.reset(len(offsets))
	p.metrics = make([]cachedRowMetrics, len(offsets))
	atomicCount, breakCount := 0, 0
	white := false
	for i := 0; i < len(text); i++ {
		if i%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return p, err
			}
		}
		c := text[i]
		if c >= 0xc0 {
			atomicCount++
		}
		nextWhite := c == ' ' || c == '\t'
		if white && !nextWhite {
			breakCount++
		}
		white = nextWhite
	}
	if white {
		breakCount++
	}
	p.atomicArena = make([]rowSpan, 0, atomicCount)
	p.breakArena = make([]int, 0, breakCount)
	for line, start := range offsets {
		if err := ctx.Err(); err != nil {
			return p, err
		}
		end := len(text)
		if line+1 < len(offsets) {
			end = offsets[line+1] - 1
		}
		raw := strings.TrimSuffix(text[start:end], "\r")
		var err error
		p.metrics[line].source, p.atomicArena, p.breakArena, err = appendRowMetrics(ctx, raw, tabWidth, p.atomicArena, p.breakArena)
		if err != nil {
			return p, err
		}
		p.metrics[line].current = true
		p.measures++
	}
	return p, nil
}
