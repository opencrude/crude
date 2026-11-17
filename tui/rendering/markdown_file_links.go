package rendering

import (
	"bytes"
	"strings"
	"unicode"
	"unicode/utf8"

	"crude/internal/filelink"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// fileReferences runs once in the existing Markdown parse. It changes no source
// bytes and never checks the filesystem. Generated autolinks reuse Glamour's
// link styling, wrapping and source-copy projection.
type fileReferences struct{}

func (fileReferences) Transform(doc *ast.Document, reader text.Reader, _ parser.Context) {
	source := reader.Source()
	var visit func(ast.Node)
	visit = func(parent ast.Node) {
		for node := parent.FirstChild(); node != nil; {
			next := node.NextSibling()
			switch n := node.(type) {
			case *ast.Link, *ast.AutoLink, *ast.Image, *ast.FencedCodeBlock, *ast.CodeBlock:
				// Authored destinations win; code blocks remain literal.
			case *ast.CodeSpan:
				first, ok := n.FirstChild().(*ast.Text)
				if ok && first.NextSibling() == nil && filelink.Candidate(string(first.Segment.Value(source))) {
					parent.ReplaceChild(parent, n, fileAutoLink(first.Segment))
				}
			case *ast.Text:
				// Goldmark can split literal text around an unmatched link
				// opener. Reuse its own merge contract before finding paths.
				for next != nil && n.Segment.Stop > 0 && !n.SoftLineBreak() && !n.HardLineBreak() && n.Merge(next, source) {
					parent.RemoveChild(parent, next)
					next = n.NextSibling()
				}
				linkFileText(parent, n, source)
			default:
				visit(node)
			}
			node = next
		}
	}
	visit(doc)
}

func linkFileText(parent ast.Node, node *ast.Text, source []byte) {
	value := node.Segment.Value(source)
	// Most prose needs no token scan or allocation.
	if !bytes.ContainsAny(value, `/\.`) {
		return
	}
	last := 0
	for start := 0; start < len(value); {
		_, size := utf8.DecodeRune(value[start:])
		if fileBoundary(value[start:]) {
			start += size
			continue
		}
		end := start
		for end < len(value) && !fileBoundary(value[end:]) {
			_, size := utf8.DecodeRune(value[end:])
			end += size
		}
		from, stop := fileTokenBounds(value, start, end)
		token := string(value[from:stop])
		if filelink.Candidate(token) {
			if from > last {
				parent.InsertBefore(parent, node, ast.NewTextSegment(text.NewSegment(node.Segment.Start+last, node.Segment.Start+from)))
			}
			segment := text.NewSegment(node.Segment.Start+from, node.Segment.Start+stop)
			parent.InsertBefore(parent, node, fileAutoLink(segment))
			last = stop
		}
		start = end
	}
	if last == 0 {
		return
	}
	// Preserve line-break flags on the final text node, including an empty tail.
	node.Segment = text.NewSegment(node.Segment.Start+last, node.Segment.Stop)
}

func fileAutoLink(segment text.Segment) *ast.AutoLink {
	node := ast.NewAutoLink(ast.AutoLinkURL, ast.NewTextSegment(segment))
	node.SetAttributeString("crude-source-reference", true)
	return node
}

func fileBoundary(value []byte) bool {
	r, _ := utf8.DecodeRune(value)
	return unicode.IsSpace(r) || strings.ContainsRune("<>\"'`,;", r)
}

func fileTokenBounds(value []byte, start, end int) (int, int) {
	for end > start && strings.ContainsRune(".,;:!?", rune(value[end-1])) {
		end--
	}
	if end-start < 2 {
		return start, end
	}
	var close byte
	switch value[start] {
	case '(':
		close = ')'
	case '[':
		close = ']'
	case '{':
		close = '}'
	}
	if close == 0 || value[end-1] != close {
		return start, end
	}
	// Strip one enclosing prose pair, not parentheses inside a real filename
	// or a route such as app/(auth)/page.tsx. One linear pass bounds the work.
	depth := 0
	for i := start; i < end; i++ {
		if value[i] == value[start] {
			depth++
		} else if value[i] == close {
			depth--
			if depth == 0 {
				if i == end-1 {
					return start + 1, end - 1
				}
				break
			}
		}
	}
	return start, end
}
