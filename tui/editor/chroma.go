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

	"crude/syntax"
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

type syntaxLine struct {
	text    string
	colours []string
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
	l := rendering.MatchLexer(path)
	if l == nil || l.Config().Name == "fallback" {
		l = lexers.Fallback
	}
	// Chroma's coalescer repeatedly concatenates strings. Retain the original
	// iterator and merge adjacent token spans by offsets instead (linear memory).
	e.chromaLexer = l
	_, native := syntax.LanguageForPath(path)
	e.syntaxPlain = !native && l.Config().Name == lexers.Fallback.Config().Name
	e.invalidateSyntax()
	e.syntaxStopped = false
	e.startSyntax()
}

func (e *Editor) invalidateSyntax() {
	e.cancelSyntax()
	e.syntaxBuffer = nil
	e.syntaxRevision = 0
	e.chromaLineCache = nil
	e.chromaLineScratch = nil
}

func (e *Editor) cancelSyntax() {
	e.syntaxGeneration++
	e.canonicalInk = nil
	if e.syntaxJob != nil {
		e.syntaxJob.cancel()
	}
	e.syntaxDocument = nil
	e.syntaxFailed = false
}

// Map retained decorations through exact source edits while the replacement
// document is prepared. They are provisional presentation, not a parse of the
// new revision. Geometry is always current; parsing never restarts from a
// viewport-dependent lexical state.
func (e *Editor) invalidateEditedSyntax() {
	previous, revision := e.syntaxRevision, e.buf.Revision()
	if e.syntaxBuffer != e.buf || revision < previous || revision-previous > uint64(len(e.buf.lineChanges)) {
		clear(e.chromaLineCache)
	} else {
		for next := previous + 1; next <= revision; next++ {
			change := e.buf.lineChanges[next%uint64(len(e.buf.lineChanges))]
			if change.revision != next {
				// Whole-source replacement has no line-preserving mutation.
				clear(e.chromaLineCache)
				break
			}
			if change.delta == 0 {
				if line, ok := e.chromaLineCache[change.start]; ok && change.start == change.end && change.inline {
					e.chromaLineCache[change.start] = mapSyntaxLine(line, change)
					continue
				}
				for line := range e.chromaLineCache {
					if line >= change.start && line <= change.end {
						delete(e.chromaLineCache, line)
					}
				}
				continue
			}
			if e.chromaLineScratch == nil {
				e.chromaLineScratch = make(map[int]syntaxLine, len(e.chromaLineCache))
			}
			clear(e.chromaLineScratch)
			for line, colours := range e.chromaLineCache {
				switch {
				case line < change.start:
					e.chromaLineScratch[line] = colours
				case line > change.end:
					e.chromaLineScratch[line+change.delta] = colours
				}
			}
			e.chromaLineCache, e.chromaLineScratch = e.chromaLineScratch, e.chromaLineCache
			clear(e.chromaLineScratch)
		}
	}
	e.cancelSyntax()
	e.syntaxBuffer, e.syntaxRevision = e.buf, revision
}

func mapSyntaxLine(line syntaxLine, edit bufferLineChange) syntaxLine {
	start, end := edit.byteStart, edit.byteEnd
	if start < 0 || end < start || end > len(line.text) {
		return syntaxLine{}
	}
	raw := line.text[:start] + edit.text + line.text[end:]
	colours := make([]string, LineVisualWidth(raw, tabWidth))
	oldOffset, oldCol, offset, col := 0, 0, 0, 0
	for rest := raw; rest != ""; {
		size, width := firstSourceUnit(rest, col)
		if size == 0 {
			break
		}
		original := offset
		if offset >= start+len(edit.text) {
			original += end - start - len(edit.text)
		} else if offset >= start {
			original = start
		}
		for oldOffset < len(line.text) {
			n, w := firstSourceUnit(line.text[oldOffset:], oldCol)
			if n == 0 || oldOffset+n > original {
				break
			}
			oldOffset, oldCol = oldOffset+n, oldCol+w
		}
		styleCol := oldCol
		if styleCol == len(line.colours) && styleCol > 0 {
			styleCol--
		}
		if styleCol < len(line.colours) {
			for x := col; x < col+width; x++ {
				colours[x] = line.colours[styleCol]
			}
		}
		offset, col, rest = offset+size, col+width, rest[size:]
	}
	return syntaxLine{text: raw, colours: colours}
}

func (e *Editor) startSyntax() {
	if !e.open || e.syntaxStopped || e.syntaxPlain || e.chromaLexer == nil || e.chromaStyle == nil || e.syntaxJob != nil || e.syntaxFailed {
		return
	}
	// Canonical native syntax comes only from the core. Shell workers handle
	// private optimistic source and read-only/non-connected surfaces.
	if e.canonicalSyntaxSource() {
		return
	}
	if e.syntaxBuffer == e.buf && e.syntaxRevision == e.buf.Revision() && e.syntaxDocument != nil {
		return
	}
	snapshot := e.buf.snapshotText()
	lexer := e.chromaLexer
	path := e.filePath
	if path == "" {
		path = e.title
	}
	language, native := syntax.LanguageForPath(path)
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
		if native {
			job.document, job.err = nativeSyntax(work, language, snapshot)
		} else {
			job.document, job.err = lexSyntax(work, lexer, snapshot)
		}
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
	document, err = syntaxSource(ctx, source)
	if err != nil {
		return nil, err
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

func syntaxSource(ctx context.Context, source textSnapshot) (*syntaxDocument, error) {
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
	document := &syntaxDocument{text: text.String(), lines: []int{0}}
	for index := range document.text {
		if document.text[index] == '\n' {
			document.lines = append(document.lines, index+1)
		}
	}
	return document, ctx.Err()
}

func nativeSyntax(ctx context.Context, language syntax.LanguageID, source textSnapshot) (*syntaxDocument, error) {
	document, err := syntaxSource(ctx, source)
	if err != nil {
		return nil, err
	}
	session, err := syntax.New(language)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	result, err := session.Parse(ctx, 1, []byte(document.text))
	if err != nil {
		return nil, err
	}
	document.spans = make([]syntaxSpan, len(result.Spans))
	for i, span := range result.Spans {
		document.spans[i] = syntaxSpan{end: int(span.End), kind: rendering.SyntaxToken(span.Kind)}
	}
	return document, ctx.Err()
}

// Lexical context is always the full immutable document. The viewport selects
// only which compact source spans to project into grapheme/tab cell colours.
func (e *Editor) ensureChromaColors(fromLine, toLine int) {
	e.finishSyntax()
	if e.canonicalSyntaxSource() {
		e.ensureCanonicalColors(fromLine, toLine)
		if e.canonicalInk != nil || e.syntaxDocument == nil {
			return
		}
		// An edit receipt may overtake shared analysis after the private worker
		// completed. Keep its exact-source presentation as provisional ink while
		// waiting, rather than blanking the pane or starting a second core parse.
	} else {
		e.startSyntax()
	}
	doc := e.syntaxDocument
	if doc == nil || e.syntaxBuffer != e.buf || e.syntaxRevision != e.buf.Revision() {
		return
	}
	fromLine, toLine = max(0, fromLine), min(len(doc.lines), toLine)
	if e.chromaLineCache == nil {
		e.chromaLineCache = make(map[int]syntaxLine)
	}
	if len(e.chromaLineCache) > max(512, (toLine-fromLine)*2) {
		clear(e.chromaLineCache)
	}
	for line := fromLine; line < toLine; line++ {
		if e.chromaLineCache[line].colours != nil {
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
		spanIndex := sort.Search(len(doc.spans), func(i int) bool { return doc.spans[i].end > start })
		e.chromaLineCache[line] = e.projectSyntaxLine(raw, start, func(offset int) chroma.TokenType {
			for spanIndex < len(doc.spans) && doc.spans[spanIndex].end <= offset {
				spanIndex++
			}
			if spanIndex < len(doc.spans) {
				return doc.spans[spanIndex].kind
			}
			return chroma.Text
		})
	}
}

// Both canonical byte spans and provisional/lexical spans use one grapheme,
// tab and theme projection. No second native-cell geometry path may drift.
func (e *Editor) projectSyntaxLine(raw string, start int, tokenAt func(int) chroma.TokenType) syntaxLine {
	colours := make([]string, LineVisualWidth(raw, tabWidth))
	col, offset := 0, start
	for rest := raw; rest != ""; {
		colour := e.chromaTokenColor(tokenAt(offset))
		size, width := firstSourceUnit(rest, col)
		if size == 0 {
			break
		}
		for x := col; x < col+width; x++ {
			colours[x] = colour
		}
		col, offset, rest = col+width, offset+size, rest[size:]
	}
	return syntaxLine{text: raw, colours: colours}
}

func (e *Editor) lineColorMap(lineIdx int) []string {
	if e.syntaxBuffer != e.buf || e.syntaxRevision != e.buf.Revision() {
		return nil
	}
	return e.chromaLineCache[lineIdx].colours
}

func (e *Editor) chromaTokenColor(tt chroma.TokenType) string {
	entry := e.chromaStyle.Get(tt)
	if entry.Colour.IsSet() {
		return entry.Colour.String()
	}
	return ""
}
