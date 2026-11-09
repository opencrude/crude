package lspcompletion

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestCompletionOriginalIdentityAndDocumentation(t *testing.T) {
	raw := []byte(`{"isIncomplete":true,"items":[{"label":"same","data":{"id":9007199254740993,"n":1.00},"documentation":"first docs"},{"label":"same","data":{"id":9007199254740995},"documentation":{"kind":"markdown","value":"second **docs**"}}]}`)
	got, err := Parse(raw)
	if err != nil || got.Null || !got.Incomplete || len(got.Items) != 2 {
		t.Fatal("literal standard completion list", got, err)
	}
	if got.Items[0].Documentation.Value != "first docs" || got.Items[0].Documentation.Kind != "plaintext" ||
		got.Items[1].Documentation.Value != "second **docs**" || got.Items[1].Documentation.Kind != "markdown" {
		t.Fatal("literal row documentation lost", got.Items)
	}
	if !bytes.Contains(got.Items[0].Original, []byte(`9007199254740993`)) ||
		!bytes.Contains(got.Items[0].Original, []byte(`1.00`)) {
		t.Fatal("opaque identity was decoded through a lossy numeric value")
	}
	original := bytes.Clone(got.Items[0].Original)
	for i := range raw {
		raw[i] = ' '
	}
	if !bytes.Equal(original, got.Items[0].Original) {
		t.Fatal("caller response mutation changed original item")
	}
	resolved := []byte(`{"documentation":{"kind":"plaintext","value":"selected first docs"},"data":{"n":1.00,"id":9007199254740993},"label":"same","detail":"selected detail"}`)
	doc, err := ResolveDocumentation(original, resolved)
	if err != nil || doc.Value != "selected first docs" || doc.Detail != "selected detail" ||
		!doc.HasDetail || !doc.HasDocumentation {
		t.Fatal("exact original item did not resolve its docs", doc, err)
	}
	if !bytes.Equal(original, got.Items[0].Original) {
		t.Fatal("documentation resolution mutated the insertion identity")
	}
}

func TestCompletionNullEmptyAndAbsentDocumentationRemainDistinct(t *testing.T) {
	null, err := Parse([]byte("null"))
	if err != nil || !null.Null || null.Incomplete || len(null.Items) != 0 {
		t.Fatal(null, err)
	}
	empty, err := Parse([]byte("[]"))
	if err != nil || empty.Null || len(empty.Items) != 0 {
		t.Fatal(empty, err)
	}
	got, err := Parse([]byte(`[{"label":"absent"},{"label":"empty","documentation":""}]`))
	if err != nil || len(got.Items) != 2 || got.Items[0].Documentation.HasDocumentation ||
		!got.Items[1].Documentation.HasDocumentation || got.Items[1].Documentation.Value != "" {
		t.Fatal("absence was restamped as observed empty documentation", got, err)
	}
}

func TestCompletionDocumentationResolveCannotChangeInsertionOrOpaqueIdentity(t *testing.T) {
	original := []byte(`{"label":"same","data":{"id":9007199254740993},"insertText":"${1:safe}$0","insertTextFormat":2,"detail":"before"}`)
	for name, resolved := range map[string]string{
		"different row": `{"label":"same","data":{"id":9007199254740995},"insertText":"${1:safe}$0","insertTextFormat":2}`,
		"insertion":     `{"label":"same","data":{"id":9007199254740993},"insertText":"unsafe","insertTextFormat":2}`,
		"missing":       `{"label":"same","data":{"id":9007199254740993},"insertTextFormat":2}`,
		"import":        `{"label":"same","data":{"id":9007199254740993},"insertText":"${1:safe}$0","insertTextFormat":2,"additionalTextEdits":[]}`,
		"command":       `{"label":"same","data":{"id":9007199254740993},"insertText":"${1:safe}$0","insertTextFormat":2,"command":{"title":"run","command":"run"}}`,
		"null docs":     `{"label":"same","data":{"id":9007199254740993},"insertText":"${1:safe}$0","insertTextFormat":2,"documentation":null}`,
		"partial":       `{"documentation":"no original label or insertion identity"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if doc, err := ResolveDocumentation(original, []byte(resolved)); !errors.Is(err, ErrInvalid) || doc != (Documentation{}) {
				t.Fatal("unsupported resolve changed original completion authority", name, doc, err)
			}
		})
	}
	numeric := []byte(`{"label":"n","data":1.00}`)
	if _, err := ResolveDocumentation(numeric, []byte(`{"label":"n","data":1,"documentation":"same math is not same opaque token"}`)); !errors.Is(err, ErrInvalid) {
		t.Fatal("numeric-token identity was normalized away", err)
	}
}

func TestCompletionOriginalJSONRefusesLossAndUnsupportedListDefaults(t *testing.T) {
	for _, raw := range []string{
		`[{"label":"x","label":"y"}]`,
		`[{"label":"x","data":{"same":1,"same":2}}]`,
		`[{"label":"x","data":"\ud800"}]`,
		`[{"label":"x","documentation":{"kind":"markdown","value":"\udc00"}}]`,
		`[{"label":1}]`, `[{"label":""}]`, `[null]`, `true`, `{"items":[]}`,
		`{"isIncomplete":null,"items":[]}`, `{"isIncomplete":false,"items":null}`,
		`{"isIncomplete":false,"items":[],"itemDefaults":{"editRange":{}}}`,
		`[{"label":"x","documentation":{"kind":"html","value":"no"}}]`,
		`[{"label":"x","detail":null}]`, `[] null`,
		`[{"label":"x","filterText":null}]`, `[{"label":"x","sortText":{}}]`,
		`[{"label":"` + string([]byte{0xff}) + `"}]`,
		`[{"label":"x","data":` + strings.Repeat("[", MaxDepth+1) + "0" + strings.Repeat("]", MaxDepth+1) + `}]`,
		`[{"label":"x","documentation":"` + strings.Repeat("d", MaxStringBytes+1) + `"}]`,
	} {
		if result, err := Parse([]byte(raw)); !errors.Is(err, ErrInvalid) || len(result.Items) != 0 {
			t.Fatalf("malformed or unsupported evidence admitted: %.100q %v", raw, err)
		}
	}
	array := "[" + strings.Repeat(`{"label":"safe"},`, MaxItems) + `{"label":"excess"}]`
	if _, err := Parse([]byte(array)); !errors.Is(err, ErrInvalid) {
		t.Fatal("accepted overbound completion list", err)
	}
	if _, err := Parse(bytes.Repeat([]byte(" "), MaxRawBytes+1)); !errors.Is(err, ErrInvalid) {
		t.Fatal("accepted overbound original response", err)
	}
}

func TestCompletionFilterAndSortRetainProviderText(t *testing.T) {
	raw := []byte(`[{"label":"display(a)","filterText":"differentMatch","sortText":"0002"},{"label":"default"}]`)
	result, err := Parse(raw)
	if err != nil || len(result.Items) != 2 {
		t.Fatal(result, err)
	}
	if result.Items[0].FilterText != "differentMatch" || result.Items[0].SortText != "0002" ||
		result.Items[1].FilterText != "default" || result.Items[1].SortText != "default" {
		t.Fatal("provider match/ranking data or absent-field defaults changed", result.Items)
	}
}

func TestCompletionOpaqueStringEscapesAndSurrogatesStayExact(t *testing.T) {
	for _, raw := range []string{
		`[{"label":"valid","data":"\ud83d\ude00","documentation":"😀"}]`,
		`[{"label":"valid","data":"\\ud800","documentation":"a \"quote\" and \\\\ pair"}]`,
		`[{"label":"valid","data":{"x\"y":"\u0000"},"documentation":"escaped literal"}]`,
	} {
		result, err := Parse([]byte(raw))
		if err != nil || len(result.Items) != 1 {
			t.Fatal("valid escaped opaque identity was refused", raw, result, err)
		}
		doc, err := ResolveDocumentation(result.Items[0].Original, result.Items[0].Original)
		if err != nil || doc != result.Items[0].Documentation {
			t.Fatal("unchanged original documentation could not be observed", doc, err)
		}
	}
}

func FuzzCompletionOriginalAndDocumentationOnlyResolve(f *testing.F) {
	f.Add([]byte(`[{"label":"same","data":9007199254740993,"documentation":"literal docs"}]`),
		[]byte(`{"label":"same","data":9007199254740993,"detail":"resolved details"}`))
	f.Add([]byte(`{"isIncomplete":true,"items":[{"label":"x","insertText":"${1:x}$0","documentation":{"kind":"markdown","value":"**not executed**"}}]}`),
		[]byte(`{"label":"x","insertText":"${1:x}$0","documentation":"resolved"}`))
	f.Add([]byte(`[{"label":"\ud800","data":{"duplicate":1,"duplicate":2}}]`), []byte(`null`))
	f.Fuzz(func(t *testing.T, raw, resolved []byte) {
		result, err := Parse(raw)
		if err != nil {
			if len(result.Items) != 0 {
				t.Fatal("refused original retained an apparently usable row")
			}
			return
		}
		if len(result.Items) > MaxItems {
			t.Fatal("accepted overbound result")
		}
		for _, item := range result.Items {
			before := bytes.Clone(item.Original)
			same, err := ResolveDocumentation(item.Original, item.Original)
			if err != nil || same != item.Documentation {
				t.Fatal("accepted original does not satisfy its own resolve identity", err)
			}
			doc, err := ResolveDocumentation(item.Original, resolved)
			if err != nil && doc != (Documentation{}) {
				t.Fatal("refused resolve retained apparently usable documentation")
			}
			if !bytes.Equal(before, item.Original) {
				t.Fatal("resolve mutated the original insertion identity")
			}
		}
	})
}

func BenchmarkCompletionOriginalAndDocumentationOnlyResolve(b *testing.B) {
	original := []byte(`{"label":"same","data":{"opaque":9007199254740993},"insertText":"${1:value}$0","documentation":"` + strings.Repeat("bounded documentation ", 256) + `"}`)
	response := append(append([]byte("["), original...), ']')
	b.Run("original", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(response)))
		for b.Loop() {
			if _, err := Parse(response); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("resolve", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(original)))
		for b.Loop() {
			if _, err := ResolveDocumentation(original, original); err != nil {
				b.Fatal(err)
			}
		}
	})
}
