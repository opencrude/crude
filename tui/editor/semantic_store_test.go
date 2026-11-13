package editor

import (
	"slices"
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"

	"crude/document"
	"crude/types"
)

// These cases are ported from VS Code's tokensStore.test.ts
// (src/vs/editor/test/common/model/tokensStore.test.ts). The `|token|`
// notation, the edits and the expected states are copied verbatim; ranges use
// VS Code's 1-based line and column. Edits run through the editor's real
// source-edit path, not through the store directly.

// parseTokensState ports the VS Code helper of the same name.
func parseTokensState(state []string) (string, []types.LSPSemanticSpan) {
	var text []string
	var spans []types.LSPSemanticSpan
	offset := 0
	for _, line := range state {
		lineText, start := "", 0
		for {
			first := strings.Index(line[start:], "|")
			if first < 0 {
				break
			}
			first += start
			second := strings.Index(line[first+1:], "|")
			if second < 0 {
				break
			}
			second += first + 1
			if first+1 == second {
				lineText += line[start : second+1] // skip ||
				start = second + 1
				continue
			}
			lineText += line[start:first]
			tokenStart := offset + len(lineText)
			lineText += line[first+1 : second]
			spans = append(spans, types.LSPSemanticSpan{Start: tokenStart, End: offset + len(lineText)})
			start = second + 1
		}
		lineText += line[start:]
		text = append(text, lineText)
		offset += len(lineText) + 1
	}
	return strings.Join(text, "\n"), spans
}

// extractState ports the VS Code helper: semantic tokens are wrapped in pipes.
func extractState(e *Editor) []string {
	text := e.Value()
	var out []string
	lineStart := 0
	tokens := e.semantic.tokens
	for _, line := range strings.Split(text, "\n") {
		lineEnd := lineStart + len(line)
		var b strings.Builder
		at := lineStart
		for _, t := range tokens {
			if t.start < lineStart || t.start >= lineEnd || t.start < at {
				continue
			}
			b.WriteString(text[at:t.start])
			b.WriteString("|" + text[t.start:min(t.end, lineEnd)] + "|")
			at = min(t.end, lineEnd)
		}
		b.WriteString(text[at:lineEnd])
		out = append(out, b.String())
		lineStart = lineEnd + 1
	}
	return out
}

type vsRange struct{ startLine, startColumn, endLine, endColumn int }

func testTokensAdjustment(t *testing.T, initial []string, edits []vsRange, texts []string, want []string) {
	t.Helper()
	text, spans := parseTokensState(initial)
	e := newInlayTestEditor(t, text)
	source, _ := e.CurrentInsightSource()
	if err := e.SetSemanticTokens(&types.LSPSemanticSet{Boundary: types.InsightBoundary{Source: source}, ViewID: "store", Sequence: 1, Full: true, Types: []string{"variable"}, Spans: spans}); err != nil {
		t.Fatal(err)
	}
	e.buf.ensureCache()
	offset := func(line, column int) int { return e.buf.lineOffsets[line-1] + column - 1 }
	vector := make([]document.Edit, len(edits))
	for i, r := range edits {
		vector[i] = document.Edit{Start: offset(r.startLine, r.startColumn), End: offset(r.endLine, r.endColumn), Text: texts[i]}
	}
	e.beginUndoGroup()
	if err := e.spliceRecordedBatch(vector, e.cursor); err != nil {
		t.Fatal(err)
	}
	e.endUndoGroup()
	if got := extractState(e); !slices.Equal(got, want) {
		t.Fatalf("tokens after edit:\n%q\nwant:\n%q", got, want)
	}
}

func TestSemanticTokensIssue86303ColorShiftingBetweenDifferentTokens(t *testing.T) {
	testTokensAdjustment(t,
		[]string{"import { |URI| } from 'vs/base/common/uri';", "const foo = |URI|.parse('hey');"},
		[]vsRange{{2, 9, 2, 10}}, []string{""},
		[]string{"import { |URI| } from 'vs/base/common/uri';", "const fo = |URI|.parse('hey');"})
}

func TestSemanticTokensDeletingANewline(t *testing.T) {
	testTokensAdjustment(t,
		[]string{"import { |URI| } from 'vs/base/common/uri';", "const foo = |URI|.parse('hey');"},
		[]vsRange{{1, 42, 2, 1}}, []string{""},
		[]string{"import { |URI| } from 'vs/base/common/uri';const foo = |URI|.parse('hey');"})
}

func TestSemanticTokensInsertingANewline(t *testing.T) {
	testTokensAdjustment(t,
		[]string{"import { |URI| } from 'vs/base/common/uri';const foo = |URI|.parse('hey');"},
		[]vsRange{{1, 42, 1, 42}}, []string{"\n"},
		[]string{"import { |URI| } from 'vs/base/common/uri';", "const foo = |URI|.parse('hey');"})
}

func TestSemanticTokensDeletingANewline2(t *testing.T) {
	testTokensAdjustment(t,
		[]string{"import { ", "    |URI| } from 'vs/base/common/uri';const foo = |URI|.parse('hey');"},
		[]vsRange{{1, 10, 2, 5}}, []string{""},
		[]string{"import { |URI| } from 'vs/base/common/uri';const foo = |URI|.parse('hey');"})
}

func TestSemanticTokensIssue179268AComplexEdit(t *testing.T) {
	testTokensAdjustment(t,
		[]string{
			"|export| |'interior_material_selector.dart'|;",
			"|export| |'mileage_selector.dart'|;",
			"|export| |'owners_selector.dart'|;",
			"|export| |'price_selector.dart'|;",
			"|export| |'seat_count_selector.dart'|;",
			"|export| |'year_selector.dart'|;",
			"|export| |'winter_options_selector.dart'|;|export| |'camera_selector.dart'|;",
		},
		[]vsRange{{1, 9, 1, 9}, {6, 9, 7, 9}, {7, 39, 7, 39}, {7, 47, 7, 48}, {7, 49, 7, 51}, {7, 52, 7, 53}},
		[]string{"camera_selector.dart';\nexport '", "", "\n", "ye", "", ""},
		[]string{
			"|export| |'|camera_selector.dart';",
			"export 'interior_material_selector.dart';",
			"|export| |'mileage_selector.dart'|;",
			"|export| |'owners_selector.dart'|;",
			"|export| |'price_selector.dart'|;",
			"|export| |'seat_count_selector.dart'|;",
			"|export| |'||winter_options_selector.dart'|;",
			"|export| |'year_selector.dart'|;",
		})
}

// The partial-result cases use VS Code's line/character tokens on a document
// whose lines are 20 bytes long, so a token (line, 5-10) is a byte range.
func partialTokens(lines map[int]int) []semanticToken {
	const width = 21 // 20 characters and a line break
	var out []semanticToken
	keys := make([]int, 0, len(lines))
	for line := range lines {
		keys = append(keys, line)
	}
	slices.Sort(keys)
	for _, line := range keys {
		out = append(out, semanticToken{start: (line-1)*width + 5, end: (line-1)*width + 10, kind: chroma.TokenType(lines[line])})
	}
	return out
}

func partialRange(startLine, startColumn, endLine, endColumn int) (int, int) {
	const width = 21
	return (startLine-1)*width + startColumn - 1, (endLine-1)*width + endColumn - 1
}

func lineTokenCount(s *semanticStore, line int) int {
	const width = 21
	count := 0
	for _, t := range s.tokens {
		if t.start >= (line-1)*width && t.start < line*width {
			count++
		}
	}
	return count
}

func TestSemanticStorePartialTokens1(t *testing.T) {
	var s semanticStore
	a, b := partialRange(1, 1, 31, 2)
	s.setPartial(a, b, partialTokens(map[int]int{5: 1, 10: 2, 15: 3, 20: 4, 25: 5, 30: 6}))
	a, b = partialRange(18, 1, 42, 1)
	s.setPartial(a, b, partialTokens(map[int]int{20: 4, 25: 5, 30: 6, 35: 7, 40: 8}))
	a, b = partialRange(1, 1, 31, 2)
	s.setPartial(a, b, partialTokens(map[int]int{5: 1, 10: 2, 15: 3, 20: 4, 25: 5, 30: 6}))
	if n := lineTokenCount(&s, 10); n != 1 {
		t.Fatalf("line 10 has %d semantic tokens, want 1: %v", n, s.tokens)
	}
}

func TestSemanticStorePartialTokens2(t *testing.T) {
	var s semanticStore
	a, b := partialRange(1, 1, 31, 2)
	s.setPartial(a, b, partialTokens(map[int]int{5: 1, 10: 2, 15: 3, 20: 4, 25: 5, 30: 6}))
	a, b = partialRange(6, 1, 36, 2)
	s.setPartial(a, b, partialTokens(map[int]int{10: 2, 15: 3, 20: 4, 25: 5, 30: 6}))
	a, b = partialRange(17, 1, 42, 1)
	s.setPartial(a, b, partialTokens(map[int]int{20: 4, 25: 5, 30: 6, 35: 7, 40: 8}))
	if n := lineTokenCount(&s, 20); n != 1 {
		t.Fatalf("line 20 has %d semantic tokens, want 1: %v", n, s.tokens)
	}
}

func TestSemanticStorePartialTokens3(t *testing.T) {
	var s semanticStore
	a, b := partialRange(1, 1, 31, 2)
	s.setPartial(a, b, partialTokens(map[int]int{5: 1, 10: 2, 15: 3, 20: 4, 25: 5, 30: 6}))
	a, b = partialRange(11, 1, 16, 2)
	s.setPartial(a, b, partialTokens(map[int]int{15: 3, 20: 4}))
	if n := lineTokenCount(&s, 5); n != 1 {
		t.Fatalf("line 5 has %d semantic tokens, want 1: %v", n, s.tokens)
	}
}

func TestSemanticStoreIssue94133ColorsDoNotStickAroundWithOnlyARangeProvider(t *testing.T) {
	var s semanticStore
	s.setPartial(0, 19, []semanticToken{{start: 9, end: 11, kind: 1}})
	s.setPartial(0, 19, nil)
	if len(s.tokens) != 0 {
		t.Fatalf("an empty range result kept %v", s.tokens)
	}
}

func TestSemanticStoreSetPartialKeepsTokensAfterTheRange(t *testing.T) {
	// "BUG: setPartial with split that creates empty first piece": the token on
	// line 11 survives a result for lines 1-5.
	var s semanticStore
	s.tokens = partialTokens(map[int]int{11: 1})
	a, b := partialRange(1, 1, 5, 1)
	s.setPartial(a, b, nil)
	if n := lineTokenCount(&s, 11); n != 1 || s.tokens[0].start != 10*21+5 || s.tokens[0].end != 10*21+10 {
		t.Fatalf("line 11 token lost or moved: %v", s.tokens)
	}
}

func TestSemanticStoreEncompassingDeletionEmptiesTheStore(t *testing.T) {
	// "piece with startLineNumber 0 and endLineNumber -1 after encompassing deletion"
	var s semanticStore
	s.tokens = []semanticToken{{start: 4 * 21, end: 4*21 + 5, kind: 1}, {start: 9 * 21, end: 9*21 + 5, kind: 2}}
	a, b := partialRange(1, 1, 20, 1)
	s.acceptEdit(a, b, "")
	if len(s.tokens) != 0 {
		t.Fatalf("encompassing deletion kept %v", s.tokens)
	}
}
