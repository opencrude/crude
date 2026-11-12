package lspcompletion_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"crude/lspcompletion"
	"crude/lspedit"
	"crude/types"
)

func initialInsertion(t *testing.T, raw string) types.CompletionItem {
	t.Helper()
	item, err := lspcompletion.Insertion(lspcompletion.Item{Original: json.RawMessage(raw)})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func TestInsertionInitialReplacementPrecedenceAndLiteralAdjuncts(t *testing.T) {
	item := initialInsertion(t, `{
		"label":"not inserted","detail":"initial detail","documentation":"initial docs","kind":3,
		"insertTextFormat":2,"insertText":"$TM_FILENAME",
		"textEdit":{"range":{"start":{"line":0,"character":1},"end":{"line":0,"character":3}},"newText":"${1|red,blue|}:$1$0"},
		"additionalTextEdits":[{"range":{"start":{"line":1,"character":0},"end":{"line":1,"character":0}},"newText":"$TM_FILENAME ${9:literal}\n"}]
	}`)
	want := types.LSPTextEdit{Line: 0, Col: 1, EndLine: 0, EndCol: 3, NewText: "red:red"}
	if item.TextEdit == nil || *item.TextEdit != want || item.InsertText != "red:red" ||
		item.Label != "not inserted" || item.Detail != "initial detail" || item.Documentation != "initial docs" || item.Kind != 3 {
		t.Fatalf("wrong initial insertion: %+v", item)
	}
	if item.Snippet == nil || !reflect.DeepEqual(item.Snippet.Occurrences, []types.CompletionOccurrence{
		{Stop: 1, StartByte: 0, EndByte: 3, Parent: -1, Choices: []string{"red", "blue"}},
		{Stop: 1, StartByte: 4, EndByte: 7, Parent: -1, Choices: []string{"red", "blue"}},
		{Stop: 0, StartByte: 7, EndByte: 7, Parent: -1, Choices: []string{}},
	}) {
		t.Fatalf("wrong numeric choices: %+v", item.Snippet)
	}
	if !reflect.DeepEqual(item.AdditionalTextEdits, []types.LSPTextEdit{
		{Line: 1, Col: 0, EndLine: 1, EndCol: 0, NewText: "$TM_FILENAME ${9:literal}\n"},
	}) {
		t.Fatalf("adjunct expanded or changed: %+v", item.AdditionalTextEdits)
	}
}

func TestInsertionNestedMirrorsHaveLiteralExpectedSpans(t *testing.T) {
	item := initialInsertion(t, `{"label":"nested","insertTextFormat":2,"insertText":"${1:outer ${2:inner}} / $1 / $2$0"}`)
	want := []types.CompletionOccurrence{
		{Stop: 1, StartByte: 0, EndByte: 11, Parent: -1, Choices: []string{}},
		{Stop: 2, StartByte: 6, EndByte: 11, Parent: 0, Choices: []string{}},
		{Stop: 1, StartByte: 14, EndByte: 25, Parent: -1, Choices: []string{}},
		{Stop: 2, StartByte: 20, EndByte: 25, Parent: 2, Choices: []string{}},
		{Stop: 2, StartByte: 28, EndByte: 33, Parent: -1, Choices: []string{}},
		{Stop: 0, StartByte: 33, EndByte: 33, Parent: -1, Choices: []string{}},
	}
	if item.InsertText != "outer inner / outer inner / inner" || item.Snippet == nil || !reflect.DeepEqual(item.Snippet.Occurrences, want) {
		t.Fatalf("text=%q plan=%+v", item.InsertText, item.Snippet)
	}
}

func TestInsertionEmptyLiteralAndFallbackSemantics(t *testing.T) {
	for _, tc := range []struct {
		name, raw, text string
		primary, plan   bool
	}{
		{"absent", `{"label":"label"}`, "label", false, false},
		{"empty insert", `{"label":"label","insertText":""}`, "", false, false},
		{"literal syntax", `{"label":"label","insertTextFormat":1,"insertText":"${1:literal}"}`, "${1:literal}", false, false},
		{"empty primary", `{"label":"label","insertText":"wrong","textEdit":{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},"newText":""}}`, "", true, false},
		{"empty snippet primary", `{"label":"label","insertTextFormat":2,"textEdit":{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},"newText":""}}`, "", true, true},
		{"empty snippet", `{"label":"label","insertTextFormat":2,"insertText":"$0"}`, "", false, true},
		{"snippet label", `{"label":"${1:label}","insertTextFormat":2}`, "label", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := initialInsertion(t, tc.raw)
			if item.InsertText != tc.text || (item.TextEdit != nil) != tc.primary || (item.Snippet != nil) != tc.plan {
				t.Fatalf("insertion=%+v; text=%q primary=%v plan=%v", item, tc.text, tc.primary, tc.plan)
			}
			edit, err := lspcompletion.PrimaryEdit("", 0, 0, item)
			if err != nil || edit.NewText != tc.text {
				t.Fatalf("primary=%+v err=%v", edit, err)
			}
		})
	}
}

func TestExplicitEmptyInsertNeverInsertsTheLabel(t *testing.T) {
	for _, raw := range []string{
		`{"label":"MUST NOT INSERT","insertText":""}`,
		`{"label":"MUST NOT INSERT","insertText":"","insertTextFormat":2}`,
	} {
		item := initialInsertion(t, raw)
		edit, err := lspcompletion.PrimaryEdit("prefix", 0, 6, item)
		if err != nil {
			t.Fatal(err)
		}
		after, err := lspedit.Apply("prefix", []types.LSPTextEdit{edit})
		if err != nil || after != "" {
			t.Fatalf("explicit empty insertion unexpectedly used the label: after=%q err=%v", after, err)
		}
	}
}

func TestInsertionReplaceUnionUsesReplacementNotInsertRange(t *testing.T) {
	item := initialInsertion(t, `{"label":"label","insertTextFormat":2,"insertText":"${WRONG}","textEdit":{"insert":{"start":{"line":0,"character":1},"end":{"line":0,"character":1}},"replace":{"start":{"line":0,"character":1},"end":{"line":0,"character":3}},"newText":"${1:X}"}}`)
	edit, err := lspcompletion.PrimaryEdit("a😀b", 0, 3, item)
	if err != nil || edit != (types.LSPTextEdit{Col: 1, EndCol: 3, NewText: "X"}) {
		t.Fatalf("replacement=%+v err=%v", edit, err)
	}
	after, err := lspedit.Apply("a😀b", []types.LSPTextEdit{edit})
	if err != nil || after != "aXb" {
		t.Fatalf("after=%q err=%v", after, err)
	}
}

func TestInsertionRefusesInvalidAndUnsupportedWithoutPartialOutput(t *testing.T) {
	const r = `{"start":{"line":0,"character":0},"end":{"line":0,"character":1}}`
	cases := []struct {
		name, fields string
		want         error
	}{
		{"command", `"command":{"title":"run","command":"provider.run"}`, lspcompletion.ErrUnsupported},
		{"null command", `"command":null`, lspcompletion.ErrUnsupported},
		{"format", `"insertTextFormat":3`, lspcompletion.ErrUnsupported},
		{"mode", `"insertTextMode":3`, lspcompletion.ErrUnsupported},
		{"null format", `"insertTextFormat":null`, lspcompletion.ErrInvalid},
		{"float format", `"insertTextFormat":2.0`, lspcompletion.ErrInvalid},
		{"string mode", `"insertTextMode":"1"`, lspcompletion.ErrInvalid},
		{"null insert", `"insertText":null`, lspcompletion.ErrInvalid},
		{"variable", `"insertTextFormat":2,"insertText":"$TM_FILENAME"`, lspcompletion.ErrUnsupported},
		{"variable default", `"insertTextFormat":2,"insertText":"${TM_FILENAME:fallback}"`, lspcompletion.ErrUnsupported},
		{"transform", `"insertTextFormat":2,"insertText":"${1/a/b/}"`, lspcompletion.ErrUnsupported},
		{"unclosed snippet", `"insertTextFormat":2,"insertText":"${1:unclosed"`, lspcompletion.ErrUnsupported},
		{"null edit", `"textEdit":null`, lspcompletion.ErrInvalid},
		{"partial edit", `"textEdit":{"range":` + r + `}`, lspcompletion.ErrInvalid},
		{"missing range", `"textEdit":{"newText":"X"}`, lspcompletion.ErrInvalid},
		{"null range", `"textEdit":{"range":null,"newText":"X"}`, lspcompletion.ErrInvalid},
		{"null replacement", `"textEdit":{"range":` + r + `,"newText":null}`, lspcompletion.ErrInvalid},
		{"partial union", `"textEdit":{"insert":` + r + `,"newText":"X"}`, lspcompletion.ErrInvalid},
		{"both unions", `"textEdit":{"range":` + r + `,"insert":` + r + `,"replace":` + r + `,"newText":"X"}`, lspcompletion.ErrInvalid},
		{"mismatched starts", `"textEdit":{"insert":` + r + `,"replace":{"start":{"line":0,"character":1},"end":{"line":0,"character":2}},"newText":"X"}`, lspcompletion.ErrInvalid},
		{"insert escapes", `"textEdit":{"insert":{"start":{"line":0,"character":0},"end":{"line":0,"character":2}},"replace":` + r + `,"newText":"X"}`, lspcompletion.ErrInvalid},
		{"multiline primary", `"textEdit":{"range":{"start":{"line":0,"character":0},"end":{"line":1,"character":0}},"newText":"X"}`, lspcompletion.ErrInvalid},
		{"null adjuncts", `"additionalTextEdits":null`, lspcompletion.ErrInvalid},
		{"partial adjunct", `"additionalTextEdits":[{"range":` + r + `}]`, lspcompletion.ErrInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := lspcompletion.Insertion(lspcompletion.Item{Original: json.RawMessage(`{"label":"safe",` + tc.fields + `}`)})
			if !errors.Is(err, tc.want) || !reflect.DeepEqual(got, types.CompletionItem{}) {
				t.Fatalf("got=%+v err=%v want zero and %v", got, err, tc.want)
			}
		})
	}
}

func TestInsertionCoordinatesRequireCompleteSigned32Integers(t *testing.T) {
	for _, pos := range []string{
		`{}`, `null`, `{"line":0}`, `{"character":0}`,
		`{"line":-1,"character":0}`, `{"line":0,"character":-1}`,
		`{"line":0,"character":0.0}`, `{"line":0,"character":1e0}`,
		`{"line":0,"character":"0"}`, `{"line":0,"character":null}`,
		`{"line":2147483648,"character":0}`, `{"line":0,"character":2147483648}`,
	} {
		t.Run(pos, func(t *testing.T) {
			raw := `{"label":"X","textEdit":{"range":{"start":` + pos + `,"end":{"line":0,"character":0}},"newText":"X"}}`
			got, err := lspcompletion.Insertion(lspcompletion.Item{Original: json.RawMessage(raw)})
			if !errors.Is(err, lspcompletion.ErrInvalid) || !reflect.DeepEqual(got, types.CompletionItem{}) {
				t.Fatalf("got=%+v err=%v", got, err)
			}
		})
	}
	for _, col := range []string{"0", "2147483647"} {
		item := initialInsertion(t, `{"label":"X","textEdit":{"range":{"start":{"line":0,"character":`+col+`},"end":{"line":0,"character":`+col+`}},"newText":"X"}}`)
		if item.TextEdit == nil {
			t.Fatal("complete integer position refused")
		}
	}
}

func TestInsertionAdjunctAndConsumedTextBounds(t *testing.T) {
	const edit = `{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},"newText":"X"}`
	for _, n := range []int{64, 65} {
		raw := `{"label":"X","additionalTextEdits":[` + strings.TrimSuffix(strings.Repeat(edit+",", n), ",") + `]}`
		got, err := lspcompletion.Insertion(lspcompletion.Item{Original: json.RawMessage(raw)})
		if n == 64 {
			if err != nil || len(got.AdditionalTextEdits) != 64 {
				t.Fatalf("64 adjuncts: got %d err=%v", len(got.AdditionalTextEdits), err)
			}
		} else if !errors.Is(err, lspcompletion.ErrInvalid) || !reflect.DeepEqual(got, types.CompletionItem{}) {
			t.Fatalf("65 adjuncts: got=%+v err=%v", got, err)
		}
	}
	for _, n := range []int{65536, 65537} {
		for _, pattern := range []string{
			`{"label":"X","insertText":%s}`,
			`{"label":"X","textEdit":{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},"newText":%s}}`,
			`{"label":"X","additionalTextEdits":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},"newText":%s}]}`,
		} {
			text, _ := json.Marshal(strings.Repeat("é", n/2) + strings.Repeat("x", n%2))
			got, err := lspcompletion.Insertion(lspcompletion.Item{Original: json.RawMessage(fmt.Sprintf(pattern, text))})
			if n == 65536 && err != nil {
				t.Fatalf("exact byte bound refused: %v", err)
			}
			if n == 65537 && (!errors.Is(err, lspcompletion.ErrInvalid) || !reflect.DeepEqual(got, types.CompletionItem{})) {
				t.Fatalf("over-bound insertion published: err=%v", err)
			}
		}
	}
}

func TestInsertionOriginalAuthorityAndCallerMutationIsolation(t *testing.T) {
	raw := json.RawMessage(`{"label":"initial","detail":"initial detail","insertTextFormat":2,"insertText":"${1|red,blue|}:$1","data":{"opaque":[1,true]},"sortText":"1","filterText":"red","preselect":true,"commitCharacters":[";"]}`)
	input := lspcompletion.Item{Label: "changed display", FilterText: "changed filter", SortText: "changed sort", Original: raw}
	input.Documentation.Detail = "resolved display"
	first, err := lspcompletion.Insertion(input)
	if err != nil || first.Label != "initial" || first.Detail != "initial detail" || first.InsertText != "red:red" {
		t.Fatalf("display fields changed insertion: %+v err=%v", first, err)
	}
	first.Snippet.Occurrences[0].Choices[0] = "mutated"
	if first.Snippet.Occurrences[1].Choices[0] != "red" {
		t.Fatal("mirror choice arrays alias")
	}
	second, err := lspcompletion.Insertion(input)
	if err != nil || second.Snippet.Occurrences[0].Choices[0] != "red" {
		t.Fatal("return plan aliases a later invocation")
	}
	for i := range raw {
		raw[i] = ' '
	}
	if second.Label != "initial" || second.InsertText != "red:red" {
		t.Fatal("return strings alias original bytes")
	}
	raw = json.RawMessage(`{"label":"X","insertTextMode":1,"textEdit":{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},"newText":"X"},"additionalTextEdits":[{"range":{"start":{"line":1,"character":0},"end":{"line":2,"character":0}},"newText":"literal"}]}`)
	first, err = lspcompletion.Insertion(lspcompletion.Item{Original: raw})
	if err != nil {
		t.Fatal(err)
	}
	first.TextEdit.NewText = "mutated"
	first.AdditionalTextEdits[0].NewText = "mutated"
	second, err = lspcompletion.Insertion(lspcompletion.Item{Original: raw})
	if err != nil || second.TextEdit.NewText != "X" || second.AdditionalTextEdits[0].NewText != "literal" {
		t.Fatalf("primary/adjunct vector aliases another invocation: %+v err=%v", second, err)
	}
}

func TestInsertionRawAndSnippetExpansionBoundsReturnZero(t *testing.T) {
	const raw = `{"label":"X"}`
	exact := raw + strings.Repeat(" ", (1<<20)-len(raw))
	got, err := lspcompletion.Insertion(lspcompletion.Item{Original: json.RawMessage(exact)})
	if err != nil || got.InsertText != "X" {
		t.Fatalf("exact raw bound refused: %v", err)
	}
	expandedSource, _ := json.Marshal("${1:" + strings.Repeat("a", 32768) + "}$1$1")
	for _, raw := range []string{
		exact + " ", `{"label":"X","label":"Y"}`, `{"label":"\ud800"}`,
		`{"label":"X","insertTextFormat":2,"insertText":` + string(expandedSource) + `}`,
	} {
		got, err := lspcompletion.Insertion(lspcompletion.Item{Original: json.RawMessage(raw)})
		if !errors.Is(err, lspcompletion.ErrInvalid) || !reflect.DeepEqual(got, types.CompletionItem{}) {
			t.Fatalf("invalid raw/expanded bound published: err=%v", err)
		}
	}
}

func TestPrimaryEditUnicodePrefixHasLiteralSourceResults(t *testing.T) {
	for _, tc := range []struct {
		name, source, after string
		line, col           int
		want                types.LSPTextEdit
	}{
		{"unicode identifier", "let\tα中_٢tail!", "let\tXtail!", 0, 8, types.LSPTextEdit{Col: 4, EndCol: 8, NewText: "X"}},
		{"supplementary boundary", "a😀b", "a😀X", 0, 4, types.LSPTextEdit{Col: 3, EndCol: 4, NewText: "X"}},
		{"tab boundary", "abc\txy", "abc\tX", 0, 6, types.LSPTextEdit{Col: 4, EndCol: 6, NewText: "X"}},
		{"line boundary", "prev\nα中", "prev\nX", 1, 2, types.LSPTextEdit{Line: 1, EndLine: 1, EndCol: 2, NewText: "X"}},
		{"line start", "prev\nx", "prev\nXx", 1, 0, types.LSPTextEdit{Line: 1, EndLine: 1, NewText: "X"}},
		{"combining mark stops prefix", "e\u0301x", "e\u0301X", 0, 3, types.LSPTextEdit{Col: 2, EndCol: 3, NewText: "X"}},
		{"crlf", "prev\r\n_9", "prev\r\nX", 1, 2, types.LSPTextEdit{Line: 1, EndLine: 1, EndCol: 2, NewText: "X"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			edit, err := lspcompletion.PrimaryEdit(tc.source, tc.line, tc.col, types.CompletionItem{InsertText: "X"})
			if err != nil || edit != tc.want {
				t.Fatalf("edit=%+v err=%v want=%+v", edit, err, tc.want)
			}
			after, err := lspedit.Apply(tc.source, []types.LSPTextEdit{edit})
			if err != nil || after != tc.after {
				t.Fatalf("after=%q err=%v want=%q", after, err, tc.after)
			}
		})
	}
}

func TestPrimaryEditStrictCursorAndExplicitContainment(t *testing.T) {
	for _, tc := range []struct {
		line, col int
		edit      types.LSPTextEdit
	}{
		{0, 2, types.LSPTextEdit{Col: 1, EndCol: 3, NewText: "X"}},
		{0, 3, types.LSPTextEdit{Col: 2, EndCol: 3, NewText: "X"}},
		{0, 0, types.LSPTextEdit{Col: 1, EndCol: 3, NewText: "X"}},
		{0, 4, types.LSPTextEdit{Col: 1, EndCol: 3, NewText: "X"}},
		{0, 3, types.LSPTextEdit{Col: 3, EndCol: 1, NewText: "X"}},
		{0, 3, types.LSPTextEdit{Col: 1, EndLine: 1, NewText: "X"}},
		{-1, 0, types.LSPTextEdit{NewText: "X"}},
		{0, -1, types.LSPTextEdit{NewText: "X"}},
		{0, 5, types.LSPTextEdit{NewText: "X"}},
		{0, 0, types.LSPTextEdit{Col: -1, EndCol: 1, NewText: "X"}},
		{0, 0, types.LSPTextEdit{EndCol: 2147483647, NewText: "X"}},
	} {
		got, err := lspcompletion.PrimaryEdit("a😀b", tc.line, tc.col, types.CompletionItem{TextEdit: &tc.edit})
		if !errors.Is(err, lspcompletion.ErrInvalid) || got != (types.LSPTextEdit{}) {
			t.Fatalf("cursor %d:%d edit=%+v: got=%+v err=%v", tc.line, tc.col, tc.edit, got, err)
		}
	}
	for _, col := range []int{1, 3} {
		original := types.LSPTextEdit{Col: 1, EndCol: 3, NewText: ""}
		got, err := lspcompletion.PrimaryEdit("a😀b", 0, col, types.CompletionItem{TextEdit: &original, Label: "not inserted"})
		if err != nil || got != original {
			t.Fatalf("inclusive cursor boundary: got=%+v err=%v", got, err)
		}
		got.NewText = "caller mutation"
		if original.NewText != "" {
			t.Fatal("returned primary aliases input")
		}
	}
}

func TestPrimaryEditSourceReplacementAndPlanBounds(t *testing.T) {
	for _, n := range []int{2 << 20, 2<<20 + 1} {
		edit, err := lspcompletion.PrimaryEdit(strings.Repeat(" ", n), 0, 0, types.CompletionItem{InsertText: "X"})
		if n == 2<<20 && (err != nil || edit != (types.LSPTextEdit{NewText: "X"})) {
			t.Fatalf("exact source bound refused: %v", err)
		}
		if n > 2<<20 && (!errors.Is(err, lspcompletion.ErrInvalid) || edit != (types.LSPTextEdit{})) {
			t.Fatalf("oversized source accepted: %v", err)
		}
	}
	for _, tc := range []struct {
		source string
		item   types.CompletionItem
	}{
		{"\xff", types.CompletionItem{InsertText: "X"}},
		{"", types.CompletionItem{InsertText: "\xff"}},
		{"", types.CompletionItem{InsertText: strings.Repeat("x", 65537)}},
		{"", types.CompletionItem{}},
		{"", types.CompletionItem{InsertText: "X", Snippet: &types.CompletionSnippet{}}},
	} {
		edit, err := lspcompletion.PrimaryEdit(tc.source, 0, 0, tc.item)
		if !errors.Is(err, lspcompletion.ErrInvalid) || edit != (types.LSPTextEdit{}) {
			t.Fatalf("invalid source/replacement/plan published: %+v err=%v", edit, err)
		}
	}
	item := initialInsertion(t, `{"label":"X","insertTextFormat":2,"insertText":"${1:X}"}`)
	item.Snippet.TextSHA256 = strings.Repeat("0", 64)
	if _, err := lspcompletion.PrimaryEdit("", 0, 0, item); !errors.Is(err, lspcompletion.ErrInvalid) {
		t.Fatalf("mismatched snippet digest accepted: %v", err)
	}
}
