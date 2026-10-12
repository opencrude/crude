// Package clientstate persists client-private composer snapshots independently
// of workspace/session databases. It has no UI or credential dependencies.
// Epochs fence attachments; record revisions fence content writes; request IDs
// identify durable receipts. None of these identities is interchangeable.
package clientstate

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

const (
	MaxIDBytes    = 256
	MaxDraftBytes = 1 << 20 // Maximum encoded snapshot size, including references.
)

var (
	ErrInvalid         = errors.New("clientstate: invalid input")
	ErrAttached        = errors.New("clientstate: client already attached")
	ErrStaleEpoch      = errors.New("clientstate: stale or released attachment epoch")
	ErrConflict        = errors.New("clientstate: draft revision conflict")
	ErrRequestMismatch = errors.New("clientstate: request ID reused with different payload")
	ErrNotFound        = errors.New("clientstate: not found")
)

// Key is the entire namespace, including the private client profile.
type Key struct {
	ClientID    string
	WorkspaceID string
	SessionID   string
}

// RefSpan retains explicit reference identity, never inferred from bracket text.
// Positions, cursor and selection are rune offsets in Text.
type RefSpan struct {
	ID    string `json:"id"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

// Draft is a complete value snapshot, not a patch. Revision is the composer's
// local editing revision; Record.Revision is the independent persistent CAS.
// Store does not retain caller slices and returns detached snapshots.
type Draft struct {
	Text          string    `json:"text"`
	RefIDs        []string  `json:"ref_ids"`
	Cursor        int       `json:"cursor"`
	SelectionFrom int       `json:"selection_from"`
	SelectionTo   int       `json:"selection_to"`
	Scroll        int       `json:"scroll"`
	Revision      uint64    `json:"revision"`
	Spans         []RefSpan `json:"spans"`
}

type Record struct {
	Key      Key
	Revision uint64
	Draft    Draft
}

type Attachment struct {
	ClientID string
	Epoch    uint64
}

// AcquireOptions requires the exact observed epoch for takeover. Normal acquire
// requires ExpectedEpoch == 0 and succeeds only for an unattached profile.
type AcquireOptions struct {
	Takeover      bool
	ExpectedEpoch uint64
}

type Put struct {
	ClientID         string
	Epoch            uint64
	RequestID        string
	Key              Key
	ExpectedRevision uint64 // Zero creates a previously absent draft.
	Draft            Draft
}

type Store struct{ db *sql.DB }

const schema = `
CREATE TABLE IF NOT EXISTS client_profiles (
 client_id TEXT PRIMARY KEY, epoch INTEGER NOT NULL, attached INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS drafts (
 client_id TEXT NOT NULL, workspace_id TEXT NOT NULL, session_id TEXT NOT NULL,
 revision INTEGER NOT NULL, draft BLOB NOT NULL,
 PRIMARY KEY(client_id, workspace_id, session_id)
);
CREATE TABLE IF NOT EXISTS draft_receipts (
 client_id TEXT NOT NULL, request_id TEXT NOT NULL, payload BLOB NOT NULL,
 workspace_id TEXT NOT NULL, session_id TEXT NOT NULL,
 revision INTEGER NOT NULL, draft BLOB NOT NULL,
 PRIMARY KEY(client_id, request_id)
);
PRAGMA user_version = 1;
`

// Open opens a dedicated database at an explicit filesystem path. The caller
// owns path selection, directory creation, profile identity and attachment
// lifetime. Close does not release profiles: a crash/reconnect needs an explicit
// epoch-fenced takeover rather than accidentally permitting concurrent owners.
func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" || path == ":memory:" {
		return nil, fmt.Errorf("%w: persistent path required", ErrInvalid)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	// A Windows drive belongs in the URI path, not its authority.
	if !strings.HasPrefix(u.Path, "/") {
		u.Path = "/" + u.Path
	}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{db: db}
	err = s.transaction(context.Background(), func(c *sql.Conn) error {
		var version int
		if e := c.QueryRowContext(context.Background(), "PRAGMA user_version").Scan(&version); e != nil {
			return e
		}
		if version != 0 && version != 1 {
			return fmt.Errorf("clientstate: unsupported schema version %d", version)
		}
		_, e := c.ExecContext(context.Background(), schema)
		return e
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// transaction takes SQLite's write reservation before reading preimages, so
// separate Store handles/processes cannot both accept the same CAS or epoch.
func (s *Store) transaction(ctx context.Context, fn func(*sql.Conn) error) error {
	c, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	if _, err = c.ExecContext(ctx, "PRAGMA busy_timeout = 5000; PRAGMA synchronous = FULL;"); err != nil {
		return err
	}
	if _, err = c.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer func() { _, _ = c.ExecContext(context.Background(), "ROLLBACK") }()
	if err = fn(c); err != nil {
		return err
	}
	_, err = c.ExecContext(ctx, "COMMIT")
	return err
}

func validID(id string) bool {
	if id == "" || len(id) > MaxIDBytes || !utf8.ValidString(id) || strings.TrimSpace(id) != id {
		return false
	}
	for _, r := range id {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validEpoch(epoch uint64) bool { return epoch > 0 && epoch <= math.MaxInt64 }

func validateKey(client string, key Key) error {
	if !validID(client) || key.ClientID != client || !validID(key.WorkspaceID) || !validID(key.SessionID) {
		return fmt.Errorf("%w: namespace", ErrInvalid)
	}
	return nil
}

func authorize(ctx context.Context, c *sql.Conn, client string, epoch uint64) error {
	if !validID(client) || !validEpoch(epoch) {
		return ErrInvalid
	}
	var current uint64
	var attached bool
	err := c.QueryRowContext(ctx, "SELECT epoch, attached FROM client_profiles WHERE client_id = ?", client).Scan(&current, &attached)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrStaleEpoch
	}
	if err != nil {
		return err
	}
	if !attached || current != epoch {
		return ErrStaleEpoch
	}
	return nil
}

func (s *Store) Acquire(ctx context.Context, client string, opts AcquireOptions) (Attachment, error) {
	if !validID(client) || (!opts.Takeover && opts.ExpectedEpoch != 0) || (opts.Takeover && !validEpoch(opts.ExpectedEpoch)) {
		return Attachment{}, ErrInvalid
	}
	var result Attachment
	err := s.transaction(ctx, func(c *sql.Conn) error {
		var epoch uint64
		var attached bool
		err := c.QueryRowContext(ctx, "SELECT epoch, attached FROM client_profiles WHERE client_id = ?", client).Scan(&epoch, &attached)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if opts.Takeover {
			if err != nil || epoch != opts.ExpectedEpoch {
				return ErrStaleEpoch
			}
		} else if attached {
			return ErrAttached
		}
		if epoch == math.MaxInt64 {
			return fmt.Errorf("%w: epoch exhausted", ErrInvalid)
		}
		epoch++
		_, err = c.ExecContext(ctx, `INSERT INTO client_profiles VALUES (?, ?, 1)
ON CONFLICT(client_id) DO UPDATE SET epoch = excluded.epoch, attached = 1`, client, epoch)
		if err == nil {
			result = Attachment{ClientID: client, Epoch: epoch}
		}
		return err
	})
	if err != nil {
		return Attachment{}, err
	}
	return result, err
}

func (s *Store) Release(ctx context.Context, client string, epoch uint64) error {
	return s.transaction(ctx, func(c *sql.Conn) error {
		if err := authorize(ctx, c, client, epoch); err != nil {
			return err
		}
		_, err := c.ExecContext(ctx, "UPDATE client_profiles SET attached = 0 WHERE client_id = ? AND epoch = ?", client, epoch)
		return err
	})
}

func (s *Store) Load(client string, epoch uint64, key Key) (Record, error) {
	return s.LoadContext(context.Background(), client, epoch, key)
}

func (s *Store) LoadContext(ctx context.Context, client string, epoch uint64, key Key) (Record, error) {
	if err := validateKey(client, key); err != nil {
		return Record{}, err
	}
	var record Record
	err := s.transaction(ctx, func(c *sql.Conn) error {
		if err := authorize(ctx, c, client, epoch); err != nil {
			return err
		}
		var revision uint64
		var raw []byte
		err := c.QueryRowContext(ctx, "SELECT revision, draft FROM drafts WHERE client_id = ? AND workspace_id = ? AND session_id = ?", client, key.WorkspaceID, key.SessionID).Scan(&revision, &raw)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		record, err = decodeRecord(key, revision, raw)
		return err
	})
	return record, err
}

func encodeDraft(d Draft) ([]byte, error) {
	if len(d.Text) > MaxDraftBytes || !utf8.ValidString(d.Text) || len(d.RefIDs) > MaxDraftBytes/2 || len(d.Spans) > MaxDraftBytes/2 {
		return nil, fmt.Errorf("%w: draft size or encoding", ErrInvalid)
	}
	n := utf8.RuneCountInString(d.Text)
	if d.Cursor < 0 || d.Cursor > n || d.SelectionFrom < 0 || d.SelectionFrom > n || d.SelectionTo < 0 || d.SelectionTo > n || d.Scroll < 0 {
		return nil, fmt.Errorf("%w: draft positions", ErrInvalid)
	}
	// Reject oversized collections before JSON allocation. Even their unescaped
	// content and minimum delimiters must fit the encoded snapshot budget.
	minimumBytes := len(d.Text)
	for _, id := range d.RefIDs {
		if !validID(id) {
			return nil, fmt.Errorf("%w: reference ID", ErrInvalid)
		}
		minimumBytes += len(id) + 3
		if minimumBytes > MaxDraftBytes {
			return nil, fmt.Errorf("%w: reference collection size", ErrInvalid)
		}
	}
	for _, span := range d.Spans {
		if !validID(span.ID) || span.Start < 0 || span.End <= span.Start || span.End > n {
			return nil, fmt.Errorf("%w: reference span", ErrInvalid)
		}
		minimumBytes += len(span.ID) + 28
		if minimumBytes > MaxDraftBytes {
			return nil, fmt.Errorf("%w: reference collection size", ErrInvalid)
		}
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxDraftBytes {
		return nil, fmt.Errorf("%w: encoded draft exceeds %d bytes", ErrInvalid, MaxDraftBytes)
	}
	return raw, nil
}

func decodeRecord(key Key, revision uint64, raw []byte) (Record, error) {
	if revision == 0 || revision > math.MaxInt64 || len(raw) > MaxDraftBytes {
		return Record{}, fmt.Errorf("clientstate: corrupt draft record")
	}
	var d Draft
	if err := json.Unmarshal(raw, &d); err != nil {
		return Record{}, err
	}
	if _, err := encodeDraft(d); err != nil {
		return Record{}, err
	}
	return Record{Key: key, Revision: revision, Draft: d}, nil
}

// Save atomically commits both the snapshot and its immutable receipt. A replay
// returns the original committed record, even after subsequent draft edits.
// Epoch is authorization, not payload: an authorized reconnect can recover a
// receipt without repeating the historical write. Old epochs still fail first.
func (s *Store) Save(ctx context.Context, put Put) (Record, error) {
	if err := validateKey(put.ClientID, put.Key); err != nil {
		return Record{}, err
	}
	if !validEpoch(put.Epoch) || !validID(put.RequestID) || put.ExpectedRevision >= math.MaxInt64 {
		return Record{}, ErrInvalid
	}
	raw, err := encodeDraft(put.Draft)
	if err != nil {
		return Record{}, err
	}
	payload, err := json.Marshal(struct {
		Key              Key
		ExpectedRevision uint64
		Draft            json.RawMessage
	}{put.Key, put.ExpectedRevision, raw})
	if err != nil {
		return Record{}, err
	}
	var result Record
	err = s.transaction(ctx, func(c *sql.Conn) error {
		if e := authorize(ctx, c, put.ClientID, put.Epoch); e != nil {
			return e
		}
		var existing []byte
		receipt, e := readReceipt(ctx, c, put.ClientID, put.RequestID, &existing)
		if e == nil {
			if !bytes.Equal(existing, payload) {
				return ErrRequestMismatch
			}
			result = receipt
			return nil
		}
		if !errors.Is(e, ErrNotFound) {
			return e
		}
		var current uint64
		e = c.QueryRowContext(ctx, "SELECT revision FROM drafts WHERE client_id = ? AND workspace_id = ? AND session_id = ?", put.ClientID, put.Key.WorkspaceID, put.Key.SessionID).Scan(&current)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if current != put.ExpectedRevision {
			return ErrConflict
		}
		revision := current + 1
		_, e = c.ExecContext(ctx, `INSERT INTO drafts VALUES (?, ?, ?, ?, ?)
ON CONFLICT(client_id, workspace_id, session_id) DO UPDATE SET revision = excluded.revision, draft = excluded.draft`, put.ClientID, put.Key.WorkspaceID, put.Key.SessionID, revision, raw)
		if e != nil {
			return e
		}
		_, e = c.ExecContext(ctx, "INSERT INTO draft_receipts VALUES (?, ?, ?, ?, ?, ?, ?)", put.ClientID, put.RequestID, payload, put.Key.WorkspaceID, put.Key.SessionID, revision, raw)
		if e != nil {
			return e
		}
		result, e = decodeRecord(put.Key, revision, raw)
		return e
	})
	if err != nil {
		return Record{}, err
	}
	return result, nil
}

func readReceipt(ctx context.Context, c *sql.Conn, client, request string, payload *[]byte) (Record, error) {
	key := Key{ClientID: client}
	var revision uint64
	var raw []byte
	err := c.QueryRowContext(ctx, "SELECT payload, workspace_id, session_id, revision, draft FROM draft_receipts WHERE client_id = ? AND request_id = ?", client, request).Scan(payload, &key.WorkspaceID, &key.SessionID, &revision, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, err
	}
	return decodeRecord(key, revision, raw)
}

// LookupReceipt is an observation only, authorized by the current attachment.
// Request IDs are client-wide and must remain unique across workspaces/sessions.
func (s *Store) LookupReceipt(ctx context.Context, client string, epoch uint64, request string) (Record, error) {
	if !validID(request) {
		return Record{}, ErrInvalid
	}
	var record Record
	err := s.transaction(ctx, func(c *sql.Conn) error {
		if err := authorize(ctx, c, client, epoch); err != nil {
			return err
		}
		var payload []byte
		var err error
		record, err = readReceipt(ctx, c, client, request, &payload)
		return err
	})
	return record, err
}
