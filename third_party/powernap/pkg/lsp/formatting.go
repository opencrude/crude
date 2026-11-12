package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"math"
	"strings"
	"unicode/utf8"
)

var ErrRangeFormattingUnsupported = errors.New("language server does not support selection formatting")

// RequestRangeFormatting sends only an advertised standard range request. It
// never substitutes a full-file request or grants authority to apply the edits.
func (c *Client) RequestRangeFormatting(ctx context.Context, uri protocol.DocumentURI, span protocol.Range, options protocol.FormattingOptions) (result json.RawMessage, err error) {
	defer completionContextResult(ctx, &result, &err)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !c.initialized.Load() {
		return nil, errors.New("client not initialized")
	}
	if len(uri) == 0 || len(uri) > 4096 || !utf8.ValidString(string(uri)) || strings.ContainsRune(string(uri), 0) || span.Start.Line > math.MaxInt32 || span.Start.Character > math.MaxInt32 || span.End.Line > math.MaxInt32 || span.End.Character > math.MaxInt32 || span.End.Line < span.Start.Line || span.End.Line == span.Start.Line && span.End.Character < span.Start.Character || options.TabSize < 1 || options.TabSize > 32 {
		return nil, errors.New("invalid formatting range or options")
	}
	supported := false
	if p := c.capabilities.DocumentRangeFormattingProvider; p != nil {
		switch v := p.Value.(type) {
		case bool:
			supported = v
		case protocol.DocumentRangeFormattingOptions:
			supported = true
		}
	}
	if !supported {
		return nil, ErrRangeFormattingUnsupported
	}
	params := protocol.DocumentRangeFormattingParams{TextDocument: protocol.TextDocumentIdentifier{URI: uri}, Range: span, Options: options}
	var raw json.RawMessage
	if err := c.conn.Call(ctx, "textDocument/rangeFormatting", params, &raw); err != nil {
		return nil, fmt.Errorf("selection formatting: %w", err)
	}
	if len(raw) == 0 || len(raw) > MaxCompletionResponseBytes || !utf8.Valid(raw) || !json.Valid(raw) {
		return nil, errors.New("invalid formatting response")
	}
	return bytes.Clone(raw), nil
}
