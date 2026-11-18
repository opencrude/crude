package sessiondb

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// InspectionMessage is public evidence, not a provider message. In particular,
// reasoning, tool arguments/results, attachments and private context are absent.
type InspectionMessage struct {
	MessageID string    `json:"message_id"`
	Role      string    `json:"role"`
	Text      string    `json:"text,omitempty"`
	ToolName  string    `json:"tool_name,omitempty"`
	Status    string    `json:"status,omitempty"`
	Truncated bool      `json:"truncated,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type InspectionInput struct {
	MessageID string `json:"message_id"`
	RunID     string `json:"run_id,omitempty"`
	Origin    string `json:"origin"`
	Preview   string `json:"preview,omitempty"`
}

type AgentWorkInspection struct {
	Available  bool                `json:"available"`
	Scope      string              `json:"scope"`
	RunID      string              `json:"run_id,omitempty"`
	Note       string              `json:"note,omitempty"`
	Turn       int                 `json:"turn,omitempty"`
	Generation uint64              `json:"generation,omitempty"`
	Input      *InspectionInput    `json:"input,omitempty"`
	Messages   []InspectionMessage `json:"messages"`
	Truncated  bool                `json:"truncated"`
}

type InspectionRecovery struct {
	ID         string `json:"recovery_id"`
	Generation uint64 `json:"recovery_generation"`
	State      string `json:"state"`
	Summary    string `json:"summary,omitempty"`
	Action     string `json:"action,omitempty"`
}

func InspectAgentRecovery(ctx context.Context, db *sql.DB, sessionID, agentID string) (*InspectionRecovery, error) {
	var result InspectionRecovery
	err := db.QueryRowContext(ctx, `SELECT incident_id,generation,state,substr(summary,1,1000),substr(action,1,1000)
		FROM agent_recoveries WHERE session_id=? AND agent_id=?
		AND (state IN ('needs_attention','interrupted') OR (state='recovering' AND retry_at>0))
		ORDER BY updated_at DESC LIMIT 1`, sessionID, agentID).
		Scan(&result.ID, &result.Generation, &result.State, &result.Summary, &result.Action)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// InspectAgentWork reads the newest persisted turn only. It does not restore a
// session, load its active context or rewrite recovery and admission records.
// The explicit scope avoids claiming buffered live work has already persisted.
func InspectAgentWork(ctx context.Context, db *sql.DB, agentID, transcriptOwnerID string) (AgentWorkInspection, error) {
	result := AgentWorkInspection{Scope: "latest_persisted_turn", Messages: []InspectionMessage{}, Note: "Persisted public evidence only; active buffered work may not be published yet."}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	var turn sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT MAX(turn_number) FROM messages WHERE agent_id IN (?,?)`, agentID, transcriptOwnerID).Scan(&turn); err != nil {
		return result, err
	}
	if !turn.Valid {
		result.Note = "No persisted transcript for this agent."
		return result, nil
	}
	result.Available, result.Turn = true, int(turn.Int64)
	var generation sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT MAX(json_extract(display_meta,'$.turn_generation'))
		FROM messages WHERE agent_id IN (?,?) AND turn_number=?`, agentID, transcriptOwnerID, result.Turn).Scan(&generation); err != nil {
		return result, err
	}
	result.Generation = uint64(max(0, generation.Int64))
	var input InspectionInput
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(internal_id,''),origin,
		CASE WHEN visibility='conversation' AND COALESCE(json_extract(display_meta,'$.context_only'),0)=0
		THEN COALESCE(substr(json_extract(display_meta,'$.snapshots[0].content'),1,1000),'') ELSE '' END
		FROM messages WHERE agent_id IN (?,?) AND turn_number=? AND role='user'
		AND origin IN ('human','peer','assignment') ORDER BY sequence_num LIMIT 1`, agentID, transcriptOwnerID, result.Turn).
		Scan(&input.MessageID, &input.Origin, &input.Preview)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	if err == nil {
		result.Input = &input
		// A collaboration admission links an input to the actual attempt. A
		// nearby timestamp or the most recently received event is not that link.
		if err := tx.QueryRowContext(ctx, `SELECT run_id FROM collaboration_admissions
			WHERE agent_id=? AND event_id=? ORDER BY admitted_at LIMIT 1`, agentID, input.MessageID).Scan(&input.RunID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return result, err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT COALESCE(m.internal_id,''),json_extract(s.value,'$.role'),
		CASE WHEN json_extract(s.value,'$.role')='assistant' THEN COALESCE(substr(json_extract(s.value,'$.content'),1,1200),'') ELSE '' END,
		COALESCE(json_extract(s.value,'$.tool_name'),''),COALESCE(json_extract(s.value,'$.tool_status'),''),m.created_at,
		(json_extract(s.value,'$.role')='assistant' AND length(COALESCE(json_extract(s.value,'$.content'),''))>1200)
		FROM messages m JOIN json_each(m.display_meta,'$.snapshots') s
		WHERE m.agent_id IN (?,?) AND m.turn_number=? AND m.visibility='conversation'
		AND COALESCE(json_extract(m.display_meta,'$.context_only'),0)=0
		AND json_extract(s.value,'$.role') IN ('assistant','tool')
		ORDER BY m.sequence_num DESC,s.key DESC LIMIT 13`, agentID, transcriptOwnerID, result.Turn)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var message InspectionMessage
		var created int64
		if err := rows.Scan(&message.MessageID, &message.Role, &message.Text, &message.ToolName, &message.Status, &created, &message.Truncated); err != nil {
			return result, err
		}
		message.CreatedAt = time.Unix(0, created).UTC()
		result.Messages = append(result.Messages, message)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	result.Truncated = len(result.Messages) > 12
	if result.Truncated {
		result.Messages = result.Messages[:12]
	}
	for left, right := 0, len(result.Messages)-1; left < right; left, right = left+1, right-1 {
		result.Messages[left], result.Messages[right] = result.Messages[right], result.Messages[left]
	}
	return result, nil
}
