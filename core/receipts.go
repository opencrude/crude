package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"

	"crude/coreprotocol"

	_ "modernc.org/sqlite"
)

// receiptJournal records intent before an effect and its exact result afterward.
// A crash between those commits is uncertain, not a license to replay the effect.
type receiptJournal struct {
	db   *sql.DB
	lock *flock.Flock
}

func privatePath(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for current := absolute; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("receipt path contains symlink: %s", current)
			}
			if current == absolute {
				if !info.Mode().IsRegular() {
					return errors.New("receipt files must be private regular files")
				}
				if err := receiptFilePermissions(current, info); err != nil {
					return fmt.Errorf("receipt file %s: %w", current, err)
				}
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	return nil
}

func openReceipts(path string) (*receiptJournal, error) {
	if path == "" {
		return nil, errors.New("receipt path is required")
	}
	if err := privatePath(path); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if err := privatePath(path + ".lock"); err != nil {
		return nil, err
	}
	lockFile, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := lockFile.Close(); err != nil {
		return nil, err
	}
	if err := privatePath(path + ".lock"); err != nil {
		return nil, err
	}
	lock := flock.New(path + ".lock")
	locked, err := lock.TryLock()
	if err != nil {
		return nil, err
	}
	if !locked {
		return nil, errors.New("receipt journal is already owned by another core")
	}
	keep := false
	defer func() {
		if !keep {
			_ = lock.Unlock()
		}
	}()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := privatePath(path + suffix); err != nil {
			return nil, err
		}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	if err := privatePath(path); err != nil {
		return nil, err
	}
	// synchronous=NORMAL keeps every commit across process crashes (WAL) and
	// skips the per-commit flush: receipts fence replays between live
	// processes, and a power loss also ends the shell holding the request
	// identities. The two flushes per mutation ran on the workspace actor and
	// cost tens to hundreds of milliseconds on Windows.
	// Pragmas in the DSN apply to replacement connections too.
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS receipts (
 client TEXT NOT NULL, request TEXT NOT NULL, digest BLOB NOT NULL,
 workspace TEXT NOT NULL, method TEXT NOT NULL, state TEXT NOT NULL,
 response BLOB, created INTEGER NOT NULL, PRIMARY KEY(client, request));
UPDATE receipts SET state='uncertain' WHERE state='pending';`)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	keep = true
	return &receiptJournal{db: db, lock: lock}, nil
}

func (s *receiptJournal) close() error {
	return errors.Join(s.db.Close(), s.lock.Unlock())
}

// requestDigest is the identity of a request's content: workspace, method and
// exact payload bytes, each length-prefixed. Epoch is connection ownership,
// not operation identity: a reconnected client must be able to recover the
// prior result using its original ID.
func requestDigest(request coreprotocol.Request) [32]byte {
	h := sha256.New()
	for _, part := range [][]byte{[]byte(request.WorkspaceID), []byte(request.Method), request.Payload} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		h.Write(size[:])
		h.Write(part)
	}
	return [32]byte(h.Sum(nil))
}

func (s *receiptJournal) begin(ctx context.Context, request coreprotocol.Request) (coreprotocol.Receipt, bool, error) {
	defer receiptLatency.Since(time.Now())
	digest := requestDigest(request)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return coreprotocol.Receipt{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	stamp := time.Now()
	result, err := tx.ExecContext(ctx, `INSERT INTO receipts(client,request,digest,workspace,method,state,created) VALUES(?,?,?,?,?,'pending',?) ON CONFLICT(client,request) DO NOTHING`, request.Owner.ClientID, request.ID, digest[:], request.WorkspaceID, request.Method, stamp.UnixNano())
	if err != nil {
		return coreprotocol.Receipt{}, false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return coreprotocol.Receipt{}, false, err
	}
	receipt, actual, err := scanReceipt(tx.QueryRowContext(ctx, `SELECT client,request,digest,workspace,method,state,response,created FROM receipts WHERE client=? AND request=?`, request.Owner.ClientID, request.ID))
	if err != nil {
		return coreprotocol.Receipt{}, false, err
	}
	if actual != digest {
		return coreprotocol.Receipt{}, false, fmt.Errorf("request identity was already used with different content")
	}
	if err := tx.Commit(); err != nil {
		return coreprotocol.Receipt{}, false, err
	}
	return receipt, n == 1, nil
}

func (s *receiptJournal) complete(ctx context.Context, request coreprotocol.Request, response coreprotocol.Response) error {
	defer receiptLatency.Since(time.Now())
	response.Value = nil
	encoded, err := coreprotocol.Marshal(&response)
	if err != nil {
		return err
	}
	digest := requestDigest(request)
	result, err := s.db.ExecContext(ctx, `UPDATE receipts SET state='completed',response=? WHERE client=? AND request=? AND digest=? AND state='pending'`, encoded, request.Owner.ClientID, request.ID, digest[:])
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("receipt was not pending for this exact request")
	}
	return nil
}

func (s *receiptJournal) lookup(ctx context.Context, client, id string) (coreprotocol.Receipt, error) {
	r, _, err := scanReceipt(s.db.QueryRowContext(ctx, `SELECT client,request,digest,workspace,method,state,response,created FROM receipts WHERE client=? AND request=?`, client, id))
	return r, err
}

func scanReceipt(row *sql.Row) (coreprotocol.Receipt, [32]byte, error) {
	var result coreprotocol.Receipt
	var digest, raw []byte
	var created int64
	err := row.Scan(&result.ClientID, &result.RequestID, &digest, &result.WorkspaceID, &result.Method, &result.State, &raw, &created)
	if err != nil {
		return result, [32]byte{}, err
	}
	if len(digest) != sha256.Size {
		return result, [32]byte{}, errors.New("corrupt receipt digest")
	}
	result.CreatedAt = time.Unix(0, created)
	if len(raw) > 0 {
		if err = coreprotocol.Decode(raw, &result.Response); err != nil {
			return result, [32]byte{}, err
		}
	}
	return result, [32]byte(digest), nil
}
