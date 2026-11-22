package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"crude/lsp/lspedit"
	"crude/types/insight"
	"crude/workspace/document"
)

func (s *DocumentService) readOnlyLSPSourceLocked(owner document.Owner, source insight.Source) ([]byte, error) {
	if s.closed || s.idleFenced {
		return nil, context.Canceled
	}
	if s.stateErr != nil {
		return nil, s.stateErr
	}
	d, err := s.owned(owner, source.DocumentID)
	if err != nil {
		return nil, err
	}
	if source.Kind != "canonical" || s.analysisIdentityLocked(d) != source {
		return nil, document.ErrRevision
	}
	if d.snapshot.Conflict || d.diskIntent != nil || d.relocation != nil {
		return nil, errors.New("resolve shared source conflicts before requesting semantic information")
	}
	if len(d.snapshot.Text) > navigationSourceBytes {
		return nil, errors.New("semantic source exceeds its 2 MiB consumer bound")
	}
	// Unsaved acknowledged text is valid navigation source. This read-only flow
	// neither demands a save nor borrows reviewed mutation's Apply authority.
	return d.snapshot.Text, nil
}

// RequestLSPNavigation returns the navigation answer for one position in a document.
func (w *Workspace) RequestLSPNavigation(ctx context.Context, owner document.Owner, source insight.Source, kind string, line, col int, includeDeclaration bool) (insight.LSPNavigation, error) {
	if w == nil || w.LiveDocuments == nil || w.LSPMgr == nil {
		return insight.LSPNavigation{}, errors.New("navigation requires shared source and a language service")
	}
	if err := ctx.Err(); err != nil {
		return insight.LSPNavigation{}, err
	}
	s := w.LiveDocuments
	s.mu.Lock()
	text, err := s.readOnlyLSPSourceLocked(owner, source)
	if err == nil {
		err = validateNavigationPosition(string(text), line, col)
	}
	s.mu.Unlock()
	if err != nil {
		return insight.LSPNavigation{}, err
	}
	result, err := w.LSPMgr.RequestNavigation(ctx, source.Path, kind, line, col, includeDeclaration)
	if err != nil {
		return insight.LSPNavigation{}, err
	}
	if result.Boundary.Source != source {
		return insight.LSPNavigation{}, document.ErrRevision
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.readOnlyLSPSourceLocked(owner, source); err != nil {
		return insight.LSPNavigation{}, err
	}
	if _, err := w.LSPMgr.navigationFence(result.Boundary, result.WireVersion, kind); err != nil {
		return insight.LSPNavigation{}, err
	}
	if err := ctx.Err(); err != nil {
		return insight.LSPNavigation{}, err
	}
	return result, nil
}

// InspectLSPNavigationTarget is an explicit selected-file read, not a second
// mutation or file-open owner. Operands confer no execution/write permission;
// canonical source ownership and the original live producer are still required.
// The shell must verify the observation against the actual opened buffer before
// positioning, because standard locations contain no target revision.
func (w *Workspace) InspectLSPNavigationTarget(ctx context.Context, owner document.Owner, boundary insight.Boundary, wire int, kind string, target insight.LSPNavigationTarget) (insight.LSPNavigationLanding, error) {
	if w == nil || w.LiveDocuments == nil || w.LSPMgr == nil {
		return insight.LSPNavigationLanding{}, errors.New("navigation source authority is unavailable")
	}
	boundary.Context.UnknownInputs = slices.Clone(boundary.Context.UnknownInputs)
	if target.Origin != nil {
		r := *target.Origin
		target.Origin = &r
	}
	path, err := navigationURIPath(target.URI)
	if err != nil {
		return insight.LSPNavigationLanding{}, err
	}
	s := w.LiveDocuments
	s.mu.Lock()
	sourceText, err := s.readOnlyLSPSourceLocked(owner, boundary.Source)
	if err == nil && target.Origin != nil {
		err = validateNavigationRange(string(sourceText), *target.Origin)
	}
	s.mu.Unlock()
	if err != nil {
		return insight.LSPNavigationLanding{}, err
	}
	check, err := w.LSPMgr.navigationFence(boundary, wire, kind)
	if err != nil {
		return insight.LSPNavigationLanding{}, err
	}
	selected, err := s.acquireAnalysisSource(ctx, path)
	if err != nil {
		return insight.LSPNavigationLanding{}, err
	}
	defer selected.release()
	if selected.snapshot.Conflict {
		return insight.LSPNavigationLanding{}, errors.New("navigation target has unresolved shared changes")
	}
	text := string(selected.snapshot.Text)
	if err := validateNavigationRange(text, target.Range); err != nil {
		return insight.LSPNavigationLanding{}, err
	}
	if err := validateNavigationRange(text, target.Selection); err != nil {
		return insight.LSPNavigationLanding{}, err
	}
	if navigationPositionBefore(target.Selection.Start, target.Range.Start) || navigationPositionBefore(target.Range.End, target.Selection.End) ||
		!target.Link && (target.Selection != target.Range || target.Origin != nil) {
		return insight.LSPNavigationLanding{}, errors.New("navigation selection does not identify its target range")
	}
	if err := selected.validate(ctx); err != nil {
		return insight.LSPNavigationLanding{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.readOnlyLSPSourceLocked(owner, boundary.Source); err != nil {
		return insight.LSPNavigationLanding{}, err
	}
	if err := selected.validateLocked(); err != nil {
		return insight.LSPNavigationLanding{}, err
	}
	if err := check(); err != nil {
		return insight.LSPNavigationLanding{}, err
	}
	if err := ctx.Err(); err != nil {
		return insight.LSPNavigationLanding{}, err
	}
	// Re-derive presentation paths and flags, ignoring caller-authored policy.
	target.Path, target.UnavailableReason = path, ""
	target.External = !documentUnder(s.root, path)
	if target.Origin != nil {
		r := *target.Origin
		target.Origin = &r
	}
	boundary.Context.UnknownInputs = slices.Clone(boundary.Context.UnknownInputs)
	sum := sha256.Sum256(selected.snapshot.Text)
	start, _ := lspedit.Offset(text, target.Selection.Start.Line, target.Selection.Start.Character)
	begin := strings.LastIndexByte(text[:start], '\n') + 1
	end := strings.IndexByte(text[start:], '\n')
	if end < 0 {
		end = len(text)
	} else {
		end += start
	}
	if end-begin > 1024 {
		end = begin + 1024
		for end > begin && !utf8.RuneStart(text[end]) {
			end--
		}
	}
	return insight.LSPNavigationLanding{Boundary: boundary, Target: target, Source: selected.ref,
		TextHash: hex.EncodeToString(sum[:]), Preview: text[begin:end]}, nil
}
