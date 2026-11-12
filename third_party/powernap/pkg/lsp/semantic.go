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

const MaxSemanticTokenBytes = 4 << 20

var (
	ErrSemanticUnsupported = errors.New("language server does not advertise this semantic token request")
	ErrSemanticInvalid     = errors.New("invalid semantic token provider, request or response")
)

type SemanticTokenSupport struct {
	Types, Modifiers   []string
	Full, Delta, Range bool
}

// SemanticTokenClientCapabilities returns a fresh standard negotiation leaf.
// It describes the full, delta and range requests supported by the editor.
func SemanticTokenClientCapabilities() map[string]any {
	return map[string]any{
		"dynamicRegistration": false,
		"requests":            map[string]any{"range": true, "full": map[string]any{"delta": true}},
		"tokenTypes":          []string{"namespace", "type", "class", "enum", "interface", "struct", "typeParameter", "parameter", "variable", "property", "enumMember", "event", "function", "method", "macro", "keyword", "modifier", "comment", "string", "number", "regexp", "operator", "decorator"},
		"tokenModifiers":      []string{"declaration", "definition", "readonly", "static", "deprecated", "abstract", "async", "modification", "documentation", "defaultLibrary"},
		"formats":             []string{"relative"}, "overlappingTokenSupport": false, "multilineTokenSupport": false, "augmentsSyntaxTokens": true,
	}
}

// SemanticTokensSupport admits only an initialized static provider with a
// bounded legend and standard full/range option shapes. Unknown token names are
// inert; the renderer decides whether it has a corresponding style.
func (c *Client) SemanticTokensSupport() (SemanticTokenSupport, error) {
	if !c.initialized.Load() {
		return SemanticTokenSupport{}, errors.New("client not initialized")
	}
	if c.offsetEncoding != UTF16 {
		return SemanticTokenSupport{}, ErrSemanticInvalid
	}
	if c.capabilities.SemanticTokensProvider == nil {
		return SemanticTokenSupport{}, ErrSemanticUnsupported
	}
	options, ok := c.capabilities.SemanticTokensProvider.(map[string]any)
	if !ok || options == nil {
		return SemanticTokenSupport{}, ErrSemanticInvalid
	}
	legend, ok := options["legend"].(map[string]any)
	if !ok || legend == nil {
		return SemanticTokenSupport{}, ErrSemanticInvalid
	}
	list := func(key string, limit int) ([]string, error) {
		raw, ok := legend[key].([]any)
		if !ok || len(raw) > limit {
			return nil, ErrSemanticInvalid
		}
		out := make([]string, 0, len(raw))
		seen := make(map[string]bool, len(raw))
		for _, v := range raw {
			s, ok := v.(string)
			if !ok || s == "" || len(s) > 256 || !utf8.ValidString(s) || strings.ContainsAny(s, "\x00\r\n") || seen[s] {
				return nil, ErrSemanticInvalid
			}
			seen[s] = true
			out = append(out, s)
		}
		return out, nil
	}
	types, err := list("tokenTypes", 256)
	if err != nil {
		return SemanticTokenSupport{}, err
	}
	modifiers, err := list("tokenModifiers", 31)
	if err != nil {
		return SemanticTokenSupport{}, err
	}
	flag := func(value any) (bool, error) {
		if value == nil {
			return false, nil
		}
		switch v := value.(type) {
		case bool:
			return v, nil
		case map[string]any:
			if v != nil {
				return true, nil
			}
		}
		return false, ErrSemanticInvalid
	}
	full, err := flag(options["full"])
	if err != nil {
		return SemanticTokenSupport{}, err
	}
	rangeOK, err := flag(options["range"])
	if err != nil {
		return SemanticTokenSupport{}, err
	}
	delta := false
	if m, ok := options["full"].(map[string]any); ok {
		if d, exists := m["delta"]; exists {
			var valid bool
			delta, valid = d.(bool)
			if !valid {
				return SemanticTokenSupport{}, ErrSemanticInvalid
			}
		}
	}
	return SemanticTokenSupport{Types: types, Modifiers: modifiers, Full: full, Delta: full && delta, Range: rangeOK}, nil
}

// RequestSemanticTokens retains the raw response. Source/legend and all delta
// validation belong to the canonical consumer, not this transport adapter.
func (c *Client) RequestSemanticTokens(ctx context.Context, uri, mode, previous string, span protocol.Range) (result json.RawMessage, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	defer func() {
		if canceled := ctx.Err(); canceled != nil {
			result = nil
			err = errors.Join(err, canceled)
		}
	}()
	support, err := c.SemanticTokensSupport()
	if err != nil {
		return nil, err
	}
	if len(uri) == 0 || len(uri) > 4096 || !utf8.ValidString(uri) || strings.IndexByte(uri, 0) >= 0 {
		return nil, ErrSemanticInvalid
	}
	params := map[string]any{"textDocument": protocol.TextDocumentIdentifier{URI: protocol.DocumentURI(uri)}}
	method := "textDocument/semanticTokens/" + mode
	switch mode {
	case "full":
		if !support.Full {
			return nil, ErrSemanticUnsupported
		}
	case "full/delta":
		if !support.Delta {
			return nil, ErrSemanticUnsupported
		}
		if previous == "" || len(previous) > 4096 || !utf8.ValidString(previous) || strings.ContainsAny(previous, "\x00\r\n") {
			return nil, ErrSemanticInvalid
		}
		params["previousResultId"] = previous
	case "range":
		if !support.Range {
			return nil, ErrSemanticUnsupported
		}
		if span.Start.Line > math.MaxInt32 || span.Start.Character > math.MaxInt32 || span.End.Line > math.MaxInt32 || span.End.Character > math.MaxInt32 || span.Start.Line > span.End.Line || span.Start.Line == span.End.Line && span.Start.Character > span.End.Character {
			return nil, ErrSemanticInvalid
		}
		params["range"] = span
	default:
		return nil, ErrSemanticInvalid
	}
	var raw json.RawMessage
	if err := c.conn.Call(ctx, method, params, &raw); err != nil {
		return nil, fmt.Errorf("semantic token request failed: %w", err)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || len(raw) > MaxSemanticTokenBytes || !utf8.Valid(raw) || !json.Valid(raw) || !bytes.Equal(raw, []byte("null")) && raw[0] != '{' {
		return nil, ErrSemanticInvalid
	}
	return bytes.Clone(raw), nil
}
