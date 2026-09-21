package plan

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotFound is returned when a plan does not exist.
var ErrNotFound = errors.New("plan: not found")

// Store is the session-scoped transactional plan store. One Store wraps one
// *sql.DB (a session database) plus that session's ID. All mutations commit in
// a single transaction: revision check, validation, plan/step/event writes,
// revision increment, and commit.
type Store struct {
	db        *sql.DB
	sessionID string
}

// NewStore creates a Store backed by db scoped to sessionID.
func NewStore(db *sql.DB, sessionID string) *Store {
	return &Store{db: db, sessionID: sessionID}
}

// DB exposes the underlying handle for migration landing that must run
// alongside the session's own schema.
func (s *Store) DB() *sql.DB { return s.db }

// Update validates and atomically commits a plan update. Creation requires a
// complete objective + ordered steps snapshot. Existing plans accept omitted
// objective/status/steps as "unchanged"; supplied steps remain a complete
// authoritative ordered snapshot.
//
// Creation is detected by the plan not yet existing for the supplied PlanID
// (the model always supplies a stable plan_id). A new plan is assigned
// revision 1 and inserts plan + steps + a plan_created event. An existing plan
// requires the request revision to match the persisted revision, then
// validates, rewrites steps, appends a plan_updated event, and bumps revision.
// Any validation failure returns the error before any write, so the request is
// all-or-nothing.
func (s *Store) Update(ctx context.Context, req UpdateRequest) (Plan, ChangeSet, error) {
	if s.db == nil {
		return Plan{}, ChangeSet{}, fmt.Errorf("plan store: nil db")
	}
	if req.PlanID == "" {
		return Plan{}, ChangeSet{}, fmt.Errorf("plan update: plan_id is required")
	}
	existing, err := s.Get(ctx, req.PlanID)
	if errors.Is(err, ErrNotFound) {
		return s.create(ctx, req)
	}
	if err != nil {
		return Plan{}, ChangeSet{}, err
	}
	req = mergeExistingPlanFields(existing, req)
	return s.updateExisting(ctx, req, existing)
}

func mergeExistingPlanFields(existing Plan, req UpdateRequest) UpdateRequest {
	if strings.TrimSpace(req.Objective) == "" {
		req.Objective = existing.Objective
	}
	if req.Status == "" {
		req.Status = existing.Status
	}
	if req.Steps == nil {
		req.Steps = cloneSteps(existing.Steps)
	}
	return req
}

func cloneSteps(steps []PlanStep) []PlanStep {
	copy := make([]PlanStep, len(steps))
	for i := range steps {
		copy[i] = steps[i]
		copy[i].DependsOn = append([]string(nil), steps[i].DependsOn...)
	}
	return copy
}

func (s *Store) create(ctx context.Context, req UpdateRequest) (Plan, ChangeSet, error) {
	if req.Status == "" {
		req.Status = StatusActive
	}
	if err := Validate(nil, req); err != nil {
		return Plan{}, ChangeSet{}, err
	}

	id := req.PlanID
	now := time.Now().UTC()
	planRow := Plan{
		ID:          id,
		Revision:    1,
		Objective:   strings.TrimSpace(req.Objective),
		Status:      req.Status,
		Explanation: req.Explanation,
		Steps:       applyPlanLifecycle(req.Status, req.Explanation, normalizeSteps(req.Steps, now, true), now),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if req.Status == StatusCompleted || req.Status == StatusCancelled {
		planRow.ClosedAt = &now
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Plan{}, ChangeSet{}, fmt.Errorf("plan create: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if req.Status == StatusActive || req.Status == StatusPaused {
		var activeID string
		err := tx.QueryRowContext(ctx,
			`SELECT id FROM plans WHERE session_id = ? AND status IN ('active','paused') LIMIT 1`, s.sessionID).Scan(&activeID)
		switch {
		case err == nil:
			return Plan{}, ChangeSet{}, fmt.Errorf("plan %q is already active; complete or cancel it before creating another", activeID)
		case !errors.Is(err, sql.ErrNoRows):
			return Plan{}, ChangeSet{}, fmt.Errorf("plan create: check active: %w", err)
		}
	}

	if err := writePlanRow(ctx, tx, s.sessionID, planRow); err != nil {
		return Plan{}, ChangeSet{}, err
	}
	if err := writeSteps(ctx, tx, planRow.ID, planRow.Steps); err != nil {
		return Plan{}, ChangeSet{}, err
	}
	if err := writeEvent(ctx, tx, planRow.ID, planRow.Revision, "plan_created", planRow); err != nil {
		return Plan{}, ChangeSet{}, err
	}
	if err := tx.Commit(); err != nil {
		return Plan{}, ChangeSet{}, fmt.Errorf("plan create: commit: %w", err)
	}
	return planRow, ChangeSet{PlanID: planRow.ID, Revision: planRow.Revision}, nil
}

func (s *Store) updateExisting(ctx context.Context, req UpdateRequest, existing Plan) (Plan, ChangeSet, error) {
	// Validation mutates nothing; run it before opening the write transaction
	// so an invalid request is rejected without locking the DB.
	if err := Validate(&existing, req); err != nil {
		return existing, ChangeSet{PlanID: existing.ID, Revision: existing.Revision}, err
	}

	now := time.Now().UTC()
	planRow := Plan{
		ID:          existing.ID,
		Revision:    existing.Revision + 1,
		Objective:   strings.TrimSpace(req.Objective),
		Status:      req.Status,
		Explanation: req.Explanation,
		Steps:       applyPlanLifecycle(req.Status, req.Explanation, updateSteps(existing, req.Steps, now), now),
		CreatedAt:   existing.CreatedAt,
		UpdatedAt:   now,
		ClosedAt:    existing.ClosedAt,
	}
	if req.Status == StatusCompleted || req.Status == StatusCancelled {
		planRow.ClosedAt = &now
	} else {
		planRow.ClosedAt = nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Plan{}, ChangeSet{}, fmt.Errorf("plan update: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	updated, err := updatePlanRowCAS(ctx, tx, s.sessionID, planRow, existing.Revision)
	if err != nil {
		return Plan{}, ChangeSet{}, err
	}
	if !updated {
		_ = tx.Rollback()
		current, getErr := s.Get(ctx, req.PlanID)
		if getErr != nil {
			return Plan{}, ChangeSet{}, getErr
		}
		return current, ChangeSet{PlanID: current.ID, Revision: current.Revision}, &RevisionConflictError{
			Current: current, Requested: req.Revision,
		}
	}
	if err := replaceSteps(ctx, tx, planRow.ID, planRow.Steps); err != nil {
		return Plan{}, ChangeSet{}, err
	}
	if err := writeEvent(ctx, tx, planRow.ID, planRow.Revision, planEventType(existing, planRow), planRow); err != nil {
		return Plan{}, ChangeSet{}, err
	}
	if err := tx.Commit(); err != nil {
		return Plan{}, ChangeSet{}, fmt.Errorf("plan update: commit: %w", err)
	}
	return planRow, ChangeSet{PlanID: planRow.ID, Revision: planRow.Revision}, nil
}

func planEventType(previous, current Plan) string {
	if previous.Status != current.Status {
		switch current.Status {
		case StatusPaused:
			return "plan_paused"
		case StatusCompleted:
			return "plan_completed"
		case StatusCancelled:
			return "plan_cancelled"
		case StatusActive:
			if previous.Status == StatusPaused || previous.Status == StatusCompleted || previous.Status == StatusCancelled {
				return "plan_resumed"
			}
		}
	}
	return "plan_updated"
}

func (s *Store) Get(ctx context.Context, id string) (Plan, error) {
	if s.db == nil {
		return Plan{}, fmt.Errorf("plan store: nil db")
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT id, objective, status, revision, COALESCE(explanation,''), created_at, updated_at, COALESCE(closed_at, 0)
		 FROM plans WHERE id = ? AND session_id = ?`, id, s.sessionID)
	var p Plan
	var created, updated, closed int64
	if err := row.Scan(&p.ID, &p.Objective, &p.Status, &p.Revision, &p.Explanation, &created, &updated, &closed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Plan{}, fmt.Errorf("%w: %q", ErrNotFound, id)
		}
		return Plan{}, fmt.Errorf("plan get: %w", err)
	}
	p.CreatedAt = time.Unix(0, created).UTC()
	p.UpdatedAt = time.Unix(0, updated).UTC()
	if closed > 0 {
		ts := time.Unix(0, closed).UTC()
		p.ClosedAt = &ts
	}
	steps, err := s.loadSteps(ctx, id)
	if err != nil {
		return Plan{}, err
	}
	p.Steps = steps
	return p, nil
}

func (s *Store) loadSteps(ctx context.Context, id string) ([]PlanStep, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT step_id, description, status, COALESCE(depends_on_json,''), COALESCE(reason,''), attempts, created_at, COALESCE(started_at,0), COALESCE(finished_at,0)
		 FROM plan_steps WHERE plan_id = ? ORDER BY position`, id)
	if err != nil {
		return nil, fmt.Errorf("plan steps: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var steps []PlanStep
	for rows.Next() {
		var st PlanStep
		var deps, reason string
		var attempts int
		var created, started, finished int64
		if err := rows.Scan(&st.ID, &st.Description, &st.Status, &deps, &reason, &attempts, &created, &started, &finished); err != nil {
			return nil, fmt.Errorf("plan step scan: %w", err)
		}
		st.DependsOn = decodeDeps(deps)
		st.Reason = reason
		st.Attempts = attempts
		st.CreatedAt = time.Unix(0, created).UTC()
		if started > 0 {
			ts := time.Unix(0, started).UTC()
			st.StartedAt = &ts
		}
		if finished > 0 {
			ts := time.Unix(0, finished).UTC()
			st.FinishedAt = &ts
		}
		steps = append(steps, st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("plan steps iterate: %w", err)
	}
	return steps, nil
}

// Active returns the most recently updated non-terminal plan for the scoped
// session, or nil if the session has no active plan.
func (s *Store) Active(ctx context.Context, sessionID string) (*Plan, error) {
	if s.db == nil {
		return nil, fmt.Errorf("plan store: nil db")
	}
	if sessionID != s.sessionID {
		return nil, fmt.Errorf("plan active: session mismatch")
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT id FROM plans WHERE session_id = ? AND status NOT IN ('completed','cancelled')
		 ORDER BY updated_at DESC LIMIT 1`, sessionID)
	var id string
	if err := row.Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("plan active: %w", err)
	}
	p, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// List returns recent plan summaries for the scoped session, newest first.
func (s *Store) List(ctx context.Context, sessionID string) ([]PlanSummary, error) {
	if s.db == nil {
		return nil, fmt.Errorf("plan store: nil db")
	}
	if sessionID != s.sessionID {
		return nil, fmt.Errorf("plan list: session mismatch")
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, objective, status, revision, updated_at FROM plans
		 WHERE session_id = ? ORDER BY updated_at DESC LIMIT 50`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("plan list: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []PlanSummary
	for rows.Next() {
		var sm PlanSummary
		var updated int64
		if err := rows.Scan(&sm.ID, &sm.Objective, &sm.Status, &sm.Revision, &updated); err != nil {
			return nil, fmt.Errorf("plan list scan: %w", err)
		}
		sm.UpdatedAt = time.Unix(0, updated).UTC()
		out = append(out, sm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("plan list iterate: %w", err)
	}
	return out, nil
}

// Events returns the durable canonical snapshots for a plan, oldest revision
// first. The event log is the plan's audit/history surface.
func (s *Store) Events(ctx context.Context, planID string) ([]PlanEvent, error) {
	if _, err := s.Get(ctx, planID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT revision, event_type, COALESCE(payload_json,''), created_at
		 FROM plan_events WHERE plan_id = ? ORDER BY revision`, planID)
	if err != nil {
		return nil, fmt.Errorf("plan events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var events []PlanEvent
	for rows.Next() {
		var event PlanEvent
		var payload string
		var created int64
		if err := rows.Scan(&event.Revision, &event.Type, &payload, &created); err != nil {
			return nil, fmt.Errorf("plan event scan: %w", err)
		}
		if payload != "" {
			if err := json.Unmarshal([]byte(payload), &event.Payload); err != nil {
				return nil, fmt.Errorf("plan event payload: %w", err)
			}
		}
		event.CreatedAt = time.Unix(0, created).UTC()
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("plan events iterate: %w", err)
	}
	return events, nil
}

// ── write helpers (all execute inside one transaction) ──

func writePlanRow(ctx context.Context, tx *sql.Tx, sessionID string, p Plan) error {
	var closed any
	if p.ClosedAt != nil {
		closed = p.ClosedAt.UnixNano()
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO plans (id, session_id, objective, status, revision, explanation, created_at, updated_at, closed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, sessionID, p.Objective, string(p.Status), p.Revision,
		p.Explanation, p.CreatedAt.UnixNano(), p.UpdatedAt.UnixNano(), closed); err != nil {
		return fmt.Errorf("plan write: %w", err)
	}
	return nil
}

func updatePlanRowCAS(ctx context.Context, tx *sql.Tx, sessionID string, p Plan, expectedRevision uint64) (bool, error) {
	var closed any
	if p.ClosedAt != nil {
		closed = p.ClosedAt.UnixNano()
	}
	result, err := tx.ExecContext(ctx,
		`UPDATE plans SET session_id = ?, objective = ?, status = ?, revision = ?, explanation = ?, updated_at = ?, closed_at = ?
		 WHERE id = ? AND session_id = ? AND revision = ?`,
		sessionID, p.Objective, string(p.Status), p.Revision, p.Explanation,
		p.UpdatedAt.UnixNano(), closed, p.ID, sessionID, expectedRevision)
	if err != nil {
		return false, fmt.Errorf("plan update CAS: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("plan update CAS rows: %w", err)
	}
	return rows == 1, nil
}

func writeSteps(ctx context.Context, tx *sql.Tx, planID string, steps []PlanStep) error {
	for i, step := range steps {
		var started, finished any
		if step.StartedAt != nil {
			started = step.StartedAt.UnixNano()
		}
		if step.FinishedAt != nil {
			finished = step.FinishedAt.UnixNano()
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO plan_steps (plan_id, step_id, position, description, status, depends_on_json, reason, attempts, created_at, started_at, finished_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			planID, step.ID, i, step.Description, string(step.Status), encodeDeps(step.DependsOn),
			step.Reason, step.Attempts, step.CreatedAt.UnixNano(), started, finished); err != nil {
			return fmt.Errorf("plan steps write: %w", err)
		}
	}
	return nil
}

func replaceSteps(ctx context.Context, tx *sql.Tx, planID string, steps []PlanStep) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM plan_steps WHERE plan_id = ?`, planID); err != nil {
		return fmt.Errorf("plan steps replace: %w", err)
	}
	return writeSteps(ctx, tx, planID, steps)
}

func writeEvent(ctx context.Context, tx *sql.Tx, planID string, revision uint64, eventType string, payload any) error {
	var payloadJSON any
	if payload != nil {
		if b, err := json.Marshal(payload); err == nil {
			payloadJSON = string(b)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO plan_events (plan_id, revision, event_type, payload_json, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		planID, int64(revision), eventType, payloadJSON, time.Now().UTC().UnixNano()); err != nil {
		return fmt.Errorf("plan event write: %w", err)
	}
	return nil
}

// ── normalization / provenance ──

func normalizeSteps(steps []PlanStep, now time.Time, isNew bool) []PlanStep {
	out := make([]PlanStep, len(steps))
	for i, s := range steps {
		cp := s
		cp.ID = strings.TrimSpace(s.ID)
		cp.Description = strings.TrimSpace(s.Description)
		cp.DependsOn = normalizedDeps(s.DependsOn)
		if cp.CreatedAt.IsZero() {
			cp.CreatedAt = now
		}
		if isNew && s.Status == StepInProgress && cp.StartedAt == nil {
			ts := now
			cp.StartedAt = &ts
			if cp.Attempts == 0 {
				cp.Attempts = 1
			}
		}
		if (s.Status == StepCompleted || s.Status == StepFailed || s.Status == StepSkipped || s.Status == StepCancelled) && cp.FinishedAt == nil {
			ts := now
			cp.FinishedAt = &ts
		}
		out[i] = cp
	}
	return out
}

func applyPlanLifecycle(status PlanStatus, explanation string, steps []PlanStep, now time.Time) []PlanStep {
	if status != StatusCancelled {
		return steps
	}
	reason := strings.TrimSpace(explanation)
	for i := range steps {
		if steps[i].Status != StepPending && steps[i].Status != StepInProgress {
			continue
		}
		steps[i].Status = StepCancelled
		if strings.TrimSpace(steps[i].Reason) == "" {
			steps[i].Reason = reason
		}
		ts := now
		steps[i].FinishedAt = &ts
	}
	return steps
}

func updateSteps(existing Plan, requested []PlanStep, now time.Time) []PlanStep {
	prior := make(map[string]PlanStep, len(existing.Steps))
	for _, s := range existing.Steps {
		prior[s.ID] = s
	}
	updated := normalizeSteps(requested, now, false)
	for i := range updated {
		p, ok := prior[updated[i].ID]
		if !ok {
			updated[i].CreatedAt = now
			if updated[i].Status == StepInProgress {
				updated[i].Attempts = max(1, updated[i].Attempts)
				ts := now
				updated[i].StartedAt = &ts
			}
			continue
		}
		step := &updated[i]
		step.CreatedAt = p.CreatedAt
		step.Attempts = max(step.Attempts, p.Attempts)
		step.StartedAt = p.StartedAt
		step.FinishedAt = p.FinishedAt
		if step.Status != p.Status {
			switch step.Status {
			case StepPending:
				step.StartedAt = nil
				step.FinishedAt = nil
			case StepInProgress:
				step.Attempts = max(1, p.Attempts+1)
				ts := now
				step.StartedAt = &ts
				step.FinishedAt = nil
			case StepCompleted, StepFailed, StepSkipped, StepCancelled:
				ts := now
				step.FinishedAt = &ts
			case StepBlocked:
				step.FinishedAt = nil
			}
		}
	}
	return updated
}

func normalizedDeps(deps []string) []string {
	var out []string
	seen := make(map[string]struct{}, len(deps))
	for _, d := range deps {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		if _, dup := seen[d]; dup {
			continue
		}
		seen[d] = struct{}{}
		out = append(out, d)
	}
	return out
}

func encodeDeps(deps []string) string {
	if len(deps) == 0 {
		return ""
	}
	b, err := json.Marshal(normalizedDeps(deps))
	if err != nil {
		return ""
	}
	return string(b)
}

func decodeDeps(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err == nil {
		return normalizedDeps(out)
	}
	return nil
}
