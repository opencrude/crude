package sessiondb

import "database/sql"

// AgentSettingOverride is one role's per-session agent override. A row with
// Inherit set ("main" or "workspace") is a live reference, not a value copy:
// provider/model/api_standard/thinking_effort stay empty and the role resolves
// through its inheritance chain at read time. Inherit empty means a manual
// override carrying explicit values.
type AgentSettingOverride struct {
	Provider    string
	AccountID   string
	Model       string
	APIStandard string
	// Inherit is the live-inheritance marker: "main" follows this session's
	// main agent, "workspace" follows the workspace's main agent.
	Inherit                  string
	ThinkingEffort           string
	ContextCompactionPercent *int
}

func LoadAgentSettingOverrides(db *sql.DB, sessionID string) (map[string]AgentSettingOverride, error) {
	rows, err := db.Query(`
		SELECT category, provider, account_id, model, api_standard, thinking_effort, context_compaction_percent, inherit_source
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
		var provider, account, model, standard, thinking, inherit sql.NullString
		var compact sql.NullInt64
		if err := rows.Scan(&category, &provider, &account, &model, &standard, &thinking, &compact, &inherit); err != nil {
			return nil, err
		}
		override := AgentSettingOverride{
			Provider: provider.String, AccountID: account.String, Model: model.String,
			APIStandard: standard.String, ThinkingEffort: thinking.String,
			Inherit: inherit.String,
		}
		if compact.Valid {
			value := int(compact.Int64)
			override.ContextCompactionPercent = &value
		}
		overrides[category] = override
	}
	return overrides, rows.Err()
}

func SaveAgentSettingOverrides(db *sql.DB, sessionID string, overrides map[string]AgentSettingOverride) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := saveAgentSettingOverrides(tx, sessionID, overrides); err != nil {
		return err
	}
	return tx.Commit()
}

func saveAgentSettingOverrides(tx *sql.Tx, sessionID string, overrides map[string]AgentSettingOverride) error {
	if _, err := tx.Exec(`DELETE FROM session_agent_settings WHERE session_id = ?`, sessionID); err != nil {
		return err
	}
	for category, override := range overrides {
		if override.Provider == "" && override.AccountID == "" && override.Model == "" &&
			override.APIStandard == "" && override.ThinkingEffort == "" &&
			override.ContextCompactionPercent == nil && override.Inherit == "" {
			continue
		}
		if _, err := tx.Exec(`
			INSERT INTO session_agent_settings
				(session_id, category, provider, account_id, model, api_standard, thinking_effort, context_compaction_percent, inherit_source)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, sessionID, category, override.Provider, override.AccountID, override.Model, override.APIStandard, override.ThinkingEffort, override.ContextCompactionPercent, override.Inherit); err != nil {
			return err
		}
	}
	return nil
}

// SaveSessionSettings commits the account identity, role pins and session-only
// choices together. The caller publishes its in-memory state after this commit.
func SaveSessionSettings(db *sql.DB, sessionID, providerID, accountID string, overrides map[string]AgentSettingOverride, flags SessionFlags) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := saveAgentSettingOverrides(tx, sessionID, overrides); err != nil {
		return err
	}
	if err := saveSessionFlags(tx, sessionID, flags); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE sessions SET provider=?, account_id=? WHERE id=?`, providerID, accountID, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

func ClearAgentSettingOverrides(db *sql.DB, sessionID string) error {
	_, err := db.Exec(`DELETE FROM session_agent_settings WHERE session_id = ?`, sessionID)
	return err
}
