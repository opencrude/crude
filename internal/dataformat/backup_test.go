package dataformat

import (
	"context"
	"crude/internal/fileuri"
	"database/sql"
	"errors"
	_ "modernc.org/sqlite"
	"path/filepath"
	"testing"
)

// An older read transaction can keep the latest commit in the WAL. A backup
// must restore that commit, not merely the pages in the main database file.
func TestBackupIncludesPinnedWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history #.db")
	db, err := sql.Open("sqlite", fileuri.SQLiteDSN(path, "_pragma=busy_timeout(1)"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`PRAGMA journal_mode=WAL; CREATE TABLE evidence(v); INSERT INTO evidence VALUES('before')`); err != nil {
		t.Fatal(err)
	}
	held, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Rollback() }()
	var count int
	if err = held.QueryRow(`SELECT count(*) FROM evidence`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO evidence VALUES('committed in WAL')`); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "copy.db")
	if err = backup(t.Context(), db, dest, nil); err != nil {
		t.Fatal(err)
	}
	restored, err := sql.Open("sqlite", fileuri.SQLiteDSN(dest, "mode=ro"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err = restored.QueryRow(`SELECT count(*) FROM evidence WHERE v='committed in WAL'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("restored %d latest rows: %v", count, err)
	}
}

func TestCancelledStepRollsBackAndKeepsCommittedBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.db")
	db, err := sql.Open("sqlite", fileuri.SQLiteDSN(path, ""))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE evidence(v);INSERT INTO evidence VALUES('old');PRAGMA user_version=1`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f := Format{Name: "test", Current: 2, Classify: func(context.Context, Querier) (int, error) { return 1, nil }, Steps: []Step{{To: 2, Title: "Update", Apply: func(ctx context.Context, u Unit) error {
		if _, err := u.Q.ExecContext(ctx, `UPDATE evidence SET v='should roll back'`); err != nil {
			return err
		}
		cancel()
		return ctx.Err()
	}}}}
	dest := filepath.Join(t.TempDir(), "old.db")
	if _, err := f.Update(ctx, db, path, dest, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, p := range []string{path, dest} {
		r, err := sql.Open("sqlite", fileuri.SQLiteDSN(p, "mode=ro"))
		if err != nil {
			t.Fatal(err)
		}
		var version int
		var val string
		if err = r.QueryRow(`PRAGMA user_version`).Scan(&version); err == nil {
			err = r.QueryRow(`SELECT v FROM evidence`).Scan(&val)
		}
		_ = r.Close()
		if err != nil || version != 1 || val != "old" {
			t.Fatalf("%s: version=%d value=%q err=%v", p, version, val, err)
		}
	}
}
