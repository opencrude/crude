package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

// MaxCompletionResponseBytes bounds encoded responses and original resolve
// objects after frame decoding. It does not bound decoder allocations or RSS.
const MaxCompletionResponseBytes = 1 << 20

var (
	ErrCompletionUnsupported = errors.New("language server does not advertise completion support")
	ErrCompletionInvalid     = errors.New("invalid completion URI, position or raw JSON")
)

// CompletionClientCapabilities constructs only the textDocument.completion leaf.
// The caller owns initialize wiring. Resolve supports documentation and detail,
// never deferred edits or commands. Request context supports invocation,
// advertised characters and incomplete-list refresh, not dynamic registration
// or list defaults. Item commit characters are supported; the provider's
// allCommitCharacters supplies the default.
func CompletionClientCapabilities(enableSnippets bool) map[string]any {
	return map[string]any{
		"dynamicRegistration": false,
		"contextSupport":      true,
		"insertTextMode":      1, // Unspecified item mode remains literal as-is.
		"completionItem": map[string]any{
			"insertTextModeSupport":   map[string]any{"valueSet": []int{1, 2}},
			"snippetSupport":          enableSnippets,
			"insertReplaceSupport":    true,
			"commitCharactersSupport": true,
			"documentationFormat":     []string{"plaintext", "markdown"},
			"resolveSupport": map[string]any{
				"properties": []string{"documentation", "detail"},
			},
		},
	}
}

// RequestCompletionsRaw sends one context-qualified invocation to the initialized
// static provider. Null, arrays and CompletionList objects remain detached
// original JSON; no CompletionItem decoding or numeric conversion occurs.
//
// Validation covers encoded bounds, UTF-8 bytes, JSON syntax and the outer
// response container only. The consumer owns strict schema, duplicate keys,
// Unicode escapes, source-relative UTF-16 boundaries, edits and provenance.
func (c *Client) RequestCompletionsRaw(ctx context.Context, uri string, line, col int, trigger protocol.CompletionContext) (result json.RawMessage, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	defer completionContextResult(ctx, &result, &err)
	if !c.initialized.Load() {
		return nil, errors.New("client not initialized")
	}
	if len(uri) == 0 || len(uri) > 4096 || !utf8.ValidString(uri) || strings.IndexByte(uri, 0) >= 0 ||
		line < 0 || col < 0 || uint64(line) > math.MaxInt32 || uint64(col) > math.MaxInt32 {
		return nil, ErrCompletionInvalid
	}
	if c.capabilities.CompletionProvider == nil {
		return nil, ErrCompletionUnsupported
	}
	if trigger.TriggerKind == 0 {
		trigger.TriggerKind = protocol.Invoked
	}
	switch trigger.TriggerKind {
	case protocol.Invoked, protocol.TriggerForIncompleteCompletions:
		if trigger.TriggerCharacter != "" {
			return nil, ErrCompletionInvalid
		}
	case protocol.TriggerCharacter:
		if !utf8.ValidString(trigger.TriggerCharacter) || utf8.RuneCountInString(trigger.TriggerCharacter) != 1 || !slices.Contains(c.capabilities.CompletionProvider.TriggerCharacters, trigger.TriggerCharacter) {
			return nil, ErrCompletionInvalid
		}
	default:
		return nil, ErrCompletionInvalid
	}
	params := struct {
		TextDocument protocol.TextDocumentIdentifier `json:"textDocument"`
		Position     protocol.Position               `json:"position"`
		Context      protocol.CompletionContext      `json:"context"`
	}{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentURI(uri)},
		Position: protocol.Position{
			Line: uint32(line), Character: uint32(col), //nolint:gosec // Checked int32 bounds above.
		},
		Context: trigger,
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err := c.conn.Call(ctx, "textDocument/completion", params, &raw); err != nil {
		return nil, fmt.Errorf("completion request failed: %w", err)
	}
	if !validCompletionRaw(raw, false) {
		return nil, ErrCompletionInvalid
	}
	return bytes.Clone(raw), nil
}

// ResolveCompletionRaw forwards a detached original object as RawMessage.
// encoding/json may compact whitespace and escape HTML when encoding params;
// opaque numbers, duplicate evidence and unknown fields are not reconstructed.
// This method never executes commands or edits. Consumers must admit only
// allowed documentation/detail changes before using a resolved item.
func (c *Client) ResolveCompletionRaw(ctx context.Context, original json.RawMessage) (result json.RawMessage, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	defer completionContextResult(ctx, &result, &err)
	if !c.initialized.Load() {
		return nil, errors.New("client not initialized")
	}
	if !c.CompletionResolveSupported() {
		return nil, ErrCompletionUnsupported
	}
	if !validCompletionRaw(original, true) {
		return nil, ErrCompletionInvalid
	}
	detached := json.RawMessage(bytes.Clone(original))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err := c.conn.Call(ctx, "completionItem/resolve", detached, &raw); err != nil {
		return nil, fmt.Errorf("completion resolve failed: %w", err)
	}
	if !validCompletionRaw(raw, true) {
		return nil, ErrCompletionInvalid
	}
	return bytes.Clone(raw), nil
}

// CompletionCommitCharacters is the initialized provider's static default.
// Items that carry their own commitCharacters replace it.
func (c *Client) CompletionCommitCharacters() []string {
	if !c.initialized.Load() || c.capabilities.CompletionProvider == nil {
		return nil
	}
	return slices.Clone(c.capabilities.CompletionProvider.AllCommitCharacters)
}

// CompletionResolveSupported observes only the initialized static advertisement.
func (c *Client) CompletionResolveSupported() bool {
	return c.initialized.Load() && c.capabilities.CompletionProvider != nil &&
		c.capabilities.CompletionProvider.ResolveProvider
}

func completionContextResult(ctx context.Context, result *json.RawMessage, err *error) {
	if canceled := ctx.Err(); canceled != nil {
		*result = nil
		*err = errors.Join(*err, canceled) // Preserve any concurrent RPC error identity.
	}
}

func validCompletionRaw(raw json.RawMessage, objectOnly bool) bool {
	if len(raw) == 0 || len(raw) > MaxCompletionResponseBytes || !utf8.Valid(raw) || !json.Valid(raw) {
		return false
	}
	value := bytes.TrimSpace(raw)
	if value[0] == '{' {
		return true
	}
	return !objectOnly && (value[0] == '[' || bytes.Equal(value, []byte("null")))
}
