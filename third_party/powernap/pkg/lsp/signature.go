package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"unicode/utf8"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

var ErrSignatureHelpUnsupported = errors.New("language server does not advertise signature help")

// SignatureHelpContext preserves the exact previous result, including optional
// fields and provider markup, instead of reconstructing it from UI text.
type SignatureHelpContext struct {
	TriggerKind         int             `json:"triggerKind"`
	TriggerCharacter    string          `json:"triggerCharacter,omitempty"`
	IsRetrigger         bool            `json:"isRetrigger"`
	ActiveSignatureHelp json.RawMessage `json:"activeSignatureHelp,omitempty"`
}

// RequestSignatureHelp uses the initialized server's static capability, not
// language-name inference or a speculative request to an unsupported provider.
// Raw JSON preserves optional activeParameter presence and both parameter label
// forms for the consumer's bounded validation. Static request context is supported;
// dynamic registration and the no-active-parameter extension are not advertised.
func (c *Client) RequestSignatureHelp(ctx context.Context, uri string, position protocol.Position, trigger SignatureHelpContext) (json.RawMessage, error) {
	if !c.initialized.Load() {
		return nil, errors.New("client not initialized")
	}
	if c.capabilities.SignatureHelpProvider == nil {
		return nil, ErrSignatureHelpUnsupported
	}
	if trigger.TriggerKind == 0 {
		trigger.TriggerKind = 1
	}
	if trigger.TriggerKind < 1 || trigger.TriggerKind > 3 || trigger.TriggerKind != 2 && trigger.TriggerCharacter != "" {
		return nil, errors.New("invalid signature-help trigger")
	}
	if trigger.TriggerKind == 2 {
		caps := c.capabilities.SignatureHelpProvider
		if !utf8.ValidString(trigger.TriggerCharacter) || utf8.RuneCountInString(trigger.TriggerCharacter) != 1 ||
			!slices.Contains(caps.TriggerCharacters, trigger.TriggerCharacter) && !(trigger.IsRetrigger && slices.Contains(caps.RetriggerCharacters, trigger.TriggerCharacter)) {
			return nil, errors.New("signature-help trigger was not advertised")
		}
	}
	if len(trigger.ActiveSignatureHelp) > 0 && (!trigger.IsRetrigger || len(trigger.ActiveSignatureHelp) > 64<<10 || !json.Valid(trigger.ActiveSignatureHelp)) {
		return nil, errors.New("invalid previous signature help")
	}
	params := struct {
		TextDocument protocol.TextDocumentIdentifier `json:"textDocument"`
		Position     protocol.Position               `json:"position"`
		Context      SignatureHelpContext            `json:"context"`
	}{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentURI(uri)},
		Position:     position,
		Context:      trigger,
	}
	var result json.RawMessage
	if err := c.conn.Call(ctx, "textDocument/signatureHelp", params, &result); err != nil {
		return nil, fmt.Errorf("signature help request failed: %w", err)
	}
	return result, nil
}
