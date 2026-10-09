package clientstate

import (
	"context"
	"database/sql"
	"errors"
	"math"
)

const MaxEditorRecoveryRecords = 128

// EditorRecoveryInfo intentionally contains no payload. Listing a private
// profile must not materialize up to 128 maximum-sized document snapshots.
type EditorRecoveryInfo struct {
	Key             EditorRecoveryKey
	Epoch, Revision uint64
	Bytes           int64
}

func (s *Store) ListEditorRecoveries(ctx context.Context, a Attachment, workspaceID string) ([]EditorRecoveryInfo, error) {
	if err := validateEditorRecoveryKey(a, EditorRecoveryKey{a.ClientID, workspaceID, "listing"}); err != nil {
		return nil, err
	}
	var result []EditorRecoveryInfo
	err := s.transaction(ctx, func(c *sql.Conn) error {
		if err := authorize(ctx, c, a.ClientID, a.Epoch); err != nil {
			return err
		}
		if _, err := c.ExecContext(ctx, editorRecoverySchema); err != nil {
			return err
		}
		rows, err := c.QueryContext(ctx, `SELECT path,revision,length(payload) FROM editor_recovery_v1 WHERE client_id=? AND workspace_id=? AND length(payload)>0 ORDER BY path LIMIT ?`, a.ClientID, workspaceID, MaxEditorRecoveryRecords)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			r := EditorRecoveryInfo{Key: EditorRecoveryKey{ClientID: a.ClientID, WorkspaceID: workspaceID}, Epoch: a.Epoch}
			if err := rows.Scan(&r.Key.Path, &r.Revision, &r.Bytes); err != nil {
				return err
			}
			if validateEditorRecoveryKey(a, r.Key) != nil || r.Revision == 0 || r.Revision > math.MaxInt64 || r.Bytes > MaxEditorRecoveryBytes {
				return ErrInvalid
			}
			result = append(result, r)
		}
		return rows.Err()
	})
	return result, err
}

// StageEditorRecovery checks both the selected immutable observation and the
// source preimage in one transaction. Unread drafts are never overwritten and
// the selected backup is never consumed.
func (s *Store) StageEditorRecovery(ctx context.Context, a Attachment, selected EditorRecoveryRecord, source EditorRecoveryKey, expected uint64) (EditorRecoveryRecord, error) {
	if validateEditorRecoveryKey(a, selected.Key) != nil || validateEditorRecoveryKey(a, source) != nil || selected.Key.WorkspaceID != source.WorkspaceID || selected.Epoch != a.Epoch || expected >= math.MaxInt64 {
		return EditorRecoveryRecord{}, ErrInvalid
	}
	var result EditorRecoveryRecord
	err := s.transaction(ctx, func(c *sql.Conn) error {
		if err := authorize(ctx, c, a.ClientID, a.Epoch); err != nil {
			return err
		}
		current, err := readEditorRecovery(ctx, c, selected.Key)
		if err != nil {
			return err
		}
		if current.Revision != selected.Revision || len(current.Payload) == 0 {
			return ErrConflict
		}
		if selected.Key == source {
			result = current
			result.Epoch = a.Epoch
			return nil
		}
		old, err := readEditorRecovery(ctx, c, source)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if old.Revision != expected || len(old.Payload) != 0 {
			return ErrConflict
		}
		_, err = c.ExecContext(ctx, `INSERT INTO editor_recovery_v1 VALUES (?,?,?,?,?) ON CONFLICT(client_id,workspace_id,path) DO UPDATE SET revision=excluded.revision,payload=excluded.payload`, source.ClientID, source.WorkspaceID, source.Path, expected+1, []byte(current.Payload))
		result = EditorRecoveryRecord{Key: source, Epoch: a.Epoch, Revision: expected + 1, Payload: current.Payload}
		return err
	})
	return result, err
}
