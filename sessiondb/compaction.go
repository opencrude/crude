package sessiondb

import (
	"database/sql"
	"errors"
	"time"
)

const (
	CompactionPreparing   = "preparing"
	CompactionExtracting  = "extracting"
	CompactionRetrying    = "retrying"
	CompactionCommitting  = "committing"
	CompactionCompleted   = "completed"
	CompactionCancelled   = "cancelled"
	CompactionFailed      = "failed"
	CompactionInterrupted = "interrupted"
)

type CompactionRecord struct {
	OperationID     string
	SessionID       string
	AgentID         string
	Trigger         string
	Role            string
	ProviderID      string
	AccountID       string
	ModelID         string
	State           string
	StageName       string
	StageIndex      int
	CompletedStages int
	StageCount      int
	RetryAttempt    int
	RetryLimit      int
	BeforeTokens    int
	AfterTokens     int
	ContextWindow   int
	Error           string
	StartedAt       time.Time
	UpdatedAt       time.Time
	EndedAt         time.Time
}

func IsCompactionTerminal(state string) bool {
	switch state {
	case CompactionCompleted, CompactionCancelled, CompactionFailed, CompactionInterrupted:
		return true
	default:
		return false
	}
}

func SaveCompactionRecord(db *sql.DB, record CompactionRecord) error {
	if db == nil {
		return nil
	}
	if record.OperationID == "" || record.SessionID == "" || record.AgentID == "" {
		return errors.New("invalid compaction record identity")
	}
	if record.StartedAt.IsZero() {
		record.StartedAt = time.Now()
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = time.Now()
	}
	if record.StageCount == 0 {
		record.StageCount = 4
	}
	if record.RetryLimit == 0 {
		record.RetryLimit = 3
	}
	var ended any
	if !record.EndedAt.IsZero() {
		ended = record.EndedAt.UnixNano()
	} else if IsCompactionTerminal(record.State) {
		ended = record.UpdatedAt.UnixNano()
	}
	_, err := db.Exec(compactionUpsertSQL,
		record.OperationID, record.SessionID, record.AgentID, record.Trigger, record.Role, record.ProviderID, record.AccountID, record.ModelID,
		record.State, record.StageName, record.StageIndex, record.CompletedStages, record.StageCount,
		record.RetryAttempt, record.RetryLimit, record.BeforeTokens, record.AfterTokens,
		record.ContextWindow, record.Error, record.StartedAt.UnixNano(), record.UpdatedAt.UnixNano(), ended,
	)
	return err
}

func saveCompactionRecordTx(tx *sql.Tx, record CompactionRecord) error {
	if record.OperationID == "" || record.SessionID == "" || record.AgentID == "" {
		return errors.New("invalid compaction record identity")
	}
	var existingSession, existingAgent, existingTrigger, existingRole string
	err := tx.QueryRow(`SELECT session_id,agent_id,trigger_kind,agent_role FROM context_compactions WHERE operation_id=?`, record.OperationID).Scan(&existingSession, &existingAgent, &existingTrigger, &existingRole)
	if err == nil && (existingSession != record.SessionID || existingAgent != record.AgentID || existingTrigger != record.Trigger || existingRole != record.Role) {
		return errors.New("compaction record identity conflict")
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if record.StartedAt.IsZero() {
		record.StartedAt = time.Now()
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = time.Now()
	}
	if record.StageCount == 0 {
		record.StageCount = 4
	}
	if record.RetryLimit == 0 {
		record.RetryLimit = 3
	}
	var ended any
	if !record.EndedAt.IsZero() {
		ended = record.EndedAt.UnixNano()
	} else if IsCompactionTerminal(record.State) {
		ended = record.UpdatedAt.UnixNano()
	}
	_, err = tx.Exec(compactionUpsertSQL, record.OperationID, record.SessionID, record.AgentID,
		record.Trigger, record.Role, record.ProviderID, record.AccountID, record.ModelID, record.State,
		record.StageName, record.StageIndex, record.CompletedStages, record.StageCount, record.RetryAttempt,
		record.RetryLimit, record.BeforeTokens, record.AfterTokens, record.ContextWindow, record.Error,
		record.StartedAt.UnixNano(), record.UpdatedAt.UnixNano(), ended)
	return err
}

const compactionUpsertSQL = `
	INSERT INTO context_compactions (
		operation_id, session_id, agent_id, trigger_kind, agent_role, provider_id, account_id, model_id, state,
		stage_name, stage_index, completed_stages, stage_count, retry_attempt,
		retry_limit, before_tokens, after_tokens, context_window, error_text,
		started_at, updated_at, ended_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(operation_id) DO UPDATE SET
		provider_id = CASE WHEN excluded.provider_id != '' THEN excluded.provider_id ELSE context_compactions.provider_id END,
		account_id = CASE WHEN excluded.account_id != '' THEN excluded.account_id ELSE context_compactions.account_id END,
		model_id = CASE WHEN excluded.model_id != '' THEN excluded.model_id ELSE context_compactions.model_id END,
		state = excluded.state,
		stage_name = excluded.stage_name,
		stage_index = excluded.stage_index,
		completed_stages = excluded.completed_stages,
		stage_count = excluded.stage_count,
		retry_attempt = excluded.retry_attempt,
		retry_limit = excluded.retry_limit,
		before_tokens = CASE WHEN excluded.before_tokens > 0 THEN excluded.before_tokens ELSE context_compactions.before_tokens END,
		after_tokens = CASE WHEN excluded.after_tokens > 0 THEN excluded.after_tokens ELSE context_compactions.after_tokens END,
		context_window = CASE WHEN excluded.context_window > 0 THEN excluded.context_window ELSE context_compactions.context_window END,
		error_text = excluded.error_text,
		updated_at = excluded.updated_at,
		ended_at = excluded.ended_at`

func ListCompactionRecords(db *sql.DB, sessionID, agentID string) ([]CompactionRecord, error) {
	if db == nil {
		return nil, nil
	}
	rows, err := db.Query(`
		SELECT operation_id, session_id, agent_id, trigger_kind, agent_role, provider_id, account_id, model_id, state,
		       stage_name, stage_index, completed_stages, stage_count, retry_attempt,
		       retry_limit, before_tokens, after_tokens, context_window, error_text,
		       started_at, updated_at, ended_at
		FROM context_compactions
		WHERE session_id = ? AND (? = '' OR agent_id = ?)
		ORDER BY started_at, operation_id`, sessionID, agentID, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []CompactionRecord
	for rows.Next() {
		var record CompactionRecord
		var started, updated int64
		var ended sql.NullInt64
		if err := rows.Scan(
			&record.OperationID, &record.SessionID, &record.AgentID, &record.Trigger, &record.Role, &record.ProviderID, &record.AccountID, &record.ModelID, &record.State,
			&record.StageName, &record.StageIndex, &record.CompletedStages, &record.StageCount, &record.RetryAttempt,
			&record.RetryLimit, &record.BeforeTokens, &record.AfterTokens, &record.ContextWindow, &record.Error,
			&started, &updated, &ended,
		); err != nil {
			return nil, err
		}
		record.StartedAt = time.Unix(0, started)
		record.UpdatedAt = time.Unix(0, updated)
		if ended.Valid {
			record.EndedAt = time.Unix(0, ended.Int64)
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func InterruptRunningCompactions(db *sql.DB, sessionID string) error {
	if db == nil {
		return nil
	}
	now := time.Now().UnixNano()
	_, err := db.Exec(`
		UPDATE context_compactions
		SET state = ?, error_text = '', updated_at = ?, ended_at = ?
		WHERE session_id = ? AND state NOT IN (?, ?, ?, ?)`,
		CompactionInterrupted, now, now, sessionID,
		CompactionCompleted, CompactionCancelled, CompactionFailed, CompactionInterrupted,
	)
	return err
}

// BeginContextCompaction atomically makes the exact source context durable and
// records the nonterminal operation before any provider extraction begins.
func BeginContextCompaction(db *sql.DB, sessionID, agentID string, source ContextState, record CompactionRecord) error {
	if db == nil {
		return nil
	}
	_, publishErr := PublishContext(db, ContextPublication{SessionID: sessionID, AgentID: agentID, RuntimeAgentID: record.AgentID,
		ExpectedRevision: source.Revision, Mutation: ContextMutation{Replace: &ContextReplacement{
			Generation: source.Generation, Reason: source.Reason, Messages: source.Messages}},
		Compaction: &record, ArchivePrevious: true})
	return publishErr

}

// CommitContextCompaction atomically archives the exact previous active
// generation, installs the new checkpoint, and marks the operation complete.
func CommitContextCompaction(db *sql.DB, sessionID, agentID string, state ContextState, record CompactionRecord) error {
	if db == nil {
		return nil
	}
	record.State = CompactionCompleted
	_, publishErr := PublishContext(db, ContextPublication{SessionID: sessionID, AgentID: agentID, RuntimeAgentID: record.AgentID,
		ExpectedRevision: state.Revision, Mutation: ContextMutation{Replace: &ContextReplacement{
			Generation: state.Generation, Reason: state.Reason, Messages: state.Messages}},
		Compaction: &record, ArchivePrevious: true})
	return publishErr

}
