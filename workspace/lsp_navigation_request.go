package workspace

import (
	"context"
	"errors"
	"path/filepath"
	"slices"

	"crude/types"
)

func navigationGate(kind string) (func(types.LSPSettings) bool, error) {
	switch kind {
	case types.LSPNavigationDefinition, types.LSPNavigationTypeDefinition, types.LSPNavigationImplementation:
		return func(s types.LSPSettings) bool { return s.Definition }, nil
	case types.LSPNavigationReferences:
		return func(s types.LSPSettings) bool { return s.References }, nil
	default:
		return nil, errors.New("unknown navigation kind")
	}
}

// RequestNavigation uses the existing synchronized producer. Definition-family
// and reference preferences remain separate; an editor-off probe is admitted by
// probe(), never by silently enabling the editor's master switch.
func (m *LSPManager) RequestNavigation(ctx context.Context, path, kind string, line, col int, includeDeclaration bool) (types.LSPNavigation, error) {
	gate, err := navigationGate(kind)
	if err != nil {
		return types.LSPNavigation{}, err
	}
	if !m.featureOn(gate) {
		return types.LSPNavigation{}, errors.New("navigation is off or blocked in Code Intelligence settings")
	}
	return m.navigationRequest(ctx, path, kind, line, col, includeDeclaration)
}

func (m *LSPManager) navigationRequest(ctx context.Context, path, kind string, line, col int, includeDeclaration bool) (types.LSPNavigation, error) {
	gate, err := navigationGate(kind)
	if err != nil {
		return types.LSPNavigation{}, err
	}
	if includeDeclaration && kind != types.LSPNavigationReferences {
		return types.LSPNavigation{}, errors.New("includeDeclaration applies only to references")
	}
	if err := ctx.Err(); err != nil {
		return types.LSPNavigation{}, err
	}
	var out types.LSPNavigation
	err = m.trackInflight(func() error {
		client, requestCtx, valid, cancel := m.requestClient(ctx, path)
		defer cancel()
		if client == nil {
			return errors.New("navigation needs a synchronized language server")
		}
		m.mu.Lock()
		doc := m.documents[filepath.Clean(path)]
		var boundary types.InsightBoundary
		wire, text := 0, ""
		root := m.workspaceRoot
		if doc != nil && doc.opened && !doc.syncPending && gate(m.settings) {
			boundary, wire, text = doc.boundary, doc.wireVersion, doc.text
			boundary.Context.UnknownInputs = slices.Clone(boundary.Context.UnknownInputs)
		}
		m.mu.Unlock()
		check, err := m.navigationFence(boundary, wire, kind)
		if err != nil {
			return err
		}
		if err := validateNavigationPosition(text, line, col); err != nil {
			return err
		}
		response, callErr := client.RequestNavigation(requestCtx, path, kind, line, col, includeDeclaration)
		if err := requestCtx.Err(); err != nil {
			return err
		}
		if !valid() {
			return errors.New("navigation source or provider changed during the request")
		}
		if err := check(); err != nil {
			return err
		}
		if callErr != nil {
			return callErr
		}
		if response.Kind != kind || response.WireVersion != wire {
			return errors.New("navigation response lost its original kind or synchronized wire source")
		}
		response.Boundary = boundary
		result, err := normalizeNavigation(root, text, response, line, col, includeDeclaration)
		if err != nil {
			return err
		}
		if err := check(); err != nil {
			return err
		}
		out = result
		return requestCtx.Err()
	})
	if err != nil {
		return types.LSPNavigation{}, err
	}
	return out, nil
}

// navigationFence is memory-only and safe under DocumentService.mu. A target
// selection may observe current bytes only while its invoking source, producer,
// feature preference and context still identify the original answer.
func (m *LSPManager) navigationFence(boundary types.InsightBoundary, wire int, kind string) (func() error, error) {
	gate, err := navigationGate(kind)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	doc := m.documents[filepath.Clean(boundary.Source.Path)]
	if doc == nil || wire <= 0 || boundary.Source.DocumentID == "" {
		return nil, errors.New("navigation source is not synchronized")
	}
	state := m.states[doc.key]
	if state == nil || state.client == nil {
		return nil, errors.New("navigation provider is unavailable")
	}
	client, generation := state.client, m.settingsGeneration
	current := func() bool {
		return !m.closed && !m.paused && m.analysisErrorLocked() == nil && gate(m.settings) &&
			m.settingsGeneration == generation && m.contextGeneration == boundary.Context.Generation &&
			m.documents[filepath.Clean(boundary.Source.Path)] == doc && doc.opened && !doc.syncPending &&
			doc.wireVersion == wire && sameInsightBoundary(doc.boundary, boundary) &&
			m.states[doc.key] == state && state.client == client && state.status == LSPConnected &&
			!state.closing && !state.stopped && state.teardownErr == nil
	}
	if !current() {
		return nil, errors.New("navigation source, provider or context is no longer synchronized")
	}
	return func() error {
		m.mu.Lock()
		defer m.mu.Unlock()
		if !current() {
			return errors.New("navigation source, provider or context changed; request navigation again")
		}
		return nil
	}, nil
}

// ProbeNavigation shares the same protocol, normalization and negative-result
// fencing as the editor. Probe() owns source acquisition, Trust and lifetime.
func (m *LSPManager) ProbeNavigation(ctx context.Context, path, kind string, position types.LSPSourcePosition, includeDeclaration bool) (result types.LSPNavigation, view LSPProbeView, err error) {
	gate, err := navigationGate(kind)
	if err != nil {
		return result, view, err
	}
	view, err = m.probe(ctx, path, gate, "Navigation", func(ctx context.Context, _ LSPClient, path string, view LSPProbeView) error {
		line, col, err := position.Resolve(view.SourceText)
		if err != nil {
			return err
		}
		result, err = m.navigationRequest(ctx, path, kind, line, col, includeDeclaration)
		return err
	})
	if err != nil {
		return types.LSPNavigation{}, view, err
	}
	return result, view, nil
}
