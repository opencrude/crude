package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"testing"
)

func TestRangeFormattingNegotiationAndWire(t *testing.T) {
	for _, capability := range []string{"null", "false", "true", "{}"} {
		t.Run(capability, func(t *testing.T) {
			c, calls := codeActionWireClient(t, "", func(r codeActionWireRequest) string {
				if r.Method != "textDocument/rangeFormatting" {
					t.Error("whole-document fallback", r.Method)
				}
				var p protocol.DocumentRangeFormattingParams
				if err := json.Unmarshal(r.Params, &p); err != nil {
					t.Error(err)
				}
				if p.TextDocument.URI != "file:///source" || p.Range.Start.Character != 2 || p.Range.End.Character != 9 || p.Options.TabSize != 8 || p.Options.InsertSpaces {
					t.Error("range/options changed", string(r.Params))
				}
				return "[]"
			})
			if err := json.Unmarshal([]byte(`{"documentRangeFormattingProvider":`+capability+`}`), &c.capabilities); err != nil {
				t.Fatal(err)
			}
			raw, err := c.RequestRangeFormatting(context.Background(), "file:///source", protocol.Range{Start: protocol.Position{Character: 2}, End: protocol.Position{Character: 9}}, protocol.FormattingOptions{TabSize: 8})
			supported := capability == "true" || capability == "{}"
			if supported {
				if err != nil || string(raw) != "[]" || calls.Load() != 1 {
					t.Fatal(raw, err)
				}
			} else if !errors.Is(err, ErrRangeFormattingUnsupported) || calls.Load() != 0 {
				t.Fatal("unsupported provider was probed", err)
			}
		})
	}
}
func TestRangeFormattingCancelledResponseAndInvalidRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c, calls := codeActionWireClient(t, "", func(codeActionWireRequest) string { cancel(); return "null" })
	c.capabilities.DocumentRangeFormattingProvider = &protocol.Or_ServerCapabilities_documentRangeFormattingProvider{Value: true}
	if raw, err := c.RequestRangeFormatting(ctx, "file:///s", protocol.Range{}, protocol.FormattingOptions{TabSize: 4}); raw != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(raw, err)
	}
	before := calls.Load()
	for _, n := range []uint32{0, 33} {
		if _, err := c.RequestRangeFormatting(context.Background(), "file:///s", protocol.Range{}, protocol.FormattingOptions{TabSize: n}); err == nil {
			t.Fatal("invalid indent width accepted")
		}
	}
	if calls.Load() != before {
		t.Fatal("invalid request reached provider")
	}
}
