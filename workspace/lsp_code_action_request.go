package workspace

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"slices"

	"crude/types"
)

// codeActionRequest retains the same synchronized manager source on all result
// paths, including empty/null evidence. It does not start another provider or
// accept the adapter's unqualified Boundary as authoritative.
func (m *LSPManager) codeActionRequest(ctx context.Context, path string, call func(context.Context, LSPClient, types.InsightBoundary, int) (types.LSPCodeActionResponse, error)) (types.LSPCodeActionResponse, error) {
	ctx, stop := context.WithTimeout(ctx, ManualLSPRequestTimeout)
	defer stop()
	if err := ctx.Err(); err != nil {
		return types.LSPCodeActionResponse{}, err
	}
	if !m.featureOn(func(s types.LSPSettings) bool { return s.Enabled }) {
		return types.LSPCodeActionResponse{}, errors.New("code actions unavailable: language intelligence is off or blocked")
	}
	var out types.LSPCodeActionResponse
	err := m.trackInflight(func() error {
		if err := m.awaitSynchronized(ctx, path); err != nil {
			return err
		}
		client, requestCtx, valid, cancel := m.requestClientWithin(ctx, path, ManualLSPRequestTimeout)
		defer cancel()
		if client == nil {
			return errors.New("code actions unavailable: no synchronized language server")
		}
		m.mu.Lock()
		doc := m.documents[filepath.Clean(path)]
		var boundary types.InsightBoundary
		wire := 0
		if doc != nil && doc.opened && !doc.syncPending {
			boundary, wire = doc.boundary, doc.wireVersion
			boundary.Context.UnknownInputs = slices.Clone(boundary.Context.UnknownInputs)
		}
		generation := m.contextGeneration
		m.mu.Unlock()
		if boundary.Source.Kind != "canonical" || boundary.Source.DocumentID == "" ||
			boundary.Context.Generation != generation || wire <= 0 {
			return errors.New("code actions require a synchronized canonical source and context")
		}
		if err := requestCtx.Err(); err != nil {
			return err
		}
		response, callErr := call(requestCtx, client, boundary, wire)
		if err := requestCtx.Err(); err != nil {
			return err
		}
		if !valid() {
			return errors.New("code-action source or provider changed during the request")
		}
		m.mu.Lock()
		current := m.documents[filepath.Clean(path)]
		exact := current == doc && current != nil && current.opened && !current.syncPending &&
			current.wireVersion == wire && sameInsightBoundary(current.boundary, boundary) &&
			generation == m.contextGeneration
		m.mu.Unlock()
		if !exact {
			return errors.New("code-action source or context changed during the request")
		}
		if callErr != nil {
			return callErr
		}
		if response.WireVersion != wire {
			return errors.New("code-action result does not identify the exact synchronized wire source")
		}
		response.Boundary = boundary
		response.JSON = bytes.Clone(response.JSON)
		out = response
		return nil
	})
	return out, err
}

func (m *LSPManager) RequestCodeActions(ctx context.Context, path string, line, col, endLine, endCol int) (types.LSPCodeActionResponse, error) {
	return m.codeActionRequest(ctx, path, func(requestCtx context.Context, client LSPClient, _ types.InsightBoundary, _ int) (types.LSPCodeActionResponse, error) {
		return client.RequestCodeActions(requestCtx, path, line, col, endLine, endCol)
	})
}

func (m *LSPManager) ResolveCodeAction(ctx context.Context, expected types.InsightBoundary, expectedWire int, original []byte) (types.LSPCodeActionResponse, error) {
	original = bytes.Clone(original)
	return m.codeActionRequest(ctx, expected.Source.Path, func(requestCtx context.Context, client LSPClient, boundary types.InsightBoundary, wire int) (types.LSPCodeActionResponse, error) {
		if !sameInsightBoundary(boundary, expected) || wire != expectedWire {
			return types.LSPCodeActionResponse{}, errors.New("the original code-action provider/source/context is no longer synchronized")
		}
		return client.ResolveCodeAction(requestCtx, expected.Source.Path, wire, original)
	})
}
