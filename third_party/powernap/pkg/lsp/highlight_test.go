package lsp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

func TestDocumentHighlightsFollowTheStaticAdvertisement(t *testing.T) {
	for _, tc := range []struct {
		name, caps string
		supported  bool
	}{
		{"absent", `{}`, false},
		{"false", `{"documentHighlightProvider":false}`, false},
		{"true", `{"documentHighlightProvider":true}`, true},
		{"options", `{"documentHighlightProvider":{"workDoneProgress":true}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var params string
			c, calls := capabilityWireClient(t, tc.caps, func(request codeActionWireRequest) string {
				if request.Method != "textDocument/documentHighlight" {
					t.Errorf("unexpected method %s", request.Method)
				}
				params = string(request.Params)
				return `,"result":[{"range":{"start":{"line":0,"character":1},"end":{"line":0,"character":4}},"kind":3}]`
			})
			if c.DocumentHighlightSupported() != tc.supported {
				t.Fatal("support differs from the initialized advertisement")
			}
			raw, err := c.RequestDocumentHighlights(context.Background(), "file:///tmp/a.go", protocol.Position{Line: 0, Character: 2})
			if !tc.supported {
				if raw != nil || !errors.Is(err, ErrHighlightUnsupported) || calls.Load() != 0 {
					t.Fatal("unsupported provider was queried", raw, err)
				}
				return
			}
			if err != nil || !strings.Contains(string(raw), `"kind":3`) || calls.Load() != 1 {
				t.Fatal("highlight answer was not returned exactly", string(raw), err)
			}
			if !strings.Contains(params, `"position":{"line":0,"character":2}`) || strings.Contains(params, "context") {
				t.Fatal("request did not send a plain text-document position", params)
			}
		})
	}
}

func TestDocumentHighlightsRefuseInvalidOperandsAndAnswers(t *testing.T) {
	c, calls := capabilityWireClient(t, `{"documentHighlightProvider":true}`, func(codeActionWireRequest) string {
		return `,"result":{"broken"`
	})
	if _, err := c.RequestDocumentHighlights(context.Background(), "", protocol.Position{}); !errors.Is(err, ErrHighlightInvalid) || calls.Load() != 0 {
		t.Fatal("an empty URI was sent", err)
	}
	if raw, err := c.RequestDocumentHighlights(context.Background(), "file:///a", protocol.Position{}); raw != nil || err == nil {
		t.Fatal("malformed JSON was accepted", string(raw))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.RequestDocumentHighlights(ctx, "file:///a", protocol.Position{}); !errors.Is(err, context.Canceled) {
		t.Fatal("a cancelled request was sent", err)
	}
}
