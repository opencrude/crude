package syntax

import (
	"context"
	"strings"
	"testing"

	ts "github.com/tree-sitter/go-tree-sitter"
)

// Regression for the Linux editor/file-flow SIGABRT in ts_parser_parse.
// Upstream tree-sitter 9ad096ef uses an unchanged progress offset to cancel in
// subtree balancing. No timer or scheduling race is needed to reach the defect.
func TestSyntaxResetAfterCancelledBalancing(t *testing.T) {
	session := newTestSession(t, JavaScript)
	source := []byte(strings.Repeat("function() {}\n", 100))
	read := func(offset int, _ ts.Point) []byte {
		if offset >= len(source) {
			return nil
		}
		return source[offset:]
	}
	var offset uint32
	inBalancing := false
	tree := session.parser.ParseWithOptions(read, nil, &ts.ParseOptions{
		ProgressCallback: func(state ts.ParseState) bool {
			if state.CurrentByteOffset != offset {
				offset = state.CurrentByteOffset
				return false
			}
			inBalancing = true
			return true
		},
	})
	if tree != nil {
		tree.Close()
		t.Fatal("the parse was not cancelled")
	}
	if !inBalancing {
		t.Fatal("the regression did not reach subtree balancing")
	}
	session.parser.Reset()
	// Use the real syntax entry point after cancellation. The old C reset left
	// canceled_balancing set while freeing finished_tree, and aborted here.
	result, err := session.Parse(context.Background(), 1, source)
	if err != nil || result.HasError {
		t.Fatalf("reparse after reset: result=%+v err=%v", result, err)
	}
	checkRanges(t, string(source), result)
	if count := session.tree.RootNode().NamedChildCount(); count != 100 {
		t.Fatalf("reparse lost source: %d top-level nodes, want 100", count)
	}
}
