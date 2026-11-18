package sessiondb

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestMigrateFailureDoesNotLeavePartialSchema(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "incompatible.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE usage_requests (request_id TEXT PRIMARY KEY); INSERT INTO usage_requests VALUES ('retained')`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err == nil {
		t.Fatal("incompatible schema accepted")
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='sessions'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed initialization left a partial session schema: count=%d err=%v", count, err)
	}
	var retained string
	if err := db.QueryRow(`SELECT request_id FROM usage_requests`).Scan(&retained); err != nil || retained != "retained" {
		t.Fatalf("existing work changed during failed initialization: %q %v", retained, err)
	}
}

func BenchmarkSessionDatabaseStartup(b *testing.B) {
	root := b.TempDir()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		db, err := OpenAndMigrate(filepath.Join(root, fmt.Sprintf("session-%d.db", i)))
		if err != nil {
			b.Fatal(err)
		}
		if err := db.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
