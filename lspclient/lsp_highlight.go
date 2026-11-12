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

// RequestDocumentHighlights forwards one caret query against the exact
// synchronized source. Interpretation and boundary qualification belong to
// the manager; every outcome crosses the same document and cancellation fences.
func (l *LSPIntegration) RequestDocumentHighlights(ctx context.Context, path string, line, col int) (types.LSPHighlightResponse, error) {
	defer l.observeRequest("highlights", time.Now())
	fp := filepath.Clean(path)
	l.mu.Lock()
	if err := l.availableLocked(ctx); err != nil {
		l.mu.Unlock()
		return types.LSPHighlightResponse{}, err
	}
	client, state := l.client, l.openFiles[fp]
	if state == nil {
		l.mu.Unlock()
		return types.LSPHighlightResponse{}, errors.New("file not open in LSP")
	}
	version, text, uri := state.version, state.text, state.uri
	l.mu.Unlock()
	if len(text) > maxNavigationSourceBytes {
		return types.LSPHighlightResponse{}, errors.New("highlight source exceeds 2 MiB")
	}
	if line < 0 || col < 0 || line > math.MaxInt32 || col > math.MaxInt32 {
		return types.LSPHighlightResponse{}, errors.New("highlight position exceeds signed LSP range")
	}
	if _, err := lspedit.Offset(text, line, col); err != nil {
		return types.LSPHighlightResponse{}, err
	}
	if err := ctx.Err(); err != nil {
		return types.LSPHighlightResponse{}, err
	}
	if !l.currentDocument(fp, client, state, version) {
		return types.LSPHighlightResponse{}, context.Canceled
	}
	raw, err := client.RequestDocumentHighlights(ctx, protocol.DocumentURI(uri),
		protocol.Position{Line: uint32(line), Character: uint32(col)})
	if !l.currentDocument(fp, client, state, version) {
		return types.LSPHighlightResponse{}, context.Canceled
	}
	if canceled := ctx.Err(); canceled != nil {
		return types.LSPHighlightResponse{}, canceled
	}
	if errors.Is(err, lsp.ErrHighlightUnsupported) {
		return types.LSPHighlightResponse{WireVersion: version, Unsupported: true}, nil
	}
	if err != nil {
		return types.LSPHighlightResponse{}, err
	}
	return types.LSPHighlightResponse{WireVersion: version, JSON: bytes.Clone(raw)}, nil
}
