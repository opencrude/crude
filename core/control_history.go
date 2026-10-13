package core

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"

	"crude/coreprotocol"
	"crude/localhistory"
	"crude/types/control"
	"crude/workspace"
)

// prepareControlHistory captures real retained owners on the actor. The parent
// dispatcher must call this before generic history/discard mapping, and fence
// new session creation until the returned publication hook finishes.
func (a *actor) prepareControlHistory(in control.Request, owner coreprotocol.Owner) (func(context.Context) control.Result, func(control.Result) control.Result, bool) {
	if in.CommandID != "history.timeline" && in.CommandID != "history.plan" && in.CommandID != "history.apply" && in.CommandID != "git.discard_preview" && in.CommandID != "git.discard_reviewed" {
		return nil, nil, false
	}
	if owner.ClientID == "" || owner.Epoch == 0 {
		return controlFailure(in, control.Invalid, "trusted caller owner is required")
	}
	// The generic resolver deliberately refuses isolated history. Resolve the
	// same exact run/pin through the read-only Git family, then use our store.
	authority := in
	authority.CommandID = "git.snapshot"
	root, err := a.controlDomainRoot(context.Background(), authority)
	if err != nil {
		return controlFailure(in, control.Denied, err.Error())
	}
	ws, documents, history, store := a.ws, a.ws.LiveDocuments, a.ws.HistoryService, a.ws.LocalHistory
	mutating := in.CommandID == "history.apply" || in.CommandID == "git.discard_reviewed"
	release := func() {}
	if mutating {
		release, err = ws.BeginHistoryRestoreLease(context.Background())
		if err != nil {
			return controlFailure(in, control.Denied, err.Error())
		}
	}
	var releaseOnce sync.Once
	finish := func() { releaseOnce.Do(release) }
	var restored localhistory.RestoreResult
	var committed bool
	var publicationErr error
	request := coreprotocol.Request{ID: in.ID, Owner: owner, WorkspaceID: in.Target.WorkspaceID, Payload: in.Input}
	work := func(ctx context.Context) coreprotocol.Response {
		fail := func(err error) coreprotocol.Response {
			finish()
			return fault(request, "ownership_blocked", err.Error())
		}
		actual, err := a.controlDomainRoot(ctx, authority)
		if err != nil || actual != root {
			return fail(errors.New("execution authority changed before effect"))
		}
		if root != ws.RootPath {
			store, actual, err = ws.ExecutionHistory(in.Target.ExecutionID)
			if err != nil || actual != root {
				return fail(errors.New("execution history authority changed"))
			}
		}
		if store == nil {
			return fail(errors.New("mandatory Local History is unavailable"))
		}
		switch in.CommandID {
		case "history.timeline":
			var p coreprotocol.HistoryListRequest
			if err := decode(in.Input, &p); err != nil {
				return fail(err)
			}
			if p.Limit <= 0 || p.Limit > 1000 {
				p.Limit = 1000
			}
			out, err := store.Timeline(p.Limit)
			if err != nil {
				return fail(err)
			}
			return response(request, out)
		case "history.plan":
			var p coreprotocol.HistoryPlanRequest
			if err := decode(in.Input, &p); err != nil {
				return fail(err)
			}
			out, err := store.PlanRestore(ctx, p.SnapshotID, p.Options)
			if err != nil {
				return fail(err)
			}
			return response(request, out)
		case "git.discard_preview":
			var p coreprotocol.GitCompareRequest
			if err := decode(in.Input, &p); err != nil {
				return fail(err)
			}
			path := p.Change.Path
			if p.Change.AbsolutePath != "" {
				path = p.Change.AbsolutePath
			}
			checked, err := controlCheckedPath(root, path, false)
			if err != nil {
				return fail(err)
			}
			p.Change.AbsolutePath = checked
			out, err := workspace.PreviewGitDiscardAt(ctx, workspace.NewGitService(root), store, p.Change)
			if err != nil {
				return fail(err)
			}
			return response(request, out)
		case "git.discard_reviewed":
			var p coreprotocol.DiscardGitReviewedRequest
			if err := decode(in.Input, &p); err != nil {
				return fail(err)
			}
			if p.Preview.Comparison.RepositoryRoot != root {
				return fail(errors.New("discard belongs to another checkout"))
			}
			path, err := controlCheckedPath(root, p.Preview.Comparison.AbsolutePath, false)
			if err != nil {
				return fail(err)
			}
			unlock, err := a.controlDomainClaims(ctx, in, []string{path})
			if err != nil {
				return fail(err)
			}
			defer unlock()
			id, err := workspace.DiscardGitReviewedAt(ctx, workspace.NewGitService(root), store, documents, p.Preview)
			if err != nil {
				if id == "" {
					return fail(err)
				}
				// The disk owner returns a safety ID even for failed publication.
				// Keep that evidence visible instead of manufacturing a no-effect result.
				publicationErr = err
			}
			committed = true
			return response(request, coreprotocol.DiscardGitReviewedResult{SafetyID: id})
		case "history.apply":
			var p coreprotocol.HistoryApplyRequest
			if err := decode(in.Input, &p); err != nil {
				return fail(err)
			}
			plan, err := store.LoadPlan(p.PlanID)
			if err != nil {
				return fail(err)
			}
			var paths []string
			for _, choice := range p.Choices {
				path, err := controlCheckedPath(root, choice.Path, true)
				if err != nil {
					return fail(err)
				}
				found := false
				for _, change := range plan.Changes {
					if filepath.Clean(change.Path) == filepath.Clean(choice.Path) {
						found = true
						break
					}
				}
				if !found {
					return fail(errors.New("restore choice is absent from immutable preview"))
				}
				paths = append(paths, path)
			}
			unlock, err := a.controlDomainClaims(ctx, in, paths)
			if err != nil {
				return fail(err)
			}
			defer unlock()
			run := func() error {
				var err error
				if root == ws.RootPath && history != nil {
					restored, err = history.ApplyRestoreChoices(ctx, p.PlanID, p.Choices)
				} else {
					restored, err = store.ApplyRestoreChoices(ctx, p.PlanID, p.Choices)
				}
				committed = err == nil
				return err
			}
			if documents == nil {
				return fail(errors.New("document authority unavailable"))
			}
			if err := documents.WithHistoryMutation(ctx, paths, run); err != nil {
				if !committed {
					return fail(err)
				}
				// Retain known effects; owner publication must not label them no-effect.
				publicationErr = err
				return response(request, restored)
			}
			return response(request, restored)
		}
		return fail(errors.New("unknown history operation"))
	}
	apply := func(receipt coreprotocol.Response) error {
		defer finish()
		if committed && in.CommandID == "history.apply" {
			if err := json.Unmarshal(receipt.Payload, &restored); err != nil {
				return err
			}
			if restored.SessionID != "" {
				return errors.Join(publicationErr, ws.AppendHistoryRestoreEvent(restored))
			}
		}
		return publicationErr
	}
	run, publish, handled := wrapControlOperation(in, work, apply)
	return func(ctx context.Context) control.Result {
		out := run(ctx)
		if out.State != control.Committed {
			finish()
		}
		return out
	}, publish, handled
}
