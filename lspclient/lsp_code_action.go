package lspclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"time"

	"crude/lspedit"
	"crude/types"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

const maxCodeActionSourceBytes = 2 << 20

// RequestCodeActions performs one manual, edit-only inspection of the exact
// synchronized source. It neither interprets actions nor executes their commands.
// All answers, including null, empty and errors, cross the same lifetime fence.
func (l *LSPIntegration) RequestCodeActions(ctx context.Context, path string, line, col, endLine, endCol int) (types.LSPCodeActionResponse, error) {
	defer l.observeRequest("codeAction", time.Now())
	fp := filepath.Clean(path)
	l.mu.Lock()
	if err := l.availableLocked(ctx); err != nil {
		l.mu.Unlock()
		return types.LSPCodeActionResponse{}, err
	}
	client, state := l.client, l.openFiles[fp]
	if state == nil {
		l.mu.Unlock()
		return types.LSPCodeActionResponse{}, errors.New("file not open in LSP")
	}
	version, text, uri := state.version, state.text, state.uri
	adjunct := state.actionDiagnostics
	sequence := state.publication.Sequence
	// Retained adjuncts are immutable and replaced as a whole under mu. The
	// selection below copies original bytes, never a UI diagnostic projection.
	l.mu.Unlock()

	start, end, err := codeActionOffsets(text, line, col, endLine, endCol)
	if err != nil {
		return types.LSPCodeActionResponse{}, err
	}
	diagnostics, detail := adjunct.selectRange(version, sequence, start, end)
	if err := ctx.Err(); err != nil {
		return types.LSPCodeActionResponse{}, err
	}
	if !l.currentDocument(fp, client, state, version) {
		return types.LSPCodeActionResponse{}, context.Canceled
	}
	_, resolve := client.CodeActionSupport()
	commands := client.CommandSupport()
	raw, err := client.RequestCodeActions(ctx, protocol.DocumentURI(uri), protocol.Range{
		Start: protocol.Position{Line: uint32(line), Character: uint32(col)},
		End:   protocol.Position{Line: uint32(endLine), Character: uint32(endCol)},
	}, diagnostics)
	if !l.currentDocument(fp, client, state, version) {
		return types.LSPCodeActionResponse{}, context.Canceled
	}
	if canceled := ctx.Err(); canceled != nil {
		return types.LSPCodeActionResponse{}, canceled
	}
	if err != nil {
		return types.LSPCodeActionResponse{}, err
	}
	return types.LSPCodeActionResponse{
		WireVersion: version, JSON: bytes.Clone(raw), ResolveSupported: resolve, CommandSupported: commands,
		DiagnosticsIncluded: len(diagnostics), DiagnosticsDetail: detail,
	}, nil
}

// ResolveCodeAction forwards an original object only to the current synchronized
// version. The caller must separately qualify provider identity and resolved
// semantics; a wire version alone is not canonical or mutation authority.
func (l *LSPIntegration) ResolveCodeAction(ctx context.Context, path string, wireVersion int, original []byte) (types.LSPCodeActionResponse, error) {
	defer l.observeRequest("codeActionResolve", time.Now())
	fp := filepath.Clean(path)
	l.mu.Lock()
	if err := l.availableLocked(ctx); err != nil {
		l.mu.Unlock()
		return types.LSPCodeActionResponse{}, err
	}
	client, state := l.client, l.openFiles[fp]
	if state == nil || state.version != wireVersion {
		l.mu.Unlock()
		return types.LSPCodeActionResponse{}, context.Canceled
	}
	l.mu.Unlock()
	if !l.currentDocument(fp, client, state, wireVersion) {
		return types.LSPCodeActionResponse{}, context.Canceled
	}
	raw, err := client.ResolveCodeAction(ctx, json.RawMessage(bytes.Clone(original)))
	if !l.currentDocument(fp, client, state, wireVersion) {
		return types.LSPCodeActionResponse{}, context.Canceled
	}
	if canceled := ctx.Err(); canceled != nil {
		return types.LSPCodeActionResponse{}, canceled
	}
	if err != nil {
		return types.LSPCodeActionResponse{}, err
	}
	_, resolve := client.CodeActionSupport()
	return types.LSPCodeActionResponse{
		WireVersion: wireVersion, JSON: bytes.Clone(raw), ResolveSupported: resolve,
		CommandSupported: client.CommandSupport(), DiagnosticsDetail: "diagnostics are not sent during resolve",
	}, nil
}

func codeActionOffsets(text string, line, col, endLine, endCol int) (int, int, error) {
	if len(text) > maxCodeActionSourceBytes {
		return 0, 0, errors.New("code action source exceeds 2 MiB")
	}
	for _, value := range []int{line, col, endLine, endCol} {
		if value < 0 || value > math.MaxInt32 {
			return 0, 0, errors.New("code action position exceeds signed LSP range")
		}
	}
	start, err := lspedit.Offset(text, line, col)
	if err != nil {
		return 0, 0, err
	}
	end, err := lspedit.Offset(text, endLine, endCol)
	if err != nil {
		return 0, 0, err
	}
	if start > end {
		return 0, 0, errors.New("code action range is reversed")
	}
	return start, end, nil
}
