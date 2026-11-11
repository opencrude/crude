package lspclient

import (
	"context"
	"crude/types"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestAssistanceCapabilitiesAndRequestContextsOnWire(t *testing.T) {
	root := t.TempDir()
	integration := protocolIntegration(t, "assistance-context", root)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := integration.Start(ctx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "held-out.fixture")
	openTestFile(t, integration, ctx, path, "held-out", "join(a, )")
	caps := integration.AssistanceCapabilities()
	if !caps.Completion || !caps.SignatureHelp || !reflect.DeepEqual(caps.CompletionTriggers, []string{":", "@"}) || !reflect.DeepEqual(caps.SignatureTriggers, []string{"?"}) || !reflect.DeepEqual(caps.SignatureRetriggers, []string{";"}) {
		t.Fatalf("lost initialized capabilities: %+v", caps)
	}
	caps.CompletionTriggers[0] = "corrupt"
	if integration.AssistanceCapabilities().CompletionTriggers[0] != ":" {
		t.Fatal("capability observations alias the transport")
	}
	for _, trigger := range []types.LSPCompletionContext{{}, {TriggerKind: 2, TriggerCharacter: ":"}, {TriggerKind: 3}} {
		if _, err := integration.RequestCompletions(ctx, path, 0, 5, trigger); err != nil {
			t.Fatal(err)
		}
	}
	first, err := integration.RequestSignatureHelp(ctx, path, 0, 5, types.LSPSignatureContext{TriggerKind: 2, TriggerCharacter: "?"})
	if err != nil || len(first.Original) == 0 {
		t.Fatal("missing original signature help", err)
	}
	for _, trigger := range []types.LSPSignatureContext{{TriggerKind: 3, IsRetrigger: true, ActiveSignatureHelp: first.Original}, {TriggerKind: 2, TriggerCharacter: ";", IsRetrigger: true, ActiveSignatureHelp: first.Original}} {
		if _, err := integration.RequestSignatureHelp(ctx, path, 0, 7, trigger); err != nil {
			t.Fatal(err)
		}
	}
	for _, trigger := range []types.LSPCompletionContext{{TriggerKind: 2, TriggerCharacter: "."}, {TriggerKind: 2, TriggerCharacter: "::"}, {TriggerKind: 1, TriggerCharacter: ":"}, {TriggerKind: 9}, {TriggerKind: -1}} {
		if _, err := integration.RequestCompletions(ctx, path, 0, 5, trigger); err == nil {
			t.Fatal("invalid completion trigger reached server", trigger)
		}
	}
	for _, trigger := range []types.LSPSignatureContext{{TriggerKind: 2, TriggerCharacter: ";"}, {TriggerKind: 3, ActiveSignatureHelp: first.Original}, {TriggerKind: 3, IsRetrigger: true, ActiveSignatureHelp: []byte(`{"bad":true}`)}} {
		if _, err := integration.RequestSignatureHelp(ctx, path, 0, 5, trigger); err == nil {
			t.Fatal("invalid signature context reached server", trigger)
		}
	}
	trace, err := integration.RequestHover(ctx, path, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var calls []struct {
		Method string
		Params struct{ Context json.RawMessage }
	}
	if err := json.Unmarshal([]byte(trace), &calls); err != nil {
		t.Fatal(err)
	}
	completions, signatures := 0, 0
	for _, call := range calls {
		switch call.Method {
		case "textDocument/completion":
			var got map[string]any
			if err := json.Unmarshal(call.Params.Context, &got); err != nil {
				t.Fatal(err)
			}
			expected := []map[string]any{{"triggerKind": float64(1)}, {"triggerKind": float64(2), "triggerCharacter": ":"}, {"triggerKind": float64(3)}}
			if completions >= len(expected) || !reflect.DeepEqual(got, expected[completions]) {
				t.Fatalf("wrong completion context: %v", got)
			}
			completions++
		case "textDocument/signatureHelp":
			var got struct {
				TriggerKind         int
				TriggerCharacter    string
				IsRetrigger         bool
				ActiveSignatureHelp json.RawMessage
			}
			if err := json.Unmarshal(call.Params.Context, &got); err != nil {
				t.Fatal(err)
			}
			expectedKind := []int{2, 3, 2}
			expectedChar := []string{"?", "", ";"}
			if signatures >= 3 || got.TriggerKind != expectedKind[signatures] || got.TriggerCharacter != expectedChar[signatures] || got.IsRetrigger != (signatures > 0) {
				t.Fatalf("wrong signature context: %+v", got)
			}
			if signatures > 0 {
				var original, echo any
				if err := json.Unmarshal(first.Original, &original); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(got.ActiveSignatureHelp, &echo); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(original, echo) {
					t.Fatal("retrigger reconstructed the server's original markup or parameter offsets")
				}
			}
			signatures++
		}
	}
	if completions != 3 || signatures != 3 {
		t.Fatal("missing or extra wire requests", completions, signatures)
	}
}
