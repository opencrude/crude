package lspclient

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"time"

	"crude/lspedit"
	"crude/types"
	"github.com/charmbracelet/x/powernap/pkg/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

// RequestSemanticTokens observes one synchronized source; it cannot stamp a
// canonical boundary or reuse a result ID on its own.
func (l *LSPIntegration) RequestSemanticTokens(ctx context.Context, path, mode, previous string, span types.LSPSemanticRange) (types.LSPSemanticResponse, error) {
	defer l.observeRequest("semantic-tokens", time.Now())
	fp := filepath.Clean(path)
	l.mu.Lock()
	if err := l.availableLocked(ctx); err != nil {
		l.mu.Unlock()
		return types.LSPSemanticResponse{}, err
	}
	client, state := l.client, l.openFiles[fp]
	if state == nil {
		l.mu.Unlock()
		return types.LSPSemanticResponse{}, errors.New("file not open in language server")
	}
	version, text, uri := state.version, state.text, state.uri
	l.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return types.LSPSemanticResponse{}, err
	}
	if !l.currentDocument(fp, client, state, version) {
		return types.LSPSemanticResponse{}, context.Canceled
	}
	support, err := client.SemanticTokensSupport()
	if errors.Is(err, lsp.ErrSemanticUnsupported) {
		return types.LSPSemanticResponse{WireVersion: version, Unsupported: true}, nil
	}
	if err != nil {
		return types.LSPSemanticResponse{}, err
	}
	out := types.LSPSemanticResponse{WireVersion: version, Support: types.LSPSemanticSupport{Types: support.Types, Modifiers: support.Modifiers, Full: support.Full, Delta: support.Delta, Range: support.Range}}
	if mode == "support" {
		return out, nil
	}
	var r protocol.Range
	if mode == "range" {
		for _, p := range []types.LSPSemanticPosition{span.Start, span.End} {
			if p.Line < 0 || p.Character < 0 || p.Line > math.MaxInt32 || p.Character > math.MaxInt32 {
				return types.LSPSemanticResponse{}, lsp.ErrSemanticInvalid
			}
		}
		a, err := lspedit.Offset(text, span.Start.Line, span.Start.Character)
		if err != nil {
			return types.LSPSemanticResponse{}, err
		}
		b, err := lspedit.Offset(text, span.End.Line, span.End.Character)
		if err != nil || b < a {
			return types.LSPSemanticResponse{}, lsp.ErrSemanticInvalid
		}
		r = protocol.Range{Start: protocol.Position{Line: uint32(span.Start.Line), Character: uint32(span.Start.Character)}, End: protocol.Position{Line: uint32(span.End.Line), Character: uint32(span.End.Character)}}
	}
	raw, err := client.RequestSemanticTokens(ctx, uri, mode, previous, r)
	if !l.currentDocument(fp, client, state, version) {
		return types.LSPSemanticResponse{}, context.Canceled
	}
	if err != nil {
		return types.LSPSemanticResponse{}, err
	}
	out.JSON = raw
	return out, nil
}
