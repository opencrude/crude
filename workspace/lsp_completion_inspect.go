package workspace

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"time"

	"crude/lsp/lspcompletion"
	"crude/lsp/lspedit"
	"crude/types/insight"
	"crude/workspace/document"
)

// InspectLSPCompletion borrows only the exact retained original row. Resolve
// cannot replace its insertion. Before acceptance (resolve=false), all original
// edits are validated atomically against the still-current canonical source.
func (w *Workspace) InspectLSPCompletion(ctx context.Context, owner document.Owner, id, digest, itemID string, resolve bool) (insight.LSPCompletionInspection, error) {
	if w == nil || w.LiveDocuments == nil || w.LSPMgr == nil {
		return insight.LSPCompletionInspection{}, errors.New("completion source authority is unavailable")
	}
	if id == "" || len(id) > 128 || len(digest) != 64 || itemID == "" || len(itemID) > 128 {
		return insight.LSPCompletionInspection{}, errors.New("invalid completion set or row identity")
	}
	s := w.LiveDocuments
	s.mu.Lock()
	s.pruneCompletionViewsLocked()
	var key completionViewKey
	var entry *completionView
	var row insight.LSPCompletionEntry
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
		return insight.LSPCompletionInspection{}, errors.New("the exact original completion set or row is unavailable")
	}
	text, err := s.readOnlyLSPSourceLocked(owner, entry.source)
	text = bytes.Clone(text)
	row.Insertion = insight.CloneCompletionItem(row.Insertion)
	original := bytes.Clone(entry.originals[itemID])
	set, check, lifetime := entry.set, entry.check, entry.ctx
	cached, hasCached := entry.resolved[itemID]
	s.mu.Unlock()
	if err != nil {
		return insight.LSPCompletionInspection{}, err
	}
	requestCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(lifetime, cancel)
	defer cancel()
	defer stop()
	if err := lifetime.Err(); err != nil {
		return insight.LSPCompletionInspection{}, err
	}
	if err := requestCtx.Err(); err != nil {
		return insight.LSPCompletionInspection{}, err
	}
	if err := check(); err != nil {
		return insight.LSPCompletionInspection{}, err
	}
	out := insight.LSPCompletionInspection{SetID: id, Digest: digest, ItemID: itemID, Boundary: set.Boundary,
		WireVersion: set.WireVersion, FeatureGeneration: set.FeatureGeneration, Documentation: row.Documentation, ResolveState: "initial",
		Detail: "The primary insertion is unchanged."}
	// If the original already declares its edits, accepting it needs no resolve.
	// Otherwise a provider can supply imports even when no docs reader was opened.
	needResolve := set.ResolveSupported && (resolve || row.Insertion.AdditionalTextEdits == nil)
	resolved := cached
	if needResolve && !hasCached {
		response, err := w.LSPMgr.ResolveCompletion(requestCtx, set.Boundary, set.WireVersion, original)
		if err != nil {
			return insight.LSPCompletionInspection{}, err
		}
		if response.Unsupported {
			return insight.LSPCompletionInspection{}, errors.New("the original provider no longer offers completion resolution")
		}
		resolved, err = lspcompletion.Resolve(original, response.JSON)
		if err != nil {
			return insight.LSPCompletionInspection{}, err
		}
	}
	if needResolve && resolve {
		out.Documentation, out.ResolveState = completionDocumentation(resolved.Documentation), "resolved"
	} else if resolve {
		out.ResolveState = "unsupported"
	}
	if !resolve || len(resolved.Additional) > 0 {
		if row.Insertion.TextEdit == nil {
			return insight.LSPCompletionInspection{}, errors.New("the primary insertion is missing")
		}
		extras := row.Insertion.AdditionalTextEdits
		if resolved.Additional != nil {
			extras = resolved.Additional
		}
		edits := append([]insight.LSPTextEdit{*row.Insertion.TextEdit}, extras...)
		if _, err := lspedit.Apply(string(text), edits); err != nil {
			return insight.LSPCompletionInspection{}, err
		}
		if !resolve {
			out.AdditionalEdits = slices.Clone(resolved.Additional)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.completionViews[key] != entry || time.Now().After(entry.expires) {
		return insight.LSPCompletionInspection{}, errors.New("the original completion view was replaced or expired")
	}
	if _, err := s.readOnlyLSPSourceLocked(owner, entry.source); err != nil {
		return insight.LSPCompletionInspection{}, err
	}
	if err := check(); err != nil {
		return insight.LSPCompletionInspection{}, err
	}
	if err := entry.ctx.Err(); err != nil {
		return insight.LSPCompletionInspection{}, err
	}
	if err := requestCtx.Err(); err != nil {
		return insight.LSPCompletionInspection{}, err
	}
	if needResolve && !hasCached {
		if prior, ok := entry.resolved[itemID]; ok {
			if !slices.Equal(prior.Additional, resolved.Additional) {
				return insight.LSPCompletionInspection{}, errors.New("the server returned different edits for the same suggestion")
			}
		} else {
			size := len(resolved.Detail) + len(resolved.Value) + 128
			for _, e := range resolved.Additional {
				size += len(e.NewText) + 48
			}
			if !s.completionBudgetLocked(entry, entry.bytes+size) {
				return insight.LSPCompletionInspection{}, errors.New("resolved suggestion is too large")
			}
			entry.resolved[itemID] = resolved
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
	if s.closed || !s.attachedOwner(owner) {
		return nil
	}
	s.pruneCompletionViewsLocked()
	key := completionViewKey{owner, viewID}
	view := s.completionViews[key]
	if view != nil && view.sequence > sequence {
		return nil
	}
	if view == nil {
		if len(s.completionViews) >= completionMaxSequences {
			return ErrCompletionBusy
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		view = &completionView{ctx: ctx, cancel: cancel}
		if s.completionViews == nil {
			s.completionViews = make(map[completionViewKey]*completionView)
		}
		s.completionViews[key] = view
	}
	view.retire()
	view.sequence, view.expires = sequence, time.Now().Add(completionLifetime)
	return nil
}
