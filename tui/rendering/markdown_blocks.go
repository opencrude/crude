package rendering

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// MarkdownBlock is one top-level block of a parsed document. Every block
// renders on its own through the same renderer as a chat message, so a
// document preview renders again only the blocks whose source changed, and
// each painted row maps back to the source lines it came from.
type MarkdownBlock struct {
	Document MarkdownDocument
	// Line is the zero-based source line where the block starts. The block
	// owns every line up to the next block's Line.
	Line int
	// Key is equal for two blocks that render identically at one width and
	// theme: the block's source and any link destinations resolved into it.
	Key string
}

// MarkdownBlocks is a parsed document split into its top-level blocks.
type MarkdownBlocks struct {
	Blocks []MarkdownBlock
	// Lines is the number of source lines.
	Lines int
}

// MarkdownLinks resolves a document's relative links. Base is the slash path
// of the document's folder; file links become absolute paths there, so they
// open the file the author meant. Self is the document's own slash path: a
// heading link (#install) becomes a link to its heading's line.
type MarkdownLinks struct {
	Base, Self string
}

var (
	blockStartsKey   = parser.NewContextKey()
	documentLinksKey = parser.NewContextKey()
)

// ParseBlocks parses once, so link definitions resolve across the whole
// document, then splits it into its top-level blocks in source order.
func (r *MarkdownRenderer) ParseBlocks(in string, links MarkdownLinks) MarkdownBlocks {
	source := []byte(in)
	pc := parser.NewContext()
	starts := make(map[ast.Node]int)
	pc.Set(blockStartsKey, starts)
	lineStarts := []int{0}
	for i, b := range source {
		if b == '\n' {
			lineStarts = append(lineStarts, i+1)
		}
	}
	lineOf := func(offset int) int { return sort.SearchInts(lineStarts, offset+1) - 1 }
	if links.Base != "" || links.Self != "" {
		pc.Set(documentLinksKey, documentLinkContext{links, lineOf})
	}
	root := r.md.Parser().Parse(text.NewReader(source), parser.WithContext(pc))

	var nodes []ast.Node
	var offsets []int
	for node := root.FirstChild(); node != nil; node = node.NextSibling() {
		offset := len(source)
		if start, ok := starts[node]; ok {
			offset = start
		}
		// A paragraph transformer (tables) or the setext heading parser can
		// replace the node that was opened. Its own content then names the
		// first line, exactly as the parser read it.
		offset = min(offset, firstSegment(node, len(source)))
		// A block owns whole lines, from the line it starts on.
		line := lineOf(min(offset, max(0, len(source)-1)))
		if len(offsets) > 0 {
			line = max(line, lineOf(offsets[len(offsets)-1]))
		}
		nodes = append(nodes, node)
		offsets = append(offsets, lineStarts[line])
	}
	out := MarkdownBlocks{Blocks: make([]MarkdownBlock, 0, len(nodes)), Lines: len(lineStarts)}
	for i, node := range nodes {
		start, end := offsets[i], len(source)
		if i+1 < len(offsets) {
			end = offsets[i+1]
		}
		doc := ast.NewDocument()
		doc.AppendChild(doc, node) // detaches node from root
		out.Blocks = append(out.Blocks, MarkdownBlock{
			Document: MarkdownDocument{source: source, node: doc},
			Line:     lineOf(start),
			Key:      blockKey(node, source[start:end]),
		})
	}
	return out
}

// blockKey includes resolved link values: a reference definition can change
// outside the block, and a heading target can move without its link changing.
func blockKey(block ast.Node, source []byte) string {
	hash := sha256.New()
	writePart := func(part []byte) {
		var length [24]byte
		hash.Write(strconv.AppendInt(length[:0], int64(len(part)), 10))
		hash.Write([]byte{':'})
		hash.Write(part)
	}
	writePart([]byte(block.Kind().String()))
	writePart(source)
	_ = ast.Walk(block, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			switch n := n.(type) {
			case *ast.Link:
				writePart(n.Destination)
				writePart(n.Title)
			case *ast.Image:
				writePart(n.Destination)
				writePart(n.Title)
			}
		}
		return ast.WalkContinue, nil
	})
	return hex.EncodeToString(hash.Sum(nil))
}

// firstSegment returns the smallest source offset any node below n holds.
func firstSegment(n ast.Node, limit int) int {
	first := limit
	_ = ast.Walk(n, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if node.Type() == ast.TypeBlock {
			if lines := node.Lines(); lines != nil && lines.Len() > 0 {
				first = min(first, lines.At(0).Start)
			}
			if code, ok := node.(*ast.FencedCodeBlock); ok && code.Info != nil {
				first = min(first, code.Info.Segment.Start)
			}
		}
		if t, ok := node.(*ast.Text); ok && t.Segment.Stop > t.Segment.Start {
			first = min(first, t.Segment.Start)
		}
		return ast.WalkContinue, nil
	})
	return first
}

// blockStart reports where each top-level block opens. Goldmark keeps no
// block positions, so the shared parser wraps its block parsers: Open sees
// the reader at the line that opens the block. Parses without a position map
// (every chat message) only pay one context lookup per top-level block.
type blockStart struct{ parser.BlockParser }

func (b blockStart) Open(parent ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	starts, _ := pc.Get(blockStartsKey).(map[ast.Node]int)
	if starts == nil || parent.Kind() != ast.KindDocument {
		return b.BlockParser.Open(parent, reader, pc)
	}
	_, segment := reader.PeekLine()
	node, state := b.BlockParser.Open(parent, reader, pc)
	if node != nil {
		starts[node] = segment.Start
	}
	return node, state
}

// SetOption forwards parser options such as automatic heading IDs.
func (b blockStart) SetOption(name parser.OptionName, value any) {
	if setter, ok := b.BlockParser.(parser.SetOptioner); ok {
		setter.SetOption(name, value)
	}
}

func withBlockStarts(values []util.PrioritizedValue) []util.PrioritizedValue {
	out := make([]util.PrioritizedValue, len(values))
	for i, value := range values {
		out[i] = util.Prioritized(blockStart{value.Value.(parser.BlockParser)}, value.Priority)
	}
	return out
}

// documentLinks resolves a document's relative destinations. Chat messages
// have no document folder and keep their links as written.
type documentLinks struct{}

type documentLinkContext struct {
	links  MarkdownLinks
	lineOf func(int) int
}

func (documentLinks) Transform(doc *ast.Document, _ text.Reader, pc parser.Context) {
	ctx, ok := pc.Get(documentLinksKey).(documentLinkContext)
	if !ok {
		return
	}
	headings := make(map[string]int)
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if heading, ok := node.(*ast.Heading); ok && entering {
			if id, ok := heading.AttributeString("id"); ok {
				if name, ok := id.([]byte); ok && heading.Lines().Len() > 0 {
					headings[string(name)] = 1 + ctx.lineOf(heading.Lines().At(0).Start)
				}
			}
		}
		return ast.WalkContinue, nil
	})
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := node.(type) {
		case *ast.Link:
			n.Destination = resolveDocumentLink(n.Destination, ctx.links, headings)
		case *ast.Image:
			n.Destination = resolveDocumentLink(n.Destination, ctx.links, headings)
		}
		return ast.WalkContinue, nil
	})
}

func resolveDocumentLink(destination []byte, links MarkdownLinks, headings map[string]int) []byte {
	value := string(destination)
	if value == "" || strings.HasPrefix(value, "//") || strings.HasPrefix(value, `\\`) {
		return destination
	}
	if fragment, ok := strings.CutPrefix(value, "#"); ok {
		line, found := headings[fragment]
		if !found || links.Self == "" {
			return destination
		}
		return []byte(links.Self + "#L" + strconv.Itoa(line))
	}
	// A drive letter parses as a one-letter URL scheme; it is an absolute path.
	drive := len(value) > 2 && value[1] == ':' && (value[2] == '/' || value[2] == '\\')
	if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" && !drive {
		return destination // a web or mail destination
	}
	if links.Base == "" || drive || strings.HasPrefix(value, "/") {
		return destination
	}
	file, fragment, _ := strings.Cut(value, "#")
	if file == "" {
		return destination
	}
	resolved := path.Join(links.Base, file)
	if fragment != "" {
		resolved += "#" + fragment
	}
	return []byte(resolved)
}
