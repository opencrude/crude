package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"

	"crude/fileio"
	"crude/provider"
	"crude/sessiondb"
)

// ExecutionRetention checks local runtime owners without changing their state.
// In particular, terminal collaboration state alone does not prove teardown ended.
func (s *Session) ExecutionRetention(id, root string) error {
	s.admissionMu.Lock()
	for pending := range s.pendingAdmissions {
		if target := pending.request.input.ControlTarget; target != nil && target.ExecutionID == id {
			s.admissionMu.Unlock()
			return fmt.Errorf("prepared input retains execution; let its admission settle")
		}
	}
	s.admissionMu.Unlock()
	if s.asyncMgr != nil {
		for _, command := range s.asyncMgr.List() {
			if command.Status == fileio.AsyncRunning && pathInside(root, command.CWD) {
				return fmt.Errorf("running command %s uses the checkout", command.Handle)
			}
		}
	}
	if runner := s.SubAgentRunner(); runner != nil {
		for _, child := range runner.Manager().ActiveHandles() {
			if child.Spec.ExecutionID == id {
				return fmt.Errorf("child %s is still releasing or using the checkout", child.Spec.ID)
			}
		}
	}
	return ExecutionDatabaseRetention(s.db, id)
}

// ExecutionDatabaseRetention is a narrow read of recoveries, selected execution
// and queued input. It neither hydrates transcripts nor starts a session.
func ExecutionDatabaseRetention(db *sql.DB, id string) error {
	if db == nil {
		return fmt.Errorf("session database unavailable")
	}
	rows, err := db.Query(`SELECT incident_id,agent_id,generation,owner_data,pending_input_json FROM agent_recoveries WHERE state NOT IN ('recovered','cancelled','failed')`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var incident, agentID string
		var generation uint64
		var owner, pending []byte
		if err := rows.Scan(&incident, &agentID, &generation, &owner, &pending); err != nil {
			return err
		}
		var retained childRecoveryOwner
		if len(owner) != 0 && json.Unmarshal(owner, &retained) != nil {
			return fmt.Errorf("unreadable recovery owner")
		}
		var input provider.UserInput
		if len(pending) != 0 && json.Unmarshal(pending, &input) != nil {
			return fmt.Errorf("unreadable recovery input")
		}
		if retained.Spec.ExecutionID == id || input.ControlTarget != nil && input.ControlTarget.ExecutionID == id {
			return fmt.Errorf("open recovery retains execution: agent_id=%s recovery_id=%s recovery_generation=%d; inspect the owner, then explicitly continue or settle it before cleanup", agentID, incident, generation)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	var selected []byte
	err = db.QueryRow(`SELECT value FROM session_runtime_state WHERE key='execution_selection'`).Scan(&selected)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	var selection ExecutionSelection
	if len(selected) != 0 && json.Unmarshal(selected, &selection) != nil {
		return fmt.Errorf("unreadable execution selection")
	}
	if selection.ID == id {
		return fmt.Errorf("session still selects execution; clear execution.select first")
	}
	var queued []byte
	err = db.QueryRow(`SELECT value FROM session_runtime_state WHERE key='queued_inputs'`).Scan(&queued)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	var items []QueuedInput
	if len(queued) != 0 && json.Unmarshal(queued, &items) != nil {
		return fmt.Errorf("unreadable queued input")
	}
	for _, item := range items {
		if item.Input.ControlTarget != nil && item.Input.ControlTarget.ExecutionID == id {
			return fmt.Errorf("queued input retains execution")
		}
	}
	return nil
}

// CancelExecutionRecovery is a workspace janitorial decision for an exact parked
// task, not authority to stop live peers. The ordinary recovery owner performs
// the durable settlement, preserving transcript and archived effects.
func (s *Session) CancelExecutionRecovery(id, agentID, incident string, generation uint64) error {
	if id == "" || agentID == "" || incident == "" || generation == 0 {
		return fmt.Errorf("settlement requires exact execution, agent and recovery identities")
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.recoveryMu.Lock()
	checkpoint, ok := s.pendingRecoveries[agentID]
	parked := s.recoverySlots[agentID] == nil
	s.recoveryMu.Unlock()
	if !ok && parked {
		var err error
		checkpoint, err = s.adoptExecutionRecovery(id, agentID, incident, generation)
		if err != nil {
			return err
		}
		ok = true
	}
	if !ok || !parked || checkpoint.Status.ID != incident || checkpoint.Status.Generation != generation {
		return fmt.Errorf("exact parked recovery unavailable; inspect its owner again")
	}
	var owner childRecoveryOwner
	if len(checkpoint.OwnerData) != 0 && json.Unmarshal(checkpoint.OwnerData, &owner) != nil {
		return fmt.Errorf("unreadable recovery execution owner")
	}
	if owner.Spec.ExecutionID != id && (checkpoint.PendingInput.ControlTarget == nil || checkpoint.PendingInput.ControlTarget.ExecutionID != id) {
		return fmt.Errorf("recovery does not retain this execution")
	}
	return s.cancelRecoveryFor(agentID, incident, generation)
}

// Launch can commit the child's checkpoint before publishing its parent mirror.
// Recover that exact durable owner for explicit settlement, never by directory
// age or an offline label. The caller holds lifecycleMu through cancellation.
func (s *Session) adoptExecutionRecovery(id, agentID, incident string, generation uint64) (sessiondb.RecoveryCheckpoint, error) {
	fail := func() (sessiondb.RecoveryCheckpoint, error) {
		return sessiondb.RecoveryCheckpoint{}, fmt.Errorf("exact parked child recovery unavailable; inspect its owner again")
	}
	if agentID == "" || agentID == "." || agentID == ".." || filepath.Base(agentID) != agentID {
		return fail()
	}
	path := s.completedSubAgentDBPath(agentID)
	store := s.CollaborationStore()
	if path == "" || store == nil || s.db == nil {
		return fail()
	}
	if runner := s.SubAgentRunner(); runner != nil {
		for _, child := range runner.Manager().ActiveHandles() {
			if string(child.Spec.ID) == agentID {
				return fail()
			}
		}
	}
	db, err := sessiondb.OpenAndMigrate(path)
	if err != nil {
		return sessiondb.RecoveryCheckpoint{}, err
	}
	defer func() { _ = db.Close() }()
	checkpoint, open, err := sessiondb.LoadRecoveryCheckpoint(db, agentID, agentID)
	if err != nil {
		return sessiondb.RecoveryCheckpoint{}, err
	}
	var owner childRecoveryOwner
	if !open || checkpoint.Status.ID != incident || checkpoint.Status.Generation != generation || checkpoint.ContextOwnerID != agentID ||
		json.Unmarshal(checkpoint.OwnerData, &owner) != nil || string(owner.Spec.ID) != agentID || owner.Spec.ExecutionID != id || owner.Spec.ExecutionRunID == "" {
		return fail()
	}
	meta, err := sessiondb.GetMeta(db)
	if err != nil || meta.ID != agentID || meta.ParentSessionID.String != s.sessionID {
		return fail()
	}
	ctx := context.Background()
	child, err := store.Agent(ctx, agentID)
	if err != nil || child.SessionID != agentID || child.ParentID != string(owner.Spec.ParentID) || child.CurrentRunID != "" {
		return fail()
	}
	parent, err := store.Agent(ctx, child.ParentID)
	if err != nil || parent.SessionID != s.sessionID {
		return fail()
	}
	run, err := store.Run(ctx, owner.Spec.ExecutionRunID)
	if err != nil || run.AgentID != agentID || !run.State.Terminal() {
		return fail()
	}
	var recorded int
	if err := s.db.QueryRow(`SELECT count(*) FROM agent_recoveries WHERE incident_id=? OR (agent_id=? AND state NOT IN ('recovered','cancelled','failed'))`, incident, agentID).Scan(&recorded); err != nil {
		return sessiondb.RecoveryCheckpoint{}, err
	}
	if recorded != 0 {
		return fail() // Never replace later or already-settled parent evidence.
	}
	mirror := checkpoint
	mirror.ContextOwnerID = ""
	if err := sessiondb.SaveRecoveryCheckpoint(s.db, s.sessionID, mirror); err != nil {
		return sessiondb.RecoveryCheckpoint{}, err
	}
	s.recoveryMu.Lock()
	s.pendingRecoveries[agentID] = mirror
	s.recoveryMu.Unlock()
	return mirror, nil
}
