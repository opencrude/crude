package syntax

import (
	"cmp"
	"context"
	ts "github.com/tree-sitter/go-tree-sitter"
	"slices"
)

// BracketIndex runs only in the existing source-analysis worker. The lexical
// owner supplies excluded string/comment positions; no language is guessed here.
// Endpoints are detached offsets, never native handles or terminal positions.
func BracketIndex(ctx context.Context, source []byte, excluded func(int) bool) ([]Bracket, bool, error) {
	var stack []int
	var pairs []Bracket
	for offset, b := range source {
		if offset%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, false, err
			}
		}
		switch b {
		case '(', '[', '{', ')', ']', '}':
		default:
			continue
		}
		if excluded(offset) {
			continue
		}
		switch b {
		case '(', '[', '{':
			if len(stack) == MaxStructure {
				return nil, true, nil
			}
			stack = append(stack, offset)
		default:
			if len(stack) == 0 {
				continue
			}
			start := stack[len(stack)-1]
			left := source[start]
			matched := left == '(' && b == ')' || left == '[' && b == ']' || left == '{' && b == '}'
			if !matched {
				stack = stack[:0]
				continue
			}
			stack = stack[:len(stack)-1]
			if len(pairs) == MaxStructure {
				return nil, true, nil
			}
			pairs = append(pairs, Bracket{Offset: uint32(start), Mate: uint32(offset)}, Bracket{Offset: uint32(offset), Mate: uint32(start)})
		}
	}
	slices.SortFunc(pairs, func(a, b Bracket) int { return cmp.Compare(a.Offset, b.Offset) })
	return pairs, false, ctx.Err()
}

func selectionRanges(ctx context.Context, root *ts.Node) ([]Range, bool, error) {
	cursor := root.Walk()
	defer cursor.Close()
	var ranges []Range
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		node := cursor.NodeValue()
		if node.IsNamed() && !node.IsError() && !node.IsMissing() && node.StartByte() < node.EndByte() {
			candidate := Range{Start: uint32(node.StartByte()), End: uint32(node.EndByte())}
			if len(ranges) == 0 || ranges[len(ranges)-1] != candidate {
				if len(ranges) == MaxStructure {
					return ranges, true, nil
				}
				ranges = append(ranges, candidate)
			}
		}
		if cursor.GotoFirstChild() {
			continue
		}
		for !cursor.GotoNextSibling() {
			if !cursor.GotoParent() {
				return ranges, false, nil
			}
		}
	}
}

func (s *Session) structure(ctx context.Context, root *ts.Node, text []byte, result *Result) error {
	var err error
	result.Selections, result.StructureTruncated, err = selectionRanges(ctx, root)
	if err != nil {
		return err
	}
	index := 0
	var truncated bool
	result.Brackets, truncated, err = BracketIndex(ctx, text, func(offset int) bool {
		for index < len(result.Spans) && result.Spans[index].End <= uint32(offset) {
			index++
		}
		if index == len(result.Spans) {
			return true
		}
		kind := result.Spans[index].Kind
		return kind == String || kind == Comment
	})
	result.StructureTruncated = result.StructureTruncated || truncated
	return err
}
