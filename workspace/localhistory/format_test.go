package localhistory

import (
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"crude/internal/dataformat"
)

// The exact v0.8.15 schema, kept as frozen test data.
//
//go:embed testdata/format-1.sql
var formatOneSchema string

func openHistoryFixture(t *testing.T, ddl string) (*sql.DB, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "manifests"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "history.db")
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if ddl != "" {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	return db, path
}

func historyShape(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`SELECT m.name, p.name, p.type, p."notnull", p.dflt_value, p.pk
		FROM sqlite_master m, pragma_table_info(m.name) p WHERE m.type='table' ORDER BY m.name, p.cid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var table, column, kind string
		var notNull, pk int
		var value sql.NullString
		if err := rows.Scan(&table, &column, &kind, &notNull, &value, &pk); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, fmt.Sprint(table, column, kind, notNull, value.String, pk))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func writeManifest(t *testing.T, path, label string, paths ...string) string {
	t.Helper()
	m := Manifest{Version: FormatVersion, Kind: "checkpoint", Label: label, Entries: map[string]Entry{}}
	for _, p := range paths {
		m.Entries[p] = Entry{Type: EntryFile, Mode: 420}
	}
	if err := (&Store{root: filepath.Dir(path)}).saveManifest(&m); err != nil {
		t.Fatal(err)
	}
	return m.ID
}

// A v0.8.15 store, and one that v0.8.16 later opened without converting, keep
// the file history of every snapshot and reach the schema of a new store.
func TestFormatIndexesEverySnapshotOfAFormatOneStore(t *testing.T) {
	db, path := openHistoryFixture(t, formatOneSchema)
	names := map[string]string{}
	for i, snapshot := range []struct {
		id    string
		paths []string
	}{{"old-one", []string{"a.go", "b.go"}}, {"old-two", []string{"a.go"}}} {
		id := writeManifest(t, path, snapshot.id, snapshot.paths...)
		names[id] = snapshot.id
		if _, err := db.Exec(`INSERT INTO history_snapshots(snapshot_id, created_at, kind) VALUES (?, ?, 'checkpoint')`, id, i+1); err != nil {
			t.Fatal(err)
		}
		for _, p := range snapshot.paths {
			if _, err := db.Exec(`INSERT INTO history_snapshot_entries(snapshot_id, path, entry_type, mode) VALUES (?, ?, 'file', 420)`, id, p); err != nil {
				t.Fatal(err)
			}
		}
	}
	// v0.8.16 indexed its own new snapshot in the compact index only.
	newID := writeManifest(t, path, "new", "c.go")
	names[newID] = "new"
	if _, err := db.Exec(compactIndexV2 + strings.ReplaceAll(`
		INSERT INTO history_snapshots(snapshot_id, created_at, kind) VALUES ('new', 9, 'checkpoint');
		INSERT INTO history_index_paths(path) VALUES ('c.go');
		INSERT INTO history_snapshot_keys(snapshot_id) VALUES ('new');
		INSERT INTO history_snapshot_paths(snapshot_seq, path_id) SELECT seq, (SELECT path_id FROM history_index_paths WHERE path='c.go') FROM history_snapshot_keys WHERE snapshot_id='new'`, "'new'", "'"+newID+"'")); err != nil {
		t.Fatal(err)
	}
	if err := migrateMetadata(db); err == nil {
		t.Fatal("product code opened a format 1 store")
	}
	if from, err := Format.Update(t.Context(), db, path, filepath.Join(t.TempDir(), "backup.db"), nil); err != nil || from != 1 {
		t.Fatalf("update from %d: %v", from, err)
	}
	if err := migrateMetadata(db); err != nil {
		t.Fatal(err)
	}
	fresh, _ := openHistoryFixture(t, "")
	if err := migrateMetadata(fresh); err != nil {
		t.Fatal(err)
	}
	if got, want := historyShape(t, db), historyShape(t, fresh); got != want {
		t.Fatalf("schema after the update:\n%s\nnew store:\n%s", got, want)
	}
	history := func(p string) []string {
		rows, err := db.Query(`SELECT k.snapshot_id FROM history_index_paths p JOIN history_snapshot_paths e ON e.path_id=p.path_id
			JOIN history_snapshot_keys k ON k.seq=e.snapshot_seq WHERE p.path=? ORDER BY k.snapshot_id`, p)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var ids []string
		for rows.Next() {
			var id string
			_ = rows.Scan(&id)
			ids = append(ids, names[id])
		}
		sort.Strings(ids)
		return ids
	}
	for p, want := range map[string]string{"a.go": "old-one,old-two", "b.go": "old-one", "c.go": "new"} {
		if got := strings.Join(history(p), ","); got != want {
			t.Fatalf("history of %s is %q, want %q", p, got, want)
		}
	}
}

// A snapshot whose manifest disagrees with its rows stops the update of that
// store; the store stays exactly as it was.
func TestFormatRefusesAStoreWhoseManifestDisagrees(t *testing.T) {
	db, path := openHistoryFixture(t, formatOneSchema)
	id := writeManifest(t, path, "s", "a.go", "b.go")
	if _, err := db.Exec(strings.ReplaceAll(`INSERT INTO history_snapshots(snapshot_id, created_at, kind) VALUES ('s', 1, 'checkpoint');
		INSERT INTO history_snapshot_entries(snapshot_id, path, entry_type, mode) VALUES ('s', 'a.go', 'file', 420)`, "'s'", "'"+id+"'")); err != nil {
		t.Fatal(err)
	}
	if _, err := Format.Update(t.Context(), db, path, filepath.Join(t.TempDir(), "backup.db"), nil); err == nil || !strings.Contains(err.Error(), "manifest has 2 paths") {
		t.Fatalf("update accepted a disagreeing manifest: %v", err)
	}
	if found, err := Format.Inspect(t.Context(), db); err != nil || found.Version != 1 || found.Stamped {
		t.Fatalf("failed update changed the store: %+v %v", found, err)
	}
	var newer *dataformat.ErrNewer
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, Format.Current+1)); err != nil {
		t.Fatal(err)
	}
	if err := migrateMetadata(db); !errors.As(err, &newer) {
		t.Fatalf("a store from a newer Crude was opened: %v", err)
	}
}

func TestAllReleasedHistoryLayoutsKeepVerifiedFiles(t *testing.T) {
	fixtures, err := filepath.Glob("testdata/release-*.sql")
	if err != nil || len(fixtures) != 2 {
		t.Fatal(fixtures, err)
	}
	for _, fixture := range fixtures {
		t.Run(filepath.Base(fixture), func(t *testing.T) {
			data, err := os.ReadFile(fixture)
			if err != nil {
				t.Fatal(err)
			}
			db, path := openHistoryFixture(t, string(data))
			id := writeManifest(t, path, "old", "keep.txt")
			if _, err := db.Exec(`INSERT INTO history_snapshots(snapshot_id,created_at,kind) VALUES(?,1,'checkpoint');`, id); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO history_snapshot_entries(snapshot_id,path,entry_type,mode) VALUES(?,'keep.txt','file',420)`, id); err != nil {
				t.Fatal(err)
			}
			if _, err := Format.Update(t.Context(), db, path, filepath.Join(t.TempDir(), "backup.db"), nil); err != nil {
				t.Fatal(err)
			}
			fresh, _ := openHistoryFixture(t, "")
			if err := migrateMetadata(fresh); err != nil {
				t.Fatal(err)
			}
			if historyShape(t, db) != historyShape(t, fresh) {
				t.Fatal("updated metadata differs from a fresh store")
			}
		})
	}
}
