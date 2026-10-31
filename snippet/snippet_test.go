package snippet

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"crude/types"
)

func occurrence(stop, start, end, parent int, choices ...string) types.CompletionOccurrence {
	return types.CompletionOccurrence{Stop: stop, StartByte: start, EndByte: end, Parent: parent, Choices: choices}
}

func TestNumericSnippetLiteralExpectedTextAndOccurrences(t *testing.T) {
	cases := []struct {
		name, source, text string
		occurrences        []types.CompletionOccurrence
	}{
		{"empty", "", "", []types.CompletionOccurrence{occurrence(0, 0, 0, -1)}},
		{"plain", "hello", "hello", []types.CompletionOccurrence{occurrence(0, 5, 5, -1)}},
		{"linked defaults", "call(${1:name}, $1, ${2:count})$0", "call(name, name, count)",
			[]types.CompletionOccurrence{occurrence(1, 5, 9, -1), occurrence(1, 11, 15, -1), occurrence(2, 17, 22, -1), occurrence(0, 23, 23, -1)}},
		{"forward default", "$1 ${1:word}", "word word",
			[]types.CompletionOccurrence{occurrence(1, 0, 4, -1), occurrence(1, 5, 9, -1), occurrence(0, 9, 9, -1)}},
		{"nested linked", "${1:outer ${2:inner}} / $1 / $2$0", "outer inner / outer inner / inner",
			[]types.CompletionOccurrence{occurrence(1, 0, 11, -1), occurrence(2, 6, 11, 0),
				occurrence(1, 14, 25, -1), occurrence(2, 20, 25, 2), occurrence(2, 28, 33, -1), occurrence(0, 33, 33, -1)}},
		{"unicode normalized number", "é😀${02:x}$2$0", "é😀xx",
			[]types.CompletionOccurrence{occurrence(2, 6, 7, -1), occurrence(2, 7, 8, -1), occurrence(0, 8, 8, -1)}},
		{"explicit early final sparse stops", "pre$0post${10:a}${2:b}", "prepostab",
			[]types.CompletionOccurrence{occurrence(0, 3, 3, -1), occurrence(10, 7, 8, -1), occurrence(2, 8, 9, -1)}},
		{"all escaped choices", `${1|red,bl\,ue,green\|ish,\\|}:$1`, "red:red",
			[]types.CompletionOccurrence{occurrence(1, 0, 3, -1, "red", "bl,ue", "green|ish", `\`),
				occurrence(1, 4, 7, -1, "red", "bl,ue", "green|ish", `\`), occurrence(0, 7, 7, -1)}},
		{"crlf empty stops", "${1:α\r\n\t中}${2:}$2", "α\r\n\t中",
			[]types.CompletionOccurrence{occurrence(1, 0, 8, -1), occurrence(2, 8, 8, -1), occurrence(2, 8, 8, -1), occurrence(0, 8, 8, -1)}},
		{"equal definitions", "${1:thing} ${01:thing}", "thing thing",
			[]types.CompletionOccurrence{occurrence(1, 0, 5, -1), occurrence(1, 6, 11, -1), occurrence(0, 11, 11, -1)}},
		{"literal escapes", `\\\$1\}`, `\$1}`, []types.CompletionOccurrence{occurrence(0, 4, 4, -1)}},
		{"nested empty", "${1:${2:}}$2", "",
			[]types.CompletionOccurrence{occurrence(1, 0, 0, -1), occurrence(2, 0, 0, 0), occurrence(2, 0, 0, -1), occurrence(0, 0, 0, -1)}},
		{"empty choice", "${1|,value|}$0", "",
			[]types.CompletionOccurrence{occurrence(1, 0, 0, -1, "", "value"), occurrence(0, 0, 0, -1)}},
		{"repeated zero", "$0$0", "",
			[]types.CompletionOccurrence{occurrence(0, 0, 0, -1), occurrence(0, 0, 0, -1)}},
		{"final default", "${0:x}", "x", []types.CompletionOccurrence{occurrence(0, 0, 1, -1)}},
		{"nested copied zero", "${1:a$0b}$1", "abab",
			[]types.CompletionOccurrence{occurrence(1, 0, 2, -1), occurrence(0, 1, 1, 0), occurrence(1, 2, 4, -1), occurrence(0, 3, 3, 2)}},
		{"positive inside final", "${0:${1:x}}", "x",
			[]types.CompletionOccurrence{occurrence(0, 0, 1, -1), occurrence(1, 0, 1, 0)}},
		{"choice dollar and brace are literal", "${1|$TM,},a|}", "$TM",
			[]types.CompletionOccurrence{occurrence(1, 0, 3, -1, "$TM", "}", "a"), occurrence(0, 3, 3, -1)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text, plan, err := Parse(tc.source)
			if err != nil || text != tc.text || plan == nil {
				t.Fatalf("numeric snippet text=%q plan=%+v err=%v; want %q", text, plan, err, tc.text)
			}
			for i := range plan.Occurrences {
				if len(plan.Occurrences[i].Choices) == 0 {
					plan.Occurrences[i].Choices = nil
				}
			}
			if !reflect.DeepEqual(plan.Occurrences, tc.occurrences) {
				t.Fatalf("occurrences=%+v, want %+v", plan.Occurrences, tc.occurrences)
			}
			if err := Validate(text, plan); err != nil {
				t.Fatalf("parser returned a plan its admission refuses: %v", err)
			}
		})
	}
}

func TestNumericSnippetRejectsUnsupportedInvalidAndCyclicInputs(t *testing.T) {
	for _, source := range []string{
		"$TM_FILENAME", "${TM_FILENAME}", "${TM_FILENAME:default}", "${1/a/b/}",
		"${1", "${1:a", "${1|a,b}", "${1|a|", "${1?}", "${1:a}${01:b}",
		"${1:$1}", "${1:$2}${2:$1}", "${1:${2:$1}}", `\x`, `${1|a\q,b|}`,
		"${1:😀}\xff", "${1:${2:a}}${1:${3:a}}",
	} {
		t.Run(source, func(t *testing.T) {
			text, plan, err := Parse(source)
			if err == nil || text != "" || plan != nil {
				t.Fatalf("rejected input published text=%q plan=%+v err=%v", text, plan, err)
			}
		})
	}
	for _, source := range []string{"$TM_FILENAME", "${TM_FILENAME:default}", "${1/a/b/}"} {
		if _, _, err := Parse(source); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("unsupported construct lost its distinction: %v", err)
		}
	}
}

func TestNumericSnippetBoundsBeforeExpansion(t *testing.T) {
	for name, source := range map[string]string{
		"source":      strings.Repeat("a", MaxSourceBytes+1),
		"stop":        "$1000001",
		"occurrences": strings.Repeat("$1", MaxOccurrences),
		"expansion":   "${1:" + strings.Repeat("a", MaxExpandedBytes/2) + "}$1$1",
		"choices":     "${1|" + strings.Repeat("a,", MaxChoices) + "a|}",
		"nesting":     strings.Repeat("${1:", MaxDepth+1) + strings.Repeat("}", MaxDepth+1),
	} {
		t.Run(name, func(t *testing.T) {
			text, plan, err := Parse(source)
			if err == nil || text != "" || plan != nil {
				t.Fatalf("over-limit input admitted: text bytes %d, plan %+v, err %v", len(text), plan, err)
			}
		})
	}
	text, plan, err := Parse(strings.Repeat("x", MaxExpandedBytes))
	if err != nil || len(text) != MaxExpandedBytes || len(plan.Occurrences) != 1 {
		t.Fatalf("exact expansion bound refused: bytes %d err %v", len(text), err)
	}
}

func TestNumericSnippetPlansRejectIndependentMalformedEvidence(t *testing.T) {
	cases := []struct {
		name, text  string
		occurrences []types.CompletionOccurrence
	}{
		{"split UTF8", "😀", []types.CompletionOccurrence{occurrence(1, 1, 4, -1), occurrence(0, 4, 4, -1)}},
		{"overlapping roots", "abc", []types.CompletionOccurrence{occurrence(1, 0, 2, -1), occurrence(2, 1, 3, -1), occurrence(0, 3, 3, -1)}},
		{"child escapes", "abc", []types.CompletionOccurrence{occurrence(1, 0, 2, -1), occurrence(2, 1, 3, 0), occurrence(0, 3, 3, -1)}},
		{"self parent", "abc", []types.CompletionOccurrence{occurrence(1, 0, 3, 0), occurrence(0, 3, 3, -1)}},
		{"reentered closed parent", "ab", []types.CompletionOccurrence{occurrence(1, 0, 1, -1), occurrence(2, 1, 2, -1), occurrence(3, 0, 1, 0), occurrence(0, 2, 2, -1)}},
		{"different mirror values", "ab:cd", []types.CompletionOccurrence{occurrence(1, 0, 2, -1), occurrence(1, 3, 5, -1), occurrence(0, 5, 5, -1)}},
		{"choice mismatches text", "red", []types.CompletionOccurrence{occurrence(1, 0, 3, -1, "blue", "red"), occurrence(0, 3, 3, -1)}},
		{"choice with child", "a", []types.CompletionOccurrence{occurrence(1, 0, 1, -1, "a", "bb"), occurrence(2, 0, 1, 0), occurrence(0, 1, 1, -1)}},
		{"missing copied subtree", "ab ab", []types.CompletionOccurrence{occurrence(1, 0, 2, -1), occurrence(2, 0, 1, 0), occurrence(1, 3, 5, -1), occurrence(0, 5, 5, -1)}},
		{"recursive stop", "", []types.CompletionOccurrence{occurrence(1, 0, 0, -1), occurrence(1, 0, 0, 0), occurrence(0, 0, 0, -1)}},
		{"no final", "", []types.CompletionOccurrence{occurrence(1, 0, 0, -1)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sum := sha256.Sum256([]byte(tc.text))
			if err := Validate(tc.text, &types.CompletionSnippet{TextSHA256: fmt.Sprintf("%x", sum), Occurrences: tc.occurrences}); err == nil {
				t.Fatal("independently malformed snippet plan admitted")
			}
		})
	}
}

func TestNumericSnippetDepthIsOccurrenceAncestryNotLeafKind(t *testing.T) {
	wrap := func(ancestors int, leaf string) string {
		for id := ancestors; id > 0; id-- {
			leaf = fmt.Sprintf("${%d:%s}", id, leaf)
		}
		return leaf
	}
	for _, leaf := range []string{"$17", "${17:}", "${17:x}", "${17|x,y|}"} {
		t.Run(leaf, func(t *testing.T) {
			_, plan, err := Parse(wrap(16, leaf))
			if err != nil || plan == nil || len(plan.Occurrences) != 18 {
				t.Fatalf("sixteen containment edges refused for leaf %q: %v", leaf, err)
			}
		})
	}
	for _, leaf := range []string{"$18", "${18:}", "${18:x}", "${18|x,y|}"} {
		if _, _, err := Parse(wrap(17, leaf)); err == nil {
			t.Fatalf("seventeen containment edges accepted for %q", leaf)
		}
	}
}

func TestNumericSnippetPlanMustRemainBoundToExpandedText(t *testing.T) {
	text, plan, err := Parse("${1:x}")
	if err != nil || text != "x" {
		t.Fatal(err)
	}
	for _, incorrect := range []string{"${1:x}", "y"} {
		if err := Validate(incorrect, plan); err == nil {
			t.Fatalf("expanded plan was rebound to unrelated/raw insertion %q", incorrect)
		}
	}
}

func TestNumericSnippetPlanDetach(t *testing.T) {
	text, first, err := Parse("${1|red,blue|}:$1")
	if err != nil {
		t.Fatal(err)
	}
	first.Occurrences[0].Choices[0] = "changed"
	if first.Occurrences[1].Choices[0] != "red" {
		t.Fatal("mirror occurrences shared mutable choice slices")
	}
	nextText, next, err := Parse("${1|red,blue|}:$1")
	if err != nil || nextText != text || next.Occurrences[0].Choices[0] != "red" {
		t.Fatal("later invocation shared private state with prior plan")
	}
}

func FuzzNumericSnippet(f *testing.F) {
	for _, source := range []string{"", "${1:outer ${2:inner}} $1 $2$0", "$1 ${1:word}", "${1|a,b|}", "${1:$1}", "$TM_FILENAME", "😀\r\n"} {
		f.Add(source)
	}
	f.Fuzz(func(t *testing.T, source string) {
		text, plan, err := Parse(source)
		if err != nil {
			if text != "" || plan != nil {
				t.Fatal("failed parse leaked partial result")
			}
			return
		}
		if err := Validate(text, plan); err != nil {
			t.Fatal(err)
		}
		nextText, next, nextErr := Parse(source)
		if nextErr != nil || nextText != text || !reflect.DeepEqual(next, plan) {
			t.Fatal("non-deterministic expansion")
		}
	})
}

func BenchmarkNumericSnippetSmall(b *testing.B) {
	for b.Loop() {
		if _, _, err := Parse("call(${1:name}, $1, ${2:count})$0"); err != nil {
			b.Fatal(err)
		}
	}
}
