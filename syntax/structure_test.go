package syntax

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestStructuralRangesAndBracketsAreDetachedExactSource(t *testing.T) {
	source := "package p\nfunc f(){call(\"([)]\", x[2]) /* } */}\n"
	s := newTestSession(t, Go)
	result := parseTest(t, s, 1, source)
	if len(result.Selections) == 0 || len(result.Brackets) != 8 || result.StructureTruncated {
		t.Fatalf("missing structure: %+v", result)
	}
	endpoints := make(map[uint32]uint32)
	for i, b := range result.Brackets {
		if i > 0 && result.Brackets[i-1].Offset >= b.Offset || int(b.Offset) >= len(source) || int(b.Mate) >= len(source) {
			t.Fatal("unordered/out-of-source pair", result.Brackets)
		}
		endpoints[b.Offset] = b.Mate
	}
	for from, to := range endpoints {
		if back, ok := endpoints[to]; !ok || back != from {
			t.Fatal("nonreciprocal pair", from, to)
		}
	}
	for _, r := range result.Selections {
		if r.Start >= r.End || int(r.End) > len(source) {
			t.Fatal("invalid named range", r)
		}
	}
	result.Selections[0].End = 0
	result.Brackets[0].Mate = 0
	next := parseTest(t, s, 2, source)
	if next.Selections[0].End == 0 || next.Brackets[0].Mate == 0 {
		t.Fatal("caller mutation corrupted accepted native state")
	}
}

func TestStructuralBracketIndexRejectsCrossingAndHonorsLimits(t *testing.T) {
	for _, source := range []string{"([)]", "(()", "{[}])"} {
		pairs, truncated, err := BracketIndex(t.Context(), []byte(source), func(int) bool { return false })
		if err != nil || truncated {
			t.Fatal(pairs, truncated, err)
		}
		if source == "(()" {
			if len(pairs) != 2 || pairs[0].Offset != 1 || pairs[0].Mate != 2 {
				t.Fatal(pairs)
			}
		} else if len(pairs) != 0 {
			t.Fatal("crossing pair accepted", pairs)
		}
	}
	pairs, truncated, err := BracketIndex(t.Context(), []byte(strings.Repeat("()", MaxStructure)), func(int) bool { return false })
	if err != nil || !truncated || len(pairs) != 0 {
		t.Fatal("unbounded or silently partial bracket output", len(pairs), truncated, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err = BracketIndex(ctx, []byte("()"), func(int) bool { return false }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestStructuralSelectionBudgetDoesNotEraseLexicalCoverage(t *testing.T) {
	source := []byte("package p\nvar a = []int{" + strings.Repeat("1,", MaxStructure) + "}\n")
	session := newTestSession(t, Go)
	// Exercise this stage's output budget on a real native tree, independently
	// of the separate five-second parse+highlight execution budget. The latter
	// can correctly expire under race instrumentation before structure starts.
	tree := session.parser.Parse(source, nil)
	if tree == nil {
		t.Fatal("native tree unavailable")
	}
	defer tree.Close()
	spans := []Span{{Start: 0, End: uint32(len(source)), Kind: Plain}}
	result := Result{Spans: spans}
	if err := session.structure(t.Context(), tree.RootNode(), source, &result); err != nil {
		t.Fatal(err)
	}
	if !result.StructureTruncated || len(result.Selections) != 65536 || len(result.Spans) != 1 || result.Spans[0] != spans[0] {
		t.Fatal("structural budget hid accepted syntax or was not reported", len(result.Selections), result.StructureTruncated)
	}
}
