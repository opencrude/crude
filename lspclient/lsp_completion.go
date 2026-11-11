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

// RequestCompletions retains the original response over one synchronized source.
// Initial insertion normalization and retained row identity belong to the
// canonical owner, not the transport or a shell-authored label.
func (l *LSPIntegration) RequestCompletions(ctx context.Context, path string, line, col int, trigger types.LSPCompletionContext) (types.LSPCompletionResponse, error) {
	defer l.observeRequest("completion", time.Now())
	fp := filepath.Clean(path)
	l.mu.Lock()
	if err := l.availableLocked(ctx); err != nil {
		l.mu.Unlock()
		return types.LSPCompletionResponse{}, err
	}
	client, state := l.client, l.openFiles[fp]
	if state == nil {
		l.mu.Unlock()
		return types.LSPCompletionResponse{}, errors.New("file not open in LSP")
	}
	version, text, uri := state.version, state.text, state.uri
	l.mu.Unlock()
	if line < 0 || col < 0 || line > math.MaxInt32 || col > math.MaxInt32 || len(text) > 2<<20 {
		return types.LSPCompletionResponse{}, errors.New("completion needs a bounded UTF-16 source position")
	}
	if _, err := lspedit.Offset(text, line, col); err != nil {
		return types.LSPCompletionResponse{}, err
	}
	if err := ctx.Err(); err != nil {
		return types.LSPCompletionResponse{}, err
	}
	if !l.currentDocument(fp, client, state, version) {
		return types.LSPCompletionResponse{}, context.Canceled
	}
	if trigger.TriggerKind < 0 || trigger.TriggerKind > types.LSPCompletionTriggerIncomplete {
		return types.LSPCompletionResponse{}, lsp.ErrCompletionInvalid
	}
	raw, callErr := client.RequestCompletionsRaw(ctx, uri, line, col, protocol.CompletionContext{TriggerKind: protocol.CompletionTriggerKind(trigger.TriggerKind), TriggerCharacter: trigger.TriggerCharacter})
	if !l.currentDocument(fp, client, state, version) {
		return types.LSPCompletionResponse{}, context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return types.LSPCompletionResponse{}, err
	}
	if errors.Is(callErr, lsp.ErrCompletionUnsupported) {
		return types.LSPCompletionResponse{WireVersion: version, Unsupported: true}, nil
	}
	if callErr != nil {
		return types.LSPCompletionResponse{}, callErr
	}
	return types.LSPCompletionResponse{
		WireVersion: version, JSON: bytes.Clone(raw), ResolveSupported: client.CompletionResolveSupported(),
	}, nil
}

// ResolveCompletion forwards only the retained original object. No decoded
// CompletionItem, numeric conversion or newly resolved edit can replace it.
func (l *LSPIntegration) ResolveCompletion(ctx context.Context, path string, wire int, original []byte) (types.LSPCompletionResponse, error) {
	defer l.observeRequest("completion-resolve", time.Now())
	fp := filepath.Clean(path)
	l.mu.Lock()
	if err := l.availableLocked(ctx); err != nil {
		l.mu.Unlock()
		return types.LSPCompletionResponse{}, err
	}
	client, state := l.client, l.openFiles[fp]
	if state == nil || wire <= 0 || state.version != wire {
		l.mu.Unlock()
		return types.LSPCompletionResponse{}, errors.New("the original completion source is no longer synchronized")
	}
	version := state.version
	l.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return types.LSPCompletionResponse{}, err
	}
	if !l.currentDocument(fp, client, state, version) {
		return types.LSPCompletionResponse{}, context.Canceled
	}
	raw, callErr := client.ResolveCompletionRaw(ctx, original)
	if !l.currentDocument(fp, client, state, version) {
		return types.LSPCompletionResponse{}, context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return types.LSPCompletionResponse{}, err
	}
	if errors.Is(callErr, lsp.ErrCompletionUnsupported) {
		return types.LSPCompletionResponse{WireVersion: version, Unsupported: true}, nil
	}
	if callErr != nil {
		return types.LSPCompletionResponse{}, callErr
	}
	return types.LSPCompletionResponse{WireVersion: version, JSON: bytes.Clone(raw), ResolveSupported: true}, nil
}
