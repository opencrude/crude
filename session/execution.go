package session

import (
	"context"
	"crude/agent"
	"crude/collaboration"
	"crude/provider"
	"crude/safety"
	"crude/sessiondb"
	"crude/tools"
	captypes "crude/types/capability"
	"crude/types/control"
	"crude/worktree"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
)

// ExecutionService is the workspace-owned execution and integration authority.
// A session borrows it; logical workspace resources never move to a checkout.
type ExecutionService interface {
	Create(context.Context, worktree.Request) (worktree.Context, error)
	List() []worktree.Context
	Inspect(string) (worktree.Context, error)
	Review(context.Context, string) (worktree.PlanDelta, error)
	Apply(context.Context, worktree.PlanDelta) (worktree.Receipt, error)
	Pin(string, worktree.Owner) error
	Unpin(string, worktree.Owner) error
	Keep(string, worktree.Owner) error
	Cleanup(context.Context, string, worktree.Owner) error
}

func (s *Session) SetExecutionService(service ExecutionService) {
	s.mu.Lock()
	s.execution = service
	s.mu.Unlock()
}

type ExecutionSelection struct {
	ID       string `json:"id"`
	Revision uint64 `json:"revision"`
}

const executionSelectionKey = "execution_selection"

// ExecutionSelection is a detached next-input preference. It never changes a
// live run, queued input, another session, or the logical workspace root.
func (s *Session) ExecutionSelection() ExecutionSelection {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ExecutionSelection{ID: s.executionSelection, Revision: s.executionRevision}
}

func (s *Session) loadExecutionSelection() error {
	raw, err := sessiondb.LoadRuntimeState(context.Background(), s.db, executionSelectionKey)
	if err != nil || len(raw) == 0 {
		return err
	}
	var selection ExecutionSelection
	if err := strictControlJSON(raw, &selection); err != nil || selection.Revision == 0 {
		return errors.New("invalid saved execution selection; align current local data manually")
	}
	s.executionSelection, s.executionRevision = selection.ID, selection.Revision
	return nil
}

// SelectExecution runs off the actor/UI and persists before acknowledging. The
// admission mutex is also the immutable input capture boundary.
func (s *Session) SelectExecution(ctx context.Context, id string, revision uint64) (ExecutionSelection, error) {
	s.admissionMu.Lock()
	defer s.admissionMu.Unlock()
	if ctx.Err() != nil || atomic.LoadInt32(&s.closing) != 0 {
		return ExecutionSelection{}, errors.New("session execution selection is cancelled or closing")
	}
	if id != "" {
		service, err := s.executionService()
		if err != nil {
			return ExecutionSelection{}, err
		}
		c, err := service.Inspect(id)
		if err != nil || !c.Ready || c.Removed || c.LogicalRoot != s.WorkDir() {
			return ExecutionSelection{}, errors.New("execution selection is unavailable or foreign")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.executionRevision != revision {
		return ExecutionSelection{}, collaboration.ErrStale
	}
	selection := ExecutionSelection{ID: id, Revision: revision}
	if id == s.executionSelection {
		return selection, nil
	}
	selection.Revision++
	raw, err := json.Marshal(selection)
	if err != nil {
		return ExecutionSelection{}, err
	}
	if s.db == nil {
		return ExecutionSelection{}, errors.New("session execution selection storage unavailable")
	}
	if err := sessiondb.SaveRuntimeState(ctx, s.db, executionSelectionKey, raw); err != nil {
		return ExecutionSelection{}, err
	}
	s.executionSelection, s.executionRevision = id, selection.Revision
	return selection, nil
}

func (s *Session) bindInputExecution(input provider.UserInput) provider.UserInput {
	input = cloneUserInput(input)
	if input.ControlTarget == nil {
		selected := s.ExecutionSelection().ID
		if selected == "" {
			return input
		}
		input.ControlTarget = &control.Target{ExecutionID: selected}
	}
	input.ControlTarget.SessionID = s.sessionID
	return input
}

func (s *Session) executionService() (ExecutionService, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.execution == nil {
		return nil, errors.New("workspace execution service unavailable")
	}
	return s.execution, nil
}

func (s *Session) CreateExecution(ctx context.Context, request worktree.Request) (worktree.Context, error) {
	e, err := s.executionService()
	if err != nil {
		return worktree.Context{}, err
	}
	return e.Create(ctx, request)
}
func (s *Session) ListExecutions() ([]worktree.Context, error) {
	e, err := s.executionService()
	if err != nil {
		return nil, err
	}
	return e.List(), nil
}
func (s *Session) InspectExecution(id string) (worktree.Context, error) {
	e, err := s.executionService()
	if err != nil {
		return worktree.Context{}, err
	}
	return e.Inspect(id)
}
func (s *Session) ReviewExecution(ctx context.Context, id string) (worktree.PlanDelta, error) {
	e, err := s.executionService()
	if err != nil {
		return worktree.PlanDelta{}, err
	}
	return e.Review(ctx, id)
}
func (s *Session) ApplyExecution(ctx context.Context, plan worktree.PlanDelta) (worktree.Receipt, error) {
	e, err := s.executionService()
	if err != nil {
		return worktree.Receipt{}, err
	}
	return e.Apply(ctx, plan)
}
func (s *Session) KeepExecution(id string, owner worktree.Owner) error {
	e, err := s.executionService()
	if err != nil {
		return err
	}
	return e.Keep(id, owner)
}
func (s *Session) CleanupExecution(ctx context.Context, id string, owner worktree.Owner) error {
	e, err := s.executionService()
	if err != nil {
		return err
	}
	return e.Cleanup(ctx, id, owner)
}

func childExecutionRoot(spec agent.SubAgentSpec, logical string) string {
	if spec.ExecutionRoot != "" {
		return spec.ExecutionRoot
	}
	return logical
}

func (r *SubAgentRunner) prepareChildExecution(ctx context.Context, spec *agent.SubAgentSpec, runID string) error {
	mode := worktree.Mode(spec.Isolation)
	if mode == "" {
		mode = worktree.Auto
	}
	if mode != worktree.Auto && mode != worktree.Shared && mode != worktree.Isolated {
		return errors.New("isolation must be auto, shared, or worktree")
	}
	root := r.session.WorkDir()
	sourceID := ""
	if spec.ControlTarget != nil {
		sourceID = spec.ControlTarget.ExecutionID
	}
	service, err := r.session.executionService()
	if err != nil {
		// Standalone sessions may explicitly share. Never silently run an
		// explicitly isolated assignment against the logical checkout.
		if mode != worktree.Shared || sourceID != "" {
			return err
		}
		spec.Isolation, spec.ExecutionRoot = string(worktree.Shared), root
	} else {
		independent := childIndependentWriter(*spec)
		execution, err := service.Create(ctx, worktree.Request{Owner: worktree.Owner{AgentID: string(spec.ID), RunID: runID}, Mode: mode, Independent: independent, SourceContextID: sourceID})
		if err != nil {
			return err
		}
		spec.Isolation, spec.ExecutionID, spec.ExecutionRoot, spec.ExecutionRunID = string(execution.Mode), execution.ID, execution.Root, runID
	}
	if spec.ControlTarget != nil {
		target := *spec.ControlTarget
		target.ExecutionID = spec.ExecutionID
		spec.ControlTarget = &target
	}
	for _, path := range spec.FileOwnership {
		if filepath.IsAbs(path) && spec.Isolation == string(worktree.Isolated) {
			rel, err := filepath.Rel(root, path)
			if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return fmt.Errorf("absolute logical-checkout ownership %q cannot be silently rebased; use a relative checkout claim or explicit shared isolation", path)
			}
		}
	}
	paths, err := validateOwnershipPaths(spec.ExecutionRoot, spec.FileOwnership)
	if err != nil {
		return err
	}
	spec.FileOwnership = paths
	if spec.ExecutionRoot != root {
		return validateExecutionWriteTargets(spec.ExecutionRoot, "", paths)
	}
	return nil
}

// Managed checkouts are native mutation boundaries, independently of optional
// ownership scopes. Absolute operands never rebase into a different checkout.
// Commands and external MCP effects are opaque and are not sandboxed here.
func validateExecutionWriteTargets(root, privateDir string, targets []string) error {
	for _, target := range targets {
		allowedRoot := root
		if privateDir != "" && pathInside(privateDir, target) {
			allowedRoot = privateDir
		}
		if !pathInside(allowedRoot, target) {
			return fmt.Errorf("write to %q is outside the execution checkout %q; use a checkout-relative path or explicit shared execution. Nothing was changed", target, root)
		}
		// Check existing ancestors as well as lexical spelling, including targets
		// that do not exist yet. A symlink inside a checkout is not an escape hatch.
		resolvedRoot, err := resolveExecutionPath(allowedRoot)
		if err != nil {
			return fmt.Errorf("resolve execution checkout %q: %w", allowedRoot, err)
		}
		resolvedTarget, err := resolveExecutionPath(target)
		if err != nil {
			return fmt.Errorf("resolve execution checkout target %q: %w", target, err)
		}
		if !pathInside(resolvedRoot, resolvedTarget) {
			return fmt.Errorf("write to %q resolves outside the execution checkout %q. Nothing was changed", target, root)
		}
	}
	return nil
}

func resolveExecutionPath(path string) (string, error) {
	path = normalizeOwnershipTarget(path)
	if _, err := os.Lstat(path); err == nil {
		return filepath.EvalSymlinks(path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	parent := filepath.Dir(path)
	if parent == path {
		return "", fmt.Errorf("cannot resolve execution path %q", path)
	}
	resolved, err := resolveExecutionPath(parent)
	return filepath.Join(resolved, filepath.Base(path)), err
}

func childIndependentWriter(spec agent.SubAgentSpec) bool {
	if spec.ParentID == "" || !spec.Policy.AllowedCapabilities[safety.CanCreate] && !spec.Policy.AllowedCapabilities[safety.CanUpdate] && !spec.Policy.AllowedCapabilities[safety.CanDelete] {
		return false
	}
	for _, def := range agent.FilterToolsForModel(tools.AgentTools, spec.AllowedTools, spec.DeniedTools, spec.Category, true, spec.Model) {
		if isWriteTool(def.Function.Name) && agent.PolicyAllows(spec.Policy, def.Function.Name) {
			return true
		}
	}
	return false
}

// releaseExecutionPin requires exact terminal evidence for the run that acquired
// the pin. Inventory's CurrentRunID is not proof that an old operation settled.
func (r *SubAgentRunner) releaseExecutionPin(spec agent.SubAgentSpec, handle *agent.SubAgentHandle) {
	if spec.ExecutionID == "" || spec.ExecutionRunID == "" {
		return
	}
	store := r.session.CollaborationStore()
	if store == nil {
		return
	}
	run, err := store.Run(context.Background(), spec.ExecutionRunID)
	if err != nil || run.AgentID != string(spec.ID) || !run.State.Terminal() {
		return
	}
	if handle != nil && handle.Agent != nil && handle.Agent.AsyncMgr != nil {
		handle.Agent.AsyncMgr.CloseAndWait()
	}
	if service, err := r.session.executionService(); err == nil {
		if err := service.Unpin(spec.ExecutionID, worktree.Owner{AgentID: string(spec.ID), RunID: run.ID}); err != nil {
			r.session.logger.Warn("EXECUTION", "unpin_failed", "run_id", run.ID, "error", err.Error())
		}
	}
}

// BindRootExecution binds an existing exact artifact to the already reserved
// durable root run. Call before guidance, tools or immutable input preparation.
func (s *Session) BindRootExecution(ctx context.Context, live *agent.Agent, id, runID string) error {
	if live == nil || live.AgentID == "" || runID == "" {
		return errors.New("execution binding requires an exact agent and durable run")
	}
	root, err := s.pinRootExecution(ctx, id, worktree.Owner{AgentID: string(live.AgentID), RunID: runID})
	if err == nil {
		live.WorkDir = root
	}
	return err
}

func (s *Session) pinRootExecution(ctx context.Context, id string, owner worktree.Owner) (string, error) {
	service, err := s.executionService()
	if err != nil {
		return "", err
	}
	store := s.CollaborationStore()
	if store == nil {
		return "", errors.New("execution binding requires collaboration runtime")
	}
	run, err := store.Run(ctx, owner.RunID)
	if err != nil || run.AgentID != owner.AgentID || run.State.Terminal() {
		return "", collaboration.ErrStale
	}
	execution, err := service.Inspect(id)
	if err != nil {
		return "", err
	}
	if !execution.Ready || execution.Removed || execution.LogicalRoot != s.WorkDir() || !filepath.IsAbs(execution.Root) {
		return "", errors.New("execution artifact is unavailable or belongs to another workspace")
	}
	if err := service.Pin(id, owner); err != nil {
		return "", err
	}
	return execution.Root, nil
}

// ReleaseExecution releases only a pin whose exact durable run is terminal.
// Recovery and root settlement use this explicitly; passive inspection does not.
func (s *Session) ReleaseExecution(ctx context.Context, id string, owner worktree.Owner) error {
	store := s.CollaborationStore()
	if store == nil {
		return errors.New("execution release requires durable terminal run evidence")
	}
	run, err := store.Run(ctx, owner.RunID)
	if err != nil {
		return err
	}
	if run.AgentID != owner.AgentID || !run.State.Terminal() {
		return collaboration.ErrStale
	}
	service, err := s.executionService()
	if err != nil {
		return err
	}
	return service.Unpin(id, owner)
}

// A root can finish while an owned command remains alive. Conservatively retain
// its artifact until every session command has settled; natural completion never
// kills background work just to release disk ownership. Explicit shutdown joins
// commands first. This also reconciles persisted pins without rerunning effects.
func (s *Session) reconcileRootExecutionPins() {
	if s.asyncMgr != nil && s.asyncMgr.Running() {
		return
	}
	service, err := s.executionService()
	store := s.CollaborationStore()
	if err != nil || store == nil {
		return
	}
	for _, c := range service.List() {
		for _, pin := range c.Pins {
			participant, err := store.Agent(context.Background(), pin.AgentID)
			if err != nil || participant.SessionID != s.sessionID || participant.ParentID != "" {
				continue
			}
			run, err := store.Run(context.Background(), pin.RunID)
			if err == nil && run.AgentID == pin.AgentID && run.State.Terminal() {
				if err := service.Unpin(c.ID, pin); err != nil && s.logger != nil {
					s.logger.Warn("EXECUTION", "root_unpin_failed", "execution_id", c.ID, "run_id", pin.RunID, "error", err.Error())
				}
			}
		}
	}
}

// Revocation uses the same exact shell/workspace task identity as acquisition.
func (r *SubAgentRunner) revokeChildCapabilityRun(handle *agent.SubAgentHandle, runID string) {
	actor := captypes.RuntimeActor{ID: string(handle.Spec.ID), SessionID: handle.SessionID, RunID: runID, RuntimeEpoch: handle.RuntimeEpoch, TurnGeneration: handle.Agent.TurnGeneration, ExecutionRoot: handle.Agent.WorkDir}
	if target := handle.Spec.ControlTarget; target != nil {
		actor.WorkspaceID = target.WorkspaceID
		if target.Shell != nil {
			owner := *target.Shell
			actor.ShellOwner = &owner
		}
	}
	r.session.revokeCapabilityRun(actor)
}
