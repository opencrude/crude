package sessiondb

import "database/sql"

// AgentSettingOverride is the session-scoped subset that may override a
// workspace agent default. Empty fields inherit from the workspace.
type AgentSettingOverride struct {
	Provider       string
	Model          string
	ThinkingEffort string
}

func LoadAgentSettingOverrides(db *sql.DB, sessionID string) (map[string]AgentSettingOverride, error) {
	rows, err := db.Query(`
		SELECT category, provider, model, thinking_effort
		FROM session_agent_settings
		WHERE session_id = ?
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	overrides := make(map[string]AgentSettingOverride)
	for rows.Next() {
		var category string
		var provider, model, thinking sql.NullString
		if err := rows.Scan(&category, &provider, &model, &thinking); err != nil {
			return nil, err
		}
		overrides[category] = AgentSettingOverride{
			Provider: provider.String, Model: model.String, ThinkingEffort: thinking.String,
		}
	}
	return overrides, rows.Err()
}

func SaveAgentSettingOverrides(db *sql.DB, sessionID string, overrides map[string]AgentSettingOverride) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM session_agent_settings WHERE session_id = ?`, sessionID); err != nil {
		return err
	}
	for category, override := range overrides {
		if override.Provider == "" && override.Model == "" && override.ThinkingEffort == "" {
			continue
		}
		if _, err := tx.Exec(`
			INSERT INTO session_agent_settings
				(session_id, category, provider, model, thinking_effort)
			VALUES (?, ?, ?, ?, ?)
		`, sessionID, category, override.Provider, override.Model, override.ThinkingEffort); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func ClearAgentSettingOverrides(db *sql.DB, sessionID string) error {
	_, err := db.Exec(`DELETE FROM session_agent_settings WHERE session_id = ?`, sessionID)
	return err
}
