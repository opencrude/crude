package localhistory

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
	"unicode/utf8"
)

// SafetyProtectionEvidence is trusted domain evidence, NOT shell-supplied proof.
// The domain owns human authority, workspace identity, original durable journal
// validation, terminal outcome and manifest coverage under its document exclusion.
// Store validates the bounded binding and original immutable snapshot, never
// inferring settlement from current files, age or owner liveness.
type SafetyProtectionEvidence struct {
	WorkspaceKey    string
	OperationID     string
	OperationDigest string
	JournalSHA256   string
	ManifestSHA256  string
	Outcome         string
	OwnerClientID   string
	OwnerEpoch      uint64
}

type SafetyProtectionChange struct {
	Reference        string
	SnapshotID       string
	Action           string
	ExpectedRevision uint64
	Evidence         SafetyProtectionEvidence
	ActorClientID    string
	ActorEpoch       uint64
	RequestID        string
}

type SafetyProtectionEvent struct {
	ID         string
	Reference  string
	SnapshotID string
	Action     string
	Revision   uint64
	CreatedAt  int64
	Snapshot   SnapshotRecord
	Change     SafetyProtectionChange
}

type SafetyProtectionRecord struct {
	Reference  string
	SnapshotID string
	Revision   uint64
	Retained   bool
	// Available in List means indexed metadata exists, not verified health.
	// Inspect returns true only after verifying manifest, entries and objects.
	Available bool
	Snapshot  SnapshotRecord
	LastEvent *SafetyProtectionEvent
}

type SafetyProtectionPage struct {
	Records       []SafetyProtectionRecord
	NextCursor    string
	RetainedCount int64
}

const safetyProtectionPayloadLimit = 64 * 1024

func validSafetyDigest(text string) bool {
	if len(text) != 64 {
		return false
	}
	for _, c := range []byte(text) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func validateSafetyProtectionChange(in SafetyProtectionChange) error {
	e := in.Evidence
	if !validSafetyReference(in.Reference) || !validSafetyDigest(in.SnapshotID) ||
		(in.Action != "release" && in.Action != "reprotect") || in.ExpectedRevision >= math.MaxInt64 ||
		e.OperationID != in.Reference || !validSafetyDigest(e.WorkspaceKey) ||
		!validSafetyDigest(e.OperationDigest) || !validSafetyDigest(e.JournalSHA256) || !validSafetyDigest(e.ManifestSHA256) ||
		(e.Outcome != "committed" && e.Outcome != "refused") ||
		e.OwnerClientID == "" || !validSelectedText(e.OwnerClientID, 512) || e.OwnerEpoch == 0 ||
		in.ActorClientID == "" || !validSelectedText(in.ActorClientID, 512) || in.ActorEpoch == 0 ||
		in.RequestID == "" || !validSelectedText(in.RequestID, 256) {
		return errors.New("localhistory: invalid safety protection change")
	}
	return nil
}

func validSafetySnapshot(record SnapshotRecord) bool {
	return validSafetyDigest(record.ID) && (record.ParentID == "" || validSafetyDigest(record.ParentID)) &&
		record.CreatedAt > 0 && record.LogicalBytes >= 0 &&
		record.Kind != "" && validSelectedText(record.Kind, 512) &&
		len(record.Label) <= 16*1024 && utf8.ValidString(record.Label)
}

// Admission and lock order match snapshot/prune. Returned cleanup only releases
// Store-owned resources; no domain callback is invoked under these locks.
func (s *Store) safetyProtectionOperation(ctx context.Context) (func(), error) {
	if err := s.beginOperation(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	fail := func() { s.mu.Unlock(); s.endOperation() }
	if err := ctx.Err(); err != nil {
		fail()
		return nil, err
	}
	unlock, err := s.lockMutation(ctx)
	if err != nil {
		fail()
		return nil, err
	}
	return func() { unlock(); fail() }, nil
}

// ListSafetyProtections enumerates the union of current pins and audited
// references, without the timeline's cap. Pages are keyset ordered by exact
// reference spelling; a traversal is not a cross-page atomic snapshot.
// This metadata-only listing does not assert content health.
func (s *Store) ListSafetyProtections(ctx context.Context, cursor string, limit int) (SafetyProtectionPage, error) {
	var after string
	if limit < 0 || limit > 200 || len(cursor) > 172 {
		return SafetyProtectionPage{}, errors.New("localhistory: invalid safety protection page")
	}
	if cursor != "" {
		decoded, err := base64.RawURLEncoding.Strict().DecodeString(cursor)
		if err != nil || !validSafetyReference(string(decoded)) ||
			base64.RawURLEncoding.EncodeToString(decoded) != cursor {
			return SafetyProtectionPage{}, errors.New("localhistory: invalid safety protection cursor")
		}
		after = string(decoded)
	}
	if limit == 0 {
		limit = 100
	}
	done, err := s.safetyProtectionOperation(ctx)
	if err != nil {
		return SafetyProtectionPage{}, err
	}
	defer done()
	comparison := " > ?"
	if cursor == "" {
		// Include an invalid empty stored reference on the first page so it
		// fails validation rather than silently disappearing behind a sentinel.
		comparison = " >= ?"
	}
	rows, err := s.db.QueryContext(ctx, `SELECT reference FROM history_retained_snapshots WHERE reference`+comparison+`
UNION SELECT reference FROM history_safety_protection_events WHERE reference`+comparison+`
ORDER BY reference COLLATE BINARY LIMIT ?`, after, after, limit+1)
	if err != nil {
		return SafetyProtectionPage{}, err
	}
	refs := make([]string, 0, limit+1)
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			_ = rows.Close()
			return SafetyProtectionPage{}, err
		}
		refs = append(refs, ref)
	}
	if err := rows.Close(); err != nil {
		return SafetyProtectionPage{}, err
	}
	if err := rows.Err(); err != nil {
		return SafetyProtectionPage{}, err
	}
	page := SafetyProtectionPage{Records: make([]SafetyProtectionRecord, 0, limit)}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM history_retained_snapshots`).Scan(&page.RetainedCount); err != nil {
		return SafetyProtectionPage{}, err
	}
	if len(refs) > limit {
		page.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(refs[limit-1]))
		refs = refs[:limit]
	}
	for _, ref := range refs {
		record, err := s.safetyProtectionRecord(ctx, ref)
		if err != nil {
			return SafetyProtectionPage{}, err
		}
		page.Records = append(page.Records, record)
	}
	return page, nil
}

// InspectSafetyProtection verifies the original indexed evidence when present.
// A released and collected reference remains inspectable from its durable audit;
// in that case Available is false and the returned Manifest is empty.
func (s *Store) InspectSafetyProtection(ctx context.Context, reference string) (SafetyProtectionRecord, Manifest, error) {
	if !validSafetyReference(reference) {
		return SafetyProtectionRecord{}, Manifest{}, errors.New("localhistory: invalid safety protection reference")
	}
	done, err := s.safetyProtectionOperation(ctx)
	if err != nil {
		return SafetyProtectionRecord{}, Manifest{}, err
	}
	defer done()
	record, err := s.safetyProtectionRecord(ctx, reference)
	if err != nil {
		return SafetyProtectionRecord{}, Manifest{}, err
	}
	if !record.Available {
		return record, Manifest{}, nil
	}
	manifest, err := s.verifiedSafetyProtectionManifest(ctx, record)
	if err != nil {
		return SafetyProtectionRecord{}, Manifest{}, err
	}
	return record, manifest, nil
}

func (s *Store) safetyProtectionSnapshot(ctx context.Context, id string) (SnapshotRecord, error) {
	var out SnapshotRecord
	err := s.db.QueryRowContext(ctx, `SELECT snapshot_id,parent_snapshot_id,created_at,kind,label,logical_bytes FROM history_snapshots WHERE snapshot_id=?`, id).
		Scan(&out.ID, &out.ParentID, &out.CreatedAt, &out.Kind, &out.Label, &out.LogicalBytes)
	if err == nil && !validSafetySnapshot(out) {
		err = errors.New("localhistory: corrupt safety snapshot metadata")
	}
	return out, err
}

const safetyProtectionEventColumns = `event_id,reference,revision,snapshot_id,action,actor_client_id,request_id,created_at,substr(payload,1,65537)`

// Persisted payload is bounded, canonical and agrees with every indexed identity.
// Canonical comparison also refuses duplicate JSON fields, invalid UTF-8,
// unknown fields and trailing data rather than silently normalizing corruption.
func readSafetyProtectionEvent(row *sql.Row) (SafetyProtectionEvent, error) {
	var indexed SafetyProtectionEvent
	var actor, request, payload string
	err := row.Scan(&indexed.ID, &indexed.Reference, &indexed.Revision, &indexed.SnapshotID,
		&indexed.Action, &actor, &request, &indexed.CreatedAt, &payload)
	if err != nil {
		return SafetyProtectionEvent{}, err
	}
	var event SafetyProtectionEvent
	if len(payload) > safetyProtectionPayloadLimit || json.Unmarshal([]byte(payload), &event) != nil {
		return SafetyProtectionEvent{}, errors.New("localhistory: corrupt safety protection payload")
	}
	canonical, err := json.Marshal(event)
	if err != nil || !bytes.Equal(canonical, []byte(payload)) ||
		validateSafetyProtectionChange(event.Change) != nil || !validSafetySnapshot(event.Snapshot) ||
		!validSafetyDigest(event.ID) || event.CreatedAt <= 0 ||
		event.ID != indexed.ID || event.Reference != indexed.Reference || event.SnapshotID != indexed.SnapshotID ||
		event.Revision != indexed.Revision || event.Action != indexed.Action || event.CreatedAt != indexed.CreatedAt ||
		event.Change.ActorClientID != actor || event.Change.RequestID != request ||
		event.Reference != event.Change.Reference || event.SnapshotID != event.Change.SnapshotID ||
		event.Snapshot.ID != event.SnapshotID || event.Action != event.Change.Action ||
		event.Revision != event.Change.ExpectedRevision+1 ||
		(event.Revision%2 == 1) != (event.Action == "release") {
		return SafetyProtectionEvent{}, errors.New("localhistory: corrupt safety protection event")
	}
	return event, nil
}

func (s *Store) safetyProtectionOriginal(ctx context.Context, event SafetyProtectionEvent) error {
	if event.Revision == 1 {
		return nil
	}
	first, err := readSafetyProtectionEvent(s.db.QueryRowContext(ctx, `SELECT `+safetyProtectionEventColumns+` FROM history_safety_protection_events WHERE reference=? AND revision=1`, event.Reference))
	if err != nil {
		return fmt.Errorf("localhistory: missing original safety protection event: %w", err)
	}
	if event.Snapshot != first.Snapshot || event.Change.Evidence != first.Change.Evidence {
		return errors.New("localhistory: safety protection original binding changed")
	}
	return nil
}

// Caller holds the operation/mutex/process gate. All rows are closed before the
// next query because Store deliberately owns a single SQLite connection.
func (s *Store) safetyProtectionRecord(ctx context.Context, reference string) (SafetyProtectionRecord, error) {
	if !validSafetyReference(reference) {
		return SafetyProtectionRecord{}, errors.New("localhistory: corrupt safety protection reference")
	}
	out := SafetyProtectionRecord{Reference: reference}
	var pin string
	err := s.db.QueryRowContext(ctx, `SELECT snapshot_id FROM history_retained_snapshots WHERE reference=?`, reference).Scan(&pin)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return SafetyProtectionRecord{}, err
	}
	out.Retained = err == nil
	event, err := readSafetyProtectionEvent(s.db.QueryRowContext(ctx, `SELECT `+safetyProtectionEventColumns+` FROM history_safety_protection_events WHERE reference=? ORDER BY revision DESC LIMIT 1`, reference))
	if err == nil {
		if err := s.safetyProtectionOriginal(ctx, event); err != nil {
			return SafetyProtectionRecord{}, err
		}
		var count uint64
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM history_safety_protection_events WHERE reference=?`, reference).Scan(&count); err != nil {
			return SafetyProtectionRecord{}, err
		}
		if count != event.Revision {
			return SafetyProtectionRecord{}, errors.New("localhistory: safety protection revision sequence is incomplete")
		}
		if out.Retained != (event.Action == "reprotect") || (out.Retained && pin != event.SnapshotID) {
			return SafetyProtectionRecord{}, errors.New("localhistory: safety protection event contradicts exact pin")
		}
		out.SnapshotID, out.Revision, out.Snapshot, out.LastEvent = event.SnapshotID, event.Revision, event.Snapshot, &event
	} else if !errors.Is(err, sql.ErrNoRows) {
		return SafetyProtectionRecord{}, err
	} else if !out.Retained {
		return SafetyProtectionRecord{}, sql.ErrNoRows
	} else {
		out.SnapshotID = pin
	}
	if !validSafetyDigest(out.SnapshotID) {
		return SafetyProtectionRecord{}, errors.New("localhistory: corrupt safety protection snapshot identity")
	}
	indexed, err := s.safetyProtectionSnapshot(ctx, out.SnapshotID)
	if errors.Is(err, sql.ErrNoRows) && !out.Retained {
		return out, nil
	}
	if err != nil {
		return SafetyProtectionRecord{}, err
	}
	if out.LastEvent != nil && indexed != out.Snapshot {
		return SafetyProtectionRecord{}, errors.New("localhistory: safety protection snapshot metadata changed")
	}
	out.Snapshot, out.Available = indexed, true
	return out, nil
}

func (s *Store) verifiedSafetyProtectionManifest(ctx context.Context, record SafetyProtectionRecord) (Manifest, error) {
	manifest, err := s.verifiedRetainedManifest(ctx, record.SnapshotID)
	if err != nil {
		return Manifest{}, err
	}
	if record.LastEvent != nil {
		payload, err := json.Marshal(manifest)
		if err != nil {
			return Manifest{}, err
		}
		if sha256Hex(payload) != record.LastEvent.Change.Evidence.ManifestSHA256 {
			return Manifest{}, errors.New("localhistory: original safety protection manifest binding mismatch")
		}
	}
	return manifest, nil
}

// ChangeSafetyProtection is the sole audited pin transition owner. The domain
// must supply trusted evidence while holding its document exclusion. Exact
// actor/request replay precedes current-state and content checks, so a completed
// result remains observable after later transitions and ordinary collection.
func (s *Store) ChangeSafetyProtection(ctx context.Context, in SafetyProtectionChange) (SafetyProtectionEvent, error) {
	if err := validateSafetyProtectionChange(in); err != nil {
		return SafetyProtectionEvent{}, err
	}
	done, err := s.safetyProtectionOperation(ctx)
	if err != nil {
		return SafetyProtectionEvent{}, err
	}
	defer done()
	replay, err := readSafetyProtectionEvent(s.db.QueryRowContext(ctx, `SELECT `+safetyProtectionEventColumns+` FROM history_safety_protection_events WHERE actor_client_id=? AND request_id=?`, in.ActorClientID, in.RequestID))
	if err == nil {
		if replay.Change != in {
			return SafetyProtectionEvent{}, errors.New("localhistory: safety protection request identity reused")
		}
		if err := s.safetyProtectionOriginal(ctx, replay); err != nil {
			return SafetyProtectionEvent{}, err
		}
		return replay, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SafetyProtectionEvent{}, err
	}
	record, err := s.safetyProtectionRecord(ctx, in.Reference)
	if err != nil {
		return SafetyProtectionEvent{}, err
	}
	if record.SnapshotID != in.SnapshotID || record.Revision != in.ExpectedRevision {
		return SafetyProtectionEvent{}, errors.New("localhistory: stale safety protection identity or revision")
	}
	if (in.Action == "release") != record.Retained ||
		(in.Action == "reprotect" && (record.LastEvent == nil || record.LastEvent.Action != "release")) {
		return SafetyProtectionEvent{}, errors.New("localhistory: invalid safety protection transition")
	}
	if record.LastEvent != nil && record.LastEvent.Change.Evidence != in.Evidence {
		return SafetyProtectionEvent{}, errors.New("localhistory: original safety protection evidence changed")
	}
	if !record.Available {
		return SafetyProtectionEvent{}, errors.New("localhistory: original safety snapshot no longer available")
	}
	manifest, err := s.verifiedSafetyProtectionManifest(ctx, record)
	if err != nil {
		return SafetyProtectionEvent{}, err
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return SafetyProtectionEvent{}, err
	}
	if sha256Hex(manifestJSON) != in.Evidence.ManifestSHA256 {
		return SafetyProtectionEvent{}, errors.New("localhistory: safety protection manifest digest mismatch")
	}
	var id [32]byte
	if _, err := rand.Read(id[:]); err != nil {
		return SafetyProtectionEvent{}, err
	}
	event := SafetyProtectionEvent{
		ID: hex.EncodeToString(id[:]), Reference: in.Reference, SnapshotID: in.SnapshotID,
		Action: in.Action, Revision: record.Revision + 1, CreatedAt: time.Now().UnixNano(),
		Snapshot: record.Snapshot, Change: in,
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return SafetyProtectionEvent{}, err
	}
	if len(payload) > safetyProtectionPayloadLimit {
		return SafetyProtectionEvent{}, errors.New("localhistory: safety protection payload too large")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SafetyProtectionEvent{}, err
	}
	defer func() { _ = tx.Rollback() }()
	inserted, err := tx.ExecContext(ctx, `INSERT INTO history_safety_protection_events
(event_id,reference,revision,snapshot_id,action,actor_client_id,request_id,created_at,payload) VALUES(?,?,?,?,?,?,?,?,?)`,
		event.ID, event.Reference, event.Revision, event.SnapshotID, event.Action,
		in.ActorClientID, in.RequestID, event.CreatedAt, string(payload))
	if err != nil {
		return SafetyProtectionEvent{}, err
	}
	events, err := inserted.RowsAffected()
	if err != nil {
		return SafetyProtectionEvent{}, err
	}
	if events != 1 {
		return SafetyProtectionEvent{}, errors.New("localhistory: safety protection event append failed")
	}
	var result sql.Result
	if in.Action == "release" {
		result, err = tx.ExecContext(ctx, `DELETE FROM history_retained_snapshots WHERE reference=? AND snapshot_id=?`, in.Reference, in.SnapshotID)
	} else {
		result, err = tx.ExecContext(ctx, `INSERT INTO history_retained_snapshots(reference,snapshot_id) VALUES(?,?)`, in.Reference, in.SnapshotID)
	}
	if err != nil {
		return SafetyProtectionEvent{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return SafetyProtectionEvent{}, err
	}
	if affected != 1 {
		return SafetyProtectionEvent{}, errors.New("localhistory: safety protection exact pin transition failed")
	}
	if err := tx.Commit(); err != nil {
		// The caller must inspect the original request on uncertain completion.
		return SafetyProtectionEvent{}, err
	}
	return event, nil
}
