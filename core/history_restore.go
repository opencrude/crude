package core

import (
	"context"
	"errors"
	"path/filepath"
	"sync"

	"crude/coreprotocol"
	"crude/localhistory"
	"crude/workspace"
)

// restoreHistoryChoices is the common disk/document owner for native UI RPCs
// and semantic controls. Its caller holds session admission through publication.
// committed distinguishes a known disk effect from a pre-effect refusal.
func restoreHistoryChoices(ctx context.Context, root string, store *localhistory.Store, history *workspace.LocalHistoryService, documents *workspace.DocumentService, input coreprotocol.HistoryApplyRequest, claim func(context.Context, []string) (func(), error)) (result localhistory.RestoreResult, committed bool, err error) {
	if store == nil || documents == nil {
		return result, false, errors.New("mandatory history/document authority is unavailable")
	}
	plan, err := store.LoadPlan(input.PlanID)
	if err != nil {
		return result, false, err
	}
	// The store's low-level nil selection means all safe paths. Interactive
	// restore must never interpret an empty selection as authority to mutate.
	if len(input.Choices) == 0 {
		return result, false, errors.New("select at least one path from the immutable restore preview")
	}
	paths := make([]string, 0, len(input.Choices))
	for _, choice := range input.Choices {
		found := false
		key := filepath.ToSlash(filepath.Clean(filepath.FromSlash(choice.Path)))
		for _, change := range plan.Changes {
			if change.Path == key {
				found = true
				break
			}
		}
		if !found {
			return result, false, errors.New("restore choice is absent from immutable preview")
		}
		// Native user Undo may reverse an accepted operation outside the project.
		// The immutable history owner still rechecks symlinks, identity and exact
		// disk preimages. A semantic agent/control caller separately retains its
		// execution confinement; plan membership never widens that authority.
		path := filepath.FromSlash(key)
		if claim != nil {
			var err error
			path, err = controlCheckedPath(root, path, true)
			if err != nil {
				return result, false, err
			}
		} else if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		paths = append(paths, path)
	}
	if claim != nil {
		release, err := claim(ctx, paths)
		if err != nil {
			return result, false, err
		}
		defer release()
	}
	err = documents.WithHistoryMutation(ctx, paths, func() error {
		var applyErr error
		if history != nil {
			result, applyErr = history.ApplyRestoreChoices(ctx, input.PlanID, input.Choices)
		} else {
			result, applyErr = store.ApplyRestoreChoices(ctx, input.PlanID, input.Choices)
		}
		committed = applyErr == nil
		return applyErr
	})
	return result, committed, err
}

// prepareHistoryApply is called only after capacity and immutable receipt
// admission. No replay can acquire a second lease or repeat the disk mutation.
func (a *actor) prepareHistoryApply(request coreprotocol.Request) (func(context.Context) coreprotocol.Response, func(coreprotocol.Response) error, func()) {
	refuse := func(err error) (func(context.Context) coreprotocol.Response, func(coreprotocol.Response) error, func()) {
		return func(context.Context) coreprotocol.Response { return fault(request, "ownership_blocked", err.Error()) }, nil, nil
	}
	var input coreprotocol.HistoryApplyRequest
	if err := decodeRequest(request, &input); err != nil {
		return func(context.Context) coreprotocol.Response { return fault(request, "invalid_request", err.Error()) }, nil, nil
	}
	if a.historyBusy || a.lifecycle {
		return refuse(errors.New("history restore or session creation owns admission"))
	}
	ws := a.ws
	release, err := ws.BeginHistoryRestoreLease(a.ctx)
	if err != nil {
		return refuse(err)
	}
	a.historyBusy = true
	var once sync.Once
	finish := func() { once.Do(func() { release(); a.historyBusy = false }) }
	root, store, history, documents := ws.RootPath, ws.LocalHistory, ws.HistoryService, ws.LiveDocuments
	var restored localhistory.RestoreResult
	var committed bool
	var publicationErr error
	run := func(ctx context.Context) coreprotocol.Response {
		var err error
		restored, committed, err = restoreHistoryChoices(ctx, root, store, history, documents, input, nil)
		if err != nil && !committed {
			return fault(request, "history_failed", err.Error())
		}
		publicationErr = err
		return response(request, restored)
	}
	publish := func(coreprotocol.Response) error {
		if committed && restored.SessionID != "" {
			publicationErr = errors.Join(publicationErr, ws.AppendHistoryRestoreEvent(restored))
		}
		return publicationErr
	}
	return run, publish, finish
}
