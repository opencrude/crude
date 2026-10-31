package workspace

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"crude/document"
	"crude/localhistory"
)

type HistorySafetyListRequest struct {
	Cursor string
	Limit  int
}

type HistorySafetySummary struct {
	Reference, SnapshotID, Label string
	CreatedAt                    int64
	Revision                     uint64
	Retained                     bool
}

type HistorySafetyPage struct {
	Records       []HistorySafetySummary
	NextCursor    string
	RetainedCount int64
}

// HistorySafetyDetail separates the immutable original edit from the later
// retention decision. Inspection never renews the original edit's Apply permit.
type HistorySafetyDetail struct {
	Reference, SnapshotID string
	Revision              uint64
	Retained, Available   bool
	Snapshot              localhistory.SnapshotRecord
	OriginalOwner         document.Owner
	OriginalState         string
	JournalSHA256         string
	Preview               ReviewedEditPreview
	Result                ReviewedEditResult
	LastEvent             *localhistory.SafetyProtectionEvent
	CanRelease            bool
	CanReprotect          bool
	BlockedReason         string
	InspectionDigest      string
}

// HistorySafetyChangeRequest carries consent to the exact inspected state, not
// caller-authored outcome evidence, attribution, source bytes or file paths.
type HistorySafetyChangeRequest struct {
	Reference, SnapshotID, OperationID, OperationDigest string
	InspectionDigest, Action                            string
	ExpectedRevision                                    uint64
}

type HistorySafetyChangeResult struct {
	Reference, SnapshotID, RequestID, State string
	Event                                   localhistory.SafetyProtectionEvent
	SnapshotDeleted                         bool
	Error                                   string
}

func validHistorySafetyText(value string, limit int) bool {
	if value == "" || len(value) > limit || !utf8.ValidString(value) {
		return false
	}
	return !strings.ContainsFunc(value, unicode.IsControl)
}

func validHistorySafetyHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (s *DocumentService) historySafetyReadyLocked(store *localhistory.Store) error {
	if s.closed || s.idleFenced {
		return context.Canceled
	}
	if s.stateErr != nil {
		return s.stateErr
	}
	if store == nil || s.stateDB == nil || store.WorkspaceRoot() != s.root {
		return errors.New("review safety requires this workspace's original document and history owners")
	}
	return nil
}

// ListHistorySafety has its own bounded traversal, independent of the ordinary
// timeline cap. A summary is metadata, not verified contents or release authority.
func (s *DocumentService) ListHistorySafety(ctx context.Context, store *localhistory.Store, in HistorySafetyListRequest) (HistorySafetyPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.historySafetyReadyLocked(store); err != nil {
		return HistorySafetyPage{}, err
	}
	page, err := store.ListSafetyProtections(ctx, in.Cursor, in.Limit)
	if err != nil {
		return HistorySafetyPage{}, err
	}
	out := HistorySafetyPage{NextCursor: page.NextCursor, RetainedCount: page.RetainedCount}
	for _, row := range page.Records {
		out.Records = append(out.Records, HistorySafetySummary{
			Reference: row.Reference, SnapshotID: row.SnapshotID, Label: row.Snapshot.Label,
			CreatedAt: row.Snapshot.CreatedAt, Revision: row.Revision, Retained: row.Retained,
		})
	}
	return out, nil
}

// InspectHistorySafety is native workspace administration. It may read an older
// client's provenance, unlike the original client's InspectReviewedEdit API.
func (s *DocumentService) InspectHistorySafety(ctx context.Context, store *localhistory.Store, reference string) (HistorySafetyDetail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	detail, _, err := s.inspectHistorySafetyLocked(ctx, store, reference)
	return detail, err
}

func (s *DocumentService) inspectHistorySafetyLocked(ctx context.Context, store *localhistory.Store, reference string) (HistorySafetyDetail, localhistory.SafetyProtectionEvidence, error) {
	var evidence localhistory.SafetyProtectionEvidence
	if err := s.historySafetyReadyLocked(store); err != nil {
		return HistorySafetyDetail{}, evidence, err
	}
	if !validHistorySafetyText(reference, 128) {
		return HistorySafetyDetail{}, evidence, errors.New("invalid safety reference")
	}
	record, manifest, err := store.InspectSafetyProtection(ctx, reference)
	if err != nil {
		return HistorySafetyDetail{}, evidence, err
	}
	detail := HistorySafetyDetail{
		Reference: record.Reference, SnapshotID: record.SnapshotID,
		Revision: record.Revision, Retained: record.Retained, Available: record.Available,
		Snapshot: record.Snapshot, LastEvent: record.LastEvent,
	}
	block := func(reason string) (HistorySafetyDetail, localhistory.SafetyProtectionEvidence, error) {
		detail.BlockedReason = reason
		return detail, evidence, nil
	}
	journal, raw, err := s.historySafetyJournalLocked(reference)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return block("No sealed reviewed-edit journal is linked to this reference. Protection cannot be released.")
		}
		return block("The original reviewed-edit evidence is invalid or unavailable. Protection cannot be released.")
	}
	detail.OriginalOwner = journal.Owner
	detail.OriginalState = journal.Result.State
	detail.Preview, detail.Result = journal.Preview, journal.Result
	detail.JournalSHA256 = reviewedHash(string(raw))
	if journal.Result.SafetyReference != record.Reference || journal.Result.SafetySnapshotID == "" ||
		journal.Result.SafetySnapshotID != record.SnapshotID {
		return block("The original journal has no complete exact binding to this safety snapshot.")
	}
	if journal.Result.State != "committed" && journal.Result.State != "refused" {
		return block("The original operation is not durably settled. Present file bytes, time and detach do not prove its historical outcome.")
	}
	if !record.Available {
		return block("The original snapshot is no longer available. Its immutable disposition remains inspectable; re-protection cannot recreate it.")
	}
	if err := s.historySafetyCoverage(journal.Preview, manifest); err != nil {
		return block("The original safety snapshot does not exactly cover the reviewed preimages.")
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return HistorySafetyDetail{}, evidence, err
	}
	evidence = localhistory.SafetyProtectionEvidence{
		WorkspaceKey: reviewedHash(s.root), OperationID: journal.Preview.ID,
		OperationDigest: journal.Preview.Digest, JournalSHA256: detail.JournalSHA256,
		ManifestSHA256: reviewedHash(string(manifestJSON)), Outcome: journal.Result.State,
		OwnerClientID: journal.Owner.ClientID, OwnerEpoch: journal.Owner.Epoch,
	}
	if record.LastEvent != nil && record.LastEvent.Change.Evidence != evidence {
		return block("The retained disposition and original operation evidence no longer agree.")
	}
	detail.CanRelease = record.Retained
	detail.CanReprotect = !record.Retained && record.LastEvent != nil && record.LastEvent.Action == "release"
	if !detail.CanRelease && !detail.CanReprotect {
		return block("There is no exact audited protection transition available.")
	}
	digestJSON, err := json.Marshal(struct {
		Workspace string
		Detail    HistorySafetyDetail
	}{reviewedHash(s.root), detail})
	if err != nil {
		return HistorySafetyDetail{}, evidence, err
	}
	detail.InspectionDigest = reviewedHash(string(digestJSON))
	return detail, evidence, nil
}

// Current writers emit canonical JSON themselves. Exact roundtrip equality
// rejects duplicate/unknown members and unsupported persisted representations
// without a legacy reader or a second interpretation of the original outcome.
func (s *DocumentService) historySafetyJournalLocked(reference string) (reviewedEditJournal, []byte, error) {
	if err := s.reviewTable(); err != nil {
		return reviewedEditJournal{}, nil, err
	}
	var raw []byte
	if err := s.stateDB.QueryRow(`SELECT substr(payload,1,2097153) FROM workspace_reviewed_edits WHERE id=?`, reference).Scan(&raw); err != nil {
		return reviewedEditJournal{}, nil, err
	}
	var journal reviewedEditJournal
	if len(raw) > 2<<20 || json.Unmarshal(raw, &journal) != nil {
		return reviewedEditJournal{}, nil, errors.New("invalid original reviewed-edit journal")
	}
	encoded, err := json.Marshal(journal)
	if err != nil || !bytes.Equal(encoded, raw) || journal.Version != 1 ||
		!validHistorySafetyText(journal.Owner.ClientID, 512) || journal.Owner.Epoch == 0 ||
		journal.Preview.ID != reference || journal.Result.ID != reference ||
		!validHistorySafetyHash(journal.Preview.Digest) || journal.Preview.Digest != journal.Result.Digest {
		return reviewedEditJournal{}, nil, errors.New("original reviewed-edit journal identity mismatch")
	}
	p := journal.Preview
	digest := p.Digest
	p.Digest = ""
	encoded, err = json.Marshal(struct {
		Owner   document.Owner
		Preview ReviewedEditPreview
	}{journal.Owner, p})
	if err != nil || reviewedHash(string(encoded)) != digest ||
		!validHistorySafetyText(p.Label, 256) || len(p.Text) > reviewedEditTextBytes ||
		!utf8.ValidString(p.Text) || len(p.Files) == 0 || len(p.Files) > reviewedEditMaxFiles {
		return reviewedEditJournal{}, nil, errors.New("original reviewed-edit preview digest or bounds mismatch")
	}
	switch journal.Result.State {
	case "prepared", "unconfirmed", "refused", "committed":
	default:
		return reviewedEditJournal{}, nil, errors.New("unsupported original reviewed-edit outcome")
	}
	return journal, raw, nil
}

func (s *DocumentService) historySafetyCoverage(preview ReviewedEditPreview, manifest localhistory.Manifest) error {
	if len(manifest.ScopePaths) != len(preview.Files) || len(manifest.Entries) != len(preview.Files) ||
		manifest.ID == "" || manifest.Kind != "safety" {
		return errors.New("review safety coverage mismatch")
	}
	keys := make([]string, 0, len(preview.Files))
	total := 0
	for _, file := range preview.Files {
		rel, err := filepath.Rel(s.root, file.Path)
		if err != nil || !filepath.IsAbs(file.Path) || filepath.Clean(file.Path) != file.Path ||
			rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) ||
			len(file.Path) > 4096 || !validHistorySafetyHash(file.BeforeSHA256) || !validHistorySafetyHash(file.AfterSHA256) ||
			file.BeforeBytes < 0 || file.BeforeBytes > reviewedEditFileBytes ||
			file.AfterBytes < 0 || file.AfterBytes > reviewedEditFileBytes {
			return errors.New("invalid original reviewed target")
		}
		key := filepath.ToSlash(rel)
		if slices.Contains(keys, key) {
			return errors.New("duplicate original reviewed target")
		}
		keys = append(keys, key)
		total += file.BeforeBytes + file.AfterBytes
		entry, ok := manifest.Entries[key]
		if !ok || entry.Type != localhistory.EntryFile || entry.Hash != file.BeforeSHA256 ||
			entry.Mode != file.Mode || entry.Size != int64(file.BeforeBytes) || total > reviewedEditMaxBytes {
			return errors.New("original reviewed preimage mismatch")
		}
	}
	slices.Sort(keys)
	if !slices.Equal(keys, manifest.ScopePaths) {
		return errors.New("original review safety scope mismatch")
	}
	return nil
}

// ChangeHistorySafetyProtection runs off the actor/UI while retaining the
// document-to-history exclusion order. Only the current native workspace path
// supplies owner/requestID; no agent or generic control grants this consent.
func (s *DocumentService) ChangeHistorySafetyProtection(ctx context.Context, store *localhistory.Store, owner document.Owner, requestID string, in HistorySafetyChangeRequest) HistorySafetyChangeResult {
	out := HistorySafetyChangeResult{Reference: in.Reference, SnapshotID: in.SnapshotID, RequestID: requestID, State: "refused"}
	fail := func(err error) HistorySafetyChangeResult { out.Error = err.Error(); return out }
	if !validHistorySafetyText(owner.ClientID, 512) || owner.Epoch == 0 ||
		!validHistorySafetyText(requestID, 256) || !validHistorySafetyText(in.Reference, 128) ||
		!validHistorySafetyHash(in.SnapshotID) || in.OperationID != in.Reference ||
		!validHistorySafetyHash(in.OperationDigest) || !validHistorySafetyHash(in.InspectionDigest) ||
		(in.Action != "release" && in.Action != "reprotect") {
		return fail(errors.New("invalid safety protection consent"))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	detail, evidence, err := s.inspectHistorySafetyLocked(ctx, store, in.Reference)
	if err != nil {
		return fail(err)
	}
	if detail.InspectionDigest != in.InspectionDigest || detail.Revision != in.ExpectedRevision ||
		detail.SnapshotID != in.SnapshotID || detail.Preview.ID != in.OperationID ||
		detail.Preview.Digest != in.OperationDigest ||
		(in.Action == "release" && !detail.CanRelease) || (in.Action == "reprotect" && !detail.CanReprotect) {
		return fail(errors.New("the inspected protection or original evidence changed; inspect it again before a new decision"))
	}
	// Detach must remain an immediate actor fence, not wait for Store I/O.
	// Reuse the document operation's cancellation registry: a detached or
	// superseded native attachment cannot publish stale consent. A Store commit
	// that already won cancellation is still observed as the original result.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.ownerMu.Lock()
	if s.detached[owner] || owner.Epoch < s.epochs[owner.ClientID] ||
		s.epochs[owner.ClientID] == 0 && len(s.epochs) >= 1024 {
		s.ownerMu.Unlock()
		return fail(document.ErrOwner)
	}
	s.epochs[owner.ClientID] = owner.Epoch
	if s.saveCancels == nil {
		s.saveCancels = make(map[document.Owner]context.CancelFunc)
	}
	s.saveCancels[owner] = cancel
	s.ownerMu.Unlock()
	defer func() {
		s.ownerMu.Lock()
		delete(s.saveCancels, owner)
		s.ownerMu.Unlock()
	}()
	event, err := store.ChangeSafetyProtection(ctx, localhistory.SafetyProtectionChange{
		Reference: in.Reference, SnapshotID: in.SnapshotID, Action: in.Action,
		ExpectedRevision: in.ExpectedRevision, Evidence: evidence,
		ActorClientID: owner.ClientID, ActorEpoch: owner.Epoch, RequestID: requestID,
	})
	if err != nil {
		// A Store error alone is not a no-effect verdict, in particular when its
		// transaction commit acknowledgement or a later reply is uncertain.
		out.State = "unconfirmed"
		return fail(fmt.Errorf("inspect this original protection request; do not replay it: %w", err))
	}
	out.State, out.Event = "committed", event
	return out
}
