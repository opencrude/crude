package localhistory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (s *Store) VerifyAll(ctx context.Context) error {
	if err := s.beginOperation(); err != nil {
		return err
	}
	defer s.endOperation()
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lockMutation(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	records, err := s.timeline(-1)
	if err != nil {
		return err
	}
	for _, record := range records {
		if err := s.verify(ctx, record.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Prune(ctx context.Context, policy RetentionPolicy) (PruneResult, error) {
	if err := s.beginOperation(); err != nil {
		return PruneResult{}, err
	}
	defer s.endOperation()
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lockMutation(ctx)
	if err != nil {
		return PruneResult{}, err
	}
	defer unlock()
	before, err := s.stats()
	if err != nil {
		return PruneResult{}, err
	}
	originalPhysical := before.PhysicalBytes
	records, err := s.timeline(-1)
	if err != nil {
		return PruneResult{}, err
	}
	if len(records) <= 1 {
		return PruneResult{}, nil
	}
	protected, err := s.protectedSnapshots()
	if err != nil {
		return PruneResult{}, err
	}
	keep := make(map[string]bool, len(protected)+1)
	for id := range protected {
		keep[id] = true
	}
	keep[records[0].ID] = true
	now := time.Now()
	if policy.KeepHours > 0 {
		cutoff := now.Add(-time.Duration(policy.KeepHours) * time.Hour).UnixNano()
		for _, record := range records {
			if record.CreatedAt >= cutoff {
				keep[record.ID] = true
			}
		}
	}
	if policy.KeepDaily > 0 {
		days := make(map[string]bool)
		for _, record := range records {
			day := time.Unix(0, record.CreatedAt).Local().Format("2006-01-02")
			if !days[day] && len(days) < policy.KeepDaily {
				keep[record.ID] = true
				days[day] = true
			}
		}
	}
	if policy.MaxSnapshots > 0 {
		bounded := make(map[string]bool, max(policy.MaxSnapshots, len(protected)))
		for id := range protected {
			bounded[id] = true
		}
		bounded[records[0].ID] = true
		for _, record := range records {
			if keep[record.ID] && len(bounded) < policy.MaxSnapshots {
				bounded[record.ID] = true
			}
		}
		for _, record := range records {
			if len(bounded) < policy.MaxSnapshots {
				bounded[record.ID] = true
			}
		}
		keep = bounded
	}
	var remove []string
	for _, record := range records {
		if !keep[record.ID] {
			remove = append(remove, record.ID)
		}
	}
	if policy.BudgetBytes > 0 && before.PhysicalBytes > policy.BudgetBytes {
		// Content-addressed objects are shared, so a snapshot's logical size
		// cannot predict physical bytes freed. When over budget, remove every
		// unprotected generation and measure the store after GC.
		for index := len(records) - 1; index >= 0; index-- {
			record := records[index]
			if protected[record.ID] || record.ID == records[0].ID || containsString(remove, record.ID) {
				continue
			}
			remove = append(remove, record.ID)
		}
	}
	if len(remove) == 0 {
		return PruneResult{}, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PruneResult{}, err
	}
	for _, id := range remove {
		if _, err := tx.ExecContext(ctx, `DELETE FROM history_message_scopes WHERE state='closed' AND (before_snapshot_id=? OR after_snapshot_id=?)`, id, id); err != nil {
			_ = tx.Rollback()
			return PruneResult{}, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM history_restore_events WHERE notified=1 AND (target_snapshot_id=? OR safety_snapshot_id=? OR after_snapshot_id=?)`, id, id, id); err != nil {
			_ = tx.Rollback()
			return PruneResult{}, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM history_snapshots WHERE snapshot_id=?`, id); err != nil {
			_ = tx.Rollback()
			return PruneResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return PruneResult{}, err
	}
	for _, id := range remove {
		_ = os.Remove(filepath.Join(s.root, "manifests", id+".json"))
	}
	if err := s.removeStalePlans(); err != nil {
		return PruneResult{}, err
	}
	removedObjects, err := s.collectGarbage(ctx)
	if err != nil {
		return PruneResult{}, err
	}
	after, err := s.stats()
	if err != nil {
		return PruneResult{}, err
	}
	result := PruneResult{RemovedSnapshots: len(remove), RemovedObjects: removedObjects, FreedBytes: max64(0, originalPhysical-after.PhysicalBytes)}
	_, _ = s.db.Exec(`INSERT INTO history_retention_runs(created_at,removed_snapshots,removed_objects,freed_bytes) VALUES(?,?,?,?)`,
		time.Now().UnixNano(), result.RemovedSnapshots, result.RemovedObjects, result.FreedBytes)
	return result, nil
}

func (s *Store) removeStalePlans() error {
	rows, err := s.db.Query(`SELECT plan_id FROM history_restore_plans WHERE target_snapshot_id NOT IN (SELECT snapshot_id FROM history_snapshots)`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := s.db.Exec(`DELETE FROM history_restore_plans WHERE plan_id=?`, id); err != nil {
			return err
		}
		_ = os.Remove(filepath.Join(s.root, "plans", id+".json"))
	}
	return nil
}

func (s *Store) protectedSnapshots() (map[string]bool, error) {
	protected := make(map[string]bool)
	queries := []string{
		`SELECT before_snapshot_id FROM history_message_scopes WHERE state<>'closed' UNION SELECT after_snapshot_id FROM history_message_scopes WHERE state<>'closed' AND after_snapshot_id<>''`,
		`SELECT target_snapshot_id FROM history_restore_plans WHERE state='preview'`,
		`SELECT safety_snapshot_id FROM history_restore_transactions WHERE state IN ('applying','conflicted')`,
		`SELECT target_snapshot_id FROM history_restore_events WHERE notified=0 UNION SELECT safety_snapshot_id FROM history_restore_events WHERE notified=0 UNION SELECT after_snapshot_id FROM history_restore_events WHERE notified=0`,
	}
	for _, query := range queries {
		rows, err := s.db.Query(query)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			if id != "" {
				protected[id] = true
			}
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return protected, nil
}

func (s *Store) collectGarbage(ctx context.Context) (int, error) {
	records, err := s.timeline(-1)
	if err != nil {
		return 0, err
	}
	files := make(map[string]bool)
	chunks := make(map[string]bool)
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		manifest, err := s.LoadManifest(record.ID)
		if err != nil {
			return 0, err
		}
		for _, entry := range manifest.Entries {
			if entry.Type != EntryFile || files[entry.Hash] {
				continue
			}
			files[entry.Hash] = true
			obj, err := s.readFileObject(entry.Hash)
			if err != nil {
				return 0, err
			}
			for _, chunk := range obj.Chunks {
				chunks[chunk.Hash] = true
			}
		}
	}
	planEntries, err := os.ReadDir(filepath.Join(s.root, "plans"))
	if err != nil {
		return 0, err
	}
	for _, entry := range planEntries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		plan, err := s.LoadPlan(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return 0, err
		}
		for _, change := range plan.Changes {
			for _, target := range []*Entry{change.Target, change.ExactTarget} {
				if target == nil || target.Type != EntryFile || files[target.Hash] {
					continue
				}
				files[target.Hash] = true
				obj, err := s.readFileObject(target.Hash)
				if err != nil {
					return 0, err
				}
				for _, chunk := range obj.Chunks {
					chunks[chunk.Hash] = true
				}
			}
		}
	}
	rows, err := s.db.Query(`SELECT before_entry,after_entry FROM history_agent_operation_paths`)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var beforeJSON, afterJSON string
		if err := rows.Scan(&beforeJSON, &afterJSON); err != nil {
			_ = rows.Close()
			return 0, err
		}
		for _, encoded := range []string{beforeJSON, afterJSON} {
			entry, err := unmarshalOptionalEntry(encoded)
			if err != nil {
				_ = rows.Close()
				return 0, err
			}
			if entry == nil || entry.Type != EntryFile || files[entry.Hash] {
				continue
			}
			files[entry.Hash] = true
			object, err := s.readFileObject(entry.Hash)
			if err != nil {
				_ = rows.Close()
				return 0, err
			}
			for _, chunk := range object.Chunks {
				chunks[chunk.Hash] = true
			}
		}
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	removed, err := removeUnreferenced(filepath.Join(s.root, "objects", "files"), ".json", files)
	if err != nil {
		return removed, err
	}
	chunkRemoved, err := removeUnreferenced(filepath.Join(s.root, "objects", "chunks"), "", chunks)
	return removed + chunkRemoved, err
}

func removeUnreferenced(root, suffix string, keep map[string]bool) (int, error) {
	removed := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		name := strings.TrimSuffix(entry.Name(), suffix)
		if !keep[name] {
			if err := os.Remove(path); err != nil {
				return err
			}
			removed++
		}
		return nil
	})
	return removed, err
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
