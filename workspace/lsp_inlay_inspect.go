package workspace

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"time"

	"crude/document"
	"crude/lspinlay"
	"crude/types"
)

// InspectLSPInlayHint selects only a retained original object. Resolve is an
// explicit tooltip-only observation; no caller-authored hint, command, text edit
// or label location can become an action through this route.
func (w *Workspace) InspectLSPInlayHint(ctx context.Context, owner document.Owner, id, digest string, index int, resolve bool) (types.LSPInlayInspection, error) {
	if w == nil || w.LiveDocuments == nil || w.LSPMgr == nil {
		return types.LSPInlayInspection{}, errors.New("inlay source authority is unavailable")
	}
	if id == "" || len(id) > 128 || len(digest) != 64 || index < 0 || index >= lspinlay.MaxHints {
		return types.LSPInlayInspection{}, errors.New("invalid inlay set or row identity")
	}
	s := w.LiveDocuments
	s.mu.Lock()
	s.pruneInlayViewsLocked()
	var key inlayViewKey
	var entry *inlayView
	for k, candidate := range s.inlayViews {
		if candidate.view.ID == id && candidate.view.Digest == digest && k.owner == owner {
			key, entry = k, candidate
			break
		}
	}
	if entry == nil || index >= len(entry.items) || entry.check == nil {
		s.mu.Unlock()
		return types.LSPInlayInspection{}, errors.New("the exact original inlay set or row is unavailable")
	}
	text, err := s.readOnlyLSPSourceLocked(owner, entry.source)
	// Resolve runs outside the document lock, so both text and raw original
	// operands must be detached before a later source mutation can proceed.
	text = bytes.Clone(text)
	original := bytes.Clone(entry.items[index].Raw)
	view, check, lifetime := cloneInlaySet(entry.view), entry.check, entry.ctx
	s.mu.Unlock()
	if err != nil {
		return types.LSPInlayInspection{}, err
	}
	requestCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(lifetime, cancel)
	defer cancel()
	defer stop()
	if err := lifetime.Err(); err != nil {
		return types.LSPInlayInspection{}, err
	}
	if err := requestCtx.Err(); err != nil {
		return types.LSPInlayInspection{}, err
	}
	if err := check(); err != nil {
		return types.LSPInlayInspection{}, err
	}
	out := types.LSPInlayInspection{SetID: id, Digest: digest, Index: index,
		Boundary: view.Boundary, WireVersion: view.WireVersion, Range: view.Range,
		Hint: cloneInlayHint(view.Hints[index]), OriginalJSON: original,
		ResolveState: "not-requested", Detail: "Read-only original evidence. Commands, edits and label locations are not executed."}
	if resolve {
		if !view.ResolveSupported {
			out.ResolveState = "unsupported"
		} else {
			response, err := w.LSPMgr.ResolveInlayHint(requestCtx, view.Boundary, view.WireVersion, original)
			if err != nil {
				return types.LSPInlayInspection{}, err
			}
			if response.Unsupported {
				return types.LSPInlayInspection{}, errors.New("the original provider no longer advertises tooltip resolution")
			}
			hint, err := lspinlay.Resolve(original, response.JSON)
			if err != nil {
				return types.LSPInlayInspection{}, err
			}
			out.Hint, err = inlayHintSummary(string(text), index, hint)
			if err != nil {
				return types.LSPInlayInspection{}, err
			}
			out.ResolvedJSON, out.ResolveState = bytes.Clone(response.JSON), "resolved-tooltips"
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inlayViews[key] != entry || time.Now().After(entry.expires) {
		return types.LSPInlayInspection{}, errors.New("the original inlay view was replaced or expired")
	}
	if _, err := s.readOnlyLSPSourceLocked(owner, entry.source); err != nil {
		return types.LSPInlayInspection{}, err
	}
	if err := check(); err != nil {
		return types.LSPInlayInspection{}, err
	}
	if err := entry.ctx.Err(); err != nil {
		return types.LSPInlayInspection{}, err
	}
	if err := requestCtx.Err(); err != nil {
		return types.LSPInlayInspection{}, err
	}
	out.Boundary.Context.UnknownInputs = slices.Clone(out.Boundary.Context.UnknownInputs)
	return out, nil
}

// ReleaseLSPInlayHints fences this owner's private view through this sequence.
// Late release of an older sequence cannot retire its newer replacement.
func (s *DocumentService) ReleaseLSPInlayHints(owner document.Owner, viewID string, sequence uint64) error {
	if !validLSPView(viewID, sequence) {
		return errors.New("invalid inlay view or sequence")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || !s.attachedOwner(owner) {
		return nil
	}
	s.pruneInlayViewsLocked()
	key := inlayViewKey{owner, viewID}
	view := s.inlayViews[key]
	if view != nil && view.sequence > sequence {
		return nil
	}
	if view == nil {
		if len(s.inlayViews) >= inlayMaxSequences {
			return ErrInlayBusy
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		view = &inlayView{ctx: ctx, cancel: cancel}
		if s.inlayViews == nil {
			s.inlayViews = make(map[inlayViewKey]*inlayView)
		}
		s.inlayViews[key] = view
	}
	view.retire()
	view.sequence, view.expires = sequence, time.Now().Add(inlayLifetime)
	return nil
}
