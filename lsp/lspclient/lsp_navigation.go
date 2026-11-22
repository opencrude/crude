package lspclient

import (
	"bytes"
	"context"
	"errors"
	"math"
	"path/filepath"
	"time"

	"crude/lsp/lspedit"
	"crude/types/insight"

	"github.com/charmbracelet/x/powernap/pkg/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

const maxNavigationSourceBytes = 2 << 20

// RequestNavigation forwards one explicit query against the exact synchronized
// source. It preserves original result JSON, including null and empty arrays;
// interpretation and boundary qualification belong to the core and manager.
// Every provider outcome crosses the same document and cancellation fences.
func (l *LSPIntegration) RequestNavigation(ctx context.Context, path, kind string, line, col int, includeDeclaration bool) (insight.LSPNavigationResponse, error) {
	defer l.observeRequest("navigation", time.Now())
	fp := filepath.Clean(path)
	l.mu.Lock()
	if err := l.availableLocked(ctx); err != nil {
		l.mu.Unlock()
		return insight.LSPNavigationResponse{}, err
	}
	client, state := l.client, l.openFiles[fp]
	if state == nil {
		l.mu.Unlock()
		return insight.LSPNavigationResponse{}, errors.New("file not open in LSP")
	}
	version, text, uri := state.version, state.text, state.uri
	l.mu.Unlock()

	switch kind {
	case insight.LSPNavigationDefinition, insight.LSPNavigationTypeDefinition,
		insight.LSPNavigationImplementation, insight.LSPNavigationReferences:
	default:
		return insight.LSPNavigationResponse{}, errors.New("invalid navigation kind")
	}
	if includeDeclaration && kind != insight.LSPNavigationReferences {
		return insight.LSPNavigationResponse{}, errors.New("includeDeclaration is only valid for references")
	}
	if len(text) > maxNavigationSourceBytes {
		return insight.LSPNavigationResponse{}, errors.New("navigation source exceeds 2 MiB")
	}
	if line < 0 || col < 0 || line > math.MaxInt32 || col > math.MaxInt32 {
		return insight.LSPNavigationResponse{}, errors.New("navigation position exceeds signed LSP range")
	}
	if _, err := lspedit.Offset(text, line, col); err != nil {
		return insight.LSPNavigationResponse{}, err
	}
	if err := ctx.Err(); err != nil {
		return insight.LSPNavigationResponse{}, err
	}
	if !l.currentDocument(fp, client, state, version) {
		return insight.LSPNavigationResponse{}, context.Canceled
	}
	raw, err := client.RequestNavigation(ctx, kind, protocol.DocumentURI(uri),
		protocol.Position{Line: uint32(line), Character: uint32(col)}, includeDeclaration)
	if !l.currentDocument(fp, client, state, version) {
		return insight.LSPNavigationResponse{}, context.Canceled
	}
	if canceled := ctx.Err(); canceled != nil {
		return insight.LSPNavigationResponse{}, canceled
	}
	if errors.Is(err, lsp.ErrNavigationUnsupported) {
		return insight.LSPNavigationResponse{WireVersion: version, Kind: kind, Unsupported: true}, nil
	}
	if err != nil {
		return insight.LSPNavigationResponse{}, err
	}
	return insight.LSPNavigationResponse{WireVersion: version, Kind: kind, JSON: bytes.Clone(raw)}, nil
}
