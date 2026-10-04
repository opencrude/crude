package sessiondb

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type SubAgentResultRow struct {
	ID               int64
	ParentSessionID  string
	ChildSessionID   string
	AgentID          string
	AgentName        string
	AgentCategory    string
	Status           string
	SummaryGist      string
	ReportID         string
	ReportRevision   int
	SessionLogPath   string
	FilesRead        []string
	FilesWritten     []string
	Errors           []string
	PromptTokens     int
	CompletionTokens int
	CostUSD          float64
	DurationMs       int64
	CreatedAt        int64
}

func InsertSubAgentResult(db *sql.DB, r SubAgentResultRow) error {
	filesRead, _ := json.Marshal(r.FilesRead)
	filesWritten, _ := json.Marshal(r.FilesWritten)
	errors, _ := json.Marshal(r.Errors)
	_, err := db.Exec(`
		INSERT INTO sub_agent_results (parent_session_id, child_session_id, agent_id, agent_name, agent_category, status, summary_gist, report_id, report_revision, session_log_path, files_read, files_written, errors, prompt_tokens, completion_tokens, cost_usd, duration_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, r.ParentSessionID, r.ChildSessionID, r.AgentID, r.AgentName, r.AgentCategory, r.Status, r.SummaryGist, r.ReportID, r.ReportRevision, r.SessionLogPath, string(filesRead), string(filesWritten), string(errors), r.PromptTokens, r.CompletionTokens, r.CostUSD, r.DurationMs, r.CreatedAt)
	if err != nil {
		return fmt.Errorf("sessiondb: insert sub_agent_result: %w", err)
	}
	return nil
}

func ListSubAgentResults(db *sql.DB, parentSessionID string) ([]SubAgentResultRow, error) {
	rows, err := db.Query(`
		SELECT id, parent_session_id, child_session_id, agent_id, agent_name, agent_category, status, summary_gist, report_id, report_revision, session_log_path, files_read, files_written, errors, prompt_tokens, completion_tokens, cost_usd, duration_ms, created_at
		FROM sub_agent_results
		WHERE parent_session_id = ?
		ORDER BY created_at
	`, parentSessionID)
	if err != nil {
		return nil, fmt.Errorf("sessiondb: list sub_agent_results: %w", err)
	}
	defer rows.Close()

	var results []SubAgentResultRow
	for rows.Next() {
		var r SubAgentResultRow
		var filesRead, filesWritten, errorsJSON string
		err := rows.Scan(&r.ID, &r.ParentSessionID, &r.ChildSessionID, &r.AgentID, &r.AgentName, &r.AgentCategory, &r.Status, &r.SummaryGist, &r.ReportID, &r.ReportRevision, &r.SessionLogPath, &filesRead, &filesWritten, &errorsJSON, &r.PromptTokens, &r.CompletionTokens, &r.CostUSD, &r.DurationMs, &r.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("sessiondb: scan sub_agent_result: %w", err)
		}
		_ = json.Unmarshal([]byte(filesRead), &r.FilesRead)
		_ = json.Unmarshal([]byte(filesWritten), &r.FilesWritten)
		_ = json.Unmarshal([]byte(errorsJSON), &r.Errors)
		results = append(results, r)
	}
	return results, nil
}

func InsertFileClaim(db *sql.DB, sessionID, agentID, agentName, path string) error {
	_, err := db.Exec(`
		INSERT INTO file_claims (session_id, agent_id, agent_name, path, claimed_at)
		VALUES (?, ?, ?, ?, ?)
	`, sessionID, agentID, agentName, path, time.Now().UnixNano())
	if err != nil {
		return fmt.Errorf("sessiondb: insert file_claim: %w", err)
	}
	return nil
}

func ReleaseFileClaims(db *sql.DB, sessionID, agentID string) error {
	_, err := db.Exec(`
		UPDATE file_claims SET released_at = ? WHERE session_id = ? AND agent_id = ? AND released_at IS NULL
	`, time.Now().UnixNano(), sessionID, agentID)
	if err != nil {
		return fmt.Errorf("sessiondb: release file_claims: %w", err)
	}
	return nil
}
