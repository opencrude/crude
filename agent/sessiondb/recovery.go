package sessiondb

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"crude/agent/provider"
	"crude/types/recovery"
)

// RecoveryCheckpoint is the durable, replay-safe state of one unfinished run.
// Archive and ActiveContext are snapshots, not instructions to restart work.
type RecoveryCheckpoint struct {
	Status       recovery.Status
	PendingInput provider.UserInput
	// PlanningCycle is the policy captured when the root turn started.
	// Nil means not yet captured; an empty string means ordinary execution.
	PlanningCycle      *string
	InputAdmitted      bool
	Archive            []provider.ChatMessage
	ActiveContext      []provider.ChatMessage
	ContextOwnerID     string
	ContextRevision    uint64
	ContextGeneration  uint64
	ContextReason      string
	OwnerData          []byte
	TerminalSettlement json.RawMessage
	InFlightTools      []provider.ChatMessage
}

// settledRecoveryStates are the states of an incident that owns no
// unfinished work (recovery.State.Settled).
var settledRecoveryStates = [4]string{string(recovery.Recovered), string(recovery.Cancelled), string(recovery.Continued), string(recovery.Failed)}

type recoveryInput struct {
	provider.UserInput
	PlanningCycle      *string                `json:"planning_cycle,omitempty"`
	TerminalSettlement json.RawMessage        `json:"terminal_settlement,omitempty"`
	InFlightTools      []provider.ChatMessage `json:"in_flight_tools,omitempty"`
}

// SaveRecoveryCheckpoint atomically persists the exact incident status,
// original input, archive, active context, and the context owner's live state.
func SaveRecoveryCheckpoint(db *sql.DB, sessionID string, checkpoint RecoveryCheckpoint) error {
	defer sessionWrites.Since("recovery checkpoint", time.Now())
	if db == nil {
		return errors.New("sessiondb: nil recovery database")
	}
	if sessionID == "" || checkpoint.Status.ID == "" || checkpoint.Status.AgentID == "" ||
		checkpoint.Status.Generation == 0 {
		return errors.New("sessiondb: invalid recovery identity")
	}
	if checkpoint.ContextOwnerID != "" {
		_, err := PublishContext(db, ContextPublication{SessionID: sessionID, AgentID: checkpoint.ContextOwnerID, RuntimeAgentID: checkpoint.Status.AgentID,
			ExpectedRevision: checkpoint.ContextRevision, Mutation: ContextMutation{Replace: &ContextReplacement{
				Generation: checkpoint.ContextGeneration, Reason: checkpoint.ContextReason, Messages: checkpoint.ActiveContext}},
			Recovery: &checkpoint})
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("sessiondb: begin recovery checkpoint: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	recorded, err := saveRecoveryCheckpointTx(tx, db, sessionID, checkpoint, false)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sessiondb: commit recovery checkpoint: %w", err)
	}
	recorded()
	return nil
}

// saveRecoveryCheckpointTx stores checkpoint. Its archive and active context
// grow by a round per checkpoint and are stored as message lists, so a round
// writes what it added. The returned function records the written lists once
// the transaction commits.
func saveRecoveryCheckpointTx(tx *sql.Tx, db *sql.DB, sessionID string, checkpoint RecoveryCheckpoint, activeOwned bool) (func(), error) {
	if sessionID == "" || checkpoint.Status.ID == "" || checkpoint.Status.AgentID == "" || checkpoint.Status.Generation == 0 {
		return nil, errors.New("sessiondb: invalid recovery identity")
	}
	var existingSession, existingAgent string
	var existingGeneration uint64
	err := tx.QueryRow(`SELECT session_id,agent_id,generation FROM agent_recoveries WHERE incident_id=?`, checkpoint.Status.ID).Scan(&existingSession, &existingAgent, &existingGeneration)
	if err == nil && (existingSession != sessionID || existingAgent != checkpoint.Status.AgentID || existingGeneration != checkpoint.Status.Generation) {
		return nil, errors.New("sessiondb: recovery identity conflict")
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	rowExists := err == nil
	pending, err := json.Marshal(recoveryInput{
		UserInput: checkpoint.PendingInput, PlanningCycle: checkpoint.PlanningCycle,
		TerminalSettlement: checkpoint.TerminalSettlement, InFlightTools: checkpoint.InFlightTools,
	})
	if err != nil {
		return nil, fmt.Errorf("sessiondb: encode recovery input: %w", err)
	}
	var refs map[string]recoveryListRef
	if rowExists {
		if refs, err = recoveryListRefsTx(tx, checkpoint.Status.ID); err != nil {
			return nil, err
		}
	}
	incidentID, agentID := checkpoint.Status.ID, checkpoint.Status.AgentID
	archiveTarget, err := recoveryListTargetTx(tx, incidentID, agentID, recoveryArchiveField, refs[recoveryArchiveField])
	if err != nil {
		return nil, err
	}
	archiveTarget, archiveStored, archive, err := planRecoveryListTx(tx, db, archiveTarget, checkpoint.Archive)
	if err != nil {
		return nil, fmt.Errorf("sessiondb: encode recovery archive: %w", err)
	}
	activeTarget, err := recoveryListTargetTx(tx, incidentID, agentID, recoveryActiveField, refs[recoveryActiveField])
	if err != nil {
		return nil, err
	}
	activeTarget, activeStored, active, err := planRecoveryListTx(tx, db, activeTarget, checkpoint.ActiveContext)
	if err != nil {
		return nil, fmt.Errorf("sessiondb: encode recovery context: %w", err)
	}
	nowTime := time.Now()
	if checkpoint.Status.StartedAt.IsZero() {
		checkpoint.Status.StartedAt = nowTime
	}
	if checkpoint.Status.UpdatedAt.IsZero() {
		checkpoint.Status.UpdatedAt = nowTime
	}
	startedAt, updatedAt := checkpoint.Status.StartedAt.UnixNano(), checkpoint.Status.UpdatedAt.UnixNano()
	retryAt := int64(0)
	if !checkpoint.Status.RetryAt.IsZero() {
		retryAt = checkpoint.Status.RetryAt.UnixNano()
	}
	var endedAt any
	if checkpoint.Status.State.Settled() {
		endedAt = updatedAt
	}
	if checkpoint.ContextGeneration == 0 {
		checkpoint.ContextGeneration = 1
	}
	if checkpoint.ContextReason == "" {
		checkpoint.ContextReason = "recovery"
	}
	_, err = tx.Exec(`
		INSERT INTO agent_recoveries (
			incident_id, session_id, agent_id, generation, state, attempt, retry_at,
			summary, detail, action, pending_input_json, input_admitted,
			archive_json, active_context_json, context_owner_id, context_revision,
			context_generation, context_reason, owner_data, created_at, updated_at, ended_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(incident_id) DO UPDATE SET
			agent_id=excluded.agent_id,
			generation=excluded.generation,
			state=excluded.state,
			attempt=excluded.attempt,
			retry_at=excluded.retry_at,
			summary=excluded.summary,
			detail=excluded.detail,
			action=excluded.action,
			pending_input_json=excluded.pending_input_json,
			input_admitted=excluded.input_admitted,
			archive_json=CASE WHEN ? THEN excluded.archive_json ELSE agent_recoveries.archive_json END,
			active_context_json=CASE WHEN ? THEN excluded.active_context_json ELSE agent_recoveries.active_context_json END,
			context_owner_id=excluded.context_owner_id,
			context_revision=excluded.context_revision,
			context_generation=excluded.context_generation,
			context_reason=excluded.context_reason,
			owner_data=excluded.owner_data,
			updated_at=excluded.updated_at,
			ended_at=excluded.ended_at
	`, checkpoint.Status.ID, sessionID, checkpoint.Status.AgentID, checkpoint.Status.Generation,
		string(checkpoint.Status.State), checkpoint.Status.Attempt, retryAt,
		checkpoint.Status.Summary, checkpoint.Status.Detail, checkpoint.Status.Action,
		pending, recoveryBoolInt(checkpoint.InputAdmitted), listBase(archive), listBase(active),
		checkpoint.ContextOwnerID, checkpoint.ContextRevision, checkpoint.ContextGeneration, checkpoint.ContextReason,
		nullBytes(checkpoint.OwnerData), startedAt, updatedAt, endedAt, archive.rewrite, active.rewrite)
	if err != nil {
		return nil, fmt.Errorf("sessiondb: save recovery checkpoint: %w", err)
	}
	archiveOwned := activeOwned && sameMessageArray(checkpoint.Archive, checkpoint.ActiveContext)
	recordArchive, err := writeRecoveryListTx(tx, db, incidentID, agentID, recoveryArchiveField, refs[recoveryArchiveField], archiveTarget, archiveStored, archive, archiveOwned)
	if err != nil {
		return nil, fmt.Errorf("sessiondb: save recovery archive: %w", err)
	}
	recordActive, err := writeRecoveryListTx(tx, db, incidentID, agentID, recoveryActiveField, refs[recoveryActiveField], activeTarget, activeStored, active, activeOwned)
	if err != nil {
		return nil, fmt.Errorf("sessiondb: save recovery context: %w", err)
	}
	return func() { recordArchive(); recordActive() }, nil
}

// listBase is the base a write inserts; an update keeps the stored base
// unless the write rewrites it, and only a rewrite can create the row.
func listBase(write listWrite) []byte {
	if write.rewrite {
		return write.base
	}
	return emptyListBase
}

const recoveryCheckpointColumns = `incident_id, agent_id, generation, state, attempt, retry_at, created_at, updated_at,
		       summary, detail, action, pending_input_json, input_admitted,
		       archive_json, active_context_json, context_owner_id, context_revision,
		       context_generation, context_reason, owner_data`

// LoadRecoveryCheckpoint returns the latest unfinished incident for an agent.
func LoadRecoveryCheckpoint(db *sql.DB, sessionID, agentID string) (RecoveryCheckpoint, bool, error) {
	if db == nil || sessionID == "" || agentID == "" {
		return RecoveryCheckpoint{}, false, nil
	}
	checkpoints, err := loadRecoveryCheckpoints(db, `SELECT `+recoveryCheckpointColumns+`
		FROM agent_recoveries
		WHERE session_id=? AND agent_id=? AND state NOT IN (?, ?, ?, ?)
		ORDER BY updated_at DESC LIMIT 1
	`, sessionID, agentID, string(recovery.Recovered), string(recovery.Cancelled), string(recovery.Continued), string(recovery.Failed))
	if err != nil || len(checkpoints) == 0 {
		return RecoveryCheckpoint{}, false, err
	}
	return checkpoints[0], true, nil
}

// LoadRecoveryIncident returns an agent's incident in any state.
func LoadRecoveryIncident(db *sql.DB, sessionID, agentID, incidentID string) (RecoveryCheckpoint, bool, error) {
	if db == nil || sessionID == "" || agentID == "" || incidentID == "" {
		return RecoveryCheckpoint{}, false, nil
	}
	checkpoints, err := loadRecoveryCheckpoints(db, `SELECT `+recoveryCheckpointColumns+`
		FROM agent_recoveries WHERE session_id=? AND agent_id=? AND incident_id=?
	`, sessionID, agentID, incidentID)
	if err != nil || len(checkpoints) == 0 {
		return RecoveryCheckpoint{}, false, err
	}
	return checkpoints[0], true, nil
}

// LoadOpenRecoveryCheckpoints returns every unfinished incident for passive
// startup hydration. It never starts provider, tool, command, or child work.
func LoadOpenRecoveryCheckpoints(db *sql.DB, sessionID string) ([]RecoveryCheckpoint, error) {
	if db == nil || sessionID == "" {
		return nil, nil
	}
	checkpoints, err := loadRecoveryCheckpoints(db, `SELECT `+recoveryCheckpointColumns+`
		FROM agent_recoveries
		WHERE session_id=? AND state NOT IN (?, ?, ?, ?)
		ORDER BY updated_at, incident_id
	`, sessionID, string(recovery.Recovered), string(recovery.Cancelled), string(recovery.Continued), string(recovery.Failed))
	if err != nil {
		return nil, fmt.Errorf("sessiondb: load open recoveries: %w", err)
	}
	return checkpoints, nil
}

// RecoveryExecutionOwner identifies the source and policy of one unfinished
// run without loading its archive or active context.
type RecoveryExecutionOwner struct {
	IncidentID, AgentID, ExecutionID string
	Generation                       uint64
	OwnerData                        []byte
	PlanningCycle                    *string
}

func LoadOpenRecoveryExecutionOwners(db *sql.DB) ([]RecoveryExecutionOwner, error) {
	rows, err := db.Query(`SELECT incident_id, agent_id, generation, owner_data, pending_input_json
		FROM agent_recoveries WHERE state NOT IN (?, ?, ?, ?)`,
		string(recovery.Recovered), string(recovery.Cancelled), string(recovery.Continued), string(recovery.Failed))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var owners []RecoveryExecutionOwner
	for rows.Next() {
		var owner RecoveryExecutionOwner
		var pending []byte
		if err := rows.Scan(&owner.IncidentID, &owner.AgentID, &owner.Generation, &owner.OwnerData, &pending); err != nil {
			return nil, err
		}
		var input recoveryInput
		if err := json.Unmarshal(pending, &input); err != nil {
			return nil, fmt.Errorf("sessiondb: decode recovery input: %w", err)
		}
		if input.ControlTarget != nil {
			owner.ExecutionID = input.ControlTarget.ExecutionID
		}
		owner.PlanningCycle = input.PlanningCycle
		owners = append(owners, owner)
	}
	return owners, rows.Err()
}

// loadRecoveryCheckpoints reads checkpoints and their list tails in one
// transaction, so no checkpoint mixes a base with another write's tail.
func loadRecoveryCheckpoints(db *sql.DB, query string, args ...any) ([]RecoveryCheckpoint, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, err
	}
	type scanned struct {
		checkpoint      RecoveryCheckpoint
		archive, active []byte
	}
	var found []scanned
	for rows.Next() {
		var item scanned
		if item.checkpoint, item.archive, item.active, err = scanRecoveryCheckpoint(rows); err != nil {
			rows.Close()
			return nil, err
		}
		found = append(found, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make([]RecoveryCheckpoint, 0, len(found))
	for _, item := range found {
		checkpoint := item.checkpoint
		refs, err := recoveryListRefsTx(tx, checkpoint.Status.ID)
		if err != nil {
			return nil, err
		}
		if checkpoint.Archive, err = loadRecoveryListTx(tx, db, refs[recoveryArchiveField], recoveryArchiveField, item.archive); err != nil {
			return nil, err
		}
		if checkpoint.ActiveContext, err = loadRecoveryListTx(tx, db, refs[recoveryActiveField], recoveryActiveField, item.active); err != nil {
			return nil, err
		}
		result = append(result, checkpoint)
	}
	return result, tx.Commit()
}

// LoadRecoveryRecords is the hydrated transcript/UI contract.
func LoadRecoveryRecords(db *sql.DB, sessionID string) ([]recovery.Status, error) {
	return LoadRecoveryRecordsForOwner(db, sessionID, "")
}

// LoadRecoveryRecordsForOwner keeps child mirror incidents out of the root
// transcript. An empty owner selects all records for session-level inventory.
func LoadRecoveryRecordsForOwner(db *sql.DB, sessionID, contextOwnerID string) ([]recovery.Status, error) {
	if db == nil || sessionID == "" {
		return nil, nil
	}
	rows, err := db.Query(`
		SELECT incident_id, agent_id, generation, state, attempt, retry_at, created_at, updated_at,
		       summary, detail, action
		FROM agent_recoveries
		WHERE session_id=? AND state<>'' AND (?='' OR context_owner_id=?)
		ORDER BY created_at, incident_id
	`, sessionID, contextOwnerID, contextOwnerID)
	if err != nil {
		return nil, fmt.Errorf("sessiondb: load recovery records: %w", err)
	}
	defer rows.Close()
	var result []recovery.Status
	for rows.Next() {
		status, err := scanRecoveryStatus(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, status)
	}
	return result, rows.Err()
}

// InterruptActiveRecoveries truthfully converts only in-process automatic
// recovery into passive startup state. Attention incidents remain parked.
func InterruptActiveRecoveries(db *sql.DB, sessionID string) error {
	if db == nil || sessionID == "" {
		return nil
	}
	now := time.Now().UnixNano()
	_, err := db.Exec(`
		UPDATE agent_recoveries
		SET state=?, retry_at=0,
		    summary='Recovery interrupted by restart',
		    action='Continue to resume this unfinished run',
		    updated_at=?
		WHERE session_id=? AND (state='' OR state=?)
	`, string(recovery.Interrupted), now, sessionID, string(recovery.Recovering))
	if err != nil {
		return fmt.Errorf("sessiondb: interrupt active recoveries: %w", err)
	}
	return nil
}

// RebindRecoveryAgent moves a passive root incident to its newly allocated
// orchestrator identity without changing the incident or generation.
func RebindRecoveryAgent(db *sql.DB, sessionID, fromAgentID, toAgentID string) error {
	if db == nil || sessionID == "" || fromAgentID == "" || toAgentID == "" || fromAgentID == toAgentID {
		return nil
	}
	_, err := db.Exec(`
		UPDATE agent_recoveries SET agent_id=?, updated_at=?
		WHERE session_id=? AND agent_id=? AND state NOT IN (?, ?, ?, ?)
	`, toAgentID, time.Now().UnixNano(), sessionID, fromAgentID,
		string(recovery.Recovered), string(recovery.Cancelled), string(recovery.Continued), string(recovery.Failed))
	return err
}

// DeleteRecoveryCheckpoint removes a finished run's incident. The lists of a
// run that owns its context stay for its agent's next run to extend. A mirror
// (a child's incident copied into its parent's database, with no context
// owner) has no next run there, so its lists go with it. Lists that no
// incident holds and that are not the newest of their agent can no longer be
// taken over either. Both are removed here, when a run ends, rather than
// while a prompt is being sent.
func DeleteRecoveryCheckpoint(db *sql.DB, incidentID string) error {
	if db == nil || incidentID == "" {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var owner string
	err = tx.QueryRow(`SELECT context_owner_id FROM agent_recoveries WHERE incident_id=?`, incidentID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var mirrored []any
	if owner == "" {
		rows, err := tx.Query(`SELECT list_id FROM agent_recovery_lists WHERE incident_id=?`, incidentID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var listID int64
			if err := rows.Scan(&listID); err != nil {
				rows.Close()
				return err
			}
			mirrored = append(mirrored, listID)
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM agent_recoveries WHERE incident_id=?`, incidentID); err != nil {
		return err
	}
	for _, listID := range mirrored {
		if _, err := tx.Exec(`DELETE FROM agent_message_lists WHERE list_id=? AND list_id NOT IN (SELECT list_id FROM agent_recovery_lists)`, listID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM agent_message_lists WHERE list_id NOT IN (SELECT list_id FROM agent_recovery_lists)
		AND list_id NOT IN (SELECT max(list_id) FROM agent_message_lists GROUP BY agent_id, field)`); err != nil {
		return err
	}
	return tx.Commit()
}

type recoveryScanner interface {
	Scan(dest ...any) error
}

// scanRecoveryCheckpoint scans a checkpoint and its archive and active
// context bases, which the caller extends with their tail rows.
func scanRecoveryCheckpoint(scanner recoveryScanner) (RecoveryCheckpoint, []byte, []byte, error) {
	var checkpoint RecoveryCheckpoint
	var state string
	var retryAt int64
	var inputAdmitted int
	var startedAt, updatedAt int64
	var pending, archive, active []byte
	if err := scanner.Scan(
		&checkpoint.Status.ID, &checkpoint.Status.AgentID, &checkpoint.Status.Generation,
		&state, &checkpoint.Status.Attempt, &retryAt, &startedAt, &updatedAt,
		&checkpoint.Status.Summary, &checkpoint.Status.Detail, &checkpoint.Status.Action,
		&pending, &inputAdmitted, &archive, &active, &checkpoint.ContextOwnerID, &checkpoint.ContextRevision,
		&checkpoint.ContextGeneration, &checkpoint.ContextReason, &checkpoint.OwnerData,
	); err != nil {
		return RecoveryCheckpoint{}, nil, nil, err
	}
	checkpoint.Status.State = recovery.State(state)
	if retryAt > 0 {
		checkpoint.Status.RetryAt = time.Unix(0, retryAt)
	}
	checkpoint.Status.StartedAt = time.Unix(0, startedAt)
	checkpoint.Status.UpdatedAt = time.Unix(0, updatedAt)
	checkpoint.InputAdmitted = inputAdmitted != 0
	var input recoveryInput
	if err := json.Unmarshal(pending, &input); err != nil {
		return RecoveryCheckpoint{}, nil, nil, fmt.Errorf("sessiondb: decode recovery input: %w", err)
	}
	checkpoint.PendingInput, checkpoint.TerminalSettlement = input.UserInput, input.TerminalSettlement
	checkpoint.PlanningCycle = input.PlanningCycle
	checkpoint.InFlightTools = input.InFlightTools
	return checkpoint, archive, active, nil
}

func scanRecoveryStatus(scanner recoveryScanner) (recovery.Status, error) {
	var status recovery.Status
	var state string
	var retryAt, startedAt, updatedAt int64
	if err := scanner.Scan(&status.ID, &status.AgentID, &status.Generation, &state,
		&status.Attempt, &retryAt, &startedAt, &updatedAt,
		&status.Summary, &status.Detail, &status.Action); err != nil {
		return recovery.Status{}, err
	}
	status.State = recovery.State(state)
	if retryAt > 0 {
		status.RetryAt = time.Unix(0, retryAt)
	}
	status.StartedAt = time.Unix(0, startedAt)
	status.UpdatedAt = time.Unix(0, updatedAt)
	return status, nil
}

func recoveryBoolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
