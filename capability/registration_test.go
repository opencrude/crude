package capability

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	captypes "crude/types/capability"
)

// Standalone service tests use an explicit settings owner. Product publication
// uses Workspace's normal settings transaction, not this test-only fixture.
type testRegistrationStore struct {
	db *sql.DB
}

func newTestRegistrationStore(t *testing.T, db *sql.DB, settings captypes.Settings) *testRegistrationStore {
	t.Helper()
	if _, err := db.Exec(`CREATE TABLE test_registration_settings (payload TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO test_registration_settings VALUES (?)`, string(encoded)); err != nil {
		t.Fatal(err)
	}
	return &testRegistrationStore{db: db}
}

func (store *testRegistrationStore) CommitRegistration(ctx context.Context, _ string, _, after captypes.Settings) (captypes.Settings, error) {
	encoded, err := json.Marshal(after)
	if err != nil {
		return captypes.Settings{}, err
	}
	_, err = store.db.ExecContext(ctx, `UPDATE test_registration_settings SET payload=?`, string(encoded))
	return captypes.Clone(after), err
}

func (store *testRegistrationStore) load(t *testing.T) captypes.Settings {
	t.Helper()
	var encoded string
	if err := store.db.QueryRow(`SELECT payload FROM test_registration_settings`).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	var settings captypes.Settings
	if err := json.Unmarshal([]byte(encoded), &settings); err != nil {
		t.Fatal(err)
	}
	return captypes.Normalize(settings)
}
