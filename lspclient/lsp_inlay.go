package lspclient

import (
	"bytes"
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

// RequestInlayHints observes one range of an already synchronized document.
// Parsing and canonical source/provider/context qualification belong to the
// coordinator, including unsupported, null and empty outcomes.
func (l *LSPIntegration) RequestInlayHints(ctx context.Context, path string, span types.LSPInlayRange) (types.LSPInlayResponse, error) {
	defer l.observeRequest("inlay-hints", time.Now())
	fp := filepath.Clean(path)
	l.mu.Lock()
	if err := l.availableLocked(ctx); err != nil {
		l.mu.Unlock()
		return types.LSPInlayResponse{}, err
	}
	client, state := l.client, l.openFiles[fp]
	if state == nil {
		l.mu.Unlock()
		return types.LSPInlayResponse{}, errors.New("file not open in LSP")
	}
	version, text, uri := state.version, state.text, state.uri
	l.mu.Unlock()
	wireSpan, err := inlayRequestRange(text, span)
	if err != nil {
		return types.LSPInlayResponse{}, err
	}
	if err := ctx.Err(); err != nil {
		return types.LSPInlayResponse{}, err
	}
	if !l.currentDocument(fp, client, state, version) {
		return types.LSPInlayResponse{}, context.Canceled
	}
	raw, callErr := client.RequestInlayHints(ctx, uri, wireSpan)
	if !l.currentDocument(fp, client, state, version) {
		return types.LSPInlayResponse{}, context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return types.LSPInlayResponse{}, err
	}
	if errors.Is(callErr, lsp.ErrInlayHintUnsupported) {
		return types.LSPInlayResponse{WireVersion: version, Unsupported: true}, nil
	}
	if callErr != nil {
		return types.LSPInlayResponse{}, callErr
	}
	return types.LSPInlayResponse{
		WireVersion: version, JSON: bytes.Clone(raw),
		ResolveSupported: client.InlayHintResolveSupported(),
	}, nil
}

// ResolveInlayHint forwards only the canonical owner's selected original object.
// It never reconstructs a hint from a shell summary or executes its adjuncts.
func (l *LSPIntegration) ResolveInlayHint(ctx context.Context, path string, wire int, original []byte) (types.LSPInlayResponse, error) {
	defer l.observeRequest("inlay-resolve", time.Now())
	fp := filepath.Clean(path)
	l.mu.Lock()
	if err := l.availableLocked(ctx); err != nil {
		l.mu.Unlock()
		return types.LSPInlayResponse{}, err
	}
	client, state := l.client, l.openFiles[fp]
	if state == nil || wire <= 0 || state.version != wire {
		l.mu.Unlock()
		return types.LSPInlayResponse{}, errors.New("the original inlay source is no longer synchronized")
	}
	version := state.version
	l.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return types.LSPInlayResponse{}, err
	}
	if !l.currentDocument(fp, client, state, version) {
		return types.LSPInlayResponse{}, context.Canceled
	}
	raw, callErr := client.ResolveInlayHint(ctx, original)
	if !l.currentDocument(fp, client, state, version) {
		return types.LSPInlayResponse{}, context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return types.LSPInlayResponse{}, err
	}
	if errors.Is(callErr, lsp.ErrInlayHintUnsupported) {
		return types.LSPInlayResponse{WireVersion: version, Unsupported: true}, nil
	}
	if callErr != nil {
		return types.LSPInlayResponse{}, callErr
	}
	return types.LSPInlayResponse{WireVersion: version, JSON: bytes.Clone(raw), ResolveSupported: true}, nil
}

func inlayRequestRange(text string, span types.LSPInlayRange) (protocol.Range, error) {
	if len(text) > 2<<20 {
		return protocol.Range{}, errors.New("inlay source exceeds its 2 MiB consumer bound")
	}
	for _, position := range []types.LSPInlayPosition{span.Start, span.End} {
		if position.Line < 0 || position.Character < 0 ||
			position.Line > math.MaxInt32 || position.Character > math.MaxInt32 {
			return protocol.Range{}, errors.New("inlay range exceeds signed LSP coordinates")
		}
	}
	start, err := lspedit.Offset(text, span.Start.Line, span.Start.Character)
	if err != nil {
		return protocol.Range{}, err
	}
	end, err := lspedit.Offset(text, span.End.Line, span.End.Character)
	if err != nil {
		return protocol.Range{}, err
	}
	if start > end {
		return protocol.Range{}, errors.New("inlay request range is reversed")
	}
	return protocol.Range{
		Start: protocol.Position{Line: uint32(span.Start.Line), Character: uint32(span.Start.Character)},
		End:   protocol.Position{Line: uint32(span.End.Line), Character: uint32(span.End.Character)},
	}, nil
}
