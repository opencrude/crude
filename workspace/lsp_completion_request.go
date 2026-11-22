package workspace

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"slices"

	"crude/types/insight"
	"crude/types/setting"
)

func completionsEnabled(s setting.LSP) bool { return s.Completions }

// CompletionsState revokes old invocations across feature off/on cycles without
// restarting a producer or changing the context of unrelated semantic facts.
func (m *LSPManager) CompletionsState() (bool, uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.closed && !m.paused && m.analysisErrorLocked() == nil && m.settings.Enabled && m.settings.Completions, m.completionGeneration
}

// completionRequest qualifies every outcome, including null/empty/unsupported,
// with the same canonical source, producer instance and original context.
func (m *LSPManager) completionRequest(ctx context.Context, path string, call func(context.Context, LSPClient, insight.Boundary, int) (insight.LSPCompletionResponse, error)) (insight.LSPCompletionResponse, error) {
	if err := ctx.Err(); err != nil {
		return insight.LSPCompletionResponse{}, err
	}
	if !m.featureOn(completionsEnabled) {
		return insight.LSPCompletionResponse{}, errors.New("completions are off or blocked in Code Intelligence settings")
	}
	var out insight.LSPCompletionResponse
	err := m.trackInflight(func() error {
		client, requestCtx, valid, cancel := m.requestClient(ctx, path)
		defer cancel()
		if client == nil {
			return errors.New("completion needs a synchronized language server")
		}
		m.mu.Lock()
		doc := m.documents[filepath.Clean(path)]
		var boundary insight.Boundary
		wire := 0
		feature := m.completionGeneration
		if doc != nil && doc.opened && !doc.syncPending {
			boundary, wire = doc.boundary, doc.wireVersion
			boundary.Context.UnknownInputs = slices.Clone(boundary.Context.UnknownInputs)
		}
		m.mu.Unlock()
		check, err := m.completionFence(boundary, wire, feature)
		if err != nil {
			return err
		}
		response, callErr := call(requestCtx, client, boundary, wire)
		if err := requestCtx.Err(); err != nil {
			return err
		}
		if !valid() {
			return errors.New("completion source or provider changed during the request")
		}
		if err := check(); err != nil {
			return err
		}
		if callErr != nil {
			return callErr
		}
		if response.WireVersion != wire {
			return errors.New("completion response lost its exact synchronized wire source")
		}
		response.Boundary, response.JSON, response.FeatureGeneration = boundary, bytes.Clone(response.JSON), feature
		out = response
		return nil
	})
	if err != nil {
		return insight.LSPCompletionResponse{}, err
	}
	return out, nil
}

// RequestCompletions asks the language server for completions at a position.
func (m *LSPManager) RequestCompletions(ctx context.Context, path string, line, col int, trigger insight.LSPCompletionContext) (insight.LSPCompletionResponse, error) {
	return m.completionRequest(ctx, path, func(ctx context.Context, client LSPClient, boundary insight.Boundary, _ int) (insight.LSPCompletionResponse, error) {
		if trigger.Producer.InstanceID != "" && trigger.Producer != boundary.Producer {
			return insight.LSPCompletionResponse{}, errors.New("language server changed; request suggestions again")
		}
		return client.RequestCompletions(ctx, path, line, col, trigger)
	})
}

// ResolveCompletion resolves one completion only if its provider and source still match.
func (m *LSPManager) ResolveCompletion(ctx context.Context, expected insight.Boundary, wire int, original []byte) (insight.LSPCompletionResponse, error) {
	original = bytes.Clone(original)
	return m.completionRequest(ctx, expected.Source.Path, func(ctx context.Context, client LSPClient, boundary insight.Boundary, version int) (insight.LSPCompletionResponse, error) {
		if wire != version || !sameInsightBoundary(boundary, expected) {
			return insight.LSPCompletionResponse{}, errors.New("the original completion provider, source or context is no longer synchronized")
		}
		return client.ResolveCompletion(ctx, expected.Source.Path, wire, original)
	})
}

// The returned guard is memory-only and may run under DocumentService.mu.
func (m *LSPManager) completionFence(boundary insight.Boundary, wire int, feature uint64) (func() error, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc := m.documents[filepath.Clean(boundary.Source.Path)]
	if doc == nil || wire <= 0 || boundary.Source.DocumentID == "" || boundary.Source.Kind != "canonical" {
		return nil, errors.New("completion source is not synchronized")
	}
	state := m.states[doc.key]
	if state == nil || state.client == nil {
		return nil, errors.New("completion provider is unavailable")
	}
	client, settings := state.client, m.settingsGeneration
	current := func() bool {
		return !m.closed && !m.paused && m.analysisErrorLocked() == nil && m.settings.Enabled && m.settings.Completions &&
			m.completionGeneration == feature &&
			m.settingsGeneration == settings && m.contextGeneration == boundary.Context.Generation &&
			m.documents[filepath.Clean(boundary.Source.Path)] == doc && doc.opened && !doc.syncPending &&
			doc.wireVersion == wire && sameInsightBoundary(doc.boundary, boundary) &&
			m.states[doc.key] == state && state.client == client && state.status == LSPConnected &&
			!state.closing && !state.stopped && state.teardownErr == nil
	}
	if !current() {
		return nil, errors.New("completion source, provider, settings or context is no longer synchronized")
	}
	return func() error {
		m.mu.Lock()
		defer m.mu.Unlock()
		if !current() {
			return errors.New("completion source, provider, settings or context changed; request completion again")
		}
		return nil
	}, nil
}
