package lspclient

import (
	"context"
	"crude/types"
	"errors"
	"github.com/charmbracelet/x/powernap/pkg/lsp"
	"reflect"
	"testing"
)

// The client must negotiate the actual protocol features before a provider can
// offer semantic colors. This traverses framed initialize, not a helper map.
func TestLSPSemanticNegotiationIsVisibleToConnectedProvider(t *testing.T) {
	l, ctx, path := completionTestIntegration(t, "completion-normal", "package p\nvar value = 1\n")
	trace := completionTrace(t, l, ctx, path)
	for _, msg := range trace {
		if msg["method"] != "initialize" {
			continue
		}
		params := msg["params"].(map[string]any)
		caps := params["capabilities"].(map[string]any)
		text := caps["textDocument"].(map[string]any)
		semantic, ok := text["semanticTokens"].(map[string]any)
		if !ok {
			t.Fatal("connected provider was not told semantic colors are supported")
		}
		requests, ok := semantic["requests"].(map[string]any)
		if !ok {
			t.Fatal("semantic request capabilities absent")
		}
		full, ok := requests["full"].(map[string]any)
		if !ok || full["delta"] != true || requests["range"] != true {
			t.Fatalf("full, delta and range were not negotiated: %v", requests)
		}
		formats, ok := semantic["formats"].([]any)
		if !ok || len(formats) != 1 || formats[0] != "relative" {
			t.Fatalf("invalid token encoding: %v", semantic["formats"])
		}
		if semantic["dynamicRegistration"] != false || semantic["overlappingTokenSupport"] != false || semantic["multilineTokenSupport"] != false {
			t.Fatalf("unsupported token shapes advertised: %v", semantic)
		}
		return
	}
	t.Fatal("initialize request absent")
}

func TestLSPSemanticAdapterObservesOnlyAdvertisedRequests(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		full, delta, span    bool
		unsupported, invalid bool
	}{
		{"semantic-full", true, true, true, false, false},
		{"semantic-range", false, false, true, false, false},
		{"semantic-none", false, false, false, true, false},
		{"semantic-invalid", false, false, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, ctx, path := completionTestIntegration(t, tc.name, "a😀x\n")
			support, err := l.RequestSemanticTokens(ctx, path, "support", "", types.LSPSemanticRange{})
			if tc.invalid {
				if !errors.Is(err, lsp.ErrSemanticInvalid) {
					t.Fatal("malformed legend granted", support, err)
				}
				return
			}
			if err != nil || support.Unsupported != tc.unsupported {
				t.Fatal(support, err)
			}
			if tc.unsupported {
				return
			}
			if support.Support.Full != tc.full || support.Support.Delta != tc.delta || support.Support.Range != tc.span || support.WireVersion <= 0 {
				t.Fatal("incorrect provider support", support)
			}
			span := types.LSPSemanticRange{Start: types.LSPSemanticPosition{Line: 0}, End: types.LSPSemanticPosition{Line: 0, Character: 4}}
			mode := "range"
			if tc.full {
				mode = "full"
			}
			result, err := l.RequestSemanticTokens(ctx, path, mode, "", span)
			if err != nil {
				t.Fatal(err)
			}
			if result.WireVersion != support.WireVersion || !reflect.DeepEqual(result.Support, support.Support) || len(result.JSON) == 0 {
				t.Fatal("lost synchronized provider evidence", result)
			}
			if tc.delta {
				delta, err := l.RequestSemanticTokens(ctx, path, "full/delta", "exact", span)
				if err != nil || string(delta.JSON) != `{"resultId":"next","edits":[{"start":0,"deleteCount":1,"data":[0]}]}` {
					t.Fatal(delta, err)
				}
			} else if _, err := l.RequestSemanticTokens(ctx, path, "full/delta", "exact", span); !errors.Is(err, lsp.ErrSemanticUnsupported) {
				t.Fatal("unadvertised delta reached server", err)
			}
			trace := completionTrace(t, l, ctx, path)
			if completionCallCount(trace, "textDocument/semanticTokens/"+mode) != 1 {
				t.Fatal("semantic request missed provider")
			}
			if !tc.delta && completionCallCount(trace, "textDocument/semanticTokens/full/delta") != 0 {
				t.Fatal("unadvertised delta was dispatched")
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := l.RequestSemanticTokens(cancelled, path, mode, "", span); !errors.Is(err, context.Canceled) {
				t.Fatal("cancelled semantic request ran", err)
			}
		})
	}
}
