package sessiondb

import "database/sql"

// AgentSettingOverride is one role's per-session agent override. A row with
// Inherit set ("main" or "workspace") is a live reference, not a value copy:
// provider/model/api_standard/thinking_effort stay empty and the role resolves
// through its inheritance chain at read time. Inherit empty means a manual
// override carrying explicit values.
type AgentSettingOverride struct {
	Provider    string
	Model       string
	APIStandard string
	// Inherit is the live-inheritance marker: "main" follows this session's
	// main agent, "workspace" follows the workspace's main agent.
	Inherit       string
	ThinkingEffort string
}

func LoadAgentSettingOverrides(db *sql.DB, sessionID string) (map[string]AgentSettingOverride, error) {
	rows, err := db.Query(`
		SELECT category, provider, model, api_standard, thinking_effort, inherit_source
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
		var provider, model, standard, thinking, inherit sql.NullString
		if err := rows.Scan(&category, &provider, &model, &standard, &thinking, &inherit); err != nil {
			return nil, err
		}
		overrides[category] = AgentSettingOverride{
			Provider: provider.String, Model: model.String,
			APIStandard: standard.String, ThinkingEffort: thinking.String,
			Inherit: inherit.String,
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
		if override.Provider == "" && override.Model == "" &&
			override.APIStandard == "" && override.ThinkingEffort == "" &&
			override.Inherit == "" {
			continue
		}
		if _, err := tx.Exec(`
			INSERT INTO session_agent_settings
				(session_id, category, provider, model, api_standard, thinking_effort, inherit_source)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, sessionID, category, override.Provider, override.Model, override.APIStandard, override.ThinkingEffort, override.Inherit); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func ClearAgentSettingOverrides(db *sql.DB, sessionID string) error {
	_, err := db.Exec(`DELETE FROM session_agent_settings WHERE session_id = ?`, sessionID)
	return err
}
