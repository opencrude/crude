package collaboration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (store *Store) RegisterAgent(ctx context.Context, registration AgentRegistration) (Agent, error) {
	if err := validateIdentity("agent_id", registration.ID, 256); err != nil {
		return Agent{}, err
	}
	if registration.Name == "" {
		registration.Name = registration.ID
	}
	if err := validateIdentity("agent name", registration.Name, 256); err != nil {
		return Agent{}, err
	}
	if err := validateIdentity("session_id", registration.SessionID, 256); err != nil {
		return Agent{}, err
	}
	if err := validateOptionalIdentity("parent_id", registration.ParentID, 256); err != nil {
		return Agent{}, err
	}
	if err := validateOptionalIdentity("category", registration.Category, 64); err != nil {
		return Agent{}, err
	}
	if err := validateOptionalIdentity("owner_instance", registration.OwnerInstance, 256); err != nil {
		return Agent{}, err
	}
	if registration.State == "" {
		registration.State = AgentAvailable
	}
	if !validateAgentState(registration.State) {
		return Agent{}, fmt.Errorf("%w: invalid agent state %q", ErrInvalid, registration.State)
	}
	if registration.OwnerInstance == "" && len(registration.Admissions) != 0 {
		return Agent{}, fmt.Errorf("%w: admission receipts require runtime owner", ErrInvalid)
	}
	for _, receipt := range registration.Admissions {
		if err := validateAdmissionReceipt(receipt); err != nil {
			return Agent{}, err
		}
	}
	freshLock, err := store.acquireRuntimeLock(registration.ID, registration.OwnerInstance)
	if err != nil {
		return Agent{}, err
	}
	keepLock := false
	defer func() {
		if freshLock && !keepLock {
			store.releaseRuntimeLock(registration.ID, registration.OwnerInstance)
		}
	}()

	var result Agent
	var notify bool
	err = store.transaction(ctx, func(tx *sql.Tx) error {
		if registration.ParentID != "" {
			parent, err := loadAgent(ctx, tx, registration.ParentID)
			if err != nil {
				return fmt.Errorf("collaboration: parent: %w", err)
			}
			if parent.State == AgentClosed {
				return fmt.Errorf("%w: parent is closed", ErrUnavailable)
			}
		}

		existing, err := loadAgent(ctx, tx, registration.ID)
		now := time.Now().UTC()
		if errors.Is(err, ErrNotFound) {
			if len(registration.Admissions) != 0 {
				return fmt.Errorf("%w: receipts for unknown agent", ErrConflict)
			}
			presence := RuntimeOffline
			epoch := uint64(0)
			heartbeat := int64(0)
			if registration.OwnerInstance != "" {
				presence = RuntimeAttached
				epoch = 1
				heartbeat = now.UnixNano()
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO collaboration_agents
				(id,workspace_id,name,session_id,parent_id,category,state,presence,runtime_epoch,owner_instance,created_at,updated_at,heartbeat_at)
				VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				registration.ID, store.workspaceID, registration.Name, registration.SessionID,
				registration.ParentID, registration.Category, registration.State, presence,
				epoch, registration.OwnerInstance, now.UnixNano(), now.UnixNano(), heartbeat); err != nil {
				return err
			}
			result, err = loadAgent(ctx, tx, registration.ID)
			notify = err == nil
			return err
		}
		if err != nil {
			return err
		}
		if existing.SessionID != registration.SessionID || existing.ParentID != registration.ParentID {
			return fmt.Errorf("%w: agent session/parent identity is immutable", ErrConflict)
		}
		if registration.OwnerInstance != "" &&
			existing.Presence == RuntimeAttached &&
			existing.OwnerInstance != "" &&
			existing.OwnerInstance != registration.OwnerInstance &&
			!freshLock {
			return fmt.Errorf("%w: agent %q is attached by %q", ErrConflict, existing.ID, existing.OwnerInstance)
		}
		epoch := existing.RuntimeEpoch
		presence := existing.Presence
		owner := existing.OwnerInstance
		heartbeat := existing.HeartbeatAt.UnixNano()
		newAttachment := registration.OwnerInstance != "" && freshLock
		if registration.OwnerInstance != "" {
			presence = RuntimeAttached
			owner = registration.OwnerInstance
			if newAttachment {
				epoch++
			}
			heartbeat = now.UnixNano()
		}
		if newAttachment {
			if err := store.recoverRuntime(ctx, tx, existing, registration.Admissions, registration.PendingTerminal, now); err != nil {
				return err
			}
		} else if len(registration.Admissions) != 0 {
			return fmt.Errorf("%w: receipts require a new attachment", ErrConflict)
		}
		notify = newAttachment || existing.Name != registration.Name ||
			existing.Category != registration.Category || existing.Presence != presence
		if _, err := tx.ExecContext(ctx, `UPDATE collaboration_agents SET
			name=?,category=?,state=?,presence=?,runtime_epoch=?,owner_instance=?,updated_at=?,heartbeat_at=?
			WHERE id=?`,
			registration.Name, registration.Category, existing.State, presence,
			epoch, owner, now.UnixNano(), heartbeat, registration.ID); err != nil {
			return err
		}
		result, err = loadAgent(ctx, tx, registration.ID)
		return err
	})
	if err == nil && registration.OwnerInstance != "" {
		keepLock = true
	}
	if err == nil && notify {
		store.signal()
	}
	return result, err
}

func (store *Store) Agent(ctx context.Context, id string) (Agent, error) {
	if err := validateIdentity("agent_id", id, 256); err != nil {
		return Agent{}, err
	}
	var result Agent
	err := store.readTransaction(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = loadAgent(ctx, tx, id)
		return err
	})
	return result, err
}

func (store *Store) ListAgents(ctx context.Context, query AgentQuery) ([]Agent, error) {
	if query.Limit <= 0 {
		query.Limit = DefaultAgentLimit
	}
	if query.Limit > MaxAgentLimit {
		query.Limit = MaxAgentLimit
	}
	if len(query.Query) > 256 {
		return nil, ErrTooLarge
	}
	if query.State != "" && !validateAgentState(query.State) {
		return nil, fmt.Errorf("%w: invalid agent state", ErrInvalid)
	}
	if query.Presence != "" && !validatePresence(query.Presence) {
		return nil, fmt.Errorf("%w: invalid runtime presence", ErrInvalid)
	}
	var result []Agent
	err := store.readTransaction(ctx, func(tx *sql.Tx) error {
		statement := `SELECT id,name,session_id,parent_id,category,state,presence,runtime_epoch,
			owner_instance,current_run_id,current_request_id,created_at,updated_at,heartbeat_at
			FROM collaboration_agents WHERE workspace_id=?`
		arguments := []any{store.workspaceID}
		if !query.IncludeClosed {
			statement += ` AND state != 'closed'`
		}
		if query.SessionID != "" {
			statement += ` AND session_id=?`
			arguments = append(arguments, query.SessionID)
		}
		if query.ParentID != "" {
			statement += ` AND parent_id=?`
			arguments = append(arguments, query.ParentID)
		}
		if query.State != "" {
			statement += ` AND state=?`
			arguments = append(arguments, query.State)
		}
		if query.Presence != "" {
			statement += ` AND presence=?`
			arguments = append(arguments, query.Presence)
		}
		if query.Query != "" {
			statement += ` AND (instr(lower(id),lower(?))>0 OR instr(lower(name),lower(?))>0 OR instr(lower(category),lower(?))>0)`
			arguments = append(arguments, query.Query, query.Query, query.Query)
		}
		statement += ` ORDER BY CASE state WHEN 'available' THEN 0 WHEN 'suspended' THEN 1 ELSE 2 END,
			CASE presence WHEN 'attached' THEN 0 WHEN 'unresponsive' THEN 1 ELSE 2 END,
			updated_at DESC,id LIMIT ?`
		arguments = append(arguments, query.Limit)
		rows, err := tx.QueryContext(ctx, statement, arguments...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			agent, err := scanAgent(rows)
			if err != nil {
				return err
			}
			result = append(result, agent)
		}
		return rows.Err()
	})
	return result, err
}

func (store *Store) Heartbeat(ctx context.Context, agentID, ownerInstance string, runtimeEpoch uint64) error {
	if err := validateIdentity("agent_id", agentID, 256); err != nil {
		return err
	}
	if err := validateIdentity("owner_instance", ownerInstance, 256); err != nil {
		return err
	}
	notify := false
	err := store.transaction(ctx, func(tx *sql.Tx) error {
		var presence string
		if err := tx.QueryRowContext(ctx, `SELECT presence FROM collaboration_agents
			WHERE id=? AND owner_instance=? AND runtime_epoch=? AND state!='closed'`,
			agentID, ownerInstance, runtimeEpoch).Scan(&presence); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrStale
			}
			return err
		}
		notify = presence != string(RuntimeAttached)
		now := time.Now().UTC().UnixNano()
		result, err := tx.ExecContext(ctx, `UPDATE collaboration_agents
			SET heartbeat_at=?,updated_at=?,presence='attached'
			WHERE id=? AND owner_instance=? AND runtime_epoch=? AND state!='closed'`,
			now, now, agentID, ownerInstance, runtimeEpoch)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return ErrStale
		}
		return nil
	})
	if err == nil && notify {
		store.signal()
	}
	return err
}

func (store *Store) MarkUnresponsiveBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	var affected int64
	err := store.transaction(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE collaboration_agents
			SET presence='unresponsive',updated_at=?
			WHERE presence='attached' AND heartbeat_at>0 AND heartbeat_at<? AND state!='closed'`,
			time.Now().UTC().UnixNano(), cutoff.UTC().UnixNano())
		if err != nil {
			return err
		}
		affected, err = result.RowsAffected()
		return err
	})
	if err == nil && affected > 0 {
		store.signal()
	}
	return affected, err
}

func (store *Store) DetachAgent(ctx context.Context, agentID, ownerInstance string, runtimeEpoch uint64) error {
	if err := validateIdentity("agent_id", agentID, 256); err != nil {
		return err
	}
	err := store.transaction(ctx, func(tx *sql.Tx) error {
		now := time.Now().UTC().UnixNano()
		result, err := tx.ExecContext(ctx, `UPDATE collaboration_agents
			SET presence='offline',owner_instance='',updated_at=?
			WHERE id=? AND owner_instance=? AND runtime_epoch=?`,
			now, agentID, ownerInstance, runtimeEpoch)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return ErrStale
		}
		return nil
	})
	if err == nil {
		store.releaseRuntimeLock(agentID, ownerInstance)
		store.signal()
	}
	return err
}

func (store *Store) SetAgentState(ctx context.Context, agentID string, expectedEpoch uint64, state AgentState) (Agent, error) {
	if err := validateIdentity("agent_id", agentID, 256); err != nil {
		return Agent{}, err
	}
	if !validateAgentState(state) {
		return Agent{}, fmt.Errorf("%w: invalid agent state", ErrInvalid)
	}
	var result Agent
	err := store.transaction(ctx, func(tx *sql.Tx) error {
		now := time.Now().UTC().UnixNano()
		statement := `UPDATE collaboration_agents SET state=?,updated_at=? WHERE id=?`
		arguments := []any{state, now, agentID}
		if expectedEpoch > 0 {
			statement += ` AND runtime_epoch=?`
			arguments = append(arguments, expectedEpoch)
		}
		update, err := tx.ExecContext(ctx, statement, arguments...)
		if err != nil {
			return err
		}
		affected, err := update.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return ErrStale
		}
		result, err = loadAgent(ctx, tx, agentID)
		return err
	})
	if err == nil {
		store.signal()
	}
	return result, err
}

func (store *Store) StartRun(ctx context.Context, request StartRunRequest) (Run, error) {
	if request.ID == "" {
		request.ID = newID("run")
	}
	if err := validateIdentity("run_id", request.ID, 256); err != nil {
		return Run{}, err
	}
	if err := validateIdentity("agent_id", request.AgentID, 256); err != nil {
		return Run{}, err
	}
	if !validateOrigin(request.Origin) || request.RuntimeEpoch == 0 {
		return Run{}, fmt.Errorf("%w: run origin/epoch", ErrInvalid)
	}
	var result Run
	err := store.transaction(ctx, func(tx *sql.Tx) error {
		agent, err := loadAgent(ctx, tx, request.AgentID)
		if err != nil {
			return err
		}
		if agent.State != AgentAvailable || agent.Presence != RuntimeAttached || agent.RuntimeEpoch != request.RuntimeEpoch {
			return ErrUnavailable
		}
		if agent.CurrentRunID != "" {
			existing, err := loadRun(ctx, tx, agent.CurrentRunID)
			if err == nil && !existing.State.Terminal() {
				return fmt.Errorf("%w: agent already owns active run %s", ErrConflict, existing.ID)
			}
		}
		now := time.Now().UTC()
		if _, err := tx.ExecContext(ctx, `INSERT INTO collaboration_runs
			(id,workspace_id,agent_id,runtime_epoch,request_id,origin,state,version,started_at,updated_at)
			VALUES(?,?,?,?,?,?,?,1,?,?)`,
			request.ID, store.workspaceID, request.AgentID, request.RuntimeEpoch, request.RequestID,
			request.Origin, RunRunning, now.UnixNano(), now.UnixNano()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE collaboration_agents
			SET current_run_id=?,current_request_id=?,updated_at=?
			WHERE id=? AND runtime_epoch=?`,
			request.ID, request.RequestID, now.UnixNano(), request.AgentID, request.RuntimeEpoch); err != nil {
			return err
		}
		var foreignDeclarations int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM collaboration_claims
			WHERE agent_id=? AND run_id='' AND released_at=0 AND request_id!=?`, request.AgentID, request.RequestID).Scan(&foreignDeclarations); err != nil {
			return err
		}
		if foreignDeclarations != 0 {
			return fmt.Errorf("%w: declared claims belong to a different assignment", ErrConflict)
		}
		// Explicit Start/Continue binds the exact preserved declarations to this
		// replacement run. Repair itself leaves them nonexecuting and unbound.
		if _, err := tx.ExecContext(ctx, `UPDATE collaboration_claims
			SET run_id=?,version=version+1
			WHERE agent_id=? AND request_id=? AND run_id='' AND released_at=0`, request.ID, request.AgentID, request.RequestID); err != nil {
			return err
		}
		result, err = loadRun(ctx, tx, request.ID)
		return err
	})
	if err == nil {
		store.signal()
	}
	return result, err
}

func (store *Store) Run(ctx context.Context, id string) (Run, error) {
	if err := validateIdentity("run_id", id, 256); err != nil {
		return Run{}, err
	}
	var result Run
	err := store.readTransaction(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = loadRun(ctx, tx, id)
		return err
	})
	return result, err
}

func (store *Store) TransitionRun(ctx context.Context, runID, agentID string, runtimeEpoch, expectedVersion uint64, state RunState) (Run, error) {
	if !validateRunState(state) || state.Terminal() {
		return Run{}, fmt.Errorf("%w: nonterminal transition state", ErrInvalid)
	}
	return store.updateRun(ctx, FinishRunRequest{
		RunID: runID, AgentID: agentID, RuntimeEpoch: runtimeEpoch,
		ExpectedVersion: expectedVersion, State: state,
	})
}

func (store *Store) FinishRun(ctx context.Context, request FinishRunRequest) (Run, error) {
	if !request.State.Terminal() {
		return Run{}, fmt.Errorf("%w: terminal run state required", ErrInvalid)
	}
	return store.updateRun(ctx, request)
}

func (store *Store) updateRun(ctx context.Context, request FinishRunRequest) (Run, error) {
	if err := validateIdentity("run_id", request.RunID, 256); err != nil {
		return Run{}, err
	}
	if err := validateIdentity("agent_id", request.AgentID, 256); err != nil {
		return Run{}, err
	}
	if request.RuntimeEpoch == 0 || request.ExpectedVersion == 0 || !validateRunState(request.State) {
		return Run{}, fmt.Errorf("%w: run epoch/version/state", ErrInvalid)
	}
	var result Run
	err := store.transaction(ctx, func(tx *sql.Tx) error {
		current, err := loadRun(ctx, tx, request.RunID)
		if err != nil {
			return err
		}
		if current.AgentID != request.AgentID || current.RuntimeEpoch != request.RuntimeEpoch ||
			current.Version != request.ExpectedVersion || current.State.Terminal() {
			return ErrStale
		}
		agent, err := loadAgent(ctx, tx, request.AgentID)
		if err != nil {
			return err
		}
		ownerEpoch := request.RuntimeEpoch
		if request.SettlementEpoch != 0 {
			if request.SettlementEpoch <= request.RuntimeEpoch || agent.CurrentRunID != "" || !request.State.Terminal() {
				return ErrStale
			}
			ownerEpoch = request.SettlementEpoch
		}
		if agent.RuntimeEpoch != ownerEpoch || agent.Presence != RuntimeAttached {
			return ErrStale
		}
		now := time.Now().UTC()
		endedAt := int64(0)
		if request.State.Terminal() {
			endedAt = now.UnixNano()
		}
		update, err := tx.ExecContext(ctx, `UPDATE collaboration_runs SET
			state=?,version=version+1,outcome=?,context_commit_id=?,updated_at=?,ended_at=?
			WHERE id=? AND agent_id=? AND runtime_epoch=? AND version=?`,
			request.State, request.Outcome, request.ContextCommitID, now.UnixNano(), endedAt,
			request.RunID, request.AgentID, request.RuntimeEpoch, request.ExpectedVersion)
		if err != nil {
			return err
		}
		affected, err := update.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return ErrStale
		}
		if request.State.Terminal() {
			if _, err := tx.ExecContext(ctx, `UPDATE collaboration_agents SET
				current_run_id='',current_request_id='',updated_at=?
				WHERE id=? AND current_run_id=? AND runtime_epoch=?`,
				now.UnixNano(), request.AgentID, request.RunID, request.RuntimeEpoch); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE collaboration_claims SET
				version=version+1,released_at=?
				WHERE run_id=? AND agent_id=? AND released_at=0`,
				now.UnixNano(), request.RunID, request.AgentID); err != nil {
				return err
			}
		}
		result, err = loadRun(ctx, tx, request.RunID)
		return err
	})
	// Reports and terminal outcomes are independent facts. A report can wake a
	// waiter before this commit, so run transitions need their own post-commit
	// signal rather than relying on a later message or heartbeat.
	if err == nil {
		store.signal()
	}
	return result, err
}

func loadAgent(ctx context.Context, tx *sql.Tx, id string) (Agent, error) {
	row := tx.QueryRowContext(ctx, `SELECT id,name,session_id,parent_id,category,state,presence,runtime_epoch,
		owner_instance,current_run_id,current_request_id,created_at,updated_at,heartbeat_at
		FROM collaboration_agents WHERE id=?`, id)
	return scanAgent(row)
}

type scanner interface {
	Scan(dest ...any) error
}

func scanAgent(row scanner) (Agent, error) {
	var agent Agent
	var state, presence string
	var createdAt, updatedAt, heartbeatAt int64
	err := row.Scan(&agent.ID, &agent.Name, &agent.SessionID, &agent.ParentID, &agent.Category,
		&state, &presence, &agent.RuntimeEpoch, &agent.OwnerInstance, &agent.CurrentRunID,
		&agent.CurrentRequestID, &createdAt, &updatedAt, &heartbeatAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Agent{}, ErrNotFound
	}
	if err != nil {
		return Agent{}, err
	}
	agent.State = AgentState(state)
	agent.Presence = RuntimePresence(presence)
	agent.CreatedAt = timeFromUnixNano(createdAt)
	agent.UpdatedAt = timeFromUnixNano(updatedAt)
	agent.HeartbeatAt = timeFromUnixNano(heartbeatAt)
	return agent, nil
}

func loadRun(ctx context.Context, tx *sql.Tx, id string) (Run, error) {
	var run Run
	var origin, state string
	var startedAt, updatedAt, endedAt int64
	err := tx.QueryRowContext(ctx, `SELECT id,agent_id,runtime_epoch,request_id,origin,state,version,
		outcome,context_commit_id,started_at,updated_at,ended_at
		FROM collaboration_runs WHERE id=?`, id).
		Scan(&run.ID, &run.AgentID, &run.RuntimeEpoch, &run.RequestID, &origin, &state,
			&run.Version, &run.Outcome, &run.ContextCommitID, &startedAt, &updatedAt, &endedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, err
	}
	run.Origin = Origin(origin)
	run.State = RunState(state)
	run.StartedAt = timeFromUnixNano(startedAt)
	run.UpdatedAt = timeFromUnixNano(updatedAt)
	run.EndedAt = timeFromUnixNano(endedAt)
	return run, nil
}

// RetireUnreferencedSession removes the directory entries of a session that
// never took part in collaboration, so a pruned empty session leaves no agent
// behind. It fails closed: when any of the session's agents is still attached,
// or any run, event, delivery, artifact, claim or child agent refers to one,
// nothing is removed and retired is false; that session holds work and must
// be kept.
func (store *Store) RetireUnreferencedSession(ctx context.Context, sessionID string) (bool, error) {
	if err := validateIdentity("session_id", sessionID, 256); err != nil {
		return false, err
	}
	retired := false
	err := store.transaction(ctx, func(tx *sql.Tx) error {
		var agents, attached int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(presence = ?), 0) FROM collaboration_agents WHERE session_id = ?`, RuntimeAttached, sessionID).Scan(&agents, &attached); err != nil {
			return err
		}
		if attached > 0 {
			return nil
		}
		var references int
		if err := tx.QueryRowContext(ctx, `WITH owned(id) AS (SELECT id FROM collaboration_agents WHERE session_id = ?)
			SELECT (SELECT COUNT(*) FROM collaboration_runs WHERE agent_id IN owned)
			     + (SELECT COUNT(*) FROM collaboration_events WHERE sender_id IN owned)
			     + (SELECT COUNT(*) FROM collaboration_deliveries WHERE recipient_id IN owned)
			     + (SELECT COUNT(*) FROM collaboration_artifacts WHERE author_id IN owned)
			     + (SELECT COUNT(*) FROM collaboration_claims WHERE agent_id IN owned)
			     + (SELECT COUNT(*) FROM collaboration_agents WHERE parent_id IN owned AND session_id != ?)`, sessionID, sessionID).Scan(&references); err != nil {
			return err
		}
		if references > 0 {
			return nil
		}
		if agents > 0 {
			if _, err := tx.ExecContext(ctx, `DELETE FROM collaboration_agents WHERE session_id = ?`, sessionID); err != nil {
				return err
			}
		}
		retired = true
		return nil
	})
	return retired, err
}
