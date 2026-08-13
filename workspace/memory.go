package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"crude/session"
)

const (
	maxMemoryTitleBytes   = 256
	maxMemoryContentBytes = 64 << 10
)

func (wdb *WorkspaceDB) RememberMemory(
	ctx context.Context,
	record session.MemoryRecord,
) (session.MemoryRecord, error) {
	if wdb == nil || wdb.DB == nil {
		return session.MemoryRecord{}, fmt.Errorf("workspace memory store is unavailable")
	}
	record.Title = strings.TrimSpace(record.Title)
	record.Content = strings.TrimSpace(record.Content)
	record.Kind = strings.TrimSpace(record.Kind)
	if record.Kind == "" {
		record.Kind = "fact"
	}
	if record.Title == "" || record.Content == "" {
		return session.MemoryRecord{}, fmt.Errorf("memory title and content are required")
	}
	if len(record.Title) > maxMemoryTitleBytes {
		return session.MemoryRecord{}, fmt.Errorf("memory title exceeds %d bytes", maxMemoryTitleBytes)
	}
	if len(record.Content) > maxMemoryContentBytes {
		return session.MemoryRecord{}, fmt.Errorf("memory content exceeds %d bytes", maxMemoryContentBytes)
	}
	if record.ID == "" {
		sum := sha256.Sum256([]byte(record.Kind + "\x00" + strings.ToLower(record.Title)))
		record.ID = "mem-" + hex.EncodeToString(sum[:8])
	}
	now := time.Now().UnixNano()
	if record.CreatedAt == 0 {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	_, err := wdb.DB.ExecContext(ctx, `
		INSERT INTO workspace_memories
			(id, kind, title, content, source_session_id, pinned, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			kind = excluded.kind,
			title = excluded.title,
			content = excluded.content,
			source_session_id = excluded.source_session_id,
			pinned = excluded.pinned,
			updated_at = excluded.updated_at
	`, record.ID, record.Kind, record.Title, record.Content, record.SourceSessionID,
		record.Pinned, record.CreatedAt, record.UpdatedAt)
	if err != nil {
		return session.MemoryRecord{}, fmt.Errorf("remember workspace memory: %w", err)
	}
	return record, nil
}

func (wdb *WorkspaceDB) RecallMemories(
	ctx context.Context,
	query string,
	limit int,
) ([]session.MemoryRecord, error) {
	if wdb == nil || wdb.DB == nil {
		return nil, fmt.Errorf("workspace memory store is unavailable")
	}
	if limit <= 0 || limit > 50 {
		limit = 8
	}
	rows, err := wdb.DB.QueryContext(ctx, `
		SELECT id, kind, title, content, source_session_id, pinned, created_at, updated_at
		FROM workspace_memories
		ORDER BY pinned DESC, updated_at DESC
		LIMIT 256
	`)
	if err != nil {
		return nil, fmt.Errorf("recall workspace memories: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type rankedMemory struct {
		record session.MemoryRecord
		score  int
	}
	terms := strings.Fields(strings.ToLower(query))
	var ranked []rankedMemory
	for rows.Next() {
		var record session.MemoryRecord
		if err := rows.Scan(
			&record.ID, &record.Kind, &record.Title, &record.Content,
			&record.SourceSessionID, &record.Pinned, &record.CreatedAt, &record.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan workspace memory: %w", err)
		}
		score := 0
		title := strings.ToLower(record.Title)
		content := strings.ToLower(record.Content)
		for _, term := range terms {
			if strings.Contains(title, term) {
				score += 8
			}
			if strings.Contains(content, term) {
				score += 2
			}
		}
		if record.Pinned {
			score += 4
		}
		if len(terms) == 0 || score > 0 {
			ranked = append(ranked, rankedMemory{record: record, score: score})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workspace memories: %w", err)
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].record.UpdatedAt > ranked[j].record.UpdatedAt
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	result := make([]session.MemoryRecord, len(ranked))
	for i := range ranked {
		result[i] = ranked[i].record
	}
	return result, nil
}

func (wdb *WorkspaceDB) ForgetMemory(ctx context.Context, id string) error {
	if wdb == nil || wdb.DB == nil {
		return fmt.Errorf("workspace memory store is unavailable")
	}
	result, err := wdb.DB.ExecContext(ctx, `DELETE FROM workspace_memories WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("forget workspace memory: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("forget workspace memory result: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("workspace memory %q was not found", id)
	}
	return nil
}
