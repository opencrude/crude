package clientstate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// View memory is one client's presentation choices for one workspace: which
// agents it minimized, its sidebar and tree toggles, its layout. Clients never
// share a record, so concurrent shells keep independent choices. The payload
// is opaque here; its schema belongs to the shell that writes it.
const MaxViewMemoryBytes = 1 << 20

// seq is a store-wide write order, not a clock, so "most recent" is exact even
// when the system clock moves.
const viewMemorySchema = `CREATE TABLE IF NOT EXISTS view_memory_v1 (
 client_id TEXT NOT NULL, workspace_id TEXT NOT NULL, seq INTEGER NOT NULL, payload BLOB NOT NULL,
 PRIMARY KEY(client_id, workspace_id));`

func validateViewMemory(a Attachment, workspaceID string) error {
	if !validID(a.ClientID) || !validEpoch(a.Epoch) || !validID(workspaceID) {
		return ErrInvalid
	}
	return nil
}

// LoadViewMemory returns this client's record for workspaceID. A client with
// no record of its own (a new terminal) starts from a copy of the most
// recently written record of any client for the same workspace, and seeded
// reports that. The copy is never written back to its source. ErrNotFound
// means no client has opened the workspace yet.
func (s *Store) LoadViewMemory(ctx context.Context, a Attachment, workspaceID string) (payload json.RawMessage, seeded bool, err error) {
	if err := validateViewMemory(a, workspaceID); err != nil {
		return nil, false, err
	}
	err = s.transaction(ctx, func(c *sql.Conn) error {
		if err := authorize(ctx, c, a.ClientID, a.Epoch); err != nil {
			return err
		}
		if _, err := c.ExecContext(ctx, viewMemorySchema); err != nil {
			return err
		}
		var raw []byte
		err := c.QueryRowContext(ctx, `SELECT payload FROM view_memory_v1 WHERE client_id=? AND workspace_id=?`, a.ClientID, workspaceID).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			seeded = true
			err = c.QueryRowContext(ctx, `SELECT payload FROM view_memory_v1 WHERE workspace_id=? ORDER BY seq DESC LIMIT 1`, workspaceID).Scan(&raw)
		}
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err == nil && (len(raw) > MaxViewMemoryBytes || !json.Valid(raw)) {
			return ErrInvalid
		}
		payload = raw
		return err
	})
	if err != nil {
		return nil, false, err
	}
	return payload, seeded, nil
}

// SaveViewMemory replaces this client's record. Only the attached epoch may
// write, so a stale shell of the same profile cannot overwrite a newer one.
func (s *Store) SaveViewMemory(ctx context.Context, a Attachment, workspaceID string, payload json.RawMessage) error {
	if err := validateViewMemory(a, workspaceID); err != nil {
		return err
	}
	if len(payload) == 0 || len(payload) > MaxViewMemoryBytes || !json.Valid(payload) {
		return ErrInvalid
	}
	return s.transaction(ctx, func(c *sql.Conn) error {
		if err := authorize(ctx, c, a.ClientID, a.Epoch); err != nil {
			return err
		}
		if _, err := c.ExecContext(ctx, viewMemorySchema); err != nil {
			return err
		}
		_, err := c.ExecContext(ctx, `INSERT INTO view_memory_v1 VALUES (?,?,(SELECT COALESCE(MAX(seq),0)+1 FROM view_memory_v1),?)
ON CONFLICT(client_id,workspace_id) DO UPDATE SET seq=excluded.seq,payload=excluded.payload`, a.ClientID, workspaceID, []byte(payload))
		return err
	})
}
