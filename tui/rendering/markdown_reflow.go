package rendering

import (
	"github.com/charmbracelet/x/ansi"
	"github.com/yuin/goldmark/ast"
)

type paragraphTrace struct {
	markdownTrace
	paragraphs []string
}

func (t *paragraphTrace) CaptureParagraph(line string) {
	t.paragraphs = append(t.paragraphs, line)
}

// CaptureParagraphs returns unwrapped styled runs for a document made only
// of top-level paragraphs. Containers (including tables and their cells)
// continue to own their structure, decoration and width in Glamour.
func (r *MarkdownRenderer) CaptureParagraphs(doc MarkdownDocument) ([]TextProjection, error) {
	if doc.node.FirstChild() == nil {
		return nil, nil
	}
	for child := doc.node.FirstChild(); child != nil; child = child.NextSibling() {
		if child.Kind() != ast.KindParagraph {
			return nil, nil
		}
	}
	trace := &paragraphTrace{}
	if _, err := r.renderParsed(doc, trace); err != nil {
		return nil, err
	}
	trace.projection("")
	paragraphs := make([]TextProjection, len(trace.paragraphs))
	for i, line := range trace.paragraphs {
		paragraphs[i] = TextProjection{ANSI: line, trace: &trace.markdownTrace}
	}
	return paragraphs, nil
}

type ParagraphWrapRange = ansi.WrapLineRange

// WrapParagraphRanges uses the same line break and ANSI token handling as
// Glamour's paragraph wrapper, with byte offsets into the unwrapped run text.
func WrapParagraphRanges(dst []ParagraphWrapRange, annotated string, width int) ([]ParagraphWrapRange, bool) {
	return ansi.AppendWrapLineRanges(dst, annotated, width, "")
}
