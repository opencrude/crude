package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"crude/coreprotocol"
	"crude/workspace"
)

// lifetimePolicy is host-provided description and the shutdown trigger. The
// Runtime owns the facts (clients, workspaces, work); the host owns the policy.
type lifetimePolicy struct {
	mu              sync.Mutex
	server          bool
	grace           time.Duration
	started         time.Time
	shutdown        func()
	idleFrom        time.Time
	buildID         string
	ownerGeneration string
}

func (r *Runtime) buildID() string {
	r.lifetime.mu.Lock()
	defer r.lifetime.mu.Unlock()
	return r.lifetime.buildID
}

// SetBuildID records the executable identity reported by core.status.
func (r *Runtime) SetBuildID(id string) {
	r.lifetime.mu.Lock()
	r.lifetime.buildID = id
	r.lifetime.mu.Unlock()
}

// SetOwnerGeneration binds automatic handover to this daemon incarnation.
// The host installs it before accepting any connections.
func (r *Runtime) SetOwnerGeneration(generation string) {
	r.lifetime.mu.Lock()
	r.lifetime.ownerGeneration = generation
	r.lifetime.mu.Unlock()
}

// SetLifetimePolicy records the daemon mode reported by core.status and the
// callback invoked after an accepted core.shutdown. The callback must be
// asynchronous with respect to the Runtime (it normally cancels a context).
func (r *Runtime) SetLifetimePolicy(server bool, grace time.Duration, shutdown func()) {
	r.lifetime.mu.Lock()
	r.lifetime.server, r.lifetime.grace, r.lifetime.shutdown = server, grace, shutdown
	if r.lifetime.started.IsZero() {
		r.lifetime.started = time.Now()
	}
	r.lifetime.mu.Unlock()
}

// inspect runs fn on the workspace's serialized actor goroutine.
func (a *actor) inspect(ctx context.Context, fn func()) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	done := make(chan struct{})
	// Cancellation may abandon a queued inspection, but cannot let a running
	// callback write its caller's captured result after that caller returns.
	// 0: queued, 1: executing/complete, 2: cancelled before execution.
	var state atomic.Uint32
	command := actorCommand{inspect: func() {
		defer close(done)
		if state.CompareAndSwap(0, 1) {
			fn()
		}
	}}
	select {
	case a.commands <- command:
	case <-ctx.Done():
		return ctx.Err()
	case <-a.stop:
		return errors.New("workspace closed")
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		if !state.CompareAndSwap(0, 2) {
			<-done
		}
		return ctx.Err()
	case <-a.stop:
		if !state.CompareAndSwap(0, 2) {
			<-done
		}
		return errors.New("workspace closed")
	}
}

func (r *Runtime) actors() []*actor {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*actor, 0, len(r.workspaces))
	for _, a := range r.workspaces {
		out = append(out, a)
	}
	slices.SortFunc(out, func(x, y *actor) int { return strings.Compare(x.id, y.id) })
	return out
}

// Each runs fn for every retained workspace on that workspace's actor goroutine,
// the only context in which workspace state may be mutated.
func (r *Runtime) Each(ctx context.Context, fn func(*workspace.Workspace)) error {
	var errs []error
	for _, a := range r.actors() {
		if err := a.inspect(ctx, func() { fn(a.ws) }); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Workspaces returns the retained workspace pointers. Callers may use only
// methods documented as goroutine-safe (for example event-bus publication).
func (r *Runtime) Workspaces() []*workspace.Workspace {
	actors := r.actors()
	out := make([]*workspace.Workspace, len(actors))
	for i, a := range actors {
		out[i] = a.ws
	}
	return out
}

// Status reports clients, workspaces and active work. Per-workspace facts are
// read on the owning actor so they are coherent with its serialized state.
func (r *Runtime) Status(ctx context.Context) (coreprotocol.CoreStatus, error) {
	r.mu.Lock()
	status := coreprotocol.CoreStatus{PID: os.Getpid(), Clients: len(r.clients), Opening: len(r.creating), Draining: r.draining}
	r.mu.Unlock()
	status.Executable, _ = os.Executable()
	r.lifetime.mu.Lock()
	status.Server, status.GraceSeconds, status.StartedAt = r.lifetime.server, r.lifetime.grace.Seconds(), r.lifetime.started
	status.BuildID = r.lifetime.buildID
	if !r.lifetime.idleFrom.IsZero() {
		status.IdleForSeconds = time.Since(r.lifetime.idleFrom).Seconds()
	}
	r.lifetime.mu.Unlock()
	var errs []error
	for _, a := range r.actors() {
		ws := coreprotocol.WorkspaceStatus{ID: a.id}
		readCtx, cancel := context.WithTimeout(ctx, diagnosticActorTimeout)
		err := a.inspect(readCtx, func() {
			ws = coreprotocol.WorkspaceStatus{ID: a.id, Root: a.ws.RootPath, Agents: a.ws.AgentMgr.Count(), Sessions: len(a.ws.Sessions), Operations: a.inFlight, Active: a.ws.ActiveWork()}
			if a.lifecycle {
				ws.Operations++
			}
		})
		cancel()
		if err != nil {
			errs = append(errs, err)
			status.Partial = true
			ws.ObservationError = "owner observation unavailable: " + err.Error()
		}
		status.Workspaces = append(status.Workspaces, ws)
	}
	return status, errors.Join(errs...)
}

// BusyWorkspaces lists the workspaces with active work or operations.
func BusyWorkspaces(status coreprotocol.CoreStatus) []coreprotocol.WorkspaceStatus {
	var out []coreprotocol.WorkspaceStatus
	for _, w := range status.Workspaces {
		if w.Busy() {
			out = append(out, w)
		}
	}
	return out
}

// Idle evaluates the default lifetime policy once. It returns true only when
// there are no clients, no active work and that state has lasted grace. A
// workspace that cannot be inspected counts as busy: lifetime decisions fail
// toward keeping work alive.
func (r *Runtime) Idle(ctx context.Context, grace time.Duration) (bool, error) {
	status, err := r.Status(ctx)
	r.lifetime.mu.Lock()
	defer r.lifetime.mu.Unlock()
	if err != nil || status.Clients > 0 || status.Opening > 0 || len(BusyWorkspaces(status)) > 0 {
		r.lifetime.idleFrom = time.Time{}
		return false, err
	}
	if r.lifetime.idleFrom.IsZero() {
		r.lifetime.idleFrom = time.Now()
	}
	return time.Since(r.lifetime.idleFrom) >= grace, nil
}

// BeginDrain atomically refuses new connections if none are attached. After it
// returns true the runtime accepts no client, so idleness cannot be invalidated
// by a connection racing the shutdown.
func (r *Runtime) BeginDrain() bool {
	r.stopMu.Lock()
	defer r.stopMu.Unlock()
	r.mu.Lock()
	if r.closed || r.draining || r.stopping || len(r.clients) != 0 || len(r.creating) != 0 || len(r.storageGates) != 0 {
		r.mu.Unlock()
		return false
	}
	r.stopping = true
	r.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	release, err := r.reserveIdle(ctx)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopping = false
	if err != nil {
		return false
	}
	r.idleRelease = release
	r.draining = true
	return true
}

// coreMethod serves core-level requests. The requesting connection is an
// administrative query, not an attached shell, so it is excluded from counts.
func (r *Runtime) coreMethod(ctx context.Context, caller *Connection, request coreprotocol.Request) coreprotocol.Response {
	if request.Method == coreprotocol.CoreStatusMethod {
		if err := decodeRequest(request, &coreprotocol.Empty{}); err != nil {
			return fault(request, "invalid_request", err.Error())
		}
		status, err := r.Status(ctx)
		if caller != nil && status.Clients > 0 {
			status.Clients--
		}
		// Status() keeps its error for fail-closed lifetime callers. The read RPC
		// preserves healthy peers plus explicit per-owner failures instead of
		// discarding the evidence an operator needs to diagnose the stall.
		_ = err
		return response(request, status)
	}
	var input coreprotocol.ShutdownRequest
	if err := decodeRequest(request, &input); err != nil {
		return fault(request, "invalid_request", err.Error())
	}
	return response(request, r.shutdown(ctx, caller, input))
}

func (r *Runtime) shutdown(ctx context.Context, caller *Connection, input coreprotocol.ShutdownRequest) coreprotocol.ShutdownResult {
	r.stopMu.Lock()
	defer r.stopMu.Unlock()
	r.lifetime.mu.Lock()
	generation, hook := r.lifetime.ownerGeneration, r.lifetime.shutdown
	r.lifetime.mu.Unlock()
	if input.ExpectedOwner != "" && (input.Force || input.ExpectedOwner != generation) {
		return coreprotocol.ShutdownResult{Reason: "endpoint owner changed; handover refused"}
	}
	if hook == nil {
		return coreprotocol.ShutdownResult{Reason: "this core was not started as a stoppable daemon"}
	}
	r.mu.Lock()
	otherClients := len(r.clients)
	if caller != nil && r.clients[caller.Owner.ClientID] == caller {
		otherClients--
	}
	if r.closed || r.draining || r.stopping {
		r.mu.Unlock()
		return coreprotocol.ShutdownResult{Reason: "core is already closing; wait for its owner to settle"}
	}
	if !input.Force && (otherClients != 0 || len(r.creating) != 0 || len(r.storageGates) != 0) {
		reason := fmt.Sprintf("%d shell(s) are attached; close them or retry --core-stop --force", otherClients)
		if otherClients == 0 {
			reason = "workspace factory or saved-storage operation is in progress; wait for it to finish and retry"
		}
		r.mu.Unlock()
		return coreprotocol.ShutdownResult{Reason: reason, Shells: otherClients}
	}
	r.stopping = true
	r.mu.Unlock()
	var release func()
	if !input.Force {
		var err error
		release, err = r.reserveIdle(ctx)
		if err != nil {
			r.mu.Lock()
			r.stopping = false
			r.mu.Unlock()
			return coreprotocol.ShutdownResult{Reason: err.Error() + "; finish or close the named work and retry, or explicitly use --core-stop --force"}
		}
	}
	r.mu.Lock()
	if err := ctx.Err(); err != nil && !input.Force {
		r.mu.Unlock()
		release()
		r.mu.Lock()
		r.stopping = false
		r.mu.Unlock()
		return coreprotocol.ShutdownResult{Reason: "shutdown inspection cancelled; nothing stopped"}
	}
	r.idleRelease = release
	r.draining = true
	r.stopping = false
	r.mu.Unlock()
	// Delay lets this response reach the requester before connections close.
	time.AfterFunc(150*time.Millisecond, hook)
	return coreprotocol.ShutdownResult{Accepted: true}
}

// publishProgress sends request-scoped output to exactly the owning connection
// and epoch. It never blocks the worker producing it.
func (r *Runtime) publishProgress(owner coreprotocol.Owner, progress coreprotocol.Progress) {
	r.mu.Lock()
	c := r.clients[owner.ClientID]
	r.mu.Unlock()
	if c == nil || c.Owner != owner {
		return
	}
	c.reportProgress(progress.RequestID, progress.Text)
}
