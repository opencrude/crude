package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

const (
	MaxCodeActionDiagnostics     = 256
	MaxCodeActionDiagnosticBytes = 256 << 10 // Sum of the supplied raw diagnostic bytes.
	MaxCodeActionBytes           = 64 << 10
	MaxCodeActionListBytes       = 1 << 20
)

var (
	ErrCodeActionUnsupported        = errors.New("language server does not advertise code actions")
	ErrCodeActionResolveUnsupported = errors.New("language server does not advertise code action resolve")
	ErrCodeActionInvalid            = errors.New("invalid or oversized code action JSON or range")
)

// RequestCodeActions sends one explicit invocation to the initialized static
// provider. Diagnostics are original protocol objects, not rendering DTOs. Their
// opaque numbers/data are never decoded or reconstructed here. Empty diagnostics
// encode as [], and there is no kind filter or automatic trigger.
//
// These are encoded-data bounds, not JSON-RPC frame/heap bounds: conn.Call owns
// response decoding and cancellation. The caller owns source-byte/UTF-16 boundary
// validation, diagnostic qualification, and all action/edit semantics.
func (c *Client) RequestCodeActions(ctx context.Context, uri protocol.DocumentURI, span protocol.Range, diagnostics []json.RawMessage) (json.RawMessage, error) {
	if !c.initialized.Load() {
		return nil, errors.New("client not initialized")
	}
	supported, _ := c.staticCodeActionSupport()
	if !supported {
		return nil, ErrCodeActionUnsupported
	}
	if !validCodeActionRange(span) || len(diagnostics) > MaxCodeActionDiagnostics {
		return nil, ErrCodeActionInvalid
	}
	total := 0
	for _, diagnostic := range diagnostics {
		if len(diagnostic) > MaxCodeActionDiagnosticBytes-total {
			return nil, ErrCodeActionInvalid
		}
		total += len(diagnostic)
		if !validCodeActionObject(diagnostic) {
			return nil, ErrCodeActionInvalid
		}
	}
	// Validate the entire adjunct before allocating/copying any caller data.
	detached := make([]json.RawMessage, len(diagnostics))
	for i, diagnostic := range diagnostics {
		detached[i] = bytes.Clone(diagnostic)
	}
	params := struct {
		TextDocument protocol.TextDocumentIdentifier `json:"textDocument"`
		Range        protocol.Range                  `json:"range"`
		Context      struct {
			Diagnostics []json.RawMessage `json:"diagnostics"`
			TriggerKind int               `json:"triggerKind"`
		} `json:"context"`
	}{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		Range:        span,
	}
	params.Context.Diagnostics = detached
	params.Context.TriggerKind = 1 // CodeActionTriggerKind.Invoked
	var result json.RawMessage
	if err := c.conn.Call(ctx, "textDocument/codeAction", params, &result); err != nil {
		return nil, fmt.Errorf("code action request failed: %w", err)
	}
	if !validCodeActionList(result) {
		return nil, ErrCodeActionInvalid
	}
	return bytes.Clone(result), nil
}

// ResolveCodeAction forwards the original object, including unknown fields and
// opaque data. It never executes a command or applies an edit. The caller must
// qualify provider identity and validate resolved semantics before using an edit.
func (c *Client) ResolveCodeAction(ctx context.Context, original json.RawMessage) (json.RawMessage, error) {
	if !c.initialized.Load() {
		return nil, errors.New("client not initialized")
	}
	_, resolve := c.staticCodeActionSupport()
	if !resolve {
		return nil, ErrCodeActionResolveUnsupported
	}
	if len(original) > MaxCodeActionBytes || !validCodeActionObject(original) {
		return nil, ErrCodeActionInvalid
	}
	var result json.RawMessage
	if err := c.conn.Call(ctx, "codeAction/resolve", json.RawMessage(bytes.Clone(original)), &result); err != nil {
		return nil, fmt.Errorf("code action resolve failed: %w", err)
	}
	if len(result) > MaxCodeActionBytes || !validCodeActionObject(result) {
		return nil, ErrCodeActionInvalid
	}
	return bytes.Clone(result), nil
}

// Initialized interface-valued capabilities decode as bool or map[string]any.
// Do not coerce strings/numbers/null into capability grants or consult dynamic
// registrations. Unknown option fields do not restrict this open protocol type.
func (c *Client) staticCodeActionSupport() (request, resolve bool) {
	switch value := c.capabilities.CodeActionProvider.(type) {
	case bool:
		return value, false
	case map[string]any:
		if value == nil {
			return false, false
		}
		for _, field := range []string{"resolveProvider", "workDoneProgress"} {
			if flag, exists := value[field]; exists {
				if _, ok := flag.(bool); !ok {
					return false, false
				}
			}
		}
		if kinds, exists := value["codeActionKinds"]; exists {
			values, ok := kinds.([]any)
			if !ok {
				return false, false
			}
			for _, kind := range values {
				if _, ok := kind.(string); !ok {
					return false, false
				}
			}
		}
		resolve, _ = value["resolveProvider"].(bool)
		return true, resolve
	default:
		return false, false
	}
}

func validCodeActionRange(span protocol.Range) bool {
	const maxPosition = 1<<31 - 1
	return span.Start.Line <= maxPosition && span.Start.Character <= maxPosition &&
		span.End.Line <= maxPosition && span.End.Character <= maxPosition &&
		(span.Start.Line < span.End.Line ||
			(span.Start.Line == span.End.Line && span.Start.Character <= span.End.Character))
}

func validCodeActionObject(raw json.RawMessage) bool {
	value := bytes.TrimSpace(raw)
	return len(value) >= 2 && value[0] == '{' && utf8.Valid(value) && json.Valid(value)
}

func validCodeActionList(raw json.RawMessage) bool {
	if len(raw) > MaxCodeActionListBytes || !utf8.Valid(raw) || !json.Valid(raw) {
		return false
	}
	value := bytes.TrimSpace(raw)
	if bytes.Equal(value, []byte("null")) {
		return true // Supported, known-empty; preserve null rather than rewriting it.
	}
	if len(value) == 0 || value[0] != '[' {
		return false
	}
	var items []json.RawMessage
	if err := json.Unmarshal(value, &items); err != nil {
		return false
	}
	for _, item := range items {
		if !validCodeActionObject(item) {
			return false
		}
	}
	return true
}

// codeActionClientCapabilities constructs only the textDocument.codeAction leaf.
// Its initialize wiring is deliberately owned by makeClientCapabilities. Kinds
// are open hierarchical strings: known values are hints, not a request filter,
// and consumers must handle unknown kinds. Workspace-edit and command capability
// leaves are declared separately by makeClientCapabilities.
func codeActionClientCapabilities() map[string]any {
	return map[string]any{
		"codeActionLiteralSupport": map[string]any{
			"codeActionKind": map[string]any{
				"valueSet": []string{"", "quickfix", "refactor", "refactor.extract", "refactor.inline", "refactor.rewrite", "source", "source.organizeImports", "source.fixAll"},
			},
		},
		"isPreferredSupport": true,
		"disabledSupport":    true,
		"dataSupport":        true,
		"resolveSupport": map[string]any{
			"properties": []string{"edit"},
		},
	}
}
