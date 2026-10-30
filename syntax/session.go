package syntax

import (
	"bytes"
	"context"
	"fmt"
	"unicode/utf8"

	ts "github.com/tree-sitter/go-tree-sitter"
)

// Session owns one grammar/parser/tree. One worker must serialize Parse and
// Close; no native pointer crosses its boundary. Call these methods off UI and
// actor goroutines. Results may be published only by their revision owner.
type Session struct {
	parser   *ts.Parser
	query    *ts.Query
	tree     *ts.Tree
	text     []byte
	revision uint64
	identity Identity
}

func New(id LanguageID) (*Session, error) {
	g, err := bundled(id)
	if err != nil {
		return nil, err
	}
	parser := ts.NewParser()
	if err := parser.SetLanguage(g.language); err != nil {
		parser.Close()
		return nil, fmt.Errorf("syntax grammar ABI: %w", err)
	}
	query, queryErr := ts.NewQuery(g.language, g.query)
	if queryErr != nil {
		parser.Close()
		return nil, fmt.Errorf("syntax highlight query: %w", queryErr)
	}
	return &Session{parser: parser, query: query, identity: g.identity}, nil
}

func (s *Session) Close() {
	if s == nil || s.parser == nil {
		return
	}
	s.tree.Close()
	s.query.Close()
	s.parser.Close()
	s.tree, s.query, s.parser, s.text = nil, nil, nil, nil
}

// Parse accepts a complete immutable source revision. It owns a copy and edits
// a clone of the prior tree, so cancellation never corrupts the accepted base.
// Tree reuse is incremental; capture derivation is currently whole-document
// and is measured separately from keystroke-to-pixel latency.
func (s *Session) Parse(ctx context.Context, revision uint64, source []byte) (result Result, err error) {
	if s == nil || s.parser == nil {
		return Result{}, ErrClosed
	}
	if revision == 0 || revision <= s.revision {
		return Result{}, ErrRevision
	}
	if len(source) > MaxDocumentBytes {
		return Result{}, ErrLimit
	}
	if !utf8.Valid(source) {
		return Result{}, ErrInvalidText
	}
	ctx, cancel := context.WithTimeout(ctx, PreparationLimit)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	text := bytes.Clone(source)
	var old *ts.Tree
	if s.tree != nil {
		old = s.tree.Clone()
		defer old.Close()
		if edit, changed := changedRegion(s.text, text); changed {
			old.Edit(&edit)
		}
	}
	tree := s.parser.ParseWithOptions(func(offset int, _ ts.Point) []byte {
		if offset >= len(text) {
			return nil
		}
		return text[offset:min(offset+16<<10, len(text))]
	}, old, &ts.ParseOptions{ProgressCallback: func(ts.ParseState) bool { return ctx.Err() != nil }})
	if tree == nil {
		s.parser.Reset()
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		return Result{}, fmt.Errorf("native syntax parse returned no tree")
	}
	accepted := false
	defer func() {
		if !accepted {
			tree.Close()
			s.parser.Reset()
		}
	}()
	root := tree.RootNode()
	result = Result{Revision: revision, Identity: s.identity, HasError: root.HasError()}
	result.Spans, err = s.highlights(ctx, root, text)
	if err != nil {
		return Result{}, err
	}
	if result.HasError {
		result.Problems, result.ProblemsTruncated, err = syntaxProblems(ctx, root)
		if err != nil {
			return Result{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	s.tree.Close()
	s.tree, s.text, s.revision = tree, text, revision
	accepted = true
	return result, nil
}

func (s *Session) highlights(ctx context.Context, root *ts.Node, text []byte) ([]Span, error) {
	cursor := ts.NewQueryCursor()
	defer cursor.Close()
	cursor.SetMatchLimit(8192)
	matches := cursor.MatchesWithOptions(s.query, root, text, ts.QueryCursorOptions{
		ProgressCallback: func(ts.QueryCursorState) bool { return ctx.Err() != nil },
	})
	names := s.query.CaptureNames()
	var captures []capture
	for match := matches.Next(); match != nil; match = matches.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Local-scope predicates need a locals owner. Until it exists, do not
		// pretend a name is a builtin despite possible shadowing.
		if len(s.query.PropertyPredicates(match.PatternIndex)) != 0 {
			continue
		}
		for _, c := range match.Captures {
			kind, ok := captureKind(names[c.Index])
			start, end := c.Node.StartByte(), c.Node.EndByte()
			if !ok || start == end {
				continue
			}
			if end > uint(len(text)) || start > end {
				return nil, fmt.Errorf("native highlight range outside source")
			}
			if len(captures) == MaxCaptures {
				return nil, ErrLimit
			}
			captures = append(captures, capture{uint32(start), uint32(end), match.PatternIndex, kind})
		}
	}
	if cursor.DidExceedMatchLimit() {
		return nil, fmt.Errorf("%w: native query match budget", ErrLimit)
	}
	return flatten(ctx, captures, uint32(len(text)))
}

func syntaxProblems(ctx context.Context, root *ts.Node) ([]Problem, bool, error) {
	cursor := root.Walk()
	defer cursor.Close()
	var problems []Problem
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		node := cursor.Node()
		problem := node.IsError() || node.IsMissing()
		if problem {
			if len(problems) == MaxProblems {
				return problems, true, nil
			}
			problems = append(problems, Problem{Start: uint32(node.StartByte()), End: uint32(node.EndByte()), Missing: node.IsMissing()})
		}
		if !problem && node.HasError() && cursor.GotoFirstChild() {
			continue
		}
		for !cursor.GotoNextSibling() {
			if !cursor.GotoParent() {
				return problems, false, nil
			}
		}
	}
}

func changedRegion(old, current []byte) (ts.InputEdit, bool) {
	prefix := 0
	for prefix < min(len(old), len(current)) && old[prefix] == current[prefix] {
		prefix++
	}
	if prefix == len(old) && prefix == len(current) {
		return ts.InputEdit{}, false
	}
	// Common byte prefixes/suffixes can split the encoding of a changed rune.
	for prefix > 0 && prefix < len(old) && !utf8.RuneStart(old[prefix]) {
		prefix--
	}
	suffix := 0
	for suffix < min(len(old), len(current))-prefix && old[len(old)-1-suffix] == current[len(current)-1-suffix] {
		suffix++
	}
	for suffix > 0 && !utf8.RuneStart(old[len(old)-suffix]) {
		suffix--
	}
	start, oldEnd, newEnd := prefix, len(old)-suffix, len(current)-suffix
	return ts.InputEdit{
		StartByte: uint(start), OldEndByte: uint(oldEnd), NewEndByte: uint(newEnd),
		StartPosition: sourcePoint(old, start), OldEndPosition: sourcePoint(old, oldEnd),
		NewEndPosition: sourcePoint(current, newEnd),
	}, true
}

func sourcePoint(text []byte, offset int) ts.Point {
	prefix := text[:offset]
	return ts.Point{Row: uint(bytes.Count(prefix, []byte{'\n'})), Column: uint(offset - bytes.LastIndexByte(prefix, '\n') - 1)}
}
