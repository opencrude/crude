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

// MaxInlayHintBytes bounds each original resolve object and raw response. It
// does not bound the connection decoder's initial frame allocation or heap.
const MaxInlayHintBytes = 1 << 20

var (
	ErrInlayHintUnsupported = errors.New("language server does not advertise inlay hint support")
	ErrInlayHintInvalid     = errors.New("invalid inlay hint URI, range or raw JSON")
)

// InlayHintClientCapabilities constructs only the textDocument.inlayHint leaf.
// Initialize negotiation wiring is pending and belongs to the caller. Only
// tooltip resolution is advertised, never edits, commands or label locations.
func InlayHintClientCapabilities() *protocol.InlayHintClientCapabilities {
	return &protocol.InlayHintClientCapabilities{
		DynamicRegistration: false,
		ResolveSupport: &protocol.ClientInlayHintResolveOptions{
			Properties: []string{"tooltip", "label.tooltip"},
		},
	}
}

// RequestInlayHints makes one standard request to the initialized static
// provider, without dynamic registration, language branches or fallback probes.
// Raw null and [] remain distinct and returned bytes are detached.
//
// Validation here covers encoded bounds, JSON structure and protocol position
// bounds only. The consumer owns strict schema/Unicode/duplicate validation,
// source-relative UTF-16 boundaries, provenance and all rendered semantics.
func (c *Client) RequestInlayHints(ctx context.Context, uri string, span protocol.Range) (result json.RawMessage, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	defer inlayHintContextResult(ctx, &result, &err)
	if !c.initialized.Load() {
		return nil, errors.New("client not initialized")
	}
	if len(uri) == 0 || len(uri) > 4096 || !utf8.ValidString(uri) || strings.IndexByte(uri, 0) >= 0 ||
		!validInlayHintRange(span) {
		return nil, ErrInlayHintInvalid
	}
	if supported, _ := c.staticInlayHintSupport(); !supported {
		return nil, ErrInlayHintUnsupported
	}
	params := struct {
		TextDocument protocol.TextDocumentIdentifier `json:"textDocument"`
		Range        protocol.Range                  `json:"range"`
	}{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentURI(uri)},
		Range:        span,
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err := c.conn.Call(ctx, "textDocument/inlayHint", params, &raw); err != nil {
		return nil, fmt.Errorf("inlay hint request failed: %w", err)
	}
	if !validInlayHintList(raw) {
		return nil, ErrInlayHintInvalid
	}
	return bytes.Clone(raw), nil
}

// ResolveInlayHint forwards the original object, not a generated InlayHint
// (whose label representation cannot retain a standard string label). Unknown
// fields and opaque numeric tokens are never decoded/reconstructed here. This
// method grants no permission to execute commands, apply edits or open targets.
func (c *Client) ResolveInlayHint(ctx context.Context, original json.RawMessage) (result json.RawMessage, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	defer inlayHintContextResult(ctx, &result, &err)
	if !c.initialized.Load() {
		return nil, errors.New("client not initialized")
	}
	if !c.InlayHintResolveSupported() {
		return nil, ErrInlayHintUnsupported
	}
	if !validInlayHintObject(original) {
		return nil, ErrInlayHintInvalid
	}
	detached := json.RawMessage(bytes.Clone(original))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err := c.conn.Call(ctx, "inlayHint/resolve", detached, &raw); err != nil {
		return nil, fmt.Errorf("inlay hint resolve failed: %w", err)
	}
	if !validInlayHintObject(raw) {
		return nil, ErrInlayHintInvalid
	}
	return bytes.Clone(raw), nil
}

func inlayHintContextResult(ctx context.Context, result *json.RawMessage, err *error) {
	if canceled := ctx.Err(); canceled != nil {
		*result = nil
		*err = errors.Join(*err, canceled) // Retain any concurrent RPC error identity.
	}
}

// InlayHintResolveSupported observes only the initialized static advertisement.
func (c *Client) InlayHintResolveSupported() bool {
	if !c.initialized.Load() {
		return false
	}
	_, resolve := c.staticInlayHintSupport()
	return resolve
}

// InlayHintProvider is interface{}, not a generated protocol union: initialized
// JSON produces bool or map[string]any. Arbitrary Go options/pointers never grant.
// Registration id/selectors are checked for their container shape, not used to
// infer a language grant. Unknown option/filter fields remain inert.
func (c *Client) staticInlayHintSupport() (request, resolve bool) {
	switch options := c.capabilities.InlayHintProvider.(type) {
	case bool:
		return options, false
	case map[string]any:
		if options == nil {
			return false, false
		}
		for _, field := range []string{"resolveProvider", "workDoneProgress"} {
			if flag, exists := options[field]; exists {
				if _, ok := flag.(bool); !ok {
					return false, false
				}
			}
		}
		if id, exists := options["id"]; exists {
			if _, ok := id.(string); !ok {
				return false, false
			}
		}
		if selector, exists := options["documentSelector"]; exists && selector != nil {
			filters, ok := selector.([]any)
			if !ok {
				return false, false
			}
			for _, filter := range filters {
				if object, ok := filter.(map[string]any); !ok || object == nil {
					return false, false
				}
			}
		}
		resolve, _ = options["resolveProvider"].(bool)
		return true, resolve
	default:
		return false, false
	}
}

func validInlayHintRange(span protocol.Range) bool {
	// Position uses uint32, so negatives are unrepresentable; reject values
	// produced by negative-to-unsigned casts along with other int32 overflow.
	return span.Start.Line <= math.MaxInt32 && span.Start.Character <= math.MaxInt32 &&
		span.End.Line <= math.MaxInt32 && span.End.Character <= math.MaxInt32 &&
		(span.Start.Line < span.End.Line ||
			(span.Start.Line == span.End.Line && span.Start.Character <= span.End.Character))
}

func validInlayHintObject(raw json.RawMessage) bool {
	if len(raw) == 0 || len(raw) > MaxInlayHintBytes {
		return false
	}
	value := bytes.TrimSpace(raw)
	return len(value) >= 2 && value[0] == '{' && utf8.Valid(value) && json.Valid(value)
}

func validInlayHintList(raw json.RawMessage) bool {
	if len(raw) == 0 || len(raw) > MaxInlayHintBytes || !utf8.Valid(raw) || !json.Valid(raw) {
		return false
	}
	value := bytes.TrimSpace(raw)
	if bytes.Equal(value, []byte("null")) {
		return true
	}
	if len(value) == 0 || value[0] != '[' {
		return false
	}
	var items []json.RawMessage
	if err := json.Unmarshal(value, &items); err != nil {
		return false
	}
	for _, item := range items {
		if !validInlayHintObject(item) {
			return false
		}
	}
	return true
}
