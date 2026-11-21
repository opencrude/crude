package ansi

import (
	"html"
	"strings"

	"github.com/yuin/goldmark/ast"
	astext "github.com/yuin/goldmark/extension/ast"
)

type blockDirection uint8

const (
	noBlockDirection blockDirection = iota
	leftToRight
	rightToLeft
)

func (s BlockStack) direction() blockDirection {
	for i := len(s) - 1; i >= 0; i-- {
		if dir := s[i].direction; dir != noBlockDirection {
			return dir
		}
	}
	return leftToRight
}

// scanContainerDirections records each list and quote's first strong prose
// direction in one walk. Mark a semantic block once even when it contributes
// to several enclosing containers. An unset direction inherits at render time.
func scanContainerDirections(container ast.Node, source []byte, trace SourceTrace, directions map[ast.Node]blockDirection) {
	var stack []ast.Node
	var prose strings.Builder
	flush := func() {
		if prose.Len() == 0 {
			return
		}
		rtl, strong := trace.Direction(prose.String())
		prose.Reset()
		if !strong {
			return
		}
		dir := leftToRight
		if rtl {
			dir = rightToLeft
		}
		for i := len(stack) - 1; i >= 0 && directions[stack[i]] == noBlockDirection; i-- {
			directions[stack[i]] = dir
		}
	}
	_ = ast.Walk(container, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if node.Type() == ast.TypeBlock {
			// Authored isolate controls don't extend across prose blocks.
			flush()
		}
		switch node.Kind() {
		case ast.KindList, ast.KindBlockquote:
			if entering {
				directions[node] = noBlockDirection
				stack = append(stack, node)
			} else {
				stack = stack[:len(stack)-1]
			}
			return ast.WalkContinue, nil
		}
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node.Kind() {
		case astext.KindTable, ast.KindCodeBlock, ast.KindFencedCodeBlock, ast.KindCodeSpan:
			return ast.WalkSkipChildren, nil
		}
		if len(stack) == 0 || directions[stack[len(stack)-1]] != noBlockDirection {
			if node.Type() == ast.TypeInline {
				return ast.WalkSkipChildren, nil
			}
			return ast.WalkContinue, nil
		}
		switch n := node.(type) {
		case *ast.Text:
			prose.WriteString(escapeReplacer.Replace(html.UnescapeString(string(n.Segment.Value(source)))))
		case *ast.String:
			prose.Write(n.Value)
		case *ast.AutoLink:
			prose.Write(n.Label(source))
		}
		return ast.WalkContinue, nil
	})
}
