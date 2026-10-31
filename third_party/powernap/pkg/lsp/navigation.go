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

// MaxNavigationBytes bounds the encoded raw result, not the connection decoder's
// initial frame allocation.
const MaxNavigationBytes = 1 << 20

var (
	ErrNavigationUnsupported = errors.New("language server does not advertise navigation")
	ErrNavigationInvalid     = errors.New("invalid navigation kind, position, URI or raw result")
)

// RequestNavigation sends one standard request to an initialized, statically
// advertised provider. It neither consults dynamic registrations nor probes an
// unsupported provider. includeDeclaration is allowed only for references.
//
// The returned bytes are detached original JSON, including null and empty lists.
// This layer validates only encoded bounds and syntax, not Location/LocationLink
// semantics, source-relative UTF-16 boundaries or target URI authority. It never
// resolves paths, executes commands, applies edits or retries a different method.
func (c *Client) RequestNavigation(ctx context.Context, kind string, uri protocol.DocumentURI, position protocol.Position, includeDeclaration bool) (result json.RawMessage, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Cancellation applies to every outcome, including validation and RPC errors.
	// Retain the original failure as well when both occur.
	defer func() {
		if canceled := ctx.Err(); canceled != nil {
			result = nil
			err = errors.Join(err, canceled)
		}
	}()
	if !c.initialized.Load() {
		return nil, errors.New("client not initialized")
	}
	var method string
	switch kind {
	case "definition":
		method = "textDocument/definition"
	case "type-definition":
		method = "textDocument/typeDefinition"
	case "implementation":
		method = "textDocument/implementation"
	case "references":
		method = "textDocument/references"
	default:
		return nil, ErrNavigationInvalid
	}
	if position.Line > math.MaxInt32 || position.Character > math.MaxInt32 ||
		len(uri) == 0 || len(uri) > 4096 || !utf8.ValidString(string(uri)) ||
		strings.IndexByte(string(uri), 0) >= 0 || (includeDeclaration && kind != "references") {
		return nil, ErrNavigationInvalid
	}
	if !c.staticNavigationSupport(kind) {
		return nil, ErrNavigationUnsupported
	}
	params := struct {
		TextDocument protocol.TextDocumentIdentifier `json:"textDocument"`
		Position     protocol.Position               `json:"position"`
		Context      *protocol.ReferenceContext      `json:"context,omitempty"`
	}{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		Position:     position,
	}
	if kind == "references" {
		params.Context = &protocol.ReferenceContext{IncludeDeclaration: includeDeclaration}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err := c.conn.Call(ctx, method, params, &raw); err != nil {
		return nil, fmt.Errorf("navigation request failed: %w", err)
	}
	if len(raw) == 0 || len(raw) > MaxNavigationBytes || !utf8.Valid(raw) || !json.Valid(raw) {
		return nil, ErrNavigationInvalid
	}
	return bytes.Clone(raw), nil
}

// These are the concrete Value variants accepted by the generated protocol
// unions, not interface-decoded maps. Definition and references have no static
// registration-options variant. Pointers (including typed nil) and unknown
// representations never grant support.
func (c *Client) staticNavigationSupport(kind string) bool {
	switch kind {
	case "definition":
		if provider := c.capabilities.DefinitionProvider; provider != nil {
			switch value := provider.Value.(type) {
			case bool:
				return value
			case protocol.DefinitionOptions:
				return true
			}
		}
	case "type-definition":
		if provider := c.capabilities.TypeDefinitionProvider; provider != nil {
			switch value := provider.Value.(type) {
			case bool:
				return value
			case protocol.TypeDefinitionOptions, protocol.TypeDefinitionRegistrationOptions:
				return true
			}
		}
	case "implementation":
		if provider := c.capabilities.ImplementationProvider; provider != nil {
			switch value := provider.Value.(type) {
			case bool:
				return value
			case protocol.ImplementationOptions, protocol.ImplementationRegistrationOptions:
				return true
			}
		}
	case "references":
		if provider := c.capabilities.ReferencesProvider; provider != nil {
			switch value := provider.Value.(type) {
			case bool:
				return value
			case protocol.ReferenceOptions:
				return true
			}
		}
	}
	return false
}
