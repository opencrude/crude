package workspace

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync/atomic"
	"time"

	"crude/document"
	"crude/lspaction"
	"crude/lspedit"
	"crude/types"
)

const codeActionMaxSets = 4

// CodeActionSet is detached presentation evidence. Original provider objects and
// executable adjuncts never cross the shell boundary. ID/digest/index select the
// canonical owner's immutable set; changing a returned summary grants nothing.
type CodeActionSet struct {
	ID, Digest          string
	Boundary            types.InsightBoundary
	Start, End          int // exact source UTF-8 bytes, not editor display columns
	Items               []CodeActionSummary
	DiagnosticsIncluded int
	DiagnosticsDetail   string
}

type CodeActionSummary struct {
	Index                             int
	Title, Kind                       string
	Preferred, HasPreferred           bool
	NeedsResolve, CanReview           bool
	DisabledReason, UnsupportedReason string
}

type codeActionSet struct {
	view    CodeActionSet
	owner   document.Owner
	wire    int
	items   []lspaction.Action
	expires time.Time
	retired atomic.Bool
	check   func() error // memory-only exact synchronized provider/source fence
}

func cloneCodeActionSet(in CodeActionSet) CodeActionSet {
	in.Items = slices.Clone(in.Items)
	in.Boundary.Context.UnknownInputs = slices.Clone(in.Boundary.Context.UnknownInputs)
	return in
}

func (s *DocumentService) codeActionSourceLocked(owner document.Owner, source types.InsightSource) ([]byte, error) {
	if err := s.validateReviewedSourceLocked(owner, source); err != nil {
		return nil, err
	}
	text := s.docs[source.DocumentID].snapshot.Text
	if len(text) > reviewedEditFileBytes {
		return nil, errors.New("code actions require source within the 2 MiB recoverable review bound")
	}
	return bytes.Clone(text), nil
}

// pruneCodeActionSetsLocked is bounded by codeActionMaxSets. It performs no
// source/disk/provider I/O and does not infer any historical mutation outcome.
func (s *DocumentService) pruneCodeActionSetsLocked() {
	for id, set := range s.codeActions {
		if time.Now().After(set.expires) ||
			s.validateReviewedSourceLocked(set.owner, set.view.Boundary.Source) != nil ||
			set.check() != nil {
			set.retired.Store(true)
			delete(s.codeActions, id)
		}
	}
}

// RequestLSPCodeActions is an explicit, current-document, edit-only discovery.
// A null/empty response is scoped evidence at this source and selection, never a
// conclusion about project cleanliness or code-action semantic completeness.
func (w *Workspace) RequestLSPCodeActions(ctx context.Context, owner document.Owner, expected types.InsightSource, start, end int) (CodeActionSet, error) {
	if w == nil || w.LiveDocuments == nil || w.LSPMgr == nil || w.LocalHistory == nil {
		return CodeActionSet{}, errors.New("code actions require canonical source, language service and recoverable Local History")
	}
	if err := ctx.Err(); err != nil {
		return CodeActionSet{}, err
	}
	s := w.LiveDocuments
	s.mu.Lock()
	s.pruneCodeActionSetsLocked()
	text, err := s.codeActionSourceLocked(owner, expected)
	if err == nil && len(s.codeActions) >= codeActionMaxSets {
		err = errors.New("close an earlier code-action list before requesting another")
	}
	s.mu.Unlock()
	if err != nil {
		return CodeActionSet{}, err
	}
	if end < start {
		return CodeActionSet{}, errors.New("code-action source range is reversed")
	}
	line, col, err := lspedit.Position(string(text), start)
	if err != nil {
		return CodeActionSet{}, err
	}
	endLine, endCol, err := lspedit.Position(string(text), end)
	if err != nil {
		return CodeActionSet{}, err
	}
	response, err := w.LSPMgr.RequestCodeActions(ctx, expected.Path, line, col, endLine, endCol)
	if err != nil {
		return CodeActionSet{}, err
	}
	if response.Boundary.Source != expected {
		return CodeActionSet{}, document.ErrRevision
	}
	items, err := lspaction.Parse(response.JSON, response.ResolveSupported)
	if err != nil {
		return CodeActionSet{}, err
	}
	fence, err := w.LSPMgr.reviewedEditFence(response.Boundary, response.WireVersion, nil)
	if err != nil {
		return CodeActionSet{}, err
	}
	set := &codeActionSet{
		owner: owner, wire: response.WireVersion, items: items,
		expires: time.Now().Add(reviewedEditLifetime),
		view: CodeActionSet{
			ID: "actions-" + rand.Text(), Boundary: response.Boundary,
			Start: start, End: end, DiagnosticsIncluded: response.DiagnosticsIncluded,
			DiagnosticsDetail: response.DiagnosticsDetail,
		},
	}
	set.check = func() error {
		if set.retired.Load() || time.Now().After(set.expires) {
			return errors.New("the original code-action list was retired or expired; request a new list")
		}
		return fence()
	}
	set.view.Boundary.Context.UnknownInputs = slices.Clone(response.Boundary.Context.UnknownInputs)
	rawDigests := make([]string, len(items))
	for index, item := range items {
		row := CodeActionSummary{
			Index: index, Title: item.Title, Kind: item.Kind,
			Preferred: item.Preferred, HasPreferred: item.HasPreferred, NeedsResolve: item.NeedsResolve,
			DisabledReason: item.DisabledReason, UnsupportedReason: item.UnsupportedReason,
		}
		if row.UnsupportedReason == "" && len(item.Edit) != 0 {
			if _, err := s.codeActionTargets(expected, set.wire, string(text), item.Edit); err != nil {
				row.UnsupportedReason = err.Error()
			}
		}
		row.CanReview = row.UnsupportedReason == "" && (item.NeedsResolve || len(item.Edit) != 0)
		set.view.Items = append(set.view.Items, row)
		rawDigests[index] = reviewedHash(string(item.Raw))
	}
	encoded, err := json.Marshal(struct {
		Owner document.Owner
		View  CodeActionSet
		Raw   []string
	}{owner, set.view, rawDigests})
	if err != nil {
		return CodeActionSet{}, err
	}
	set.view.Digest = reviewedHash(string(encoded))
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneCodeActionSetsLocked()
	if err := ctx.Err(); err != nil {
		return CodeActionSet{}, err
	}
	if err := s.validateReviewedSourceLocked(owner, expected); err != nil {
		return CodeActionSet{}, err
	}
	if err := fence(); err != nil {
		return CodeActionSet{}, err
	}
	if len(s.codeActions) >= codeActionMaxSets {
		return CodeActionSet{}, errors.New("close an earlier code-action list before requesting another")
	}
	if s.codeActions == nil {
		s.codeActions = make(map[string]*codeActionSet)
	}
	s.codeActions[set.view.ID] = set
	return cloneCodeActionSet(set.view), nil
}

// codeActionTargets admits only the invoking canonical path and strict original
// ordinary text edits. Unsupported rows remain inspectable but cannot acquire an
// Apply permit or drop a command/resource/multi-file part.
func (s *DocumentService) codeActionTargets(source types.InsightSource, wireVersion int, text string, edit []byte) ([]lspedit.DocumentEdit, error) {
	targets, err := lspedit.Parse(edit)
	if err != nil {
		return nil, errors.New("the action's ordinary text edits are malformed or exceed review bounds")
	}
	if len(targets) != 1 {
		return nil, errors.New("only ordinary text edits to the current document are supported")
	}
	if targets[0].Versioned && (targets[0].Version == nil || wireVersion <= 0 || int(*targets[0].Version) != wireVersion) {
		return nil, errors.New("the text-document edit does not name the exact original synchronized source version")
	}
	path, err := reviewedURIPath(s.root, targets[0].URI)
	if err != nil || path != source.Path {
		return nil, errors.New("the action targets another or unsupported file, not the current canonical document")
	}
	after, err := lspedit.Apply(text, targets[0].Edits)
	if err != nil {
		return nil, errors.New("the action's ranges are invalid, ambiguous or outside this exact source")
	}
	if after == text {
		return nil, errors.New("the action proposes no source changes")
	}
	if len(after) > reviewedEditFileBytes {
		return nil, errors.New("the action's result exceeds the 2 MiB recoverable review bound")
	}
	return targets, nil
}

// PrepareLSPCodeAction resolves at most this exact retained row and returns the
// same reviewed-edit capability used by rename. It never mutates files, executes
// a command or accepts caller-authored raw provider objects.
func (w *Workspace) PrepareLSPCodeAction(ctx context.Context, owner document.Owner, id, digest string, index int) (ReviewedEditPreview, error) {
	if w == nil || w.LiveDocuments == nil || w.LSPMgr == nil || w.LocalHistory == nil {
		return ReviewedEditPreview{}, errors.New("code-action review is unavailable")
	}
	if id == "" || len(id) > 128 || len(digest) != 64 || index < 0 || index >= 64 {
		return ReviewedEditPreview{}, errors.New("invalid code-action set or row identity")
	}
	s := w.LiveDocuments
	s.mu.Lock()
	s.pruneCodeActionSetsLocked()
	set := s.codeActions[id]
	if set == nil || set.owner != owner || digest != set.view.Digest || index < 0 || index >= len(set.items) {
		s.mu.Unlock()
		return ReviewedEditPreview{}, errors.New("the exact original code-action set or row is unavailable")
	}
	if !set.view.Items[index].CanReview {
		s.mu.Unlock()
		return ReviewedEditPreview{}, errors.New("this disabled or unsupported action cannot be reviewed")
	}
	text, err := s.codeActionSourceLocked(owner, set.view.Boundary.Source)
	item := set.items[index]
	s.mu.Unlock()
	if err != nil {
		return ReviewedEditPreview{}, err
	}
	if err := ctx.Err(); err != nil {
		return ReviewedEditPreview{}, err
	}
	if err := set.check(); err != nil {
		return ReviewedEditPreview{}, err
	}
	originalDigest := reviewedHash(string(item.Raw))
	if item.NeedsResolve {
		response, err := w.LSPMgr.ResolveCodeAction(ctx, set.view.Boundary, set.wire, item.Raw)
		if err != nil {
			return ReviewedEditPreview{}, err
		}
		item, err = lspaction.Resolve(item.Raw, response.JSON)
		if err != nil {
			return ReviewedEditPreview{}, err
		}
	}
	if item.UnsupportedReason != "" || len(item.Edit) == 0 {
		return ReviewedEditPreview{}, errors.New("the action is not a supported edit-only literal")
	}
	targets, err := s.codeActionTargets(set.view.Boundary.Source, set.wire, string(text), item.Edit)
	if err != nil {
		return ReviewedEditPreview{}, err
	}
	summary := fmt.Sprintf("Action title (literal): %s\nKind (literal): %s\nOriginal action SHA-256: %s\nReviewed action SHA-256: %s\n\n",
		strconv.Quote(item.Title), strconv.Quote(item.Kind), originalDigest, reviewedHash(string(item.Raw)))
	// A release during the resolve RPC or native staging revokes preparation and
	// Apply without taking DocumentService.mu recursively in the memory fence.
	return s.prepareReviewedEdit(ctx, owner, "LSP code action", summary, set.view.Boundary, targets, set.check)
}

func (s *DocumentService) ReleaseLSPCodeActions(owner document.Owner, id, digest string) error {
	if id == "" || len(id) > 128 || len(digest) != 64 {
		return errors.New("invalid code-action set identity")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	set := s.codeActions[id]
	if set == nil {
		return nil
	}
	if set.owner != owner || set.view.Digest != digest {
		return document.ErrOwner
	}
	set.retired.Store(true)
	delete(s.codeActions, id)
	return nil
}

func (s *DocumentService) closeCodeActionsLocked(owner document.Owner, documentID string) {
	for id, set := range s.codeActions {
		if set.owner == owner && (documentID == "" || set.view.Boundary.Source.DocumentID == documentID) {
			set.retired.Store(true)
			delete(s.codeActions, id)
		}
	}
}
