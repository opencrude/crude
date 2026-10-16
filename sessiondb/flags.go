package sessiondb

import "database/sql"

// SessionFlags holds session-scoped settings that are not per-role overlays.
// A nil MultiAgent or CodeIntelligence means inherit the workspace default.
type SessionFlags struct {
	MultiAgent       *bool
	MainAgent        string
	CodeIntelligence *bool
	MaxSubAgents     *int
}

func LoadSessionFlags(db *sql.DB, sessionID string) (SessionFlags, error) {
	var flags SessionFlags
	var multi, code, maxAgents sql.NullInt64
	var mainAgent string
	err := db.QueryRow(`
		SELECT multi_agent, main_agent, code_intelligence, max_sub_agents FROM session_flags WHERE session_id = ?
	`, sessionID).Scan(&multi, &mainAgent, &code, &maxAgents)
	if err == sql.ErrNoRows {
		return flags, nil
	}
	if err != nil {
		return flags, err
	}
	if multi.Valid {
		enabled := multi.Int64 != 0
		flags.MultiAgent = &enabled
	}
	if code.Valid {
		enabled := code.Int64 != 0
		flags.CodeIntelligence = &enabled
	}
	if maxAgents.Valid {
		value := int(maxAgents.Int64)
		flags.MaxSubAgents = &value
	}
	flags.MainAgent = mainAgent
	return flags, nil
}

func SaveSessionFlags(db *sql.DB, sessionID string, flags SessionFlags) error {
	return saveSessionFlags(db, sessionID, flags)
}

func saveSessionFlags(db interface {
	Exec(string, ...any) (sql.Result, error)
}, sessionID string, flags SessionFlags) error {
	if flags.MultiAgent == nil && flags.MainAgent == "" && flags.CodeIntelligence == nil && flags.MaxSubAgents == nil {
		_, err := db.Exec(`DELETE FROM session_flags WHERE session_id = ?`, sessionID)
		return err
	}
	_, err := db.Exec(`
		INSERT INTO session_flags (session_id, multi_agent, main_agent, code_intelligence, max_sub_agents)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET multi_agent = excluded.multi_agent, main_agent = excluded.main_agent,
			code_intelligence = excluded.code_intelligence, max_sub_agents = excluded.max_sub_agents
	`, sessionID, nullableFlag(flags.MultiAgent), flags.MainAgent, nullableFlag(flags.CodeIntelligence), nullableInt(flags.MaxSubAgents))
	return err
}

func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableFlag(value *bool) any {
	switch {
	case value == nil:
		return nil
	case *value:
		return 1
	default:
		return 0
	}
}

func ClearSessionFlags(db *sql.DB, sessionID string) error {
	_, err := db.Exec(`DELETE FROM session_flags WHERE session_id = ?`, sessionID)
	return err
}

// LoadDisabledCapabilities returns the sorted capability IDs this session has
// turned off.
func LoadDisabledCapabilities(db *sql.DB, sessionID string) ([]string, error) {
	rows, err := db.Query(`SELECT capability_id FROM session_capabilities WHERE session_id = ? ORDER BY capability_id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SaveDisabledCapabilities replaces the session's complete disabled set.
func SaveDisabledCapabilities(db *sql.DB, sessionID string, ids []string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM session_capabilities WHERE session_id = ?`, sessionID); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := tx.Exec(`INSERT INTO session_capabilities (session_id, capability_id) VALUES (?, ?)`, sessionID, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
