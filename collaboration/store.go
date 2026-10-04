package collaboration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

type Store struct {
	db           *sql.DB
	workspaceID  string
	lockDir      string
	lockMu       sync.Mutex
	runtimeLocks map[string]ownedRuntimeLock
	subscriberMu sync.Mutex
	subscribers  map[chan struct{}]struct{}
}

type ownedRuntimeLock struct {
	owner string
	lock  *processLock
}

var errProcessLockHeld = errors.New("collaboration runtime lock is held")

const schema = `
CREATE TABLE IF NOT EXISTS collaboration_workspace (
	singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
	workspace_id TEXT NOT NULL,
	schema_version INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS collaboration_state (
	singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
	last_sequence INTEGER NOT NULL DEFAULT 0,
	human_read_sequence INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS collaboration_agents (
	id TEXT PRIMARY KEY,
	workspace_id TEXT NOT NULL,
	name TEXT NOT NULL,
	session_id TEXT NOT NULL,
	parent_id TEXT NOT NULL DEFAULT '',
	category TEXT NOT NULL DEFAULT '',
	state TEXT NOT NULL CHECK (state IN ('available','suspended','closed')),
	presence TEXT NOT NULL CHECK (presence IN ('attached','offline','unresponsive')),
	runtime_epoch INTEGER NOT NULL DEFAULT 0,
	owner_instance TEXT NOT NULL DEFAULT '',
	current_run_id TEXT NOT NULL DEFAULT '',
	current_request_id TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	heartbeat_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_collaboration_agents_session
	ON collaboration_agents(session_id, state, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_collaboration_agents_parent
	ON collaboration_agents(parent_id, state, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_collaboration_agents_live_run
	ON collaboration_agents(state, presence, current_run_id);
CREATE TABLE IF NOT EXISTS collaboration_runs (
	id TEXT PRIMARY KEY,
	workspace_id TEXT NOT NULL,
	agent_id TEXT NOT NULL,
	runtime_epoch INTEGER NOT NULL,
	request_id TEXT NOT NULL DEFAULT '',
	origin TEXT NOT NULL CHECK (origin IN ('human','peer','runtime')),
	state TEXT NOT NULL,
	version INTEGER NOT NULL DEFAULT 1,
	outcome TEXT NOT NULL DEFAULT '',
	context_commit_id TEXT NOT NULL DEFAULT '',
	started_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	ended_at INTEGER NOT NULL DEFAULT 0,
	FOREIGN KEY (agent_id) REFERENCES collaboration_agents(id)
);
CREATE INDEX IF NOT EXISTS idx_collaboration_runs_agent
	ON collaboration_runs(agent_id, started_at DESC);
CREATE TABLE IF NOT EXISTS collaboration_events (
	id TEXT PRIMARY KEY,
	workspace_id TEXT NOT NULL,
	sequence INTEGER NOT NULL UNIQUE,
	sender_id TEXT NOT NULL,
	idempotency_key TEXT NOT NULL,
	fingerprint TEXT NOT NULL,
	origin TEXT NOT NULL,
	kind TEXT NOT NULL,
	body TEXT NOT NULL,
	reply_to TEXT NOT NULL DEFAULT '',
	thread_id TEXT NOT NULL,
	request_id TEXT NOT NULL DEFAULT '',
	request_revision INTEGER NOT NULL DEFAULT 0,
	run_id TEXT NOT NULL DEFAULT '',
	supersedes TEXT NOT NULL DEFAULT '',
	event_json TEXT NOT NULL,
	payload_bytes INTEGER NOT NULL,
	archived INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL,
	UNIQUE (workspace_id, sender_id, idempotency_key)
);
CREATE INDEX IF NOT EXISTS idx_collaboration_events_thread
	ON collaboration_events(thread_id, sequence);
CREATE INDEX IF NOT EXISTS idx_collaboration_events_kind
	ON collaboration_events(kind, sequence);
CREATE TABLE IF NOT EXISTS collaboration_deliveries (
	id TEXT PRIMARY KEY,
	event_id TEXT NOT NULL,
	recipient_id TEXT NOT NULL,
	state TEXT NOT NULL CHECK (state IN ('pending','admitting','admitted','superseded','withdrawn','rejected','expired')),
	version INTEGER NOT NULL DEFAULT 1,
	reserved_epoch INTEGER NOT NULL DEFAULT 0,
	context_commit_id TEXT NOT NULL DEFAULT '',
	reason TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	admitted_at INTEGER NOT NULL DEFAULT 0,
	UNIQUE (event_id, recipient_id),
	FOREIGN KEY (event_id) REFERENCES collaboration_events(id),
	FOREIGN KEY (recipient_id) REFERENCES collaboration_agents(id)
);
CREATE INDEX IF NOT EXISTS idx_collaboration_deliveries_pending
	ON collaboration_deliveries(recipient_id, state, created_at, id);
CREATE TABLE IF NOT EXISTS collaboration_artifacts (
	id TEXT NOT NULL,
	revision INTEGER NOT NULL,
	workspace_id TEXT NOT NULL,
	author_id TEXT NOT NULL,
	run_id TEXT NOT NULL DEFAULT '',
	request_id TEXT NOT NULL DEFAULT '',
	kind TEXT NOT NULL CHECK (kind IN ('interim','final','answer')),
	summary TEXT NOT NULL,
	body TEXT NOT NULL,
	digest TEXT NOT NULL,
	supersedes_revision INTEGER NOT NULL DEFAULT 0,
	references_json TEXT NOT NULL DEFAULT '[]',
	metadata_json TEXT NOT NULL DEFAULT '{}',
	created_at INTEGER NOT NULL,
	PRIMARY KEY (id, revision),
	FOREIGN KEY (author_id) REFERENCES collaboration_agents(id)
);
CREATE TABLE IF NOT EXISTS collaboration_report_dedupe (
	author_id TEXT NOT NULL,
	idempotency_key TEXT NOT NULL,
	fingerprint TEXT NOT NULL,
	artifact_id TEXT NOT NULL,
	revision INTEGER NOT NULL,
	event_id TEXT NOT NULL,
	PRIMARY KEY (author_id, idempotency_key)
);
CREATE TABLE IF NOT EXISTS collaboration_claims (
	id TEXT PRIMARY KEY,
	path TEXT NOT NULL,
	agent_id TEXT NOT NULL,
	run_id TEXT NOT NULL DEFAULT '',
	request_id TEXT NOT NULL DEFAULT '',
	version INTEGER NOT NULL DEFAULT 1,
	acquired_at INTEGER NOT NULL,
	released_at INTEGER NOT NULL DEFAULT 0,
	FOREIGN KEY (agent_id) REFERENCES collaboration_agents(id)
);
CREATE INDEX IF NOT EXISTS idx_collaboration_claims_active
	ON collaboration_claims(released_at, path);
`

func Attach(db *sql.DB, workspaceID, lockDir string) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: nil database", ErrInvalid)
	}
	if err := validateIdentity("workspace_id", workspaceID, 512); err != nil {
		return nil, err
	}
	if strings.TrimSpace(lockDir) == "" {
		lockDir = filepath.Join(os.TempDir(), "crude-collaboration-locks", hashBytes([]byte(workspaceID)))
	}
	store := &Store{
		db: db, workspaceID: workspaceID,
		lockDir: lockDir, runtimeLocks: make(map[string]ownedRuntimeLock),
		subscribers: make(map[chan struct{}]struct{}),
	}
	err := transact(context.Background(), db, func(tx *sql.Tx) error {
		if _, err := tx.Exec(schema); err != nil {
			return fmt.Errorf("collaboration: create schema: %w", err)
		}
		if _, err := tx.Exec(`INSERT INTO collaboration_workspace(singleton,workspace_id,schema_version)
			VALUES(1,?,?) ON CONFLICT(singleton) DO NOTHING`, workspaceID, SchemaVersion); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO collaboration_state(singleton,last_sequence,human_read_sequence)
			VALUES(1,0,0) ON CONFLICT(singleton) DO NOTHING`); err != nil {
			return err
		}
		var attached string
		var version int
		if err := tx.QueryRow(`SELECT workspace_id,schema_version FROM collaboration_workspace WHERE singleton=1`).Scan(&attached, &version); err != nil {
			return err
		}
		if attached != workspaceID {
			return ErrWorkspace
		}
		if version != SchemaVersion {
			return fmt.Errorf("%w: collaboration schema %d, need %d", ErrConflict, version, SchemaVersion)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return store, nil
}

func (store *Store) WorkspaceID() string {
	if store == nil {
		return ""
	}
	return store.workspaceID
}

func (store *Store) acquireRuntimeLock(agentID, owner string) (bool, error) {
	if store == nil || owner == "" {
		return false, nil
	}
	store.lockMu.Lock()
	defer store.lockMu.Unlock()
	if existing, ok := store.runtimeLocks[agentID]; ok {
		if existing.owner == owner {
			return false, nil
		}
		return false, fmt.Errorf("%w: agent %q is owned by %q", ErrConflict, agentID, existing.owner)
	}
	if err := os.MkdirAll(store.lockDir, 0o700); err != nil {
		return false, fmt.Errorf("collaboration: create runtime lock directory: %w", err)
	}
	path := filepath.Join(store.lockDir, hashBytes([]byte(store.workspaceID+"\x00"+agentID))+".lock")
	lock, err := acquireProcessLock(path)
	if err != nil {
		if errors.Is(err, errProcessLockHeld) {
			return false, fmt.Errorf("%w: agent %q already has a live runtime owner", ErrConflict, agentID)
		}
		return false, err
	}
	store.runtimeLocks[agentID] = ownedRuntimeLock{owner: owner, lock: lock}
	return true, nil
}

func (store *Store) releaseRuntimeLock(agentID, owner string) {
	if store == nil || agentID == "" {
		return
	}
	store.lockMu.Lock()
	owned, ok := store.runtimeLocks[agentID]
	if !ok || owner != "" && owned.owner != owner {
		store.lockMu.Unlock()
		return
	}
	delete(store.runtimeLocks, agentID)
	store.lockMu.Unlock()
	_ = owned.lock.release()
}

func (store *Store) Close() error {
	if store == nil {
		return nil
	}
	store.lockMu.Lock()
	locks := make([]ownedRuntimeLock, 0, len(store.runtimeLocks))
	for _, owned := range store.runtimeLocks {
		locks = append(locks, owned)
	}
	store.runtimeLocks = make(map[string]ownedRuntimeLock)
	store.lockMu.Unlock()
	var first error
	for _, owned := range locks {
		if err := owned.lock.release(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (store *Store) Subscribe() (<-chan struct{}, func()) {
	notifications := make(chan struct{}, 1)
	if store == nil {
		close(notifications)
		return notifications, func() {}
	}
	store.subscriberMu.Lock()
	store.subscribers[notifications] = struct{}{}
	store.subscriberMu.Unlock()
	cancel := sync.OnceFunc(func() {
		store.subscriberMu.Lock()
		if _, exists := store.subscribers[notifications]; exists {
			delete(store.subscribers, notifications)
			close(notifications)
		}
		store.subscriberMu.Unlock()
	})
	return notifications, cancel
}

func (store *Store) signal() {
	if store == nil {
		return
	}
	store.subscriberMu.Lock()
	defer store.subscriberMu.Unlock()
	for subscriber := range store.subscribers {
		select {
		case subscriber <- struct{}{}:
		default:
		}
	}
}

func transact(ctx context.Context, db *sql.DB, operation func(*sql.Tx) error) error {
	deadline := time.Now().Add(5 * time.Second)
	delay := 2 * time.Millisecond
	for {
		err := func() error {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer func() { _ = tx.Rollback() }()
			if err := operation(tx); err != nil {
				return err
			}
			return tx.Commit()
		}()
		if err == nil || !sqliteBusy(err) || time.Now().After(deadline) {
			return err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if delay < 25*time.Millisecond {
			delay *= 2
		}
	}
}

func sqliteBusy(err error) bool {
	var coded interface{ Code() int }
	return errors.As(err, &coded) && (coded.Code()&255 == 5 || coded.Code()&255 == 6)
}

func (store *Store) transaction(ctx context.Context, operation func(*sql.Tx) error) error {
	if store == nil || store.db == nil {
		return fmt.Errorf("%w: detached store", ErrInvalid)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return transact(ctx, store.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE collaboration_state SET last_sequence=last_sequence WHERE singleton=1`); err != nil {
			return err
		}
		return operation(tx)
	})
}

func (store *Store) readTransaction(ctx context.Context, operation func(*sql.Tx) error) error {
	if store == nil || store.db == nil {
		return fmt.Errorf("%w: detached store", ErrInvalid)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return transact(ctx, store.db, operation)
}

func newID(prefix string) string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", prefix, time.Now().UnixNano())))
		return prefix + "_" + hex.EncodeToString(sum[:16])
	}
	return prefix + "_" + hex.EncodeToString(value[:])
}

func NewInstanceID() string {
	return newID("instance")
}

func hashBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func hashJSON(value any) (string, []byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", nil, err
	}
	return hashBytes(encoded), encoded, nil
}

func validateIdentity(name, value string, limit int) error {
	if value == "" || len(value) > limit || !utf8.ValidString(value) {
		return fmt.Errorf("%w: %s must be nonempty UTF-8 within %d bytes", ErrInvalid, name, limit)
	}
	if strings.TrimSpace(value) != value || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return fmt.Errorf("%w: %s contains whitespace/control boundaries", ErrInvalid, name)
	}
	return nil
}

func validateOptionalIdentity(name, value string, limit int) error {
	if value == "" {
		return nil
	}
	return validateIdentity(name, value, limit)
}

func validateBody(body string, limit int) error {
	if strings.TrimSpace(body) == "" || !utf8.ValidString(body) {
		return fmt.Errorf("%w: body must be nonempty UTF-8", ErrInvalid)
	}
	if len(body) > limit {
		return ErrTooLarge
	}
	return nil
}

func validateReferences(references []Reference) error {
	if len(references) > MaxReferences {
		return ErrTooLarge
	}
	for _, reference := range references {
		if err := validateIdentity("reference kind", reference.Kind, 48); err != nil {
			return err
		}
		if err := validateIdentity("reference id", reference.ID, 512); err != nil {
			return err
		}
		if reference.Revision < 0 {
			return fmt.Errorf("%w: reference revision", ErrInvalid)
		}
	}
	return nil
}

func validateAgentState(state AgentState) bool {
	switch state {
	case AgentAvailable, AgentSuspended, AgentClosed:
		return true
	default:
		return false
	}
}

func validatePresence(presence RuntimePresence) bool {
	switch presence {
	case RuntimeAttached, RuntimeOffline, RuntimeUnresponsive:
		return true
	default:
		return false
	}
}

func validateOrigin(origin Origin) bool {
	switch origin {
	case OriginHuman, OriginPeer, OriginRuntime:
		return true
	default:
		return false
	}
}

func validateEventKind(kind EventKind) bool {
	switch kind {
	case EventHumanMessage, EventHumanSteering, EventPeerNote, EventPeerReply,
		EventWorkRequest, EventWorkRevision, EventRequestDisposition,
		EventReportPublished, EventRunOutcome, EventPermissionRequest,
		EventQuestionRequest, EventLifecycleControl, EventControlOutcome, EventCommandOutcome:
		return true
	default:
		return false
	}
}

func validateRunState(state RunState) bool {
	switch state {
	case RunQueued, RunRunning, RunWaitingHuman, RunWaitingEvent, RunPausing,
		RunPaused, RunStopping, RunSucceeded, RunFailed, RunCancelled, RunInterrupted:
		return true
	default:
		return false
	}
}

func validateReportKind(kind ReportKind) bool {
	switch kind {
	case ReportInterim, ReportFinal, ReportAnswer:
		return true
	default:
		return false
	}
}

func normalizedPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("%w: empty claim path", ErrInvalid)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("%w: normalize claim path: %v", ErrInvalid, err)
	}
	return filepath.Clean(absolute), nil
}

func pathsOverlap(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	return left == right || pathWithin(left, right) || pathWithin(right, left)
}

func pathWithin(path, parent string) bool {
	relative, err := filepath.Rel(parent, path)
	if err != nil {
		return false
	}
	return relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func timeFromUnixNano(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(0, value).UTC()
}
