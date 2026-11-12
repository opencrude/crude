package lspclient

import (
	"context"
	"crude/lspedit"
	"crude/types"
	"errors"
	"github.com/charmbracelet/x/powernap/pkg/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"path/filepath"
	"time"
)

func (l *LSPIntegration) RequestRangeFormatting(ctx context.Context, path string, span types.LSPFormattingSpan, options types.LSPFormattingOptions) (types.LSPFormattingResponse, error) {
	defer l.observeRequest("range-formatting", time.Now())
	path = filepath.Clean(path)
	l.mu.Lock()
	if err := l.availableLocked(ctx); err != nil {
		l.mu.Unlock()
		return types.LSPFormattingResponse{}, err
	}
	client, state := l.client, l.openFiles[path]
	if state == nil {
		l.mu.Unlock()
		return types.LSPFormattingResponse{}, errors.New("file not open in LSP")
	}
	version, text, uri := state.version, state.text, state.uri
	l.mu.Unlock()
	if len(text) > maxNavigationSourceBytes || span.End <= span.Start || options.TabSize < 1 || options.TabSize > 32 {
		return types.LSPFormattingResponse{}, errors.New("invalid selection formatting request")
	}
	sl, sc, err := lspedit.Position(text, span.Start)
	if err != nil {
		return types.LSPFormattingResponse{}, err
	}
	el, ec, err := lspedit.Position(text, span.End)
	if err != nil {
		return types.LSPFormattingResponse{}, err
	}
	if !l.currentDocument(path, client, state, version) {
		return types.LSPFormattingResponse{}, context.Canceled
	}
	raw, err := client.RequestRangeFormatting(ctx, protocol.DocumentURI(uri), protocol.Range{Start: protocol.Position{Line: uint32(sl), Character: uint32(sc)}, End: protocol.Position{Line: uint32(el), Character: uint32(ec)}}, protocol.FormattingOptions{TabSize: uint32(options.TabSize), InsertSpaces: options.InsertSpaces})
	if !l.currentDocument(path, client, state, version) {
		return types.LSPFormattingResponse{}, context.Canceled
	}
	if ctx.Err() != nil {
		return types.LSPFormattingResponse{}, ctx.Err()
	}
	if errors.Is(err, lsp.ErrRangeFormattingUnsupported) {
		return types.LSPFormattingResponse{WireVersion: version, Unsupported: true}, nil
	}
	if err != nil {
		return types.LSPFormattingResponse{}, err
	}
	return types.LSPFormattingResponse{WireVersion: version, JSON: raw}, nil
}
