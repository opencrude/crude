package localhistory

import (
	"context"
	"fmt"
	"path/filepath"

	"crude/internal/dataformat"
)

// Format is the Local History metadata format (history.db). The data update
// runs its steps before the core opens any store; migrateMetadata opens only
// the current version.
//
//   - Version 1 is the shape of Crude v0.8.15 and earlier: every snapshot
//     entry has a row in history_snapshot_entries. Stores that v0.8.16 or
//     v0.8.17 opened also have the compact index, but only for new snapshots.
//   - Version 2 keeps only which paths each snapshot has, as small rows in
//     the compact index; each snapshot's manifest file holds its entries.
//
// Steps are frozen: they use the tables of their own time, never current code.
var Format = dataformat.Format{
	Name:     "Local History",
	Current:  2,
	Classify: classifyFormat,
	Steps: []dataformat.Step{
		{To: 2, Title: "Index the paths of each Local History snapshot", Apply: compactSnapshotIndex},
	},
}

func classifyFormat(ctx context.Context, q dataformat.Querier) (int, error) {
	return dataformat.ClassifyTables(ctx, q, `'history_agent_operation_paths','history_agent_operation_runs','history_agent_operations','history_index_paths','history_message_scopes','history_restore_events','history_restore_plans','history_restore_transactions','history_retained_snapshots','history_retention_runs','history_safety_protection_events','history_snapshot_entries','history_snapshot_keys','history_snapshot_paths','history_snapshots'`, releasedLayouts)
}

// Digests of the owned table columns, defaults and keys in the release tags.
// Do not stamp a database from an unrecognized layout on the strength of a single table.
var releasedLayouts = map[string]int{
	"78598b8a4a274e590a6f80a9e05033345600e12107f884cb7fb8406920125d8b": 1, // v0.1.1 through v0.2.2
	"0b14188eab5859d8335ad69b2a31fc816c0d71b4bdca9a64a62818356095a82f": 1, // v0.2.3 through v0.7.2
	"c9ee7229e38d332cbfff32be6977f634008827339ad95185333de6d0cc90dbfd": 1, // v0.7.3 through v0.8.15
	"a256e7600679d09a8d2ef798edb432c44df00748ac397703946482239b4cdb92": 2, // v0.8.16 through format-2
	"74ebd2e90d4316b3ef5bca8ad0866944a30abd415748cabb17292048c03d8e28": 1, // mixed-history through mixed-history
}

// Version 2 index tables, as they were when the step was written.
const compactIndexV2 = `
CREATE TABLE IF NOT EXISTS history_index_paths (
  path_id INTEGER PRIMARY KEY,
  path TEXT NOT NULL UNIQUE
);
CREATE TABLE IF NOT EXISTS history_snapshot_keys (
  seq INTEGER PRIMARY KEY,
  snapshot_id TEXT NOT NULL UNIQUE REFERENCES history_snapshots(snapshot_id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS history_snapshot_paths (
  snapshot_seq INTEGER NOT NULL REFERENCES history_snapshot_keys(seq) ON DELETE CASCADE,
  path_id INTEGER NOT NULL,
  PRIMARY KEY(snapshot_seq,path_id)
) WITHOUT ROWID;
`

// compactSnapshotIndex copies every snapshot's paths from the per-entry table
// into the compact index, checks each snapshot against its old rows and, when
// its manifest file exists, against the manifest, then drops the old table.
func compactSnapshotIndex(ctx context.Context, u dataformat.Unit) error {
	q := u.Q
	if _, err := q.ExecContext(ctx, releaseAdditions); err != nil {
		return err
	}
	for _, statement := range []string{
		compactIndexV2,
		`INSERT OR IGNORE INTO history_index_paths(path) SELECT DISTINCT path FROM history_snapshot_entries ORDER BY path`,
		`INSERT OR IGNORE INTO history_snapshot_keys(snapshot_id) SELECT snapshot_id FROM history_snapshots ORDER BY created_at, rowid`,
		`INSERT OR IGNORE INTO history_snapshot_paths(snapshot_seq, path_id)
		 SELECT k.seq, p.path_id FROM history_snapshot_entries e
		 JOIN history_snapshot_keys k ON k.snapshot_id = e.snapshot_id
		 JOIN history_index_paths p ON p.path = e.path`,
	} {
		if _, err := q.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	u.Progress(0.5)
	rows, err := q.QueryContext(ctx, `SELECT snapshot_id FROM history_snapshots ORDER BY rowid`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	manifests := &Store{root: filepath.Dir(u.Path)}
	for i, id := range ids {
		manifest, err := manifests.LoadManifest(id)
		if err != nil {
			return fmt.Errorf("snapshot %s: %w", id, err)
		}
		if err := verifyIndexedSnapshot(ctx, q, id, manifest); err != nil {
			return err
		}
		u.Progress(0.5 + 0.5*float64(i+1)/float64(len(ids)))
	}
	_, err = q.ExecContext(ctx, `DROP INDEX IF EXISTS idx_history_entries_path; DROP TABLE history_snapshot_entries`)
	return err
}

// Additions made between the earliest release and this step's input schema.
const releaseAdditions = `
CREATE TABLE IF NOT EXISTS history_retained_snapshots (
  reference TEXT PRIMARY KEY,
  snapshot_id TEXT NOT NULL REFERENCES history_snapshots(snapshot_id) ON DELETE RESTRICT
);
CREATE TABLE IF NOT EXISTS history_safety_protection_events (
  event_id TEXT PRIMARY KEY,
  reference TEXT NOT NULL,
  revision INTEGER NOT NULL CHECK(revision > 0),
  snapshot_id TEXT NOT NULL,
  action TEXT NOT NULL CHECK(action IN ('release','reprotect')),
  actor_client_id TEXT NOT NULL,
  request_id TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  payload TEXT NOT NULL,
  UNIQUE(reference,revision),
  UNIQUE(actor_client_id,request_id)
);
CREATE TABLE IF NOT EXISTS history_agent_operation_runs (
  run_id TEXT NOT NULL,
  operation_id TEXT NOT NULL UNIQUE REFERENCES history_agent_operations(operation_id) ON DELETE CASCADE,
  PRIMARY KEY(run_id, operation_id)
);
`

// Before discarding an entry, check its complete preimage against the
// content-addressed manifest and check that its path is in the final index.
func verifyIndexedSnapshot(ctx context.Context, q dataformat.Querier, id string, m Manifest) error {
	rows, err := q.QueryContext(ctx, `SELECT p.path FROM history_snapshot_keys k JOIN history_snapshot_paths s ON s.snapshot_seq=k.seq JOIN history_index_paths p ON p.path_id=s.path_id WHERE k.snapshot_id=?`, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	indexed := 0
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return err
		}
		if _, found := m.Entries[path]; !found {
			return fmt.Errorf("snapshot %s does not contain indexed path %q", id, path)
		}
		indexed++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(m.Entries) != indexed {
		return fmt.Errorf("snapshot %s manifest has %d paths, the index %d", id, len(m.Entries), indexed)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	entries, err := q.QueryContext(ctx, `SELECT path,entry_type,mode,hash,size,symlink_target,hardlink_target,special_mode FROM history_snapshot_entries WHERE snapshot_id=?`, id)
	if err != nil {
		return err
	}
	defer entries.Close()
	for entries.Next() {
		var path string
		var e Entry
		if err := entries.Scan(&path, &e.Type, &e.Mode, &e.Hash, &e.Size, &e.SymlinkTarget, &e.HardlinkTarget, &e.SpecialMode); err != nil {
			return err
		}
		if want, ok := m.Entries[path]; !ok || want != e {
			return fmt.Errorf("snapshot %s entry %q differs from its manifest", id, path)
		}
	}
	return entries.Err()
}
