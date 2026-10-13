package sessiondb

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func InsertPromptReference(db *sql.DB, ref Reference) error {
	if ref.CreatedAt == 0 {
		ref.CreatedAt = time.Now().UnixNano()
	}
	_, err := db.Exec(`
		INSERT OR REPLACE INTO prompt_references (
			id, session_id, kind, label, title, mime, content, file_path,
			inline, created_at, deleted_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, ref.ID, ref.SessionID, string(ref.Kind), ref.Label, ref.Title, ref.Mime,
		nullString(ref.Content), nullString(ref.FilePath), boolInt(ref.Inline), ref.CreatedAt, nullDeletedAt(ref.DeletedAt))
	if err != nil {
		return fmt.Errorf("sessiondb: insert prompt reference: %w", err)
	}
	return nil
}

func UpdatePromptReferenceContent(db *sql.DB, id, content string) error {
	_, err := db.Exec(`
		UPDATE prompt_references SET content = ? WHERE id = ? AND deleted_at IS NULL
	`, content, id)
	if err != nil {
		return fmt.Errorf("sessiondb: update prompt reference content: %w", err)
	}
	return nil
}

func DeletePromptReference(db *sql.DB, id string) error {
	_, err := db.Exec(`
		UPDATE prompt_references SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL
	`, time.Now().UnixNano(), id)
	if err != nil {
		return fmt.Errorf("sessiondb: delete prompt reference: %w", err)
	}
	return nil
}

func UpdatePromptReferenceLabel(db *sql.DB, id, label string) error {
	_, err := db.Exec(`
		UPDATE prompt_references SET label = ? WHERE id = ? AND deleted_at IS NULL
	`, label, id)
	if err != nil {
		return fmt.Errorf("sessiondb: update prompt reference label: %w", err)
	}
	return nil
}

func LoadPromptReferences(db *sql.DB) ([]Reference, error) {
	rows, err := db.Query(`
		SELECT id, session_id, kind, label, title, mime, content, file_path,
		       inline, created_at, COALESCE(deleted_at, 0)
		FROM prompt_references
		WHERE deleted_at IS NULL
		ORDER BY created_at
	`)
	if err != nil {
		return nil, fmt.Errorf("sessiondb: load prompt references: %w", err)
	}
	defer rows.Close()

	var refs []Reference
	for rows.Next() {
		var r Reference
		var kind string
		var content, filePath sql.NullString
		var inline int
		if err := rows.Scan(&r.ID, &r.SessionID, &kind, &r.Label, &r.Title, &r.Mime,
			&content, &filePath, &inline, &r.CreatedAt, &r.DeletedAt); err != nil {
			return nil, fmt.Errorf("sessiondb: load prompt references scan: %w", err)
		}
		r.Kind = Kind(kind)
		r.Content = content.String
		r.FilePath = filePath.String
		r.Inline = inline != 0
		refs = append(refs, r)
	}
	return refs, nil
}

// LoadPromptReferencesByIDs loads only the specified active prompt references.
// Missing or deleted IDs are silently skipped.
func LoadPromptReferencesByIDs(db *sql.DB, ids []string) ([]Reference, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	params := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		params[i] = "?"
		args[i] = id
	}
	query := fmt.Sprintf(`
		SELECT id, session_id, kind, label, title, mime, content, file_path,
		       inline, created_at, COALESCE(deleted_at, 0)
		FROM prompt_references
		WHERE id IN (%s) AND deleted_at IS NULL
		ORDER BY created_at
	`, strings.Join(params, ","))

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("sessiondb: load prompt references by ids: %w", err)
	}
	defer rows.Close()

	var refs []Reference
	for rows.Next() {
		var r Reference
		var kind string
		var content, filePath sql.NullString
		var inline int
		if err := rows.Scan(&r.ID, &r.SessionID, &kind, &r.Label, &r.Title, &r.Mime,
			&content, &filePath, &inline, &r.CreatedAt, &r.DeletedAt); err != nil {
			return nil, fmt.Errorf("sessiondb: load prompt references by ids scan: %w", err)
		}
		r.Kind = Kind(kind)
		r.Content = content.String
		r.FilePath = filePath.String
		r.Inline = inline != 0
		refs = append(refs, r)
	}
	return refs, nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func nullDeletedAt(v int64) sql.NullInt64 {
	if v == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: v, Valid: true}
}
