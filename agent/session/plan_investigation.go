package session

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"

	"crude/agent/collaboration"
	"crude/agent/sessiondb"
	"crude/agent/worktree"
)

const planInvestigationKey = "plan_investigation"

var errPlanInvestigationNotOpen = errors.New("open a Plan investigation for this source first")

// planModeKey's nonempty value is the current cycle. A turn and its recovery
// keep the captured value when the next-turn mode changes. Older bindings
// still identify evidence and consent for that exact cycle, not the next one.
type planInvestigationBinding struct {
	Cycle    string `json:"cycle"`
	SourceID string `json:"source_id"`
	ID       string `json:"id"`
	Consent  string `json:"consent,omitempty"`
}

type planInvestigationCycleKey struct{}

type planInvestigationTurn struct{ cycle, source string }

// The operation lease is exclusive, but retention checks only observe it.
// A pin write or cleanup check must not look like an investigation operation.
type planInvestigationToolGate struct{ active atomic.Bool }

func (g *planInvestigationToolGate) TryLock() bool { return g.active.CompareAndSwap(false, true) }
func (g *planInvestigationToolGate) Unlock()       { g.active.Store(false) }
func (g *planInvestigationToolGate) Busy() bool    { return g.active.Load() }

func newPlanInvestigationCycle() string { return rand.Text() }

func (s *Session) planInvestigationCycle(ctx context.Context) (string, error) {
	value, err := sessiondb.LoadRuntimeState(ctx, s.db, planModeKey)
	return string(value), err
}

// Capture the next-turn decision and register its owner before a concurrent
// mode toggle can close the cycle. This bridges the short admission window
// before the initial durable recovery checkpoint or an exact checkout pin.
func (s *Session) capturePlanInvestigationTurn(ctx context.Context, persisted *string, sourceID, inputID string) (string, func(), error) {
	noRelease := func() {}
	if persisted != nil && *persisted == "" {
		return "", noRelease, nil // already admitted as an ordinary turn
	}
	// A reserved input or live turn already owns the session lifetime.
	// Capture under the settings lock, but do not request a new operation:
	// Close must still save inputs that it admitted before setting closing.
	s.settingsOperationMu.Lock()
	defer s.settingsOperationMu.Unlock()
	var cycle string
	if persisted != nil {
		cycle = *persisted
	} else if s.PlanMode() {
		// The exact first input committed by New session always implements
		// the approved plan, even if Plan mode changed before the queue ran.
		handoff, err := s.isPlanHandoffInput(ctx, inputID)
		if err != nil || handoff {
			return "", noRelease, err
		}
		cycle, err = s.planInvestigationCycle(ctx)
		if err != nil {
			return "", noRelease, err
		}
	}
	if cycle == "" {
		return "", noRelease, nil
	}
	turn := &planInvestigationTurn{cycle: cycle, source: sourceID}
	s.planInvestigationTurns.Store(turn, struct{}{})
	return cycle, func() { s.planInvestigationTurns.Delete(turn) }, nil
}

func (s *Session) planInvestigationTurnRetains(binding planInvestigationBinding) bool {
	retained := false
	s.planInvestigationTurns.Range(func(key, _ any) bool {
		turn := key.(*planInvestigationTurn)
		retained = turn.cycle == binding.Cycle && turn.source == binding.SourceID
		return !retained
	})
	return retained
}

func withPlanInvestigationCycle(ctx context.Context, cycle string) context.Context {
	return context.WithValue(ctx, planInvestigationCycleKey{}, cycle)
}

func planInvestigationCycleFromContext(ctx context.Context) string {
	cycle, _ := ctx.Value(planInvestigationCycleKey{}).(string)
	return cycle
}

func readPlanInvestigations(ctx context.Context, db *sql.DB) ([]planInvestigationBinding, error) {
	raw, err := sessiondb.LoadRuntimeState(ctx, db, planInvestigationKey)
	if err != nil || len(raw) == 0 {
		return nil, err
	}
	var bindings []planInvestigationBinding
	if err := strictControlJSON(raw, &bindings); err != nil {
		return nil, fmt.Errorf("read Plan investigations: %w", err)
	}
	cycles := map[[2]string]bool{}
	ids := map[string]bool{}
	for _, b := range bindings {
		key := [2]string{b.Cycle, b.SourceID}
		if b.Cycle == "" || b.ID == "" || ids[b.ID] || cycles[key] ||
			(b.Consent != "" && b.Consent != "allow" && b.Consent != "deny") {
			return nil, errors.New("invalid saved Plan investigation")
		}
		ids[b.ID], cycles[key] = true, true
	}
	return bindings, nil
}

func (s *Session) savePlanInvestigations(ctx context.Context, bindings []planInvestigationBinding) error {
	if s.db == nil {
		return errors.New("a saved session is required for Plan investigation")
	}
	raw, err := json.Marshal(bindings)
	if err != nil {
		return err
	}
	return sessiondb.SaveRuntimeState(ctx, s.db, planInvestigationKey, raw)
}

// ClosedPlanInvestigationIDs returns only bound, no-longer-current cycles.
// Run, recovery, checkout and document settlement are independent checks.
func (s *Session) ClosedPlanInvestigationIDs(ctx context.Context) ([]string, error) {
	if err := s.beginSessionControlOperation(); err != nil {
		return nil, err
	}
	defer s.endSettingsOperation()
	bindings, err := readPlanInvestigations(ctx, s.db)
	if err != nil {
		return nil, err
	}
	cycle, err := s.planInvestigationCycle(ctx)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, binding := range bindings {
		if binding.Cycle != cycle {
			ids = append(ids, binding.ID)
		}
	}
	return ids, nil
}

type planInvestigationPinner interface {
	LogicalRoot() string
	PinInvestigation(context.Context, string, worktree.Investigation, string, worktree.Owner) (worktree.Context, error)
}

func (s *Session) openPlanInvestigation(ctx context.Context, sourceID string, owner worktree.Owner) (worktree.Context, error) {
	return s.resolvePlanInvestigation(ctx, sourceID, owner, true)
}

func (s *Session) activePlanInvestigation(ctx context.Context, sourceID string, owner worktree.Owner) (worktree.Context, error) {
	return s.resolvePlanInvestigation(ctx, sourceID, owner, false)
}

func (s *Session) resolvePlanInvestigation(ctx context.Context, sourceID string, owner worktree.Owner, create bool) (worktree.Context, error) {
	if err := ctx.Err(); err != nil {
		return worktree.Context{}, err
	}
	cycle := planInvestigationCycleFromContext(ctx)
	if cycle == "" {
		return worktree.Context{}, errPlanInvestigationNotOpen
	}
	if err := s.beginSessionControlOperation(); err != nil {
		return worktree.Context{}, err
	}
	defer s.endSettingsOperation()
	if s.db == nil || s.sessionID == "" {
		return worktree.Context{}, PlanReviewError("Save the session before you open a Plan investigation.")
	}
	store := s.CollaborationStore()
	if store == nil {
		return worktree.Context{}, errors.New("an exact root run is required for Plan investigation")
	}
	participant, err := store.Agent(ctx, owner.AgentID)
	if err != nil || participant.ParentID != "" || participant.SessionID != s.sessionID || participant.CurrentRunID != owner.RunID || owner.RunID == "" {
		return worktree.Context{}, collaboration.ErrStale
	}
	run, err := store.Run(ctx, owner.RunID)
	if err != nil || run.AgentID != owner.AgentID || run.State.Terminal() {
		return worktree.Context{}, collaboration.ErrStale
	}
	service, err := s.executionService()
	if err != nil {
		return worktree.Context{}, err
	}
	pinner, ok := service.(planInvestigationPinner)
	if !ok {
		return worktree.Context{}, PlanReviewError("The Plan investigation checkout is not available.")
	}
	if pinner.LogicalRoot() != s.WorkDir() {
		return worktree.Context{}, PlanReviewError("The Plan investigation belongs to another workspace.")
	}
	bindings, err := readPlanInvestigations(ctx, s.db)
	if err != nil {
		return worktree.Context{}, err
	}
	binding := worktree.Investigation{SessionID: s.sessionID, Cycle: cycle}
	for _, b := range bindings {
		if b.Cycle == cycle && b.SourceID == sourceID {
			return pinner.PinInvestigation(ctx, b.ID, binding, sourceID, owner)
		}
	}
	if !create {
		return worktree.Context{}, errPlanInvestigationNotOpen
	}
	c, err := service.Create(ctx, worktree.Request{Owner: owner, Mode: worktree.Isolated, SourceContextID: sourceID, Investigation: binding})
	if err == nil {
		// The creator's pin already protects the checkout while ownership is
		// committed. A crash before this commit leaves a reviewable artifact,
		// but no active investigation or consent to recover from it.
		err = s.savePlanInvestigations(ctx, append(bindings, planInvestigationBinding{Cycle: cycle, SourceID: sourceID, ID: c.ID}))
	}
	if err != nil {
		if c.ID != "" {
			err = fmt.Errorf("%w (retained checkout %s; inspect it before cleanup)", err, c.ID)
		}
		return worktree.Context{}, err
	}
	c.Baseline, c.DocumentRevisions = nil, nil
	return c, nil
}

func (s *Session) investigationConsent(id string) (string, error) {
	bindings, err := readPlanInvestigations(context.Background(), s.db)
	if err != nil {
		return "", err
	}
	for _, b := range bindings {
		if b.ID == id {
			return b.Consent, nil
		}
	}
	return "", errPlanInvestigationNotOpen
}

// setInvestigationConsent records a trusted human decision for one checkout.
// The first recorded answer is final; later cycles have a new checkout.
func (s *Session) setInvestigationConsent(id, decision string) error {
	if decision != "allow" && decision != "deny" {
		return errors.New("an allow or deny decision is required for Plan investigation")
	}
	if err := s.beginSessionControlOperation(); err != nil {
		return err
	}
	defer s.endSettingsOperation()
	ctx := context.Background()
	bindings, err := readPlanInvestigations(ctx, s.db)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(bindings, func(b planInvestigationBinding) bool { return b.ID == id })
	if i < 0 {
		return errPlanInvestigationNotOpen
	}
	if bindings[i].Consent != "" {
		if bindings[i].Consent == decision {
			return nil
		}
		return errors.New("a decision has already been recorded for this Plan investigation")
	}
	bindings[i].Consent = decision
	return s.savePlanInvestigations(ctx, bindings)
}

func (b planInvestigationBinding) retains(cycle *string, sourceID string) bool {
	return cycle != nil && b.Cycle != "" && b.Cycle == *cycle && b.SourceID == sourceID
}
