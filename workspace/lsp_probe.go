package workspace

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	ttypes "crude/types"
)

// ErrLSPStopped reports that the human stopped language servers; agent
// requests never restart them behind that decision.
var ErrLSPStopped = errors.New("language servers were stopped from the Code Intelligence controls; start them there to use code intelligence")

type LSPProbeView = ttypes.LSPProbeView

// probe borrows a language-server view of path for one agent request. The
// editor's live buffer is used when the file is open there; otherwise the disk
// text is sent and the view is released afterwards. Agent probes are admitted
// independently of the editor's own enable switch (the session decides), but
// never while the human has stopped language servers.
func (m *LSPManager) probe(ctx context.Context, path string, gate func(ttypes.LSPSettings) bool, gateName string, fn func(context.Context, LSPClient, string, LSPProbeView) error) (LSPProbeView, error) {
	path = filepath.Clean(path)
	m.mu.Lock()
	if !filepath.IsAbs(path) {
		path = filepath.Join(m.workspaceRoot, path)
	}
	if m.closed {
		m.mu.Unlock()
		return LSPProbeView{}, context.Canceled
	}
	if m.paused {
		m.mu.Unlock()
		return LSPProbeView{}, ErrLSPStopped
	}
	if err := m.analysisErrorLocked(); err != nil {
		m.mu.Unlock()
		return LSPProbeView{}, err
	}
	if gate != nil && !gate(m.settings) {
		m.mu.Unlock()
		return LSPProbeView{}, fmt.Errorf("%s is turned off in Code Intelligence settings", gateName)
	}
	spec, ok := m.resolveServerLocked(path)
	limit := m.settings.MaxDocumentBytes
	sources := m.sources
	generation := m.settingsGeneration
	m.mu.Unlock()
	if !ok {
		return LSPProbeView{}, fmt.Errorf("no enabled language server handles %s files", languageLabel(path))
	}
	if sources == nil {
		return LSPProbeView{}, errors.New("canonical analysis source authority is unavailable")
	}
	source, err := sources.acquireAnalysisSource(ctx, path)
	if err != nil {
		return LSPProbeView{}, err
	}
	defer source.release()
	if limit > 0 && len(source.snapshot.Text) > limit {
		return LSPProbeView{}, fmt.Errorf("%s is above the %d-byte language server document limit", path, limit)
	}

	// Respect the same document -> producer lock order as canonical mutation.
	// A source acquired before an edit must never roll back its newer send intent.
	sources.mu.Lock()
	if err := source.validateLocked(); err != nil {
		sources.mu.Unlock()
		return LSPProbeView{}, err
	}
	m.mu.Lock()
	if m.closed || m.paused || generation != m.settingsGeneration || m.analysisErrorLocked() != nil ||
		gate != nil && !gate(m.settings) {
		m.mu.Unlock()
		sources.mu.Unlock()
		return LSPProbeView{}, errors.New("workspace analysis execution admission changed while acquiring source; retry with current Workspace settings")
	}
	doc := m.documents[path]
	if doc == nil {
		doc = &lspDocument{text: string(source.snapshot.Text), source: source.ref, revision: 1}
		m.documents[path] = doc
		m.advanceContextLocked("Analysis source view opened; dependent context needs reanalysis.")
	} else if doc.text != string(source.snapshot.Text) || doc.source != source.ref {
		doc.text = string(source.snapshot.Text)
		doc.source = source.ref
		doc.revision++
		m.advanceContextLocked("Requested analysis source changed.")
	}
	view := LSPProbeView{BufferDiffers: source.snapshot.Dirty || source.snapshot.Conflict || !source.snapshot.DiskExists,
		Server: spec.Command, Source: source.ref, SourceText: string(source.snapshot.Text)}
	doc.probes++
	m.signalLocked()
	m.mu.Unlock()
	sources.mu.Unlock()
	defer m.releaseProbe(path, doc)

	client, err := m.awaitProbeClient(ctx, path, doc, generation)
	if err != nil {
		return view, err
	}
	m.mu.Lock()
	boundary := doc.boundary
	admitted := generation == m.settingsGeneration && m.analysisErrorLocked() == nil && !m.closed && !m.paused &&
		(gate == nil || gate(m.settings))
	m.mu.Unlock()
	if !admitted {
		return view, errors.New("workspace analysis execution admission changed before request; retry with current Workspace settings")
	}
	if err := fn(ctx, client, path, view); err != nil {
		return view, err
	}
	if err := source.validate(ctx); err != nil {
		return view, err
	}
	// Canonical source and producer are adjudicated in one authority ordering.
	// An edit between an unlocked source check and the producer check must not
	// make an older answer escape as the just-observed source.
	sources.mu.Lock()
	if err := source.validateLocked(); err != nil {
		sources.mu.Unlock()
		return LSPProbeView{}, err
	}
	m.mu.Lock()
	current := generation == m.settingsGeneration && m.analysisErrorLocked() == nil &&
		(gate == nil || gate(m.settings)) &&
		m.documents[path] == doc && doc.source == source.ref && !m.paused && !m.closed &&
		doc.opened && doc.syncedContext == m.contextGeneration && sameInsightBoundary(boundary, doc.boundary) &&
		m.states[doc.key] != nil && m.states[doc.key].client == client && !m.states[doc.key].closing &&
		!m.states[doc.key].stopped && m.states[doc.key].status == LSPConnected
	m.mu.Unlock()
	sources.mu.Unlock()
	if !current {
		return view, errors.New("analysis source or producer context changed during the request")
	}
	return view, nil
}

func languageLabel(path string) string {
	if ext := filepath.Ext(path); ext != "" {
		return ext
	}
	return filepath.Base(path)
}

// awaitProbeClient waits for the document's server to connect and receive the
// current text. Failures that need a human (missing binary, stopped server,
// server limit) are returned at once instead of waiting out the deadline.
func (m *LSPManager) awaitProbeClient(ctx context.Context, path string, doc *lspDocument, generation uint64) (LSPClient, error) {
	for {
		m.mu.Lock()
		if m.documents[path] != doc || m.closed || generation != m.settingsGeneration {
			m.mu.Unlock()
			return nil, errors.New("the language service was reconfigured during the request; retry")
		}
		if m.paused {
			m.mu.Unlock()
			return nil, ErrLSPStopped
		}
		if err := m.analysisErrorLocked(); err != nil {
			m.mu.Unlock()
			return nil, err
		}
		state := m.states[doc.key]
		changed := m.stateChanged
		switch {
		case state == nil:
		case state.stopped:
			m.mu.Unlock()
			return nil, fmt.Errorf("%s was stopped from the Language Servers list", state.cmd)
		case state.status == LSPFailed:
			err := errors.New(state.err)
			m.mu.Unlock()
			return nil, err
		case state.waiting:
			err := errors.New(state.err)
			m.mu.Unlock()
			return nil, err
		case state.status == LSPConnected && state.client != nil && !state.closing && doc.opened && doc.syncedRevision == doc.revision && doc.syncedContext == m.contextGeneration:
			client := state.client
			m.mu.Unlock()
			return client, nil
		}
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("the language server was not ready before the request ended: %w", ctx.Err())
		case <-changed:
		}
	}
}

func (m *LSPManager) releaseProbe(path string, doc *lspDocument) {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc.probes--
	if m.documents[path] != doc || doc.probes > 0 {
		return
	}
	if !doc.editor {
		m.dropDocumentLocked(path, doc)
		return
	}
	if m.wantedLocked(doc) {
		return
	}
	// The editor holds the file but its own language features are off: close
	// the server view the probe opened so the server can go idle.
	if state := m.states[doc.key]; doc.opened && state != nil && state.client != nil && !state.closing {
		m.closedDocuments = append(m.closedDocuments, lspClosedDocument{path, state.client})
	}
	doc.key, doc.opened, doc.syncedRevision = "", false, 0
	m.advanceContextLocked("Borrowed producer view released; retained facts are stale.")
	m.recomputeActiveStatusLocked()
	m.signalLocked()
}

// ProbeDiagnostics returns the server's diagnostics for path once analysis has
// settled. settled=false means the server was still working when ctx ended and
// the list may be incomplete.
func (m *LSPManager) ProbeDiagnostics(ctx context.Context, path string) (answer ttypes.InsightDiagnostics, settled bool, view LSPProbeView, err error) {
	view, err = m.probe(ctx, path, func(s ttypes.LSPSettings) bool { return s.Diagnostics }, "Diagnostics", func(ctx context.Context, client LSPClient, path string, view LSPProbeView) error {
		publication, quiet := client.AwaitDiagnostics(ctx, path)
		settled = quiet
		m.mu.Lock()
		doc := m.documents[path]
		var key string
		var state *lspClientState
		if doc != nil {
			key, state = doc.key, m.states[doc.key]
		}
		m.mu.Unlock()
		if publication.Sequence != 0 && state != nil {
			m.receiveDiagnostics(key, state, publication)
		}
		answer, _ = m.probeDiagnosticFacts(view.Source)
		return nil
	})
	if err != nil {
		answer, settled = ttypes.InsightDiagnostics{}, false
	} else if answer.ID == "" {
		settled = false
	}
	return answer, settled, view, err
}

func (m *LSPManager) ProbeDefinition(ctx context.Context, path string, position ttypes.LSPSourcePosition) (locs []LSPLocation, view LSPProbeView, err error) {
	view, err = m.probe(ctx, path, func(s ttypes.LSPSettings) bool { return s.Definition }, "Go to definition", func(ctx context.Context, client LSPClient, path string, view LSPProbeView) error {
		line, col, err := position.Resolve(view.SourceText)
		if err != nil {
			return err
		}
		locs = client.RequestDefinition(ctx, path, line, col)
		return nil
	})
	if err != nil {
		locs = nil
	}
	return locs, view, err
}

func (m *LSPManager) ProbeReferences(ctx context.Context, path string, position ttypes.LSPSourcePosition) (locs []LSPLocation, view LSPProbeView, err error) {
	view, err = m.probe(ctx, path, func(s ttypes.LSPSettings) bool { return s.References }, "Find references", func(ctx context.Context, client LSPClient, path string, view LSPProbeView) error {
		line, col, err := position.Resolve(view.SourceText)
		if err != nil {
			return err
		}
		locs = client.FindReferences(ctx, path, line, col, true)
		return nil
	})
	if err != nil {
		locs = nil
	}
	return locs, view, err
}

func (m *LSPManager) ProbeHover(ctx context.Context, path string, position ttypes.LSPSourcePosition) (text string, view LSPProbeView, err error) {
	view, err = m.probe(ctx, path, func(s ttypes.LSPSettings) bool { return s.Hover }, "Hover", func(ctx context.Context, client LSPClient, path string, view LSPProbeView) error {
		line, col, err := position.Resolve(view.SourceText)
		if err != nil {
			return err
		}
		var hoverErr error
		text, hoverErr = client.RequestHover(ctx, path, line, col)
		return hoverErr
	})
	if err != nil {
		text = ""
	}
	return text, view, err
}

func (m *LSPManager) ProbeSymbols(ctx context.Context, path string) (symbols []LSPSymbol, view LSPProbeView, err error) {
	view, err = m.probe(ctx, path, func(s ttypes.LSPSettings) bool { return s.Outline }, "Document outline", func(ctx context.Context, client LSPClient, path string, _ LSPProbeView) error {
		symbols = client.RequestDocumentSymbols(ctx, path)
		return nil
	})
	if err != nil {
		symbols = nil
	}
	return symbols, view, err
}

// EditorEnabled reports the workspace's own LSP switch, which a session's
// code intelligence follows unless the session overrides it.
func (m *LSPManager) EditorEnabled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.analysisErrorLocked() == nil && m.settings.Enabled
}

// ServesWarm reports whether a connected server already holds path's language
// and root, so an automatic post-edit check never cold-starts a server.
func (m *LSPManager) ServesWarm(path string) bool {
	path = filepath.Clean(path)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.paused || m.closed || m.analysisErrorLocked() != nil || !m.settings.Diagnostics {
		return false
	}
	spec, ok := m.resolveServerLocked(path)
	if !ok {
		return false
	}
	for _, state := range m.states {
		if state.status == LSPConnected && state.client != nil && !state.closing && !state.stopped &&
			state.cmd == spec.Command && pathWithin(filePathFromURI(state.rootURI), path) {
			return true
		}
	}
	return false
}
