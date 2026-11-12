package lspclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/lspcompletion"
	"crude/snippet"
	"crude/types"

	"github.com/charmbracelet/x/powernap/pkg/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

// Literal wire fixtures cross the real framed subprocess. Only the consumer,
// never the adapter, interprets snippets and insertion semantics.
func completionProtocolResponse(mode string) any {
	switch mode {
	case "completion-array", "completion-no-resolve", "completion-stale", "completion-resolve-stale", "completion-resolve-error", "completion-resolve-null", "completion-resolve-array", "completion-resolve-large":
		return json.RawMessage(`[{"label":"same","insertTextFormat":2,"insertText":"${1:first}$0","data":{"token":9007199254740993,"fraction":1.2300e+09},"future":{"opaque":true}},{"label":"same","insertText":"second","data":{"token":9007199254740995}}]`)
	case "completion-list":
		return json.RawMessage(`{"isIncomplete":true,"items":[{"label":"same","data":9007199254740993},{"label":"same","data":9007199254740995}],"future":{"opaque":true}}`)
	case "completion-null":
		return nil
	case "completion-empty":
		return json.RawMessage(`[]`)
	case "completion-list-empty":
		return json.RawMessage(`{"isIncomplete":false,"items":[]}`)
	case "completion-malformed":
		return true
	case "completion-schema":
		return json.RawMessage(`{"items":null,"future":9007199254740993}`)
	case "completion-large":
		return json.RawMessage(`[{"label":"` + strings.Repeat("x", 1<<20) + `"}]`)
	case "completion-many":
		return json.RawMessage(`[` + strings.TrimSuffix(strings.Repeat(`{"label":"same"},`, 257), ",") + `]`)
	}
	r := func(start, end int) any {
		return map[string]any{"start": map[string]any{"line": 0, "character": start}, "end": map[string]any{"line": 0, "character": end}}
	}
	return map[string]any{"isIncomplete": false, "items": []any{
		map[string]any{"label": "nested", "insertTextFormat": 2, "insertText": "$TM_FILENAME",
			"textEdit":            map[string]any{"range": r(3, 4), "newText": "$1 / ${1:é${2:😀}}$0"},
			"additionalTextEdits": []any{map[string]any{"range": r(0, 0), "newText": "$TM_FILENAME ${9:literal}\n"}}},
		map[string]any{"label": "replace", "insertTextFormat": 2, "insertText": "${WRONG}",
			"textEdit": map[string]any{"insert": r(3, 3), "replace": r(3, 4), "newText": `${1|red,bl\,ue,green\|ish|}:$1$0`}},
		map[string]any{"label": "empty primary", "insertTextFormat": 2, "insertText": "WRONG",
			"textEdit": map[string]any{"range": r(3, 4), "newText": ""}},
		map[string]any{"label": "empty insert", "insertTextFormat": 2, "insertText": "$0"},
		map[string]any{"label": "${1:label}", "insertTextFormat": 2},
		map[string]any{"label": "literal", "insertTextFormat": 1, "insertText": "${1:literal}"},
		map[string]any{"label": "variable", "insertTextFormat": 2, "insertText": "$TM_FILENAME"},
		map[string]any{"label": "transform", "insertTextFormat": 2, "insertText": "${1/a/b/}"},
		map[string]any{"label": "malformed", "insertTextFormat": 2, "insertText": "${1:unterminated"},
		map[string]any{"label": "command", "insertTextFormat": 2, "insertText": "${1:safe}", "command": map[string]any{"title": "no", "command": "no"}},
		map[string]any{"label": "unknown format", "insertTextFormat": 3, "insertText": "no"},
	}}
}

func TestLSPCompletionFramedExpandedPlan(t *testing.T) {
	root := t.TempDir()
	l := protocolIntegration(t, "completion-normal", root)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := l.Start(ctx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "test.unknown")
	openTestFile(t, l, ctx, path, "arbitrary-language-id", "a😀b")
	response, err := l.RequestCompletions(ctx, path, 0, 3, types.LSPCompletionContext{})
	if err != nil || response.WireVersion == 0 || response.Unsupported || !response.ResolveSupported {
		t.Fatalf("completion evidence: %+v, %v", response, err)
	}
	parsed, err := lspcompletion.Parse(response.JSON)
	if err != nil || len(parsed.Items) != 11 {
		t.Fatalf("adapter must retain all initial objects: %+v, %v", parsed, err)
	}
	var items []types.CompletionItem
	for _, original := range parsed.Items {
		item, err := lspcompletion.Insertion(original)
		if err == nil {
			items = append(items, item)
		} else if !errors.Is(err, lspcompletion.ErrUnsupported) {
			t.Fatalf("unexpected insertion error: %v", err)
		}
	}
	if len(items) != 6 {
		t.Fatalf("expected six safe results only, got %+v", items)
	}
	if items[0].InsertText != "é😀 / é😀" || items[0].TextEdit == nil ||
		items[0].TextEdit.NewText != "é😀 / é😀" || items[0].TextEdit.Col != 3 || items[0].TextEdit.EndCol != 4 {
		t.Fatalf("primary TextEdit was not authoritative: %+v", items[0])
	}
	want := []types.CompletionOccurrence{
		{Stop: 1, StartByte: 0, EndByte: 6, Parent: -1},
		{Stop: 2, StartByte: 2, EndByte: 6, Parent: 0},
		{Stop: 1, StartByte: 9, EndByte: 15, Parent: -1},
		{Stop: 2, StartByte: 11, EndByte: 15, Parent: 2},
		{Stop: 0, StartByte: 15, EndByte: 15, Parent: -1},
	}
	if items[0].Snippet == nil {
		t.Fatal("expanded completion omitted numeric plan")
	}
	for i := range items[0].Snippet.Occurrences {
		if len(items[0].Snippet.Occurrences[i].Choices) == 0 {
			items[0].Snippet.Occurrences[i].Choices = nil
		}
	}
	if !reflect.DeepEqual(items[0].Snippet.Occurrences, want) {
		t.Fatalf("wire plan = %+v, want %+v", items[0].Snippet.Occurrences, want)
	}
	if !reflect.DeepEqual(items[0].AdditionalTextEdits, []types.LSPTextEdit{{NewText: "$TM_FILENAME ${9:literal}\n"}}) {
		t.Fatalf("additional edit must remain literal UTF16 edit: %+v", items[0].AdditionalTextEdits)
	}
	if items[1].InsertText != "red:red" || items[1].TextEdit == nil ||
		items[1].TextEdit.Col != 3 || items[1].TextEdit.EndCol != 4 || items[1].TextEdit.NewText != "red:red" {
		t.Fatalf("InsertReplaceEdit must use replacement: %+v", items[1])
	}
	if items[1].Snippet == nil || !reflect.DeepEqual(items[1].Snippet.Occurrences[0].Choices, []string{"red", "bl,ue", "green|ish"}) {
		t.Fatalf("escaped choices lost: %+v", items[1].Snippet)
	}
	for _, i := range []int{2, 3} {
		if items[i].InsertText != "" || items[i].Snippet == nil || len(items[i].Snippet.Occurrences) != 1 {
			t.Fatalf("empty expansion fell back to label: %+v", items[i])
		}
		o := items[i].Snippet.Occurrences[0]
		if o.Stop != 0 || o.StartByte != 0 || o.EndByte != 0 || o.Parent != -1 || len(o.Choices) != 0 {
			t.Fatalf("empty expansion lost final landing: %+v", o)
		}
	}
	if items[2].TextEdit == nil || items[2].TextEdit.NewText != "" || items[4].InsertText != "label" ||
		items[5].InsertText != "${1:literal}" || items[5].Snippet != nil {
		t.Fatalf("empty primary/label fallback/plain text: %+v", items)
	}
	for _, item := range items[:5] {
		if err := snippet.Validate(item.InsertText, item.Snippet); err != nil {
			t.Fatalf("wire plan not bound to expanded primary: %v", err)
		}
	}
	items[1].Snippet.Occurrences[0].Choices[0] = "mutated"
	if items[1].Snippet.Occurrences[1].Choices[0] != "red" {
		t.Fatal("choice occurrence aliases its mirror")
	}
	items[0].AdditionalTextEdits[0].NewText = "mutated"
	next, err := l.RequestCompletions(ctx, path, 0, 3, types.LSPCompletionContext{})
	if err != nil || !bytes.Equal(next.JSON, response.JSON) {
		t.Fatalf("consumer expansion mutated raw transport evidence: %s, %v", next.JSON, err)
	}
	// Confirm the generic client advertises snippet support, not a language
	// switch inferred from the deliberately unknown document language.
	value, err := l.RequestHover(ctx, path, 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	var trace []map[string]any
	if err := json.Unmarshal([]byte(value), &trace); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, msg := range trace {
		if msg["method"] == "initialize" {
			caps := msg["params"].(map[string]any)["capabilities"].(map[string]any)
			completion := caps["textDocument"].(map[string]any)["completion"].(map[string]any)
			want := `{"completionItem":{"commitCharactersSupport":true,"documentationFormat":["plaintext","markdown"],"insertReplaceSupport":true,"insertTextModeSupport":{"valueSet":[1,2]},"resolveSupport":{"properties":["documentation","detail"]},"snippetSupport":true},"contextSupport":true,"dynamicRegistration":false,"insertTextMode":1}`
			actual, err := json.Marshal(completion)
			if err != nil || string(actual) != want {
				t.Fatalf("actual negotiated completion grants: %s, %v", actual, err)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("initialize evidence absent")
	}
}

func completionProtocolGate() {
	root, _ := os.Getwd()
	if os.WriteFile(filepath.Join(root, "requested"), nil, 0600) != nil {
		os.Exit(2)
	}
	for {
		if _, err := os.Stat(filepath.Join(root, "release")); err == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

func completionProtocolResolved(original json.RawMessage, mode string) any {
	switch mode {
	case "completion-resolve-null":
		return nil
	case "completion-resolve-array":
		return json.RawMessage(`[]`)
	case "completion-resolve-large":
		return map[string]any{"label": "same", "documentation": strings.Repeat("x", 1<<20)}
	}
	// Preserve the original tokens rather than decoding opaque numbers through
	// interface{}; the client must send the exact retained object, not its label.
	result := bytes.TrimSpace(original)
	return json.RawMessage(string(result[:len(result)-1]) + `,"detail":"resolved detail","documentation":{"kind":"markdown","value":"**resolved**"}}`)
}

func completionTestIntegration(t *testing.T, mode, text string) (*LSPIntegration, context.Context, string) {
	t.Helper()
	root := t.TempDir()
	l := protocolIntegration(t, mode, root)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	t.Cleanup(cancel)
	if err := l.Start(ctx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "test.unknown")
	openTestFile(t, l, ctx, path, "arbitrary-language-id", text)
	return l, ctx, path
}

func completionTrace(t *testing.T, l *LSPIntegration, ctx context.Context, path string) []map[string]any {
	t.Helper()
	value, err := l.RequestHover(ctx, path, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var trace []map[string]any
	if err := json.Unmarshal([]byte(value), &trace); err != nil {
		t.Fatal(err)
	}
	return trace
}

func completionCallCount(trace []map[string]any, method string) int {
	count := 0
	for _, msg := range trace {
		if msg["method"] == method {
			count++
		}
	}
	return count
}

func TestLSPCompletionFramedOriginalVariants(t *testing.T) {
	for _, tc := range []struct {
		mode, want string
		null       bool
		incomplete bool
		count      int
	}{
		{"completion-array", `[{"label":"same","insertTextFormat":2,"insertText":"${1:first}$0","data":{"token":9007199254740993,"fraction":1.2300e+09},"future":{"opaque":true}},{"label":"same","insertText":"second","data":{"token":9007199254740995}}]`, false, false, 2},
		{"completion-list", `{"isIncomplete":true,"items":[{"label":"same","data":9007199254740993},{"label":"same","data":9007199254740995}],"future":{"opaque":true}}`, false, true, 2},
		{"completion-null", `null`, true, false, 0},
		{"completion-empty", `[]`, false, false, 0},
		{"completion-list-empty", `{"isIncomplete":false,"items":[]}`, false, false, 0},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			l, ctx, path := completionTestIntegration(t, tc.mode, "a😀b")
			response, err := l.RequestCompletions(ctx, path, 0, 3, types.LSPCompletionContext{})
			if err != nil || string(response.JSON) != tc.want || response.WireVersion <= 0 ||
				response.Unsupported || !response.ResolveSupported {
				t.Fatalf("original response: %s wire=%d unsupported=%v resolve=%v err=%v",
					response.JSON, response.WireVersion, response.Unsupported, response.ResolveSupported, err)
			}
			parsed, err := lspcompletion.Parse(response.JSON)
			if err != nil || parsed.Null != tc.null || parsed.Incomplete != tc.incomplete || len(parsed.Items) != tc.count {
				t.Fatalf("consumer result: %+v, %v", parsed, err)
			}
			for i := range response.JSON {
				response.JSON[i] = 'x'
			}
			again, err := l.RequestCompletions(ctx, path, 0, 3, types.LSPCompletionContext{})
			if err != nil || string(again.JSON) != tc.want {
				t.Fatalf("response storage shared: %s, %v", again.JSON, err)
			}
			trace := completionTrace(t, l, ctx, path)
			if completionCallCount(trace, "textDocument/completion") != 2 {
				t.Fatal("completion retried or bypassed the framed request")
			}
			for _, msg := range trace {
				if msg["method"] != "textDocument/completion" {
					continue
				}
				params := msg["params"].(map[string]any)
				if params["textDocument"].(map[string]any)["uri"] != string(protocol.URIFromPath(path)) ||
					!reflect.DeepEqual(params["position"], map[string]any{"line": float64(0), "character": float64(3)}) ||
					!reflect.DeepEqual(params["context"], map[string]any{"triggerKind": float64(1)}) {
					t.Fatalf("not one explicit strict UTF16 invocation: %+v", params)
				}
			}
		})
	}
}

func TestLSPCompletionFramedResolveOriginalIdentity(t *testing.T) {
	l, ctx, path := completionTestIntegration(t, "completion-array", "a😀b")
	response, err := l.RequestCompletions(ctx, path, 0, 3, types.LSPCompletionContext{})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := lspcompletion.Parse(response.JSON)
	if err != nil || len(parsed.Items) != 2 {
		t.Fatalf("initial originals: %+v, %v", parsed, err)
	}
	// Both rows have the same label: only the original ordinal's complete raw
	// object preserves which item, large token and insertion are being resolved.
	wantOriginals := []string{
		`{"label":"same","insertTextFormat":2,"insertText":"${1:first}$0","data":{"token":9007199254740993,"fraction":1.2300e+09},"future":{"opaque":true}}`,
		`{"label":"same","insertText":"second","data":{"token":9007199254740995}}`,
	}
	var resolved [][]byte
	for i, item := range parsed.Items {
		if string(item.Original) != wantOriginals[i] {
			t.Fatalf("original identity %d: %s", i, item.Original)
		}
		got, err := l.ResolveCompletion(ctx, path, response.WireVersion, item.Original)
		want := strings.TrimSuffix(wantOriginals[i], "}") + `,"detail":"resolved detail","documentation":{"kind":"markdown","value":"**resolved**"}}`
		if err != nil || string(got.JSON) != want || got.WireVersion != response.WireVersion ||
			got.Unsupported || !got.ResolveSupported {
			t.Fatalf("resolved original %d: %+v, %v", i, got, err)
		}
		doc, err := lspcompletion.ResolveDocumentation(item.Original, got.JSON)
		if err != nil || doc.Detail != "resolved detail" || doc.Value != "**resolved**" || doc.Kind != "markdown" {
			t.Fatalf("resolved documentation: %+v, %v", doc, err)
		}
		resolved = append(resolved, got.JSON)
	}
	for i := range resolved[0] {
		resolved[0][i] = 'x'
	}
	again, err := l.ResolveCompletion(ctx, path, response.WireVersion, parsed.Items[0].Original)
	if err != nil || !bytes.Contains(again.JSON, []byte(`9007199254740993`)) ||
		!bytes.Contains(resolved[1], []byte(`9007199254740995`)) {
		t.Fatalf("resolve result storage shared: %s, %v", again.JSON, err)
	}
	trace := completionTrace(t, l, ctx, path)
	var originals []string
	for _, msg := range trace {
		if raw, ok := msg["resolveOriginal"].(string); ok {
			originals = append(originals, raw)
		}
	}
	if !reflect.DeepEqual(originals, []string{wantOriginals[0], wantOriginals[1], wantOriginals[0]}) ||
		completionCallCount(trace, "completionItem/resolve") != 3 ||
		completionCallCount(trace, "workspace/executeCommand") != 0 {
		t.Fatalf("opaque originals reconstructed, retried or executed: %q", originals)
	}
}

func TestLSPCompletionFramedUnsupportedNeverCalls(t *testing.T) {
	for _, mode := range []string{"completion-unsupported", "completion-no-resolve"} {
		t.Run(mode, func(t *testing.T) {
			l, ctx, path := completionTestIntegration(t, mode, "hello")
			response, err := l.RequestCompletions(ctx, path, 0, 1, types.LSPCompletionContext{})
			unsupported := mode == "completion-unsupported"
			if err != nil || response.WireVersion <= 0 || response.Unsupported != unsupported || response.ResolveSupported {
				t.Fatalf("static capability result: %+v, %v", response, err)
			}
			if unsupported && response.JSON != nil {
				t.Fatal("unsupported completion fabricated empty JSON")
			}
			resolved, err := l.ResolveCompletion(ctx, path, response.WireVersion, []byte(`{"label":"original"}`))
			if err != nil || !resolved.Unsupported || resolved.JSON != nil || resolved.WireVersion != response.WireVersion {
				t.Fatalf("unsupported resolve: %+v, %v", resolved, err)
			}
			trace := completionTrace(t, l, ctx, path)
			wantCalls := 1
			if unsupported {
				wantCalls = 0
			}
			if completionCallCount(trace, "textDocument/completion") != wantCalls ||
				completionCallCount(trace, "completionItem/resolve") != 0 {
				t.Fatal("unsupported static provider made an RPC")
			}
		})
	}
}

func TestLSPCompletionFramedConsumerOwnsSchemaAndCount(t *testing.T) {
	for _, mode := range []string{"completion-schema", "completion-many"} {
		t.Run(mode, func(t *testing.T) {
			l, ctx, path := completionTestIntegration(t, mode, "hello")
			response, err := l.RequestCompletions(ctx, path, 0, 1, types.LSPCompletionContext{})
			if err != nil || response.Unsupported || response.JSON == nil {
				t.Fatalf("adapter normalized consumer-owned evidence: %+v, %v", response, err)
			}
			if _, err := lspcompletion.Parse(response.JSON); !errors.Is(err, lspcompletion.ErrInvalid) {
				t.Fatalf("invalid consumer evidence admitted: %v", err)
			}
			if mode == "completion-many" {
				var originals []json.RawMessage
				if err := json.Unmarshal(response.JSON, &originals); err != nil || len(originals) != 257 {
					t.Fatalf("adapter truncated originals: %d, %v", len(originals), err)
				}
			} else if string(response.JSON) != `{"items":null,"future":9007199254740993}` {
				t.Fatalf("adapter rewrote unknown evidence: %s", response.JSON)
			}
		})
	}
}

func TestLSPCompletionFramedTransportErrors(t *testing.T) {
	for _, mode := range []string{
		"completion-malformed", "completion-large", "completion-error",
		"completion-resolve-null", "completion-resolve-array", "completion-resolve-large", "completion-resolve-error",
	} {
		t.Run(mode, func(t *testing.T) {
			l, ctx, path := completionTestIntegration(t, mode, "hello")
			got, err := l.RequestCompletions(ctx, path, 0, 1, types.LSPCompletionContext{})
			method := "textDocument/completion"
			if strings.HasPrefix(mode, "completion-resolve-") {
				if err != nil {
					t.Fatal(err)
				}
				got, err = l.ResolveCompletion(ctx, path, got.WireVersion, []byte(`{"label":"same","data":9007199254740993}`))
				method = "completionItem/resolve"
			}
			if err == nil || !reflect.DeepEqual(got, types.LSPCompletionResponse{}) {
				t.Fatalf("transport failure became empty/partial success: %+v, %v", got, err)
			}
			if strings.HasSuffix(mode, "-error") {
				if errors.Is(err, lsp.ErrCompletionUnsupported) {
					t.Fatalf("RPC error was misclassified as unsupported: %v", err)
				}
			} else if !errors.Is(err, lsp.ErrCompletionInvalid) {
				t.Fatalf("invalid raw result error identity: %v", err)
			}
			if completionCallCount(completionTrace(t, l, ctx, path), method) != 1 {
				t.Fatal("failed request retried")
			}
		})
	}
}

func TestLSPCompletionFramedSourceBoundaries(t *testing.T) {
	for _, resolve := range []bool{false, true} {
		for _, action := range []string{"close", "close-reopen", "version", "equal-text-version", "cancel"} {
			name := "completion/" + action
			mode := "completion-stale"
			if resolve {
				name, mode = "resolve/"+action, "completion-resolve-stale"
			}
			t.Run(name, func(t *testing.T) {
				l, ctx, path := completionTestIntegration(t, mode, "a😀b")
				requestCtx, cancel := context.WithCancel(ctx)
				defer cancel()
				var wire int
				if resolve {
					initial, err := l.RequestCompletions(ctx, path, 0, 3, types.LSPCompletionContext{})
					if err != nil {
						t.Fatal(err)
					}
					wire = initial.WireVersion
				}
				type outcome struct {
					response types.LSPCompletionResponse
					err      error
				}
				result := make(chan outcome, 1)
				go func() {
					var got types.LSPCompletionResponse
					var err error
					if resolve {
						got, err = l.ResolveCompletion(requestCtx, path, wire, []byte(`{"label":"same","data":9007199254740993}`))
					} else {
						got, err = l.RequestCompletions(requestCtx, path, 0, 3, types.LSPCompletionContext{})
					}
					result <- outcome{got, err}
				}()
				root := filepath.Dir(path)
				waitProtocolFile(t, filepath.Join(root, "requested"))
				switch action {
				case "close", "close-reopen":
					l.CloseFile(ctx, path)
					if action == "close-reopen" {
						openTestFile(t, l, ctx, path, "arbitrary-language-id", "a😀b")
					}
				case "version", "equal-text-version":
					text := "a😀bc"
					boundary := testBoundary(path)
					if action == "equal-text-version" {
						text = "a😀b"
						boundary.Source.Revision++
					}
					if _, err := l.UpdateFile(ctx, path, text, boundary); err != nil {
						t.Fatal(err)
					}
				case "cancel":
					cancel()
				}
				if err := os.WriteFile(filepath.Join(root, "release"), nil, 0600); err != nil {
					t.Fatal(err)
				}
				select {
				case got := <-result:
					if !errors.Is(got.err, context.Canceled) || !reflect.DeepEqual(got.response, types.LSPCompletionResponse{}) {
						t.Fatalf("stale/cancelled evidence escaped: %+v, %v", got.response, got.err)
					}
				case <-ctx.Done():
					t.Fatal("completion did not retire after server released")
				}
			})
		}
	}
}

func TestLSPCompletionInvalidSourceAndOriginalNeverDispatch(t *testing.T) {
	l, ctx, path := completionTestIntegration(t, "completion-array", "a😀b\r\nx")
	for _, position := range [][2]int{{-1, 0}, {0, -1}, {0, 2}, {0, 5}, {1, 2}, {2, 0}, {1 << 30, 0}} {
		if got, err := l.RequestCompletions(ctx, path, position[0], position[1], types.LSPCompletionContext{}); err == nil || got.JSON != nil {
			t.Fatalf("invalid UTF16 source position %v admitted: %+v, %v", position, got, err)
		}
	}
	if got, err := l.RequestCompletions(ctx, filepath.Join(filepath.Dir(path), "not-open"), 0, 0, types.LSPCompletionContext{}); err == nil || got.JSON != nil {
		t.Fatalf("unopened source admitted: %+v, %v", got, err)
	}
	initial, err := l.RequestCompletions(ctx, path, 0, 3, types.LSPCompletionContext{})
	if err != nil {
		t.Fatal(err)
	}
	for _, original := range []string{"", "{", "null", "[]", `{"label":"` + strings.Repeat("x", 1<<20) + `"}`} {
		got, err := l.ResolveCompletion(ctx, path, initial.WireVersion, []byte(original))
		if !errors.Is(err, lsp.ErrCompletionInvalid) || got.JSON != nil {
			t.Fatalf("invalid original length %d admitted: %+v, %v", len(original), got, err)
		}
	}
	for _, wire := range []int{-1, 0, initial.WireVersion + 1} {
		if got, err := l.ResolveCompletion(ctx, path, wire, []byte(`{"label":"same"}`)); err == nil || got.JSON != nil {
			t.Fatalf("wrong original wire version %d admitted: %+v, %v", wire, got, err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if got, err := l.RequestCompletions(canceled, path, 0, 3, types.LSPCompletionContext{}); !errors.Is(err, context.Canceled) || got.JSON != nil {
		t.Fatalf("pre-canceled request admitted: %+v, %v", got, err)
	}
	if got, err := l.ResolveCompletion(canceled, path, initial.WireVersion, []byte(`{"label":"same"}`)); !errors.Is(err, context.Canceled) || got.JSON != nil {
		t.Fatalf("pre-canceled resolve admitted: %+v, %v", got, err)
	}
	trace := completionTrace(t, l, ctx, path)
	if completionCallCount(trace, "textDocument/completion") != 1 || completionCallCount(trace, "completionItem/resolve") != 0 {
		t.Fatal("invalid input or cancellation dispatched an RPC")
	}
}

func TestLSPCompletionSourceByteBound(t *testing.T) {
	l, ctx, path := completionTestIntegration(t, "completion-empty", strings.Repeat("x", 2<<20))
	atBound, err := l.RequestCompletions(ctx, path, 0, 2<<20, types.LSPCompletionContext{})
	if err != nil || string(atBound.JSON) != "[]" {
		t.Fatalf("exact 2MiB source refused: %+v, %v", atBound, err)
	}
	if _, err := l.UpdateFile(ctx, path, strings.Repeat("x", (2<<20)+1), testBoundary(path)); err != nil {
		t.Fatal(err)
	}
	if got, err := l.RequestCompletions(ctx, path, 0, 0, types.LSPCompletionContext{}); err == nil || got.JSON != nil {
		t.Fatalf("oversized source admitted: %+v, %v", got, err)
	}
	if completionCallCount(completionTrace(t, l, ctx, path), "textDocument/completion") != 1 {
		t.Fatal("oversized source reached server")
	}
}
