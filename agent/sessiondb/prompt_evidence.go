package sessiondb

import (
	"context"
	"database/sql"
	"errors"
	"time"
	"unicode/utf8"

	"crude/types/accounting"
)

// HistoricalPrompt carries public authored text, not the provider's enriched
// user message. A model comes from completed normal requests for its turn.
type HistoricalPrompt struct {
	ID             string        `json:"prompt_id"`
	Origin         string        `json:"origin"`
	CreatedAt      time.Time     `json:"created_at"`
	Turn           int           `json:"turn"`
	Generation     uint64        `json:"turn_generation,omitempty"`
	Text           string        `json:"text"`
	TextAvailable  bool          `json:"text_available"`
	TextBytes      int64         `json:"text_bytes"`
	TextOffset     int64         `json:"text_offset"`
	NextTextOffset *int64        `json:"next_text_offset,omitempty"`
	Models         []PromptModel `json:"models"`
	ModelsOmitted  bool          `json:"models_omitted,omitempty"`
}

type PromptModel struct {
	ID                 string `json:"model_id"`
	Provider           string `json:"provider_id"`
	SuccessfulRequests int    `json:"successful_requests"`
}

const humanPromptRows = `m.agent_id IN (?,?) AND m.role='user' AND m.origin IN ('human','human_steering')
	AND m.visibility='conversation' AND COALESCE(json_extract(m.display_meta,'$.context_only'),0)=0`

const successfulPromptModels = `u.session_id=? AND u.agent_id=? AND u.turn_generation=CAST(json_extract(m.display_meta,'$.turn_generation') AS TEXT)
	AND u.request_kind='normal' AND u.request_outcome=? AND u.model_id!=''`

type HistoricalPromptQuery struct {
	PromptID      string
	Model         string
	FirstForModel bool
	Offset        int64
	MaxBytes      int
}

// RecentPromptIDs pages through the same public human transcript as the exact
// text reader. Use the message row ID as a keyset cursor, not a text offset.
func RecentPromptIDs(ctx context.Context, db *sql.DB, agent, transcript string, before int64, limit int) ([]string, int64, error) {
	if before < 0 || limit < 1 || limit > 32 {
		return nil, 0, errors.New("invalid prompt page")
	}
	rows, err := db.QueryContext(ctx, `SELECT m.id, m.internal_id FROM messages m WHERE `+humanPromptRows+
		` AND m.internal_id != '' AND (?=0 OR m.id<?) ORDER BY m.id DESC LIMIT ?`, agent, transcript, before, before, limit+1)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var ids []string
	var last int64
	for rows.Next() {
		if len(ids) == limit {
			return ids, last, nil
		}
		var id string
		if err := rows.Scan(&last, &id); err != nil {
			return nil, 0, err
		}
		ids = append(ids, id)
	}
	return ids, 0, rows.Err()
}

// InspectHistoricalPrompt selects the first human input, the first human-led
// turn with a matching recorded model, or one exact message by ID. An agent
// alias, configured model, assignment, background request, or nearby timestamp
// is not evidence that a particular model answered this prompt.
func InspectHistoricalPrompt(ctx context.Context, db *sql.DB, sessionID, agentID, transcriptOwner string, q HistoricalPromptQuery) (*HistoricalPrompt, error) {
	offset, maxBytes := q.Offset, q.MaxBytes
	if offset < 0 || offset >= 1<<32 || maxBytes < utf8.UTFMax || maxBytes > 8192 {
		return nil, errors.New("invalid historical prompt range")
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	condition := humanPromptRows
	args := []any{offset + 1, maxBytes + utf8.UTFMax - 1, agentID, transcriptOwner}
	if q.PromptID != "" {
		condition += ` AND m.internal_id=?`
		args = append(args, q.PromptID)
	} else if q.FirstForModel {
		condition += ` AND m.origin='human' AND EXISTS(SELECT 1 FROM usage_requests u WHERE ` + successfulPromptModels + ` AND instr(lower(u.model_id),lower(?))>0)`
		args = append(args, sessionID, agentID, accounting.Succeeded, q.Model)
	}
	var prompt HistoricalPrompt
	var created int64
	var rawText sql.NullString
	const textColumn = `CAST(json_extract(m.display_meta,'$.snapshots[0].content') AS BLOB)`
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(m.internal_id,''),m.origin,m.created_at,m.turn_number,
		CAST(COALESCE(json_extract(m.display_meta,'$.turn_generation'),0) AS TEXT),
		substr(`+textColumn+`,?,?), COALESCE(length(`+textColumn+`),0)
		FROM messages m WHERE `+condition+` ORDER BY m.sequence_num,m.id LIMIT 1`, args...).Scan(
		&prompt.ID, &prompt.Origin, &created, &prompt.Turn, &prompt.Generation, &rawText, &prompt.TextBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if offset > prompt.TextBytes {
		return nil, errors.New("text offset is after the end of this prompt")
	}
	prompt.CreatedAt = time.Unix(0, created).UTC()
	end := min(len(rawText.String), maxBytes)
	for end < len(rawText.String) && !utf8.RuneStart(rawText.String[end]) {
		end--
	}
	if !utf8.ValidString(rawText.String[:end]) {
		return nil, errors.New("text offset must start at a UTF-8 character boundary; use next_text_offset")
	}
	prompt.Text, prompt.TextOffset, prompt.TextAvailable = rawText.String[:end], offset, rawText.Valid
	if next := offset + int64(end); next < prompt.TextBytes {
		prompt.NextTextOffset = &next
	}
	prompt.Models = []PromptModel{}
	// A steering message was admitted into an existing turn. Generation alone
	// cannot prove a model saw it, so report its text without a model link.
	if prompt.Origin != "human" || prompt.Generation == 0 {
		return &prompt, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT model_id, provider_id, COUNT(*) FROM usage_requests
		WHERE session_id=? AND agent_id=? AND turn_generation=? AND request_kind='normal' AND request_outcome=? AND model_id!=''
		GROUP BY model_id,provider_id ORDER BY instr(lower(model_id),lower(?))>0 DESC, MIN(created_at), model_id,provider_id LIMIT 9`,
		sessionID, agentID, prompt.Generation, accounting.Succeeded, q.Model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		if len(prompt.Models) == 8 {
			prompt.ModelsOmitted = true
			break
		}
		var m PromptModel
		if err := rows.Scan(&m.ID, &m.Provider, &m.SuccessfulRequests); err != nil {
			return nil, err
		}
		prompt.Models = append(prompt.Models, m)
	}
	return &prompt, rows.Err()
}
