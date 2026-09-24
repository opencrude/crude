package sessiondb

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type SessionMeta struct {
	ID              string
	ProjectPath     string
	ProjectName     string
	Model           string
	Provider        string
	AccountID       string
	CreatedAt       int64
	ClosedAt        sql.NullInt64
	MessageCount    int
	TurnCount       int
	RoundCount      int // derived assistant/model rounds; not stored in sessions
	FirstPrompt     sql.NullString
	LastPrompt      sql.NullString
	Summary         sql.NullString
	ParentSessionID sql.NullString
	AgentCategory   sql.NullString
	AgentName       sql.NullString
	Depth           int
}

func CountAssistantRounds(db *sql.DB) (int, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM messages WHERE role = 'assistant'`).Scan(&count)
	return count, err
}

func Create(db *sql.DB, meta SessionMeta) error {
	_, err := db.Exec(`
		INSERT INTO sessions (id, project_path, project_name, model, provider, account_id, created_at, message_count, turn_count, first_prompt, last_prompt, parent_session_id, agent_category, agent_name, depth)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, meta.ID, meta.ProjectPath, meta.ProjectName, meta.Model, meta.Provider, meta.AccountID,
		meta.CreatedAt, meta.MessageCount, meta.TurnCount,
		meta.FirstPrompt, meta.LastPrompt,
		meta.ParentSessionID, meta.AgentCategory, meta.AgentName, meta.Depth)
	if err != nil {
		return fmt.Errorf("sessiondb: create session: %w", err)
	}
	return nil
}

// UpdateConnection persists the main role's provider/account pair atomically.
func UpdateConnection(db *sql.DB, sessionID, providerID, accountID string) error {
	_, err := db.Exec(`UPDATE sessions SET provider=?, account_id=? WHERE id=?`, providerID, accountID, sessionID)
	return err
}

// UpdateProject rebinds a session's project metadata when a workspace-owned
// backend is created or resumed under an explicit root.
func UpdateProject(db *sql.DB, id, projectPath, projectName string) error {
	_, err := db.Exec(`UPDATE sessions SET project_path = ?, project_name = ? WHERE id = ?`, projectPath, projectName, id)
	return err
}

func CloseSession(db *sql.DB, sessionID string) error {
	_, err := db.Exec(`
		UPDATE sessions SET closed_at = ? WHERE id = ?
	`, time.Now().UnixNano(), sessionID)
	if err != nil {
		return fmt.Errorf("sessiondb: close session: %w", err)
	}
	return nil
}

func UpdateLastPrompt(db *sql.DB, sessionID, prompt string) error {
	if len(prompt) > 200 {
		prompt = prompt[:200]
	}
	_, err := db.Exec(`
		UPDATE sessions SET last_prompt = ? WHERE id = ?
	`, prompt, sessionID)
	if err != nil {
		return fmt.Errorf("sessiondb: update last prompt: %w", err)
	}
	return nil
}

func UpdateFirstPrompt(db *sql.DB, sessionID, prompt string) error {
	if len(prompt) > 200 {
		prompt = prompt[:200]
	}
	_, err := db.Exec(`
		UPDATE sessions SET first_prompt = ? WHERE id = ? AND first_prompt IS NULL
	`, prompt, sessionID)
	if err != nil {
		return fmt.Errorf("sessiondb: update first prompt: %w", err)
	}
	return nil
}

func GetMeta(db *sql.DB) (SessionMeta, error) {
	var m SessionMeta
	err := db.QueryRow(`
		SELECT id, project_path, project_name, model, provider, account_id,
		       created_at, closed_at, message_count, turn_count,
		       first_prompt, last_prompt, summary,
		       parent_session_id, agent_category, agent_name, depth
		FROM sessions
	`).Scan(&m.ID, &m.ProjectPath, &m.ProjectName, &m.Model, &m.Provider, &m.AccountID,
		&m.CreatedAt, &m.ClosedAt, &m.MessageCount, &m.TurnCount,
		&m.FirstPrompt, &m.LastPrompt, &m.Summary,
		&m.ParentSessionID, &m.AgentCategory, &m.AgentName, &m.Depth)
	if err != nil {
		return m, fmt.Errorf("sessiondb: get meta: %w", err)
	}
	return m, nil
}

// GetCumulativeCost derives the known cumulative USD amount from the
// authoritative request ledger. Requests with unavailable pricing are not
// silently treated as known free calls.
func GetCumulativeCost(db *sql.DB) (float64, error) {
	var totalCost float64
	err := db.QueryRow(`
		SELECT COALESCE(SUM(cost_usd), 0) FROM usage_requests WHERE cost_known=1
	`).Scan(&totalCost)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0.0, nil
		}
		return 0.0, fmt.Errorf("sessiondb: get cumulative cost: %w", err)
	}
	return totalCost, nil
}

type ListSessionsOpts struct {
	Limit  int
	Offset int
}

type dbFileWithModTime struct {
	path    string
	modTime time.Time
}

func ListSessionsPaginated(sessionsRoot string, opts ListSessionsOpts) ([]SessionMeta, int, error) {
	files, err := filepath.Glob(filepath.Join(sessionsRoot, "*.db"))
	if err != nil {
		return nil, 0, fmt.Errorf("sessiondb: list sessions glob: %w", err)
	}

	if len(files) == 0 {
		return nil, 0, nil
	}

	fileInfos := make([]dbFileWithModTime, 0, len(files))
	for _, f := range files {
		fi, err := os.Stat(f)
		if err != nil {
			continue
		}
		fileInfos = append(fileInfos, dbFileWithModTime{path: f, modTime: fi.ModTime()})
	}

	sort.Slice(fileInfos, func(i, j int) bool {
		return fileInfos[i].modTime.After(fileInfos[j].modTime)
	})

	var allMetas []SessionMeta
	for _, fi := range fileInfos {
		db, err := sql.Open("sqlite", fi.path+"?mode=ro&_journal_mode=WAL")
		if err != nil {
			continue
		}

		var m SessionMeta
		err = db.QueryRow(`
			SELECT id, project_path, project_name, model, provider, account_id,
			       created_at, closed_at, message_count, turn_count,
			       first_prompt, last_prompt, summary,
			       parent_session_id, agent_category, agent_name, depth
			FROM sessions
		`).Scan(&m.ID, &m.ProjectPath, &m.ProjectName, &m.Model, &m.Provider, &m.AccountID,
			&m.CreatedAt, &m.ClosedAt, &m.MessageCount, &m.TurnCount,
			&m.FirstPrompt, &m.LastPrompt, &m.Summary,
			&m.ParentSessionID, &m.AgentCategory, &m.AgentName, &m.Depth)
		db.Close()
		if err != nil {
			continue
		}
		if m.TurnCount == 0 {
			continue
		}
		allMetas = append(allMetas, m)
	}

	total := len(allMetas)

	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}

	start := opts.Offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}

	return allMetas[start:end], total, nil
}

func ListSessions(sessionsRoot string) ([]SessionMeta, error) {
	metas, _, err := ListSessionsPaginated(sessionsRoot, ListSessionsOpts{Limit: 0, Offset: 0})
	if err != nil {
		return nil, err
	}
	return metas, nil
}

func DeleteSession(sessionsRoot, sessionID string) error {
	dbPath := filepath.Join(sessionsRoot, sessionID+".db")
	if err := os.Remove(dbPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("sessiondb: delete db: %w", err)
	}
	dirPath := filepath.Join(sessionsRoot, sessionID)
	if err := os.RemoveAll(dirPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("sessiondb: delete dir: %w", err)
	}
	return nil
}

func CleanOldSessions(sessionsRoot string) {
	entries, err := os.ReadDir(sessionsRoot)
	if err != nil {
		return
	}

	cutoff := time.Now().Add(-30 * 24 * time.Hour)

	for _, entry := range entries {
		if entry.IsDir() {
			fi, err := entry.Info()
			if err != nil {
				continue
			}
			if fi.ModTime().Before(cutoff) {
				os.RemoveAll(filepath.Join(sessionsRoot, entry.Name()))
			}
			continue
		}
		if filepath.Ext(entry.Name()) == ".db" {
			fi, err := entry.Info()
			if err != nil {
				continue
			}
			if fi.ModTime().Before(cutoff) {
				os.Remove(filepath.Join(sessionsRoot, entry.Name()))
			}
		}
	}
}
