package localhistory

import (
	"context"
	"errors"
	"fmt"
)

// SnapshotRetained captures and durably pins a non-agent safety snapshot under
// one mutation gate with pruning. The opaque owner-generated reference must be
// 1–128 ASCII bytes: an alphanumeric followed by alphanumerics, '_' or '-'.
// A duplicate reference fails before capture; it never retargets existing work.
// Callers own release, including after cancellation, shutdown or an ambiguous
// result. Capture/registration errors never authorize a workspace mutation.
func (s *Store) SnapshotRetained(ctx context.Context, reference string, opts SnapshotOptions) (Manifest, error) {
	if !validSafetyReference(reference) {
		return Manifest{}, errors.New("localhistory: invalid retained snapshot reference")
	}
	if err := s.beginOperation(); err != nil {
		return Manifest{}, err
	}
	defer s.endOperation()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	unlock, err := s.lockMutation(ctx)
	if err != nil {
		return Manifest{}, err
	}
	defer unlock()
	if err := s.checkRetainedReferenceAvailable(ctx, reference); err != nil {
		return Manifest{}, err
	}
	manifest, err := s.snapshot(ctx, opts)
	if err != nil {
		return Manifest{}, err
	}
	if err := s.pinRetainedManifest(ctx, reference, manifest.ID); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (s *Store) checkRetainedReferenceAvailable(ctx context.Context, reference string) error {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM history_retained_snapshots WHERE reference=? UNION ALL SELECT 1 FROM history_safety_protection_events WHERE reference=?)`, reference, reference).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return errors.New("localhistory: retained snapshot reference already exists")
	}
	return nil
}

// Verify reused objects as well as new content before claiming a protected
// success. Both callers hold the same mutation gate as pruning.
func (s *Store) pinRetainedManifest(ctx context.Context, reference, id string) error {
	if _, err := s.verifiedRetainedManifest(ctx, id); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO history_retained_snapshots(reference,snapshot_id) VALUES(?,?)`, reference, id)
	return err
}

// LoadRetainedSnapshot proves the exact indexed manifest and all its content
// objects while excluding pruning. It never repairs missing evidence.
func (s *Store) LoadRetainedSnapshot(ctx context.Context, reference string) (Manifest, error) {
	if !validSafetyReference(reference) {
		return Manifest{}, errors.New("localhistory: invalid retained snapshot reference")
	}
	if err := s.beginOperation(); err != nil {
		return Manifest{}, err
	}
	defer s.endOperation()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	unlock, err := s.lockMutation(ctx)
	if err != nil {
		return Manifest{}, err
	}
	defer unlock()
	var id string
	if err := s.db.QueryRowContext(ctx, `SELECT snapshot_id FROM history_retained_snapshots WHERE reference=?`, reference).Scan(&id); err != nil {
		return Manifest{}, err
	}
	return s.verifiedRetainedManifest(ctx, id)
}

// ReleaseRetainedSnapshot removes only a never-audited exact reference. It is
// idempotent for those references and does not delete the snapshot. Audited
// references must use ChangeSafetyProtection, preserving the disposition truth.
func (s *Store) ReleaseRetainedSnapshot(ctx context.Context, reference string) error {
	if !validSafetyReference(reference) {
		return errors.New("localhistory: invalid retained snapshot reference")
	}
	if err := s.beginOperation(); err != nil {
		return err
	}
	defer s.endOperation()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	unlock, err := s.lockMutation(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	var audited bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM history_safety_protection_events WHERE reference=?)`, reference).Scan(&audited); err != nil {
		return err
	}
	if audited {
		return errors.New("localhistory: audited reference requires a safety protection change")
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM history_retained_snapshots WHERE reference=?`, reference)
	return err
}

func validSafetyReference(reference string) bool {
	if len(reference) == 0 || len(reference) > 128 {
		return false
	}
	for i := 0; i < len(reference); i++ {
		c := reference[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			continue
		}
		if i > 0 && (c == '_' || c == '-') {
			continue
		}
		return false
	}
	return true
}

// Called only under the store/process mutation gate, or at exclusive startup
// before orphan recovery. Read rows completely before querying the single DB
// connection again.
func (s *Store) retainedSafetySnapshots(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT reference,snapshot_id FROM history_retained_snapshots`)
	if err != nil {
		return nil, err
	}
	protected := make(map[string]bool)
	for rows.Next() {
		var reference, id string
		if err := rows.Scan(&reference, &id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if !validSafetyReference(reference) || !validID(id) {
			_ = rows.Close()
			return nil, errors.New("localhistory: corrupt retained snapshot reference")
		}
		protected[id] = true
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for id := range protected {
		if _, err := s.verifiedRetainedManifest(ctx, id); err != nil {
			return nil, fmt.Errorf("localhistory: retained safety snapshot %s: %w", id, err)
		}
	}
	return protected, nil
}

func (s *Store) verifiedRetainedManifest(ctx context.Context, id string) (Manifest, error) {
	if !validID(id) {
		return Manifest{}, errors.New("localhistory: invalid retained snapshot ID")
	}
	var indexed SnapshotRecord
	if err := s.db.QueryRowContext(ctx, `SELECT snapshot_id,parent_snapshot_id,created_at,kind,label,logical_bytes FROM history_snapshots WHERE snapshot_id=?`, id).
		Scan(&indexed.ID, &indexed.ParentID, &indexed.CreatedAt, &indexed.Kind, &indexed.Label, &indexed.LogicalBytes); err != nil {
		return Manifest{}, err
	}
	manifest, err := s.LoadManifest(id)
	if err != nil {
		return Manifest{}, err
	}
	if indexed.ParentID != manifest.ParentID || indexed.CreatedAt != manifest.CreatedAt || indexed.Kind != manifest.Kind || indexed.Label != manifest.Label {
		return Manifest{}, errors.New("localhistory: retained snapshot metadata mismatch")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT path,entry_type,mode,hash,size,symlink_target,hardlink_target,special_mode FROM history_snapshot_entries WHERE snapshot_id=?`, id)
	if err != nil {
		return Manifest{}, err
	}
	var count int
	var logical int64
	for rows.Next() {
		var path string
		var entry Entry
		if err := rows.Scan(&path, &entry.Type, &entry.Mode, &entry.Hash, &entry.Size, &entry.SymlinkTarget, &entry.HardlinkTarget, &entry.SpecialMode); err != nil {
			_ = rows.Close()
			return Manifest{}, err
		}
		expected, ok := manifest.Entries[path]
		if !ok || entry != expected {
			_ = rows.Close()
			return Manifest{}, errors.New("localhistory: retained snapshot entry mismatch")
		}
		count++
		if entry.Type == EntryFile {
			logical += entry.Size
		}
	}
	if err := rows.Close(); err != nil {
		return Manifest{}, err
	}
	if err := rows.Err(); err != nil {
		return Manifest{}, err
	}
	if count != len(manifest.Entries) || logical != indexed.LogicalBytes {
		return Manifest{}, errors.New("localhistory: retained snapshot index incomplete")
	}
	for _, entry := range manifest.Entries {
		if err := ctx.Err(); err != nil {
			return Manifest{}, err
		}
		if entry.Type != EntryFile && entry.Type != EntryHardlink {
			continue
		}
		object, err := s.readFileObject(entry.Hash)
		if err != nil {
			return Manifest{}, err
		}
		if object.Size != entry.Size {
			return Manifest{}, errors.New("localhistory: retained snapshot object size mismatch")
		}
	}
	return manifest, nil
}
