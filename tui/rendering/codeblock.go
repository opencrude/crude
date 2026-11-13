package rendering

import (
	"charm.land/glamour/v2/ansi"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// ExtractLastCodeBlock shares the renderer's Goldmark parser and code-node
// extraction. Fences, nesting, lists and indented code have one authority.
func ExtractLastCodeBlock(source string) (language, code string, ok bool) {
	data := []byte(source)
	r := newMarkdownRenderer(ansi.Options{})
	node := r.md.Parser().Parse(text.NewReader(data))
	_ = ast.Walk(node, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering && (n.Kind() == ast.KindFencedCodeBlock || n.Kind() == ast.KindCodeBlock) {
			block := ansi.CodeBlock(n, data)
			language, code, ok = block.Language, block.Code, true
		}
		return ast.WalkContinue, nil
	})
	return
}
