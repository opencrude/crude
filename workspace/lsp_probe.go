package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf8"

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
func (m *LSPManager) probe(ctx context.Context, path string, gate func(ttypes.LSPSettings) bool, gateName string, fn func(context.Context, LSPClient) error) (LSPProbeView, error) {
	path = filepath.Clean(path)
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return LSPProbeView{}, context.Canceled
	}
	if m.paused {
		m.mu.Unlock()
		return LSPProbeView{}, ErrLSPStopped
	}
	if gate != nil && !gate(m.settings) {
		m.mu.Unlock()
		return LSPProbeView{}, fmt.Errorf("%s is turned off in Code Intelligence settings", gateName)
	}
	spec, ok := m.resolveServerLocked(path)
	limit := m.settings.MaxDocumentBytes
	m.mu.Unlock()
	if !ok {
		return LSPProbeView{}, fmt.Errorf("no enabled language server handles %s files", languageLabel(path))
	}
	disk, err := readProbeText(path, limit)
	if err != nil {
		return LSPProbeView{}, err
	}

	m.mu.Lock()
	doc := m.documents[path]
	if doc == nil {
		doc = &lspDocument{text: disk, revision: 1}
		m.documents[path] = doc
	} else if !doc.editor && doc.text != disk {
		doc.text = disk
		doc.revision++
	}
	view := LSPProbeView{BufferDiffers: doc.editor && doc.text != disk, Server: spec.Command}
	doc.probes++
	m.signalLocked()
	m.mu.Unlock()
	defer m.releaseProbe(path, doc)

	client, err := m.awaitProbeClient(ctx, path, doc)
	if err != nil {
		return view, err
	}
	return view, fn(ctx, client)
}

func languageLabel(path string) string {
	if ext := filepath.Ext(path); ext != "" {
		return ext
	}
	return filepath.Base(path)
}

func readProbeText(path string, limit int) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", path)
	}
	if limit > 0 && info.Size() > int64(limit) {
		return "", fmt.Errorf("%s is %d bytes, above the %d-byte language server document limit", path, info.Size(), limit)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(data) {
		return "", fmt.Errorf("%s is not UTF-8 text", path)
	}
	return string(data), nil
}

// awaitProbeClient waits for the document's server to connect and receive the
// current text. Failures that need a human (missing binary, stopped server,
// server limit) are returned at once instead of waiting out the deadline.
func (m *LSPManager) awaitProbeClient(ctx context.Context, path string, doc *lspDocument) (LSPClient, error) {
	for {
		m.mu.Lock()
		if m.documents[path] != doc || m.closed {
			m.mu.Unlock()
			return nil, errors.New("the language service was reconfigured during the request; retry")
		}
		if m.paused {
			m.mu.Unlock()
			return nil, ErrLSPStopped
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
		case state.status == LSPConnected && state.client != nil && !state.closing && doc.opened && doc.syncedRevision == doc.revision:
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
	m.setFileDiagnosticsLocked(m.fileDiagnosticServer[path], path, nil)
	m.recomputeActiveStatusLocked()
	m.signalLocked()
}

// ProbeDiagnostics returns the server's diagnostics for path once analysis has
// settled. settled=false means the server was still working when ctx ended and
// the list may be incomplete.
func (m *LSPManager) ProbeDiagnostics(ctx context.Context, path string) (diags []LSPDiagnostic, settled bool, view LSPProbeView, err error) {
	view, err = m.probe(ctx, path, func(s ttypes.LSPSettings) bool { return s.Diagnostics }, "Diagnostics", func(ctx context.Context, client LSPClient) error {
		diags, settled = client.AwaitDiagnostics(ctx, path)
		return nil
	})
	return diags, settled, view, err
}

func (m *LSPManager) ProbeDefinition(ctx context.Context, path string, line, col int) (locs []LSPLocation, view LSPProbeView, err error) {
	view, err = m.probe(ctx, path, func(s ttypes.LSPSettings) bool { return s.Definition }, "Go to definition", func(ctx context.Context, client LSPClient) error {
		locs = client.RequestDefinition(ctx, path, line, col)
		return nil
	})
	return locs, view, err
}

func (m *LSPManager) ProbeReferences(ctx context.Context, path string, line, col int) (locs []LSPLocation, view LSPProbeView, err error) {
	view, err = m.probe(ctx, path, func(s ttypes.LSPSettings) bool { return s.References }, "Find references", func(ctx context.Context, client LSPClient) error {
		locs = client.FindReferences(ctx, path, line, col, true)
		return nil
	})
	return locs, view, err
}

func (m *LSPManager) ProbeHover(ctx context.Context, path string, line, col int) (text string, view LSPProbeView, err error) {
	view, err = m.probe(ctx, path, func(s ttypes.LSPSettings) bool { return s.Hover }, "Hover", func(ctx context.Context, client LSPClient) error {
		var hoverErr error
		text, hoverErr = client.RequestHover(ctx, path, line, col)
		return hoverErr
	})
	return text, view, err
}

func (m *LSPManager) ProbeSymbols(ctx context.Context, path string) (symbols []LSPSymbol, view LSPProbeView, err error) {
	view, err = m.probe(ctx, path, func(s ttypes.LSPSettings) bool { return s.Outline }, "Document outline", func(ctx context.Context, client LSPClient) error {
		symbols = client.RequestDocumentSymbols(ctx, path)
		return nil
	})
	return symbols, view, err
}

// EditorEnabled reports the workspace's own LSP switch, which a session's
// code intelligence follows unless the session overrides it.
func (m *LSPManager) EditorEnabled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings.Enabled
}

// ServesWarm reports whether a connected server already holds path's language
// and root, so an automatic post-edit check never cold-starts a server.
func (m *LSPManager) ServesWarm(path string) bool {
	path = filepath.Clean(path)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.paused || m.closed || !m.settings.Diagnostics {
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
