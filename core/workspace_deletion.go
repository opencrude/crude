package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"crude/coreprotocol"
	"crude/fileio"
	"crude/types"
	"crude/workspace"
)

type workspaceStorageGate struct {
	ready chan struct{}
	refs  int
}

type workspaceDeletionPreview struct {
	owner   coreprotocol.Owner
	plan    workspace.WorkspaceDeletion
	expires time.Time
}

// Saved IDs and actor IDs are intentionally different. This gate uses the
// former, because that is the storage identity shared by attach/delete/Undo.
// No I/O or actor join runs with the runtime registry mutex held.
func (r *Runtime) lockWorkspaceStorage(ctx context.Context, id string) (func(), error) {
	r.mu.Lock()
	if r.closed || r.draining || r.stopping {
		r.mu.Unlock()
		return nil, errors.New("core is closing")
	}
	if r.storageGates == nil {
		r.storageGates = make(map[string]*workspaceStorageGate)
	}
	g := r.storageGates[id]
	if g == nil {
		g = &workspaceStorageGate{ready: make(chan struct{}, 1)}
		g.ready <- struct{}{}
		r.storageGates[id] = g
	}
	g.refs++
	r.mu.Unlock()
	drop := func() {
		r.mu.Lock()
		g.refs--
		if g.refs == 0 {
			delete(r.storageGates, id)
		}
		r.mu.Unlock()
	}
	select {
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	case <-g.ready:
		return func() { g.ready <- struct{}{}; drop() }, nil
	}
}

func workspaceDeletionMethod(method string) bool {
	return method == coreprotocol.WorkspaceDeletePreview || method == coreprotocol.WorkspaceDelete || method == coreprotocol.WorkspaceRestore
}

// Filesystem work and full resource teardown never block an actor or the wire
// reader. The runtime joins these workers before closing its receipt journal.
func (c *Connection) dispatchWorkspaceDeletion(ctx context.Context, request coreprotocol.Request) <-chan coreprotocol.Response {
	ready := make(chan coreprotocol.Response, 1)
	r := c.runtime
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		ready <- fault(request, "closed", "core is closing")
		return ready
	}
	r.factoryWG.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.factoryWG.Done()
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		stop := context.AfterFunc(c.ctx, cancel)
		defer cancel()
		defer stop()
		if request.Method == coreprotocol.WorkspaceDeletePreview {
			ready <- c.workspaceDeletionCall(ctx, request)
			return
		}
		prior, fresh, err := r.receipts.begin(ctx, request)
		if err != nil {
			ready <- fault(request, "receipt_failed", err.Error())
			return
		}
		if !fresh {
			if prior.State == "completed" {
				ready <- prior.Response
			} else {
				ready <- fault(request, "outcome_"+prior.State, "Observe the original deletion receipt; do not repeat this operation.")
			}
			return
		}
		out := c.workspaceDeletionCall(ctx, request)
		if err := r.receipts.complete(context.WithoutCancel(ctx), request, out); err != nil {
			out = fault(request, "outcome_uncertain", "Workspace operation may have completed. Inspect its original receipt before trying again: "+err.Error())
		}
		ready <- out
	}()
	return ready
}

func deletionIssue(summary, detail, guidance string) *types.OperationIssue {
	return &types.OperationIssue{Title: "Workspace is in use", Summary: summary, Detail: detail, Guidance: guidance}
}

// retireSavedWorkspace runs under the saved-ID gate. Opening attempts are
// excluded, existing activity is refused, and idle session admissions are fenced
// before the decision. Dirty accepted documents remain durable saved data.
func (r *Runtime) retireSavedWorkspace(ctx context.Context, id string) (*types.OperationIssue, error) {
	r.mu.Lock()
	// A timed-out attach may have left a factory worker running without a client.
	for _, creation := range r.creating {
		if creation.savedID == id {
			r.mu.Unlock()
			return deletionIssue("This workspace is still opening.", "The core is preparing its saved resources.", "Wait for it to finish opening, switch away, then review deletion again."), nil
		}
	}
	var target *actor
	for _, a := range r.workspaces {
		if workspace.WorkspaceID(a.ws.RootPath) == id {
			target = a
			break
		}
	}
	r.mu.Unlock()
	if target == nil {
		return nil, ctx.Err()
	}
	type retirement struct {
		issue   *types.OperationIssue
		release func()
	}
	decision := make(chan retirement, 1)
	command := actorCommand{retire: func() (retired bool) {
		var out retirement
		defer func() { decision <- out }()
		if ctx.Err() != nil {
			return false
		}
		// Unsubscribe fences services before queueing its actor-owned detach.
		// Keep even a closing connection counted until that detach is reduced;
		// otherwise shutdown could race its final document/terminal releases.
		views := len(target.subscribers)
		if views > 0 {
			out.issue = deletionIssue(fmt.Sprintf("This workspace is open in %d view(s).", views), "Deleting it would disconnect those views.", "Switch those views to another workspace or close them, then review deletion again.")
			return false
		}
		if target.inFlight > 0 || len(target.settingsQueue) > 0 || len(target.controlQueue) > 0 {
			out.issue = deletionIssue("Workspace operations are still finishing.", fmt.Sprintf("%d operation(s) are in progress.", target.inFlight), "Wait for those operations to finish, then review deletion again.")
			return false
		}
		active := target.ws.ActiveWork()
		if active.Any() {
			out.issue = deletionIssue("This workspace still has background work.", fmt.Sprintf("%d agent turn(s), %d child agent(s), %d command(s), %d compaction(s), %d memory job(s), %d goal(s), %d loop(s).", active.Turns, active.Children, active.AsyncCommands, active.Compaction, active.MemoryJobs, active.Goals, active.Loops), "Open this workspace to finish or stop its work, switch away, then review deletion again. Nothing was stopped.")
			return false
		}
		if target.ws.TermMgr != nil {
			for _, terminal := range target.ws.TermMgr.List() {
				if terminal.Alive {
					out.issue = deletionIssue("A terminal is still running in this workspace.", terminal.Title, "Open this workspace and close its running terminals, then switch away and review deletion again. No terminal was stopped.")
					return false
				}
			}
		}
		var leaseErr error
		out.release, leaseErr = target.ws.BeginHistoryRestoreLease(ctx)
		if leaseErr != nil {
			out.issue = deletionIssue("An agent turn is active or pending.", "The workspace could not be reserved for safe deletion.", "Open it to finish or stop pending work, then switch away and review deletion again. Nothing was stopped.")
			return false
		}
		if target.ws.ActiveWork().Any() {
			out.release()
			out.release = nil
			out.issue = deletionIssue("Background work started while checking this workspace.", "The deletion was not admitted.", "Let the work finish, then review deletion again. Nothing was stopped.")
			return false
		}
		target.retiring = true
		return true
	}}
	select {
	case target.commands <- command:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-target.stop:
		return nil, errors.New("workspace is closing")
	}
	// An admitted decision has an exact completion obligation. Do not abandon
	// it on cancellation after it might have fenced session admissions.
	out := <-decision
	if out.release == nil {
		return out.issue, ctx.Err()
	}
	defer out.release()
	// The actor is quiescent. Preserve full flush/join ordering, including all
	// SQLite owners, before hashing the immutable preview or moving its directory.
	target.close()
	r.mu.Lock()
	if r.workspaces[target.id] == target {
		delete(r.workspaces, target.id)
	}
	r.mu.Unlock()
	return nil, ctx.Err()
}

func (c *Connection) workspaceDeletionCall(ctx context.Context, request coreprotocol.Request) coreprotocol.Response {
	c.mu.Lock()
	current := c.attached[request.WorkspaceID]
	c.mu.Unlock()
	if current == nil || c.ctx.Err() != nil {
		return fault(request, "not_attached", "The requesting workspace view is no longer attached.")
	}
	r := c.runtime
	var id, project string
	var plan workspace.WorkspaceDeletion
	var deleted workspace.DeletedWorkspace
	switch request.Method {
	case coreprotocol.WorkspaceDeletePreview:
		var in coreprotocol.WorkspaceDeletePreviewRequest
		if err := decodeRequest(request, &in); err != nil {
			return fault(request, "invalid_request", err.Error())
		}
		id, project = in.ID, in.ProjectDir
	case coreprotocol.WorkspaceDelete:
		var in coreprotocol.WorkspaceDeleteRequest
		if err := decodeRequest(request, &in); err != nil {
			return fault(request, "invalid_request", err.Error())
		}
		r.mu.Lock()
		preview, ok := r.deletionPreviews[in.Token]
		if ok && preview.owner == c.Owner {
			delete(r.deletionPreviews, in.Token)
		}
		r.mu.Unlock()
		if !ok || preview.owner != c.Owner || time.Now().After(preview.expires) {
			return fault(request, "preview_expired", "This deletion preview expired or was already used. Review deletion again; no saved data was moved.")
		}
		plan = preview.plan
		id, project = plan.ID, plan.ProjectDir
	case coreprotocol.WorkspaceRestore:
		if err := decodeRequest(request, &deleted); err != nil {
			return fault(request, "invalid_request", err.Error())
		}
		id, project = deleted.ID, deleted.ProjectDir
	}
	if id == "" || id != workspace.WorkspaceID(project) {
		return fault(request, "invalid_request", "Workspace identity does not match the selected project.")
	}
	if id == workspace.WorkspaceID(current.ws.RootPath) {
		return fault(request, "workspace_in_use", "This is the current workspace. Switch to another workspace before deleting its saved data.")
	}
	release, err := r.lockWorkspaceStorage(ctx, id)
	if err != nil {
		return fault(request, "workspace_busy", "Workspace storage could not be reserved: "+err.Error())
	}
	defer release()
	if request.Method == coreprotocol.WorkspaceRestore {
		// Undo must not shut down a replacement workspace merely to discover
		// that its directory exists. Preserve both the replacement and recovery.
		r.mu.Lock()
		replacement := false
		for _, a := range r.workspaces {
			replacement = replacement || workspace.WorkspaceID(a.ws.RootPath) == id
		}
		for _, opening := range r.creating {
			replacement = replacement || opening.savedID == id
		}
		r.mu.Unlock()
		if replacement {
			return fault(request, "workspace_replaced", "A replacement workspace exists or is opening for this project. Undo will not overwrite or close it. Recovery data was preserved at "+deleted.RecoveryPath)
		}
		err = workspace.RestoreDeletedWorkspace(ctx, deleted)
		return workspaceDeletionResponse(request, deleted, err)
	}
	issue, err := r.retireSavedWorkspace(ctx, id)
	if err != nil {
		return fault(request, "workspace_unavailable", "Workspace resources could not be released safely: "+err.Error())
	}
	if issue != nil {
		if request.Method == coreprotocol.WorkspaceDeletePreview {
			return response(request, workspace.WorkspaceDeletion{ID: id, ProjectDir: project, Issue: issue})
		}
		return fault(request, "workspace_in_use", strings.Join([]string{issue.Summary, issue.Detail, issue.Guidance}, "\n"))
	}
	if request.Method == coreprotocol.WorkspaceDeletePreview {
		plan, err := workspace.PreviewWorkspaceDeletion(ctx, id, project, current.ws.RootPath)
		if err != nil {
			return fault(request, "preview_failed", "Saved workspace could not be reviewed: "+err.Error())
		}
		plan.Token = NewClientID()
		r.mu.Lock()
		if ctx.Err() != nil || c.ctx.Err() != nil {
			r.mu.Unlock()
			return fault(request, "cancelled", "Deletion preview was cancelled; saved data is unchanged.")
		}
		if r.deletionPreviews == nil {
			r.deletionPreviews = make(map[string]workspaceDeletionPreview)
		}
		for token, prior := range r.deletionPreviews {
			if prior.owner == c.Owner || time.Now().After(prior.expires) {
				delete(r.deletionPreviews, token)
			}
		}
		r.deletionPreviews[plan.Token] = workspaceDeletionPreview{owner: c.Owner, plan: plan, expires: time.Now().Add(10 * time.Minute)}
		r.mu.Unlock()
		return response(request, plan)
	}
	deleted, err = workspace.DeleteSavedWorkspace(ctx, plan)
	return workspaceDeletionResponse(request, deleted, err)
}

func workspaceDeletionResponse(request coreprotocol.Request, deleted workspace.DeletedWorkspace, err error) coreprotocol.Response {
	if err != nil && !fileio.WriteCommitted(err) {
		return fault(request, "workspace_storage_failed", "Saved workspace operation did not complete. Project files were not changed.\n"+err.Error()+"\nIf another program holds the saved data open, close that program and review the operation again. Recovery data is preserved.")
	}
	out := coreprotocol.WorkspaceDeletionResult{Deleted: deleted}
	if err != nil {
		out.Warning = err.Error()
	}
	return response(request, out)
}
