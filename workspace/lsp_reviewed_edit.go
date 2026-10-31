package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"crude/document"
	"crude/lspedit"
	"crude/types"
)

// PrepareLSPRename is the one native/connected rename preparation route. The
// actor supplies owner; expected identifies the invoking acknowledged source.
// Provider JSON is normalized here, never decoded and rebuilt by the shell.
func (w *Workspace) PrepareLSPRename(ctx context.Context, owner document.Owner, expected types.InsightSource, line, col int, name string) (ReviewedEditPreview, error) {
	if w == nil || w.LiveDocuments == nil || w.LSPMgr == nil || w.LocalHistory == nil {
		return ReviewedEditPreview{}, errors.New("rename requires canonical source, language service and recoverable Local History")
	}
	if name == "" || len(name) > 256 || !utf8.ValidString(name) || strings.ContainsAny(name, "\r\n\x00") {
		return ReviewedEditPreview{}, errors.New("invalid or oversized rename label")
	}
	s := w.LiveDocuments
	s.mu.Lock()
	err := s.validateReviewedSourceLocked(owner, expected)
	var text []byte
	if err == nil {
		source := s.docs[expected.DocumentID].snapshot.Text
		if len(source) > reviewedEditFileBytes {
			err = errors.New("invoking rename source exceeds its 2 MiB review bound")
		} else {
			text = bytes.Clone(source)
		}
	}
	s.mu.Unlock()
	if err != nil {
		return ReviewedEditPreview{}, err
	}
	if _, err := lspedit.Offset(string(text), line, col); err != nil {
		return ReviewedEditPreview{}, err
	}
	result, err := w.LSPMgr.RequestRename(ctx, expected.Path, line, col, name)
	if err != nil {
		return ReviewedEditPreview{}, err
	}
	if result.Boundary.Source != expected {
		return ReviewedEditPreview{}, document.ErrRevision
	}
	targets, err := lspedit.Parse(result.JSON)
	if err != nil {
		return ReviewedEditPreview{}, err
	}
	check, err := w.LSPMgr.reviewedEditFence(result, targets)
	if err != nil {
		return ReviewedEditPreview{}, err
	}
	return s.prepareReviewedEdit(ctx, owner, "LSP rename", result.Boundary, targets, check)
}

type reviewedLSPSource struct {
	path     string
	document *lspDocument
	boundary types.InsightBoundary
	wire     int
}

// Captures only memory identities. Callers may use the returned fence while
// holding DocumentService.mu; it never acquires documents or performs I/O.
func (m *LSPManager) reviewedEditFence(result types.LSPRenameEdit, targets []lspedit.DocumentEdit) (func() error, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	main := m.documents[filepath.Clean(result.Boundary.Source.Path)]
	if main == nil || !main.opened || main.syncPending || main.wireVersion != result.WireVersion ||
		!sameInsightBoundary(main.boundary, result.Boundary) || main.boundary.Context.Generation != m.contextGeneration {
		return nil, errors.New("rename source/provider/context is no longer synchronized")
	}
	state := m.states[main.key]
	if state == nil || state.client == nil || state.closing || state.stopped || state.teardownErr != nil {
		return nil, errors.New("rename provider is no longer available")
	}
	client, settingsGeneration, contextGeneration := state.client, m.settingsGeneration, m.contextGeneration
	capture := func(path string, doc *lspDocument) reviewedLSPSource {
		boundary := doc.boundary
		boundary.Context.UnknownInputs = slices.Clone(boundary.Context.UnknownInputs)
		return reviewedLSPSource{path, doc, boundary, doc.wireVersion}
	}
	sources := []reviewedLSPSource{capture(result.Boundary.Source.Path, main)}
	for _, target := range targets {
		path, err := reviewedURIPath(m.workspaceRoot, target.URI)
		if err != nil {
			return nil, err
		}
		if !target.Versioned || target.Version == nil {
			continue
		}
		doc := m.documents[path]
		if doc == nil || !doc.opened || doc.syncPending || doc.key != main.key ||
			doc.wireVersion != int(*target.Version) || doc.wireVersion <= 0 ||
			doc.boundary.Producer != result.Boundary.Producer || doc.boundary.Context.Generation != contextGeneration {
			return nil, fmt.Errorf("reviewed target does not match its exact provider wire version")
		}
		sources = append(sources, capture(path, doc))
	}
	return func() error {
		m.mu.Lock()
		defer m.mu.Unlock()
		currentState := m.states[main.key]
		if m.closed || m.paused || m.settingsGeneration != settingsGeneration || m.contextGeneration != contextGeneration ||
			currentState != state || state.client != client || state.closing || state.stopped || state.teardownErr != nil {
			return errors.New("reviewed edit provider or semantic context changed")
		}
		for _, source := range sources {
			doc := m.documents[filepath.Clean(source.path)]
			if doc != source.document || !doc.opened || doc.syncPending || doc.wireVersion != source.wire ||
				!sameInsightBoundary(doc.boundary, source.boundary) {
				return errors.New("reviewed edit synchronized source changed")
			}
		}
		return nil
	}, nil
}
