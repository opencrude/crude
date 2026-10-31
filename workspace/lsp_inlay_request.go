package workspace

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"slices"

	"crude/types"
)

func inlayHintsEnabled(s types.LSPSettings) bool { return s.InlayHints }

// InlayHintsState is a constant-size view gate. Its epoch revokes prior evidence
// across disable/re-enable, without restarting the producer or restamping facts.
func (m *LSPManager) InlayHintsState() (bool, uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.closed && !m.paused && m.analysisErrorLocked() == nil && m.settings.Enabled && m.settings.InlayHints, m.inlayGeneration
}

// inlayRequest borrows only the existing synchronized producer. No request,
// including tooltip-only resolve, starts a second server or trusts an
// adapter-authored Boundary. Negative answers cross the identical fences.
func (m *LSPManager) inlayRequest(ctx context.Context, path string, call func(context.Context, LSPClient, types.InsightBoundary, int) (types.LSPInlayResponse, error)) (types.LSPInlayResponse, error) {
	if err := ctx.Err(); err != nil {
		return types.LSPInlayResponse{}, err
	}
	if !m.featureOn(inlayHintsEnabled) {
		return types.LSPInlayResponse{}, errors.New("inlay hints are off or blocked in Code Intelligence settings")
	}
	var out types.LSPInlayResponse
	err := m.trackInflight(func() error {
		client, requestCtx, valid, cancel := m.requestClient(ctx, path)
		defer cancel()
		if client == nil {
			return errors.New("inlay hints need a synchronized language server")
		}
		m.mu.Lock()
		doc := m.documents[filepath.Clean(path)]
		var boundary types.InsightBoundary
		wire := 0
		if doc != nil && doc.opened && !doc.syncPending {
			boundary, wire = doc.boundary, doc.wireVersion
			boundary.Context.UnknownInputs = slices.Clone(boundary.Context.UnknownInputs)
		}
		m.mu.Unlock()
		check, err := m.inlayFence(boundary, wire)
		if err != nil {
			return err
		}
		response, callErr := call(requestCtx, client, boundary, wire)
		if err := requestCtx.Err(); err != nil {
			return err
		}
		if !valid() {
			return errors.New("inlay source or provider changed during the request")
		}
		if err := check(); err != nil {
			return err
		}
		if callErr != nil {
			return callErr
		}
		if response.WireVersion != wire {
			return errors.New("inlay response lost its exact synchronized wire source")
		}
		response.Boundary = boundary
		response.JSON = bytes.Clone(response.JSON)
		out = response
		return nil
	})
	if err != nil {
		return types.LSPInlayResponse{}, err
	}
	return out, nil
}

func (m *LSPManager) RequestInlayHints(ctx context.Context, path string, span types.LSPInlayRange) (types.LSPInlayResponse, error) {
	return m.inlayRequest(ctx, path, func(ctx context.Context, client LSPClient, _ types.InsightBoundary, _ int) (types.LSPInlayResponse, error) {
		return client.RequestInlayHints(ctx, path, span)
	})
}

func (m *LSPManager) ResolveInlayHint(ctx context.Context, expected types.InsightBoundary, wire int, original []byte) (types.LSPInlayResponse, error) {
	original = bytes.Clone(original)
	return m.inlayRequest(ctx, expected.Source.Path, func(ctx context.Context, client LSPClient, boundary types.InsightBoundary, version int) (types.LSPInlayResponse, error) {
		if wire != version || !sameInsightBoundary(boundary, expected) {
			return types.LSPInlayResponse{}, errors.New("the original inlay provider, source or context is no longer synchronized")
		}
		return client.ResolveInlayHint(ctx, expected.Source.Path, wire, original)
	})
}

// inlayFence is memory-only, safe under DocumentService.mu, and never a source
// reader. Settings/Trust, context, synchronized source and exact process instance
// all remain authoritative after a request has completed.
func (m *LSPManager) inlayFence(boundary types.InsightBoundary, wire int) (func() error, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc := m.documents[filepath.Clean(boundary.Source.Path)]
	if doc == nil || wire <= 0 || boundary.Source.DocumentID == "" || boundary.Source.Kind != "canonical" {
		return nil, errors.New("inlay source is not synchronized")
	}
	state := m.states[doc.key]
	if state == nil || state.client == nil {
		return nil, errors.New("inlay provider is unavailable")
	}
	client, settings, inlays := state.client, m.settingsGeneration, m.inlayGeneration
	current := func() bool {
		return !m.closed && !m.paused && m.analysisErrorLocked() == nil && m.settings.Enabled && m.settings.InlayHints &&
			m.settingsGeneration == settings && m.contextGeneration == boundary.Context.Generation &&
			m.inlayGeneration == inlays &&
			m.documents[filepath.Clean(boundary.Source.Path)] == doc && doc.opened && !doc.syncPending &&
			doc.wireVersion == wire && sameInsightBoundary(doc.boundary, boundary) &&
			m.states[doc.key] == state && state.client == client && state.status == LSPConnected &&
			!state.closing && !state.stopped && state.teardownErr == nil
	}
	if !current() {
		return nil, errors.New("inlay source, provider, settings or context is no longer synchronized")
	}
	return func() error {
		m.mu.Lock()
		defer m.mu.Unlock()
		if !current() {
			return errors.New("inlay source, provider, settings or context changed; request hints again")
		}
		return nil
	}, nil
}
