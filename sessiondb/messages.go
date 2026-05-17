package sessiondb

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"crude/provider"
)

type MessageRow struct {
	Role             string
	Content          sql.NullString
	ReasoningContent sql.NullString
	Reasoning        sql.NullString
	ToolCalls        sql.NullString
	ToolName         sql.NullString
	ToolResult       sql.NullString
	ToolCallID       sql.NullString
	CacheControl     sql.NullString
	TokenCount       int
	SequenceNum      int
	TurnNumber       int
	AgentID          sql.NullString
	CreatedAt        int64
}

func ReplaceMessages(db *sql.DB, sessionID string, msgs []provider.ChatMessage, turnNumbers []int) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("sessiondb: replace messages begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM messages"); err != nil {
		return 0, fmt.Errorf("sessiondb: replace messages delete: %w", err)
	}

	maxTurn := 0
	for _, t := range turnNumbers {
		if t > maxTurn {
			maxTurn = t
		}
	}

	if len(msgs) == 0 {
		if _, err := tx.Exec(`
			UPDATE sessions SET message_count = 0, turn_count = ? WHERE id = ?
		`, maxTurn, sessionID); err != nil {
			return 0, fmt.Errorf("sessiondb: replace messages update session: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return 0, fmt.Errorf("sessiondb: replace messages commit: %w", err)
		}
		return 0, nil
	}

	stmt, err := tx.Prepare(`
		INSERT INTO messages (
			role, content, reasoning_content, reasoning,
			tool_calls, tool_name, tool_result, tool_call_id,
			cache_control, token_count, sequence_num, turn_number,
			agent_id, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return 0, fmt.Errorf("sessiondb: replace messages prepare: %w", err)
	}
	defer stmt.Close()

	now := time.Now().UnixNano()

	for i, m := range msgs {
		var toolCallsJSON []byte
		if len(m.ToolCalls) > 0 {
			toolCallsJSON, _ = json.Marshal(m.ToolCalls)
		}

		var cacheControlJSON []byte
		if m.CacheControl != nil {
			cacheControlJSON, _ = json.Marshal(m.CacheControl)
		}

		content := m.Content
		toolResult := ""
		if m.Role == "tool" {
			toolResult = m.Content
			content = ""
		}

		turn := 0
		if i < len(turnNumbers) {
			turn = turnNumbers[i]
		}

		_, err := stmt.Exec(
			m.Role,
			nullString(content),
			nullString(m.ReasoningContent),
			nullString(m.Reasoning),
			nullBytes(toolCallsJSON),
			nullString(""),
			nullString(toolResult),
			nullString(m.ToolCallID),
			nullBytes(cacheControlJSON),
			0,
			i,
			turn,
			nil,
			now,
		)
		if err != nil {
			return 0, fmt.Errorf("sessiondb: replace messages exec seq=%d: %w", i, err)
		}
	}

	msgCount := len(msgs)
	_, err = tx.Exec(`
		UPDATE sessions SET message_count = ?, turn_count = ? WHERE id = ?
	`, msgCount, maxTurn, sessionID)
	if err != nil {
		return 0, fmt.Errorf("sessiondb: replace messages update session: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("sessiondb: replace messages commit: %w", err)
	}

	return msgCount, nil
}

func LoadMessages(db *sql.DB) ([]provider.ChatMessage, []int, error) {
	rows, err := db.Query(`
		SELECT role, content, reasoning_content, reasoning,
		       tool_calls, tool_name, tool_result, tool_call_id,
		       cache_control, token_count, sequence_num, turn_number,
		       agent_id, created_at
		FROM messages ORDER BY sequence_num
	`)
	if err != nil {
		return nil, nil, fmt.Errorf("sessiondb: load messages: %w", err)
	}
	defer rows.Close()

	var msgs []provider.ChatMessage
	var turns []int
	for rows.Next() {
		var r MessageRow
		err := rows.Scan(
			&r.Role, &r.Content, &r.ReasoningContent, &r.Reasoning,
			&r.ToolCalls, &r.ToolName, &r.ToolResult, &r.ToolCallID,
			&r.CacheControl, &r.TokenCount, &r.SequenceNum, &r.TurnNumber,
			&r.AgentID, &r.CreatedAt,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("sessiondb: load messages scan: %w", err)
		}

		m := provider.ChatMessage{
			Role:    r.Role,
			Content: r.Content.String,
		}

		if r.ReasoningContent.Valid {
			m.ReasoningContent = r.ReasoningContent.String
		}
		if r.Reasoning.Valid {
			m.Reasoning = r.Reasoning.String
		}
		if r.ToolCallID.Valid {
			m.ToolCallID = r.ToolCallID.String
		}
		if r.ToolCalls.Valid && r.ToolCalls.String != "" {
			json.Unmarshal([]byte(r.ToolCalls.String), &m.ToolCalls)
		}
		if r.CacheControl.Valid && r.CacheControl.String != "" {
			var cc provider.CacheControl
			if json.Unmarshal([]byte(r.CacheControl.String), &cc) == nil {
				m.CacheControl = &cc
			}
		}
		if m.Role == "tool" && r.ToolResult.Valid {
			m.Content = r.ToolResult.String
		}

		msgs = append(msgs, m)
		turns = append(turns, r.TurnNumber)
	}

	return msgs, turns, nil
}

func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func nullBytes(b []byte) sql.NullString {
	if len(b) == 0 {
		return sql.NullString{}
	}
	return sql.NullString{String: string(b), Valid: true}
}
