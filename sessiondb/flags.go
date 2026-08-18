package sessiondb

import "database/sql"

// SessionFlags holds session-scoped settings that are not per-role overlays.
// A nil MultiAgent means inherit the workspace/profile default.
type SessionFlags struct {
	MultiAgent *bool
}

func LoadSessionFlags(db *sql.DB, sessionID string) (SessionFlags, error) {
	var flags SessionFlags
	var multi sql.NullInt64
	err := db.QueryRow(`
		SELECT multi_agent FROM session_flags WHERE session_id = ?
	`, sessionID).Scan(&multi)
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
	return flags, nil
}

func SaveSessionFlags(db *sql.DB, sessionID string, flags SessionFlags) error {
	if flags.MultiAgent == nil {
		return ClearSessionFlags(db, sessionID)
	}
	value := 0
	if *flags.MultiAgent {
		value = 1
	}
	_, err := db.Exec(`
		INSERT INTO session_flags (session_id, multi_agent)
		VALUES (?, ?)
		ON CONFLICT(session_id) DO UPDATE SET multi_agent = excluded.multi_agent
	`, sessionID, value)
	return err
}

func ClearSessionFlags(db *sql.DB, sessionID string) error {
	_, err := db.Exec(`DELETE FROM session_flags WHERE session_id = ?`, sessionID)
	return err
}
