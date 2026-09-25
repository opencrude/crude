package sessiondb

import (
	"database/sql"
	"time"
)

const (
	TurnRunning     = "running"
	TurnCompleted   = "completed"
	TurnInterrupted = "interrupted"
	TurnFailed      = "failed"
)

func BeginAgentTurn(db *sql.DB, sessionID, agentID string, generation uint64, origin string) error {
	_, err := db.Exec(`
		INSERT INTO agent_turns(session_id, agent_id, generation, origin, state, started_at, ended_at)
		VALUES(?, ?, ?, ?, ?, ?, NULL)
		ON CONFLICT(session_id, agent_id, generation) DO UPDATE SET
			origin=excluded.origin, state=excluded.state, started_at=excluded.started_at, ended_at=NULL
	`, sessionID, agentID, generation, origin, TurnRunning, time.Now().UnixNano())
	return err
}

func FinishAgentTurn(db *sql.DB, sessionID, agentID string, generation uint64, state string) error {
	_, err := db.Exec(`
		UPDATE agent_turns SET state=?, ended_at=?
		WHERE session_id=? AND agent_id=? AND generation=?
	`, state, time.Now().UnixNano(), sessionID, agentID, generation)
	return err
}

func InterruptRunningTurns(db *sql.DB, sessionID string) error {
	_, err := db.Exec(`
		UPDATE agent_turns SET state=?, ended_at=? WHERE session_id=? AND state=?
	`, TurnInterrupted, time.Now().UnixNano(), sessionID, TurnRunning)
	return err
}

// AgentTurnGenerations includes stopped or failed requests with no usage report.
// Resume must not recycle those generations or move the displayed clock back.
func AgentTurnGenerations(db *sql.DB, sessionID string) (map[string]uint64, error) {
	rows, err := db.Query(`SELECT agent_id, MAX(generation) FROM agent_turns WHERE session_id=? GROUP BY agent_id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]uint64)
	for rows.Next() {
		var id string
		var generation uint64
		if err := rows.Scan(&id, &generation); err != nil {
			return nil, err
		}
		result[id] = generation
	}
	return result, rows.Err()
}
