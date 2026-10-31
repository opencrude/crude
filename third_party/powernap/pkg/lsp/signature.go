package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

var ErrSignatureHelpUnsupported = errors.New("language server does not advertise signature help")

// RequestSignatureHelp uses the initialized server's static capability, not
// language-name inference or a speculative request to an unsupported provider.
// Raw JSON preserves optional activeParameter presence and both parameter label
// forms for the consumer's bounded validation. No dynamic registration, automatic
// triggering or no-active-parameter extension is advertised.
func (c *Client) RequestSignatureHelp(ctx context.Context, uri string, position protocol.Position) (json.RawMessage, error) {
	if !c.initialized.Load() {
		return nil, errors.New("client not initialized")
	}
	if c.capabilities.SignatureHelpProvider == nil {
		return nil, ErrSignatureHelpUnsupported
	}
	params := protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentURI(uri)},
		Position:     position,
	}
	var result json.RawMessage
	if err := c.conn.Call(ctx, "textDocument/signatureHelp", params, &result); err != nil {
		return nil, fmt.Errorf("signature help request failed: %w", err)
	}
	return result, nil
}
