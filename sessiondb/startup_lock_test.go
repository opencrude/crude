package sessiondb

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestMigrationWaitsForExistingMetadataReaderBeforeEnablingWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.db")
	reader, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := reader.Exec(`CREATE TABLE metadata_probe (value TEXT)`); err != nil {
		t.Fatal(err)
	}
	tx, err := reader.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var count int
	if err := tx.QueryRow(`SELECT count(*) FROM metadata_probe`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	writer, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	result := make(chan error, 1)
	go func() { result <- Migrate(writer) }()
	select {
	case err := <-result:
		t.Fatalf("migration must wait for metadata read lock, not fail immediately: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("migration did not finish after metadata reader released its lock")
	}
}
