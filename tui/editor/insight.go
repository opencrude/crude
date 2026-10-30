package editor

import (
	"sort"

	"crude/syntax"
	"crude/tui/rendering"
	"crude/types"

	"github.com/alecthomas/chroma/v2"
)

// InstallDocumentInsight caches an immutable canonical publication on Update.
// The independent text stream may lag it; projection waits for the exact source.
// Stale or future insight never changes text, undo, diagnostics or cursor state.
func (e *Editor) InstallDocumentInsight(view types.DocumentInsight) bool {
	p := e.documentProjection
	if !e.open || p == nil || view.DocumentID != p.snapshot.ID || view.Generation == "" || view.Version == 0 ||
		view.Revision < p.snapshot.Revision {
		return false
	}
	if old := e.canonicalInsight; old != nil && view.Generation == old.Generation && view.Version <= old.Version {
		return false
	}
	e.canonicalInsight = &view
	// Do not cancel an optimistic source job or clear mapped decorations when
	// the independent canonical topic overtakes its text/receipt. The first
	// exact-revision projection below performs that handoff.
	return true
}

func (e *Editor) canonicalSyntaxSource() bool {
	p := e.documentProjection
	if p == nil || len(p.pending) != 0 || p.inflight != nil || p.conflict != nil {
		return false
	}
	_, supported := syntax.LanguageForPath(e.filePath)
	return supported
}

// InsightStatus is coverage-qualified syntax status, not a diagnostic count.
// A private draft is explicitly provisional even if the last shared result was
// current. Reading status never starts work or performs I/O.
func (e *Editor) InsightStatus() (types.InsightState, string) {
	if e.documentProjection == nil {
		return "", ""
	}
	if !e.canonicalSyntaxSource() {
		if _, supported := syntax.LanguageForPath(e.filePath); supported {
			return types.InsightProvisional, "Provisional syntax for unacknowledged local text"
		}
	}
	view := e.canonicalInsight
	if view == nil || view.Revision != e.documentProjection.snapshot.Revision || view.Path != e.filePath {
		return types.InsightPending, "Waiting for shared syntax"
	}
	return view.State, view.Detail
}

func (e *Editor) ensureCanonicalColors(fromLine, toLine int) {
	view := e.canonicalInsight
	p := e.documentProjection
	if view == nil || view.State != types.InsightCurrent || view.Syntax == nil ||
		view.Path != e.filePath || view.Revision != p.snapshot.Revision || view.Syntax.Revision != view.Revision {
		return
	}
	if e.canonicalInk != view {
		e.cancelSyntax()
		clear(e.chromaLineCache)
		e.syntaxBuffer, e.syntaxRevision = e.buf, e.buf.Revision()
		e.canonicalInk = view
	}
	if e.syntaxBuffer != e.buf || e.syntaxRevision != e.buf.Revision() {
		e.invalidateEditedSyntax()
	}
	e.buf.ensureCache()
	fromLine, toLine = max(0, fromLine), min(len(e.buf.lineOffsets), toLine)
	if e.chromaLineCache == nil {
		e.chromaLineCache = make(map[int]syntaxLine)
	}
	if len(e.chromaLineCache) > max(512, (toLine-fromLine)*2) {
		clear(e.chromaLineCache)
	}
	spans := view.Syntax.Spans
	for line := fromLine; line < toLine; line++ {
		if e.chromaLineCache[line].colours != nil {
			continue
		}
		rawBytes, _ := e.buf.lineView(line)
		raw := string(rawBytes)
		start := e.buf.lineOffsets[line]
		index := sort.Search(len(spans), func(i int) bool { return int(spans[i].End) > start })
		e.chromaLineCache[line] = e.projectSyntaxLine(raw, start, func(offset int) chroma.TokenType {
			for index < len(spans) && int(spans[index].End) <= offset {
				index++
			}
			if index < len(spans) {
				return rendering.SyntaxToken(spans[index].Kind)
			}
			return chroma.Text
		})
	}
}
