package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

// MaxHighlightBytes bounds one encoded document-highlight answer.
const MaxHighlightBytes = 1 << 20

var (
	ErrHighlightUnsupported = errors.New("language server does not advertise document highlights")
	ErrHighlightInvalid     = errors.New("invalid document highlight URI, position or raw JSON")
)

// RequestDocumentHighlights asks the initialized static provider for the
// occurrences of the symbol at one position. The original JSON is returned
// detached; the consumer validates ranges against its exact source.
func (c *Client) RequestDocumentHighlights(ctx context.Context, uri protocol.DocumentURI, position protocol.Position) (result json.RawMessage, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	defer func() {
		if canceled := ctx.Err(); canceled != nil {
			result = nil
			err = errors.Join(err, canceled)
		}
	}()
	if !c.initialized.Load() {
		return nil, errors.New("client not initialized")
	}
	if position.Line > math.MaxInt32 || position.Character > math.MaxInt32 ||
		len(uri) == 0 || len(uri) > 4096 || !utf8.ValidString(string(uri)) || strings.IndexByte(string(uri), 0) >= 0 {
		return nil, ErrHighlightInvalid
	}
	if !c.DocumentHighlightSupported() {
		return nil, ErrHighlightUnsupported
	}
	params := protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: uri}, Position: position}
	var raw json.RawMessage
	if err := c.conn.Call(ctx, "textDocument/documentHighlight", params, &raw); err != nil {
		return nil, fmt.Errorf("document highlight request failed: %w", err)
	}
	if len(raw) == 0 || len(raw) > MaxHighlightBytes || !utf8.Valid(raw) || !json.Valid(raw) {
		return nil, ErrHighlightInvalid
	}
	return bytes.Clone(raw), nil
}

// DocumentHighlightSupported observes only the initialized static advertisement.
func (c *Client) DocumentHighlightSupported() bool {
	if !c.initialized.Load() || c.capabilities.DocumentHighlightProvider == nil {
		return false
	}
	switch value := c.capabilities.DocumentHighlightProvider.Value.(type) {
	case bool:
		return value
	case protocol.DocumentHighlightOptions:
		return true
	}
	return false
}
