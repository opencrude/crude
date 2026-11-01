package workspace

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"time"

	"crude/document"
	"crude/lspcompletion"
	"crude/lspedit"
	"crude/types"
)

// InspectLSPCompletion borrows only the exact retained original row. Resolve
// cannot replace its insertion. Before acceptance (resolve=false), all original
// edits are validated atomically against the still-current canonical source.
func (w *Workspace) InspectLSPCompletion(ctx context.Context, owner document.Owner, id, digest, itemID string, resolve bool) (types.LSPCompletionInspection, error) {
	if w == nil || w.LiveDocuments == nil || w.LSPMgr == nil {
		return types.LSPCompletionInspection{}, errors.New("completion source authority is unavailable")
	}
	if id == "" || len(id) > 128 || len(digest) != 64 || itemID == "" || len(itemID) > 128 {
		return types.LSPCompletionInspection{}, errors.New("invalid completion set or row identity")
	}
	s := w.LiveDocuments
	s.mu.Lock()
	s.pruneCompletionViewsLocked()
	var key completionViewKey
	var entry *completionView
	var row types.LSPCompletionEntry
	for k, candidate := range s.completionViews {
		if candidate.set.ID == id && candidate.set.Digest == digest && k.owner == owner {
			key, entry = k, candidate
			break
		}
	}
	if entry != nil {
		for _, candidate := range entry.set.Entries {
			if candidate.ID == itemID {
				row = candidate
				break
			}
		}
	}
	if entry == nil || row.ID == "" || entry.check == nil {
		s.mu.Unlock()
		return types.LSPCompletionInspection{}, errors.New("the exact original completion set or row is unavailable")
	}
	text, err := s.readOnlyLSPSourceLocked(owner, entry.source)
	text = bytes.Clone(text)
	row.Insertion = types.CloneCompletionItem(row.Insertion)
	original := bytes.Clone(entry.originals[itemID])
	set, check, lifetime := entry.set, entry.check, entry.ctx
	cached, hasCached := entry.resolved[itemID]
	s.mu.Unlock()
	if err != nil {
		return types.LSPCompletionInspection{}, err
	}
	requestCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(lifetime, cancel)
	defer cancel()
	defer stop()
	if err := requestCtx.Err(); err != nil {
		return types.LSPCompletionInspection{}, err
	}
	if err := check(); err != nil {
		return types.LSPCompletionInspection{}, err
	}
	out := types.LSPCompletionInspection{SetID: id, Digest: digest, ItemID: itemID, Boundary: set.Boundary,
		WireVersion: set.WireVersion, FeatureGeneration: set.FeatureGeneration, Documentation: row.Documentation, ResolveState: "initial",
		Detail: "The original insertion is unchanged. Documentation and detail are inert provider text."}
	if !resolve {
		if row.Insertion.TextEdit == nil {
			return types.LSPCompletionInspection{}, errors.New("retained completion lost its original primary edit")
		}
		edits := append([]types.LSPTextEdit{*row.Insertion.TextEdit}, row.Insertion.AdditionalTextEdits...)
		if _, err := lspedit.Apply(string(text), edits); err != nil {
			return types.LSPCompletionInspection{}, err
		}
	} else if hasCached {
		out.Documentation, out.ResolveState = cached, "resolved"
	} else if !set.ResolveSupported {
		out.ResolveState = "unsupported"
	} else {
		response, err := w.LSPMgr.ResolveCompletion(requestCtx, set.Boundary, set.WireVersion, original)
		if err != nil {
			return types.LSPCompletionInspection{}, err
		}
		if response.Unsupported {
			return types.LSPCompletionInspection{}, errors.New("the original provider no longer advertises completion resolution")
		}
		doc, err := lspcompletion.ResolveDocumentation(original, response.JSON)
		if err != nil {
			return types.LSPCompletionInspection{}, err
		}
		out.Documentation, out.ResolveState = completionDocumentation(doc), "resolved"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.completionViews[key] != entry || time.Now().After(entry.expires) {
		return types.LSPCompletionInspection{}, errors.New("the original completion view was replaced or expired")
	}
	if _, err := s.readOnlyLSPSourceLocked(owner, entry.source); err != nil {
		return types.LSPCompletionInspection{}, err
	}
	if err := check(); err != nil {
		return types.LSPCompletionInspection{}, err
	}
	if err := requestCtx.Err(); err != nil {
		return types.LSPCompletionInspection{}, err
	}
	if resolve && out.ResolveState == "resolved" && !hasCached {
		// Cache only display data, never a resolved insertion or raw object.
		size := len(out.Documentation.Detail) + len(out.Documentation.Value) + 128
		if !s.completionBudgetLocked(entry, entry.bytes+size) {
			return types.LSPCompletionInspection{}, errors.New("retained completion documentation exceeds its consumer bound")
		}
		if _, exists := entry.resolved[itemID]; !exists {
			entry.resolved[itemID] = out.Documentation
			entry.bytes += size
		}
	}
	out.Boundary.Context.UnknownInputs = slices.Clone(out.Boundary.Context.UnknownInputs)
	return out, nil
}

func (s *DocumentService) ReleaseLSPCompletions(owner document.Owner, viewID string, sequence uint64) error {
	if !validLSPView(viewID, sequence) {
		return errors.New("invalid completion view or sequence")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if view := s.completionViews[completionViewKey{owner, viewID}]; view != nil && view.sequence == sequence {
		view.retire()
	}
	return nil
}
