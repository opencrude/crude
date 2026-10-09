package clientstate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"unicode/utf8"
)

// EditorRecoveryRecord stores a current-format detached document payload. It
// borrows the private Store DB and attachment, never workspace persistence.
type EditorRecoveryKey struct{ ClientID, WorkspaceID, Path string }
type EditorRecoveryRecord struct {
	Key      EditorRecoveryKey
	Epoch    uint64 // Attachment which authorized this observation/write.
	Revision uint64
	Payload  json.RawMessage
}

const MaxEditorRecoveryBytes = 128 << 20
const editorRecoverySchema = `CREATE TABLE IF NOT EXISTS editor_recovery_v1 (
 client_id TEXT NOT NULL, workspace_id TEXT NOT NULL, path TEXT NOT NULL,
 revision INTEGER NOT NULL, payload BLOB,
 PRIMARY KEY(client_id, workspace_id, path));`

func validateEditorRecoveryKey(a Attachment, k EditorRecoveryKey) error {
	if !validID(a.ClientID) || !validEpoch(a.Epoch) || k.ClientID != a.ClientID || !validID(k.WorkspaceID) || k.Path == "" || len(k.Path) > 32768 || !utf8.ValidString(k.Path) || strings.ContainsRune(k.Path, 0) {
		return ErrInvalid
	}
	return nil
}
func readEditorRecovery(ctx context.Context, c *sql.Conn, k EditorRecoveryKey) (EditorRecoveryRecord, error) {
	r := EditorRecoveryRecord{Key: k}
	var raw []byte
	err := c.QueryRowContext(ctx, `SELECT revision,payload FROM editor_recovery_v1 WHERE client_id=? AND workspace_id=? AND path=?`, k.ClientID, k.WorkspaceID, k.Path).Scan(&r.Revision, &raw)
	r.Payload = raw
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err == nil && (r.Revision == 0 || r.Revision > math.MaxInt64 || !validEditorRecoveryPayload(raw)) {
		return EditorRecoveryRecord{}, ErrInvalid
	}
	return r, err
}

func validEditorRecoveryPayload(raw []byte) bool {
	if len(raw) == 0 {
		return true
	}
	if len(raw) > MaxEditorRecoveryBytes {
		return false
	}
	var header struct{ Version int }
	return json.Unmarshal(raw, &header) == nil && header.Version == 1
}
func (s *Store) LoadEditorRecovery(ctx context.Context, a Attachment, k EditorRecoveryKey) (EditorRecoveryRecord, error) {
	if err := validateEditorRecoveryKey(a, k); err != nil {
		return EditorRecoveryRecord{}, err
	}
	var r EditorRecoveryRecord
	err := s.transaction(ctx, func(c *sql.Conn) error {
		if err := authorize(ctx, c, a.ClientID, a.Epoch); err != nil {
			return err
		}
		if _, err := c.ExecContext(ctx, editorRecoverySchema); err != nil {
			return err
		}
		var err error
		r, err = readEditorRecovery(ctx, c, k)
		return err
	})
	r.Epoch = a.Epoch
	return r, err
}
func (s *Store) SaveEditorRecovery(ctx context.Context, a Attachment, k EditorRecoveryKey, expected uint64, payload json.RawMessage) (EditorRecoveryRecord, error) {
	if err := validateEditorRecoveryKey(a, k); err != nil {
		return EditorRecoveryRecord{}, err
	}
	if expected >= math.MaxInt64 || !validEditorRecoveryPayload(payload) {
		return EditorRecoveryRecord{}, ErrInvalid
	}
	var result EditorRecoveryRecord
	err := s.transaction(ctx, func(c *sql.Conn) error {
		if err := authorize(ctx, c, a.ClientID, a.Epoch); err != nil {
			return err
		}
		if _, err := c.ExecContext(ctx, editorRecoverySchema); err != nil {
			return err
		}
		r, err := readEditorRecovery(ctx, c, k)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if r.Revision != expected {
			return ErrConflict
		}
		_, err = c.ExecContext(ctx, `INSERT INTO editor_recovery_v1 VALUES (?,?,?,?,?) ON CONFLICT(client_id,workspace_id,path) DO UPDATE SET revision=excluded.revision,payload=excluded.payload`, k.ClientID, k.WorkspaceID, k.Path, expected+1, []byte(payload))
		result = EditorRecoveryRecord{Key: k, Epoch: a.Epoch, Revision: expected + 1, Payload: append(json.RawMessage(nil), payload...)}
		return err
	})
	if err != nil {
		return EditorRecoveryRecord{}, err
	}
	return result, nil
}

// Clear leaves a revisioned tombstone so stale create/update cannot revive it.
func (s *Store) ClearEditorRecovery(ctx context.Context, a Attachment, k EditorRecoveryKey, expected uint64) (EditorRecoveryRecord, error) {
	return s.SaveEditorRecovery(ctx, a, k, expected, nil)
}
