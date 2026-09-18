package lsp

import (
	"testing"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

func TestCacheDocumentSaveCapabilities(t *testing.T) {
	client := &Client{capabilities: protocol.ServerCapabilities{TextDocumentSync: map[string]any{
		"willSave": true,
		"save":     map[string]any{"includeText": true},
	}}}
	client.cacheDocumentSaveCapabilities()
	if !client.willSave || !client.didSave || !client.didSaveText {
		t.Fatalf("capabilities: will=%v did=%v text=%v", client.willSave, client.didSave, client.didSaveText)
	}

	numeric := &Client{capabilities: protocol.ServerCapabilities{TextDocumentSync: float64(2)}}
	numeric.cacheDocumentSaveCapabilities()
	if numeric.willSave || numeric.didSave || numeric.didSaveText {
		t.Fatalf("numeric sync invented save support: %+v", numeric)
	}
}
