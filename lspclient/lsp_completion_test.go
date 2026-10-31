package lspclient

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/snippet"
	"crude/types"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

// Literal wire fixtures exercise the real decoder, primary-edit precedence and
// transport plan together; expected output below is not derived from Parse.
func completionProtocolResponse(mode string) any {
	if mode == "completion-many" {
		items := make([]any, maxCompletionItems+1)
		for i := range items {
			items[i] = map[string]any{"label": "too many"}
		}
		return items
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
	items := l.RequestCompletions(ctx, path, 0, 3)
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
	next := l.RequestCompletions(ctx, path, 0, 3)
	if len(next) != 6 || next[1].Snippet.Occurrences[0].Choices[0] != "red" ||
		next[0].AdditionalTextEdits[0].NewText != "$TM_FILENAME ${9:literal}\n" {
		t.Fatal("returned mutable data corrupted another answer")
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
			if completion["completionItem"].(map[string]any)["snippetSupport"] != true {
				t.Fatal("numeric snippet capability missing")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("initialize evidence absent")
	}
}

func TestLSPCompletionRefusesNullTaggedEdits(t *testing.T) {
	for _, value := range []any{nil, (*protocol.TextEdit)(nil), (*protocol.InsertReplaceEdit)(nil), "unknown"} {
		item := protocol.CompletionItem{Label: "must not fall back", TextEdit: &protocol.Or_CompletionItem_textEdit{Value: value}}
		if got := convertCompletions(context.Background(), []protocol.CompletionItem{item}); len(got) != 0 {
			t.Fatalf("invalid tagged primary admitted: %+v", got)
		}
	}
	for _, value := range []any{nil, (*protocol.MarkupContent)(nil)} {
		item := protocol.CompletionItem{Label: "safe", Documentation: &protocol.Or_CompletionItem_documentation{Value: value}}
		if got := convertCompletions(context.Background(), []protocol.CompletionItem{item}); len(got) != 1 || got[0].Documentation != "" {
			t.Fatalf("null documentation panicked or changed safe insertion: %+v", got)
		}
	}
}

func TestLSPCompletionConsumerBounds(t *testing.T) {
	format := protocol.SnippetTextFormat
	for name, items := range map[string][]protocol.CompletionItem{
		"item count":       make([]protocol.CompletionItem, maxCompletionItems+1),
		"source bytes":     {{Label: "bounded", InsertText: strings.Repeat("x", snippet.MaxSourceBytes+1), InsertTextFormat: &format}},
		"metadata bytes":   {{Label: strings.Repeat("x", snippet.MaxSourceBytes+1)}},
		"additional count": {{Label: "bounded", AdditionalTextEdits: make([]protocol.TextEdit, maxCompletionAdditionalEdits+1)}},
		"additional bytes": {{Label: "bounded", AdditionalTextEdits: []protocol.TextEdit{{NewText: strings.Repeat("x", snippet.MaxSourceBytes+1)}}}},
		"aggregate input": func() []protocol.CompletionItem {
			items := make([]protocol.CompletionItem, 17)
			for i := range items {
				items[i].InsertText = strings.Repeat("x", snippet.MaxSourceBytes)
			}
			return items
		}(),
		"aggregate expanded": func() []protocol.CompletionItem {
			items := make([]protocol.CompletionItem, 18)
			for i := range items {
				items[i].InsertText = "${1:" + strings.Repeat("x", 30_000) + "}$1"
				items[i].InsertTextFormat = &format
			}
			return items
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if got := convertCompletions(context.Background(), items); got != nil {
				t.Fatalf("over-bound result must be refused atomically, got %d items", len(got))
			}
		})
	}
	items := make([]protocol.CompletionItem, maxCompletionItems)
	for i := range items {
		items[i].Label = "safe"
	}
	if got := convertCompletions(context.Background(), items); len(got) != maxCompletionItems {
		t.Fatalf("exact item bound refused: %d", len(got))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := convertCompletions(ctx, items); got != nil {
		t.Fatal("cancelled conversion published an answer")
	}
}

func TestLSPCompletionFramedConsumerCountBound(t *testing.T) {
	root := t.TempDir()
	l := protocolIntegration(t, "completion-many", root)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := l.Start(ctx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "test.go")
	openTestFile(t, l, ctx, path, "go", "hello")
	if got := l.RequestCompletions(ctx, path, 0, 1); got != nil {
		t.Fatal("oversized framed result was truncated instead of refused")
	}
}

func TestLSPCompletionFramedSourceBoundary(t *testing.T) {
	for _, action := range []string{"close-reopen", "version", "cancel"} {
		t.Run(action, func(t *testing.T) {
			root := t.TempDir()
			l := protocolIntegration(t, "completion-stale", root)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := l.Start(ctx); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "test.go")
			openTestFile(t, l, ctx, path, "go", "a😀b")
			result := make(chan []CompletionItem, 1)
			go func() { result <- l.RequestCompletions(ctx, path, 0, 3) }()
			waitProtocolFile(t, filepath.Join(root, "requested"))
			switch action {
			case "close-reopen":
				l.CloseFile(ctx, path)
				openTestFile(t, l, ctx, path, "go", "a😀b")
			case "version":
				if _, err := l.UpdateFile(ctx, path, "a😀bc", testBoundary(path)); err != nil {
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
				if got != nil {
					t.Fatal("stale/cancelled completion escaped source boundary")
				}
			case <-time.After(time.Second):
				t.Fatal("completion did not retire after server released")
			}
		})
	}
}
