package workspace

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
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
	items, err := lspaction.Parse(response.JSON, response.ResolveSupported, response.CommandSupported)
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
		if row.UnsupportedReason == "" && item.Workspace != nil {
			if _, err := s.codeActionTargets(expected, set.wire, string(text), item.Workspace); err != nil {
				row.UnsupportedReason = err.Error()
			}
		}
		row.CanReview = row.UnsupportedReason == "" && (item.NeedsResolve || item.Workspace != nil || item.Command != nil)
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

// codeActionTargets admits only workspace-local ordinary text edits and file
// creation. Resource operations other than create are refused as a whole.
func (s *DocumentService) codeActionTargets(source types.InsightSource, wireVersion int, text string, edit *lspedit.WorkspaceEdit) ([]lspedit.DocumentEdit, error) {
	if edit == nil || len(edit.Operations) == 0 {
		return nil, errors.New("the action has no source changes")
	}
	targets := make([]lspedit.DocumentEdit, 0, len(edit.Operations))
	index := make(map[string]int)
	for _, operation := range edit.Operations {
		path, err := reviewedURIPath(s.root, operation.URI)
		if err != nil {
			return nil, errors.New("the action targets another or unsupported file")
		}
		if operation.Kind == lspedit.OperationCreate {
			if operation.Versioned || len(operation.Edits) != 0 {
				return nil, errors.New("the action's create operation is malformed")
			}
			if existing, ok := index[path]; ok {
				if !targets[existing].Create {
					return nil, errors.New("the action edits a file it also creates")
				}
				continue
			}
			index[path] = len(targets)
			targets = append(targets, lspedit.DocumentEdit{URI: operation.URI, Create: true})
			continue
		}
		if operation.Kind != lspedit.OperationText || len(operation.Edits) == 0 {
			return nil, errors.New("the action's edit operation is malformed")
		}
		if existing, ok := index[path]; ok {
			if targets[existing].Create && len(targets[existing].Edits) == 0 {
				if operation.Versioned && operation.Version != nil && *operation.Version != 0 {
					return nil, errors.New("the new file's edit does not name version zero")
				}
				targets[existing].Edits = append(targets[existing].Edits, operation.Edits...)
				continue
			}
			return nil, errors.New("the action has more than one edit range for a file")
		}
		if operation.Versioned && (operation.Version == nil || wireVersion <= 0 || int(*operation.Version) != wireVersion) {
			return nil, errors.New("the text-document edit does not name the exact original synchronized source version")
		}
		index[path] = len(targets)
		targets = append(targets, lspedit.DocumentEdit{URI: operation.URI, Edits: operation.Edits, Versioned: operation.Versioned, Version: operation.Version})
	}
	for _, target := range targets {
		if target.Create {
			continue
		}
		path, _ := reviewedURIPath(s.root, target.URI)
		if path != source.Path {
			// prepareReviewedEdit owns the exact preimage for every other
			// workspace file, including its canonical version fence.
			continue
		}
		after, err := lspedit.ApplyOrdered(text, target.Edits)
		if err != nil {
			return nil, errors.New("the action's ranges are invalid, ambiguous or outside this exact source")
		}
		if after == text {
			return nil, errors.New("the action proposes no source changes")
		}
		if len(after) > reviewedEditFileBytes {
			return nil, errors.New("the action's result exceeds the 2 MiB recoverable review bound")
		}
	}
	return targets, nil
}

func (w *Workspace) executeCodeActionCommand(ctx context.Context, set *codeActionSet, item lspaction.Action) (types.LSPCommandResponse, error) {
	if item.Command == nil {
		return types.LSPCommandResponse{}, errors.New("the action has no command")
	}
	return w.LSPMgr.ExecuteCodeActionCommand(ctx, set.view.Boundary, set.wire, item.Command.Name, item.Command.Arguments)
}

// combineCommandWorkspaceEdits preserves documentChanges order across the
// captured applyEdit requests. Duplicate text targets are refused because their
// relative order would be ambiguous.
func combineCommandWorkspaceEdits(raw [][]byte) (*lspedit.WorkspaceEdit, error) {
	combined := &lspedit.WorkspaceEdit{}
	for _, edit := range raw {
		parsed, err := lspedit.ParseWorkspace(edit)
		if err != nil {
			return nil, err
		}
		combined.Operations = append(combined.Operations, parsed.Operations...)
	}
	if len(combined.Operations) == 0 {
		return nil, errors.New("the command edit has no operations")
	}
	return combined, nil
}

func commandActionOutcome(response types.LSPCommandResponse) (message, url string) {
	if response.ShowDocument != nil && response.ShowDocument.URI != "" {
		if isBrowserURL(response.ShowDocument.URI) {
			url = response.ShowDocument.URI
		} else {
			message = "Open " + response.ShowDocument.URI
		}
	}
	if len(response.Result) != 0 {
		var text string
		if json.Unmarshal(response.Result, &text) == nil && text != "" {
			message = text
			if isBrowserURL(text) {
				url = text
			}
		}
	}
	if len(response.Messages) != 0 {
		message = response.Messages[len(response.Messages)-1]
	}
	if url == "" {
		for _, candidate := range response.Messages {
			// A message names a page inside prose; take only the one word.
			for _, word := range strings.Fields(candidate) {
				word = strings.TrimLeft(strings.TrimRight(word, ".,;:)]}>\"'"), "([{<\"'")
				if isBrowserURL(word) {
					url = word
					break
				}
			}
			if url != "" {
				break
			}
		}
	}
	if message == "" {
		if url != "" {
			message = "Opened " + url
		} else {
			message = "The action completed without a visible result."
		}
	}
	return message, url
}

func isBrowserURL(value string) bool {
	// Mirror the browser opener: HTTPS, or HTTP only on this computer.
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || u.User != nil {
		return false
	}
	host := u.Hostname()
	return u.Scheme == "https" || u.Scheme == "http" && (host == "127.0.0.1" || host == "localhost" || host == "::1")
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
	if item.UnsupportedReason != "" || item.Workspace == nil && item.Command == nil {
		return ReviewedEditPreview{}, errors.New("the action has no supported edit or command")
	}
	if item.Command != nil {
		response, err := w.executeCodeActionCommand(ctx, set, item)
		if err != nil {
			return ReviewedEditPreview{}, err
		}
		if len(response.WorkspaceEdits) != 0 {
			combined, err := combineCommandWorkspaceEdits(response.WorkspaceEdits)
			if err != nil {
				return ReviewedEditPreview{}, err
			}
			item.Workspace = combined
		} else {
			message, url := commandActionOutcome(response)
			return ReviewedEditPreview{Message: message, URL: url}, nil
		}
	}
	targets, err := s.codeActionTargets(set.view.Boundary.Source, set.wire, string(text), item.Workspace)
	if err != nil {
		return ReviewedEditPreview{}, err
	}
	summary := fmt.Sprintf("Action title (literal): %s\nKind (literal): %s\nOriginal action SHA-256: %s\nReviewed action SHA-256: %s\n\n",
		strconv.Quote(item.Title), strconv.Quote(item.Kind), originalDigest, reviewedHash(string(item.Raw)))
	if item.Command != nil {
		summary += "Command (literal): " + strconv.Quote(item.Command.Name) + "\n"
	}
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
