package editor

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"

	"crude/tui/rendering"
)

// Syntax is source-sized work, never frame-sized work. Only two documents lex
// concurrently across all editor views; a superseded job cancels before the next
// snapshot is admitted. Colour maps are built lazily for the visible lines.
var syntaxWorkers = make(chan struct{}, 2)

type syntaxSpan struct {
	end  int // exclusive source byte offset
	kind chroma.TokenType
}

type syntaxDocument struct {
	text  string
	lines []int
	spans []syntaxSpan
}

type syntaxJob struct {
	done                 chan struct{}
	cancel               context.CancelFunc
	buffer               *PieceTable
	revision, generation uint64
	delivering           bool
	document             *syntaxDocument
	err                  error
}

// SyntaxReadyMsg is exact-editor/exact-job completion, not keyboard-focus input.
// Apply is safe after a close, theme change, replacement or newer source edit.
type SyntaxReadyMsg struct {
	editor *Editor
	job    *syntaxJob
}

func (msg SyntaxReadyMsg) Apply() {
	if msg.editor != nil && msg.editor.syntaxJob == msg.job {
		msg.editor.finishSyntax()
	}
}

func (msg SyntaxReadyMsg) Editor() *Editor { return msg.editor }

func (msg SyntaxReadyMsg) Failure() string {
	if msg.editor == nil || !msg.editor.open || msg.job.generation != msg.editor.syntaxGeneration || msg.job.revision != msg.editor.Revision() || msg.job.err == nil {
		return ""
	}
	return fmt.Sprintf("Syntax highlighting unavailable for %s; showing plain text: %v", msg.editor.BaseTitle(), msg.job.err)
}

func (e *Editor) initChroma() {
	e.chromaStyle = rendering.SourceStyle(e.th)
	path := e.filePath
	if path == "" {
		path = e.title
	}
	l := lexers.Match(path)
	if l == nil || l.Config().Name == "fallback" {
		l = lexers.Fallback
	}
	// Chroma's coalescer repeatedly concatenates strings. Retain the original
	// iterator and merge adjacent token spans by offsets instead (linear memory).
	e.chromaLexer = l
	e.syntaxPlain = l.Config().Name == lexers.Fallback.Config().Name
	e.invalidateSyntax()
	e.syntaxStopped = false
	e.startSyntax()
}

func (e *Editor) invalidateSyntax() {
	e.syntaxGeneration++
	if e.syntaxJob != nil {
		e.syntaxJob.cancel()
	}
	e.syntaxDocument = nil
	e.chromaLineCache = nil
	e.syntaxFailed = false
}

func (e *Editor) startSyntax() {
	if !e.open || e.syntaxStopped || e.syntaxPlain || e.chromaLexer == nil || e.chromaStyle == nil || e.syntaxJob != nil || e.syntaxFailed {
		return
	}
	if e.syntaxBuffer == e.buf && e.syntaxRevision == e.buf.Revision() && e.syntaxDocument != nil {
		return
	}
	snapshot := e.buf.snapshotText()
	lexer := e.chromaLexer
	ctx, cancel := context.WithCancel(context.Background())
	job := &syntaxJob{done: make(chan struct{}), cancel: cancel, buffer: e.buf, revision: e.buf.Revision(), generation: e.syntaxGeneration}
	e.syntaxJob = job
	go func() {
		defer close(job.done)
		defer cancel()
		select {
		case syntaxWorkers <- struct{}{}:
			defer func() { <-syntaxWorkers }()
		case <-ctx.Done():
			job.err = ctx.Err()
			return
		}
		// Queueing behind another document is not time spent preparing this
		// source. Keep the execution budget independent of scheduler contention.
		work, stop := context.WithTimeout(ctx, 5*time.Second)
		defer stop()
		job.document, job.err = lexSyntax(work, lexer, snapshot)
	}()
}

func (e *Editor) finishSyntax() {
	job := e.syntaxJob
	if job == nil {
		return
	}
	select {
	case <-job.done:
	default:
		return
	}
	e.syntaxJob = nil
	if !e.open || job.buffer != e.buf || job.revision != e.buf.Revision() || job.generation != e.syntaxGeneration {
		return
	}
	e.chromaLineCache = nil
	e.syntaxBuffer, e.syntaxRevision = job.buffer, job.revision
	e.syntaxDocument = job.document
	e.syntaxFailed = job.err != nil
	if job.err != nil {
		e.syntaxDocument = nil
	}
}

// SyntaxCommand admits or joins one owned parse and delivers its completion to
// Update. It never exports or lexes the document on the caller/UI goroutine.
func (e *Editor) SyntaxCommand() tea.Cmd {
	e.startSyntax()
	job := e.syntaxJob
	if job == nil || job.delivering {
		return nil
	}
	job.delivering = true
	return func() tea.Msg { <-job.done; return SyntaxReadyMsg{editor: e, job: job} }
}

// SyntaxDone exposes the exact admitted job for the shell's shutdown barrier.
// A nil channel means no syntax work is owned by this editor.
func (e *Editor) SyntaxDone() <-chan struct{} {
	if e.syntaxJob == nil {
		return nil
	}
	return e.syntaxJob.done
}

func (e *Editor) StopSyntax() { e.invalidateSyntax(); e.syntaxStopped = true }

func lexSyntax(ctx context.Context, lexer chroma.Lexer, source textSnapshot) (document *syntaxDocument, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			document = nil
			err = fmt.Errorf("syntax lexer failed: %v", recovered)
		}
	}()
	var text strings.Builder
	text.Grow(source.size)
	cursor := source.cursor(false)
	for span := cursor.span(); len(span) > 0; span = cursor.span() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		text.Write(span)
		cursor.offset += len(span)
	}
	document = &syntaxDocument{text: text.String(), lines: []int{0}}
	for index := range document.text {
		if document.text[index] == '\n' {
			document.lines = append(document.lines, index+1)
		}
	}
	iterator, err := lexer.Tokenise(&chroma.TokeniseOptions{State: "root"}, document.text)
	if err != nil {
		return nil, err
	}
	offset := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		token := iterator()
		if token == chroma.EOF {
			break
		}
		offset = min(len(document.text), offset+len(token.Value))
		if n := len(document.spans); n > 0 && document.spans[n-1].kind == token.Type {
			document.spans[n-1].end = offset
		} else {
			document.spans = append(document.spans, syntaxSpan{end: offset, kind: token.Type})
		}
	}
	return document, ctx.Err()
}

// Lexical context is always the full immutable document. The viewport selects
// only which compact source spans to project into grapheme/tab cell colours.
func (e *Editor) ensureChromaColors(fromLine, toLine int) {
	e.finishSyntax()
	e.startSyntax()
	doc := e.syntaxDocument
	if doc == nil || e.syntaxBuffer != e.buf || e.syntaxRevision != e.buf.Revision() {
		return
	}
	fromLine, toLine = max(0, fromLine), min(len(doc.lines), toLine)
	if e.chromaLineCache == nil {
		e.chromaLineCache = make(map[int][]string)
	}
	if len(e.chromaLineCache) > max(512, (toLine-fromLine)*2) {
		clear(e.chromaLineCache)
	}
	for line := fromLine; line < toLine; line++ {
		if e.chromaLineCache[line] != nil {
			continue
		}
		start, end := doc.lines[line], len(doc.text)
		if line+1 < len(doc.lines) {
			end = doc.lines[line+1] - 1
			if end > start && doc.text[end-1] == '\r' {
				end--
			}
		}
		raw := doc.text[start:end]
		cm := make([]string, LineVisualWidth(raw, tabWidth))
		spanIndex := sort.Search(len(doc.spans), func(i int) bool { return doc.spans[i].end > start })
		col, offset := 0, start
		for rest := raw; rest != ""; {
			for spanIndex < len(doc.spans) && doc.spans[spanIndex].end <= offset {
				spanIndex++
			}
			colour := ""
			if spanIndex < len(doc.spans) {
				colour = e.chromaTokenColor(doc.spans[spanIndex].kind)
			}
			cluster, width := rendering.FirstGrapheme(rest)
			if rest[0] == '\t' {
				cluster, width = rest[:1], TabStopAdvance(col, tabWidth)
			}
			if len(cluster) == 0 {
				break
			}
			for c := col; c < col+width; c++ {
				cm[c] = colour
			}
			col, offset, rest = col+width, offset+len(cluster), rest[len(cluster):]
		}
		e.chromaLineCache[line] = cm
	}
}

func (e *Editor) lineColorMap(lineIdx int) []string {
	if e.syntaxBuffer != e.buf || e.syntaxRevision != e.buf.Revision() {
		return nil
	}
	return e.chromaLineCache[lineIdx]
}

func (e *Editor) chromaTokenColor(tt chroma.TokenType) string {
	entry := e.chromaStyle.Get(tt)
	if entry.Colour.IsSet() {
		return entry.Colour.String()
	}
	return ""
}
