package sessiondb

import (
	"database/sql"
	"fmt"
	"time"
)

// StatusRow is a durable transcript line for a change the user made to the
// session: Plan mode, YOLO or the goal.
type StatusRow struct {
	ID        string
	Phase     string
	Content   string
	CreatedAt time.Time
}

func AppendStatusRow(db *sql.DB, row StatusRow) error {
	if db == nil {
		return nil
	}
	if _, err := db.Exec(`INSERT INTO session_status_rows (id, phase, content, created_at) VALUES (?, ?, ?, ?)`,
		row.ID, row.Phase, row.Content, row.CreatedAt.UnixNano()); err != nil {
		return fmt.Errorf("sessiondb: append status row: %w", err)
	}
	return nil
}

// ListStatusRows returns the rows in the order they were recorded.
func ListStatusRows(db *sql.DB) ([]StatusRow, error) {
	if db == nil {
		return nil, nil
	}
	rows, err := db.Query(`SELECT id, phase, content, created_at FROM session_status_rows ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("sessiondb: list status rows: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []StatusRow
	for rows.Next() {
		var row StatusRow
		var created int64
		if err := rows.Scan(&row.ID, &row.Phase, &row.Content, &created); err != nil {
			return nil, fmt.Errorf("sessiondb: scan status row: %w", err)
		}
		row.CreatedAt = time.Unix(0, created)
		out = append(out, row)
	}
	return out, rows.Err()
}
