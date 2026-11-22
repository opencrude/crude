package workspace

import (
	"context"
	"errors"
	"path/filepath"
	"slices"

	"crude/lsp/lspedit"
	"crude/lsp/lspnav"
	"crude/types/insight"
	"crude/types/setting"
	"crude/workspace/document"
)

// LSPHighlightClient is the optional document-highlight capability of a
// transport client. Clients without it answer as an unsupported provider.
type LSPHighlightClient interface {
	RequestDocumentHighlights(context.Context, string, int, int) (insight.LSPHighlightResponse, error)
}

// Occurrences of the symbol at the caret are the in-file part of Find
// references, so they follow that preference instead of adding another switch.
func highlightsEnabled(s setting.LSP) bool { return s.References }

// ReferencesEnabled reads the existing references gate without copying all
// language settings. Occurrences use the same authority as Find references.
func (m *LSPManager) ReferencesEnabled() bool { return m.featureOn(highlightsEnabled) }

// RequestDocumentHighlights qualifies every answer, including null, empty and
// unsupported, with the synchronized source, provider and context it described.
func (m *LSPManager) RequestDocumentHighlights(ctx context.Context, path string, line, col int) (insight.LSPHighlights, error) {
	if err := ctx.Err(); err != nil {
		return insight.LSPHighlights{}, err
	}
	if !m.featureOn(highlightsEnabled) {
		return insight.LSPHighlights{}, errors.New("occurrence highlights follow Find references, which is off or blocked")
	}
	var out insight.LSPHighlights
	err := m.trackInflight(func() error {
		client, requestCtx, valid, cancel := m.requestClient(ctx, path)
		defer cancel()
		if client == nil {
			return errors.New("occurrence highlights need a synchronized language server")
		}
		m.mu.Lock()
		doc := m.documents[filepath.Clean(path)]
		var boundary insight.Boundary
		wire, text := 0, ""
		if doc != nil && doc.opened && !doc.syncPending {
			boundary, wire, text = doc.boundary, doc.wireVersion, doc.text
			boundary.Context.UnknownInputs = slices.Clone(boundary.Context.UnknownInputs)
		}
		m.mu.Unlock()
		check, err := m.semanticFence(boundary, wire, highlightsEnabled, "highlight")
		if err != nil {
			return err
		}
		if err := validateNavigationPosition(text, line, col); err != nil {
			return err
		}
		response := insight.LSPHighlightResponse{WireVersion: wire, Unsupported: true}
		var callErr error
		if highlighter, ok := client.(LSPHighlightClient); ok {
			response, callErr = highlighter.RequestDocumentHighlights(requestCtx, path, line, col)
		}
		if err := requestCtx.Err(); err != nil {
			return err
		}
		if !valid() {
			return errors.New("highlight source or provider changed during the request")
		}
		if err := check(); err != nil {
			return err
		}
		if callErr != nil {
			return callErr
		}
		if response.WireVersion != wire {
			return errors.New("highlight response lost its exact synchronized wire source")
		}
		result, err := normalizeHighlights(text, response, line, col)
		if err != nil {
			return err
		}
		result.Boundary = boundary
		out = result
		return check()
	})
	if err != nil {
		return insight.LSPHighlights{}, err
	}
	return out, nil
}

// normalizeHighlights accepts only occurrences that fit the exact original
// source. One invalid range refuses the answer; nothing is truncated or moved.
func normalizeHighlights(text string, response insight.LSPHighlightResponse, line, col int) (insight.LSPHighlights, error) {
	out := insight.LSPHighlights{WireVersion: response.WireVersion, Line: line, Col: col}
	if response.Unsupported {
		if len(response.JSON) != 0 {
			return insight.LSPHighlights{}, errors.New("unsupported highlight response also contains data")
		}
		out.Answer = insight.LSPHighlightsUnsupported
		return out, nil
	}
	parsed, err := lspnav.ParseHighlights(response.JSON)
	if err != nil {
		return insight.LSPHighlights{}, err
	}
	switch parsed.Answer {
	case lspnav.NullAnswer:
		out.Answer = insight.LSPHighlightsNull
	case lspnav.EmptyAnswer:
		out.Answer = insight.LSPHighlightsEmpty
	default:
		out.Answer = insight.LSPHighlightsFound
	}
	if len(parsed.Highlights) != 0 {
		if len(text) > navigationSourceBytes {
			return insight.LSPHighlights{}, errors.New("an occurrence does not fit the exact original source")
		}
		source, err := lspedit.NewSource(text)
		if err != nil {
			return insight.LSPHighlights{}, err
		}
		for _, h := range parsed.Highlights {
			span := navigationRange(h.Range)
			if _, err := source.Offset(span.Start.Line, span.Start.Character); err != nil {
				return insight.LSPHighlights{}, errors.New("an occurrence does not fit the exact original source")
			}
			if _, err := source.Offset(span.End.Line, span.End.Character); err != nil {
				return insight.LSPHighlights{}, errors.New("an occurrence does not fit the exact original source")
			}
			out.Highlights = append(out.Highlights, insight.LSPHighlight{Range: span, Kind: h.Kind})
		}
	}
	return out, nil
}

// RequestLSPHighlights is a passive read over acknowledged canonical source. The
// answer never becomes a selection, an edit or a save; it is presentation only.
func (w *Workspace) RequestLSPHighlights(ctx context.Context, owner document.Owner, source insight.Source, line, col int) (insight.LSPHighlights, error) {
	if w == nil || w.LiveDocuments == nil || w.LSPMgr == nil {
		return insight.LSPHighlights{}, errors.New("occurrence highlights require shared source and a language service")
	}
	if err := ctx.Err(); err != nil {
		return insight.LSPHighlights{}, err
	}
	s := w.LiveDocuments
	s.mu.Lock()
	text, err := s.readOnlyLSPSourceLocked(owner, source)
	if err == nil {
		err = validateNavigationPosition(string(text), line, col)
	}
	s.mu.Unlock()
	if err != nil {
		return insight.LSPHighlights{}, err
	}
	result, err := w.LSPMgr.RequestDocumentHighlights(ctx, source.Path, line, col)
	if err != nil {
		return insight.LSPHighlights{}, err
	}
	if result.Boundary.Source != source {
		return insight.LSPHighlights{}, document.ErrRevision
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.readOnlyLSPSourceLocked(owner, source); err != nil {
		return insight.LSPHighlights{}, err
	}
	if _, err := w.LSPMgr.semanticFence(result.Boundary, result.WireVersion, highlightsEnabled, "highlight"); err != nil {
		return insight.LSPHighlights{}, err
	}
	if err := ctx.Err(); err != nil {
		return insight.LSPHighlights{}, err
	}
	return result, nil
}
