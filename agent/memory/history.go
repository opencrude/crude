package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// RecordTurn upserts one compact cross-session history row. Texts are
// truncated to keep the mirror small; full transcripts stay in the
// per-session databases; this index only has to make them findable.
func (st *Store) RecordTurn(ctx context.Context, turn TurnRecord) error {
	if turn.SessionID == "" || turn.Turn <= 0 {
		return fmt.Errorf("memory: turn record requires session id and turn number")
	}
	userText := TruncateBytes(Redact(strings.TrimSpace(turn.UserText)), maxTurnTextBytes)
	assistantText := TruncateBytes(Redact(strings.TrimSpace(turn.AssistantText)), maxTurnTextBytes)
	toolNames := TruncateBytes(Redact(strings.Join(turn.ToolNames, " ")), maxTurnTextBytes)
	files := TruncateBytes(Redact(strings.Join(turn.Files, " ")), maxTurnTextBytes)
	norm := NormalizeText(userText, assistantText, toolNames, files)
	createdAt := turn.CreatedAt
	if createdAt == 0 {
		createdAt = time.Now().UnixNano()
	}
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO memory_history_turns
			(session_id, turn, user_text, assistant_text, tool_names, files, norm, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id, turn) DO UPDATE SET
			user_text = excluded.user_text,
			assistant_text = excluded.assistant_text,
			tool_names = excluded.tool_names,
			files = excluded.files,
			norm = excluded.norm
	`, turn.SessionID, turn.Turn, userText, assistantText, toolNames, files, norm, createdAt)
	if err != nil {
		return fmt.Errorf("memory: record turn: %w", err)
	}
	for position, observation := range SanitizeObservations(turn.Observations) {
		if observation.SessionID != turn.SessionID || observation.Turn != turn.Turn {
			return fmt.Errorf("memory: observation origin does not match turn")
		}
		payload, err := json.Marshal(observation)
		if err != nil {
			return err
		}
		var existing string
		err = tx.QueryRowContext(ctx, `SELECT observation FROM memory_observations WHERE session_id=? AND turn=? AND position=?`, turn.SessionID, turn.Turn, position).Scan(&existing)
		if err == nil && existing != string(payload) {
			return fmt.Errorf("%w: turn observation changed", ErrExists)
		}
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == sql.ErrNoRows {
			if _, err := tx.ExecContext(ctx, `INSERT INTO memory_observations(session_id, turn, position, message_id, tool_call_id, observation) VALUES (?, ?, ?, ?, ?, ?)`,
				turn.SessionID, turn.Turn, position, observation.MessageID, observation.ToolCallID, string(payload)); err != nil {
				return err
			}
		}
		if observation.ToolCallID != "" && observation.Method != "" {
			if err := recordEpisodeTx(ctx, tx, episodeForObservation(userText, observation)); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (st *Store) ObservationsForSession(ctx context.Context, sessionID string, afterTurn, throughTurn int) ([]Observation, error) {
	rows, err := st.db.QueryContext(ctx, `SELECT observation FROM memory_observations
		WHERE session_id=? AND turn>? AND turn<=? ORDER BY turn, position LIMIT 288`, sessionID, afterTurn, throughTurn)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var observations []Observation
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var observation Observation
		if err := json.Unmarshal([]byte(payload), &observation); err != nil {
			return nil, err
		}
		observations = append(observations, observation)
	}
	return observations, rows.Err()
}

// TurnsForSession returns a session's mirrored turns after afterTurn,
// oldest first, forming the extraction pipeline's input window.
func (st *Store) TurnsForSession(
	ctx context.Context, sessionID string, afterTurn, limit int,
) ([]HistoryHit, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	rows, err := st.db.QueryContext(ctx, `
		SELECT session_id, turn, user_text, assistant_text, tool_names, files, created_at
		FROM memory_history_turns
		WHERE session_id = ? AND turn > ?
		ORDER BY turn ASC LIMIT ?
	`, sessionID, afterTurn, limit)
	if err != nil {
		return nil, fmt.Errorf("memory: turns for session: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanHistoryHits(rows)
}

// SessionTurnHeads returns the highest mirrored turn per session, used by
// the maintenance sweep to find sessions with unextracted turns.
func (st *Store) SessionTurnHeads(ctx context.Context) (map[string]int, error) {
	rows, err := st.db.QueryContext(ctx, `
		SELECT session_id, MAX(turn) FROM memory_history_turns GROUP BY session_id
	`)
	if err != nil {
		return nil, fmt.Errorf("memory: session turn heads: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]int)
	for rows.Next() {
		var sessionID string
		var head int
		if err := rows.Scan(&sessionID, &head); err != nil {
			return nil, fmt.Errorf("memory: scan turn head: %w", err)
		}
		out[sessionID] = head
	}
	return out, rows.Err()
}

// GetSessionSummary returns one session's summary; found=false when absent.
func (st *Store) GetSessionSummary(
	ctx context.Context, sessionID string,
) (SessionSummary, bool, error) {
	row := st.db.QueryRowContext(ctx, `
		SELECT session_id, title, summary, key_files, outcome,
		       turn_count, created_at, updated_at
		FROM memory_session_summaries WHERE session_id = ?
	`, sessionID)
	var s SessionSummary
	var keyFiles string
	err := row.Scan(&s.SessionID, &s.Title, &s.Summary, &keyFiles, &s.Outcome,
		&s.TurnCount, &s.CreatedAt, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		return SessionSummary{}, false, nil
	}
	if err != nil {
		return SessionSummary{}, false, fmt.Errorf("memory: get session summary: %w", err)
	}
	if keyFiles != "" {
		s.KeyFiles = strings.Fields(keyFiles)
	}
	return s, true, nil
}

// SearchHistory searches the cross-session turn mirror and session
// summaries. Both result sets are capped at limit.
func (st *Store) SearchHistory(
	ctx context.Context, query string, limit int,
) ([]HistoryHit, []SessionSummary, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil, fmt.Errorf("memory: history search requires a query")
	}

	var hits []HistoryHit
	var err error
	if st.ftsOK {
		if match := BuildMatchQuery(query); match != "" {
			hits, err = st.historyFTS(ctx, match, limit)
			if err != nil {
				_ = st.SetMeta(ctx, MetaFTSFault, "history query: "+err.Error())
				hits = nil
			}
		}
	}
	if hits == nil {
		hits, err = st.historyScan(ctx, query, limit)
		if err != nil {
			return nil, nil, err
		}
	}

	summaries, err := st.searchSummaries(ctx, query, limit)
	if err != nil {
		return nil, nil, err
	}
	return hits, summaries, nil
}

func (st *Store) historyFTS(ctx context.Context, match string, limit int) ([]HistoryHit, error) {
	rows, err := st.db.QueryContext(ctx, `
		SELECT t.session_id, t.turn, t.user_text, t.assistant_text,
		       t.tool_names, t.files, t.created_at
		FROM memory_history_fts
		JOIN memory_history_turns t ON t.id = memory_history_fts.rid
		WHERE memory_history_fts MATCH ?
		ORDER BY bm25(memory_history_fts, 0, 2.0, 1.0, 1.5, 2.0, 1.5)
		LIMIT ?
	`, match, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanHistoryHits(rows)
}

func (st *Store) historyScan(ctx context.Context, query string, limit int) ([]HistoryHit, error) {
	rows, err := st.db.QueryContext(ctx, `
		SELECT session_id, turn, user_text, assistant_text, tool_names, files, created_at
		FROM memory_history_turns
		ORDER BY created_at DESC LIMIT 512
	`)
	if err != nil {
		return nil, fmt.Errorf("memory: history scan: %w", err)
	}
	defer func() { _ = rows.Close() }()
	all, err := scanHistoryHits(rows)
	if err != nil {
		return nil, err
	}
	terms := strings.Fields(strings.ToLower(query))
	var out []HistoryHit
	for _, hit := range all {
		if len(out) >= limit {
			break
		}
		haystack := strings.ToLower(
			hit.UserText + " " + hit.AssistantText + " " + hit.ToolNames + " " + hit.Files)
		matched := false
		for _, term := range terms {
			if strings.Contains(haystack, term) {
				matched = true
				break
			}
		}
		if matched {
			out = append(out, hit)
		}
	}
	return out, nil
}

func scanHistoryHits(rows *sql.Rows) ([]HistoryHit, error) {
	var out []HistoryHit
	for rows.Next() {
		var hit HistoryHit
		if err := rows.Scan(
			&hit.SessionID, &hit.Turn, &hit.UserText, &hit.AssistantText,
			&hit.ToolNames, &hit.Files, &hit.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("memory: scan history hit: %w", err)
		}
		out = append(out, hit)
	}
	return out, rows.Err()
}

func (st *Store) searchSummaries(ctx context.Context, query string, limit int) ([]SessionSummary, error) {
	if st.ftsOK {
		if match := BuildMatchQuery(query); match != "" {
			rows, err := st.db.QueryContext(ctx, `
				SELECT s.session_id, s.title, s.summary, s.key_files, s.outcome,
				       s.turn_count, s.created_at, s.updated_at
				FROM memory_summaries_fts
				JOIN memory_session_summaries s
				     ON s.session_id = memory_summaries_fts.session_id
				WHERE memory_summaries_fts MATCH ?
				ORDER BY bm25(memory_summaries_fts, 0, 3.0, 1.0, 2.0, 1.0)
				LIMIT ?
			`, match, limit)
			if err == nil {
				defer func() { _ = rows.Close() }()
				return scanSummaries(rows)
			}
			_ = st.SetMeta(ctx, MetaFTSFault, "summary query: "+err.Error())
		}
	}
	rows, err := st.db.QueryContext(ctx, `
		SELECT session_id, title, summary, key_files, outcome,
		       turn_count, created_at, updated_at
		FROM memory_session_summaries
		ORDER BY updated_at DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("memory: summary scan: %w", err)
	}
	defer func() { _ = rows.Close() }()
	all, err := scanSummaries(rows)
	if err != nil {
		return nil, err
	}
	terms := strings.Fields(strings.ToLower(query))
	var out []SessionSummary
	for _, s := range all {
		haystack := strings.ToLower(s.Title + " " + s.Summary + " " + s.Outcome + " " +
			strings.Join(s.KeyFiles, " "))
		for _, term := range terms {
			if strings.Contains(haystack, term) {
				out = append(out, s)
				break
			}
		}
	}
	return out, nil
}

func scanSummaries(rows *sql.Rows) ([]SessionSummary, error) {
	var out []SessionSummary
	for rows.Next() {
		var s SessionSummary
		var keyFiles string
		if err := rows.Scan(
			&s.SessionID, &s.Title, &s.Summary, &keyFiles, &s.Outcome,
			&s.TurnCount, &s.CreatedAt, &s.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("memory: scan summary: %w", err)
		}
		if keyFiles != "" {
			s.KeyFiles = strings.Fields(keyFiles)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SaveSessionSummary upserts the durable summary for one session.
func (st *Store) SaveSessionSummary(ctx context.Context, summary SessionSummary) error {
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := saveSessionSummaryTx(ctx, tx, summary); err != nil {
		return err
	}
	return tx.Commit()
}

func saveSessionSummaryTx(ctx context.Context, tx *sql.Tx, summary SessionSummary) error {
	if summary.SessionID == "" {
		return fmt.Errorf("memory: session summary requires a session id")
	}
	summary.Title = TruncateBytes(Redact(strings.TrimSpace(summary.Title)), MaxTitleBytes)
	summary.Summary = TruncateBytes(Redact(strings.TrimSpace(summary.Summary)), maxTurnTextBytes)
	summary.Outcome = TruncateBytes(Redact(strings.TrimSpace(summary.Outcome)), 1024)
	for index, path := range summary.KeyFiles {
		summary.KeyFiles[index] = TruncateBytes(Redact(path), 512)
	}
	if len(summary.KeyFiles) > 16 {
		summary.KeyFiles = summary.KeyFiles[:16]
	}
	if summary.Summary == "" {
		return fmt.Errorf("memory: session summary text is required")
	}
	now := time.Now().UnixNano()
	if summary.CreatedAt == 0 {
		summary.CreatedAt = now
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO memory_session_summaries
			(session_id, title, summary, key_files, outcome, turn_count, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			title = excluded.title,
			summary = excluded.summary,
			key_files = excluded.key_files,
			outcome = excluded.outcome,
			turn_count = excluded.turn_count,
			updated_at = excluded.updated_at
	`, summary.SessionID, summary.Title, summary.Summary,
		strings.Join(summary.KeyFiles, " "), summary.Outcome, summary.TurnCount,
		summary.CreatedAt, now)
	if err != nil {
		return fmt.Errorf("memory: save session summary: %w", err)
	}
	return journalTx(ctx, tx, ActorAuto, "summary", "", summary.SessionID, TruncateBytes(summary.Title, 120))
}

// LatestSummaries returns the most recently updated session summaries,
// excluding the given session (the caller's own).
func (st *Store) LatestSummaries(
	ctx context.Context, excludeSessionID string, limit int,
) ([]SessionSummary, error) {
	if limit <= 0 || limit > 10 {
		limit = 2
	}
	rows, err := st.db.QueryContext(ctx, `
		SELECT session_id, title, summary, key_files, outcome,
		       turn_count, created_at, updated_at
		FROM memory_session_summaries
		WHERE session_id != ?
		ORDER BY updated_at DESC LIMIT ?
	`, excludeSessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("memory: latest summaries: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanSummaries(rows)
}
