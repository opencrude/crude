package workspace

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"crude/document"
	"crude/localhistory"
	"crude/lspedit"
	"crude/types"
)

const (
	reviewedEditMaxFiles   = 200
	reviewedEditMaxBytes   = 16 << 20
	reviewedEditFileBytes  = 2 << 20
	reviewedEditTextBytes  = 64 << 10
	reviewedEditMaxPending = 4
	reviewedEditLifetime   = 5 * time.Minute
)

// ReviewedEditPreview is detached presentation evidence. Only ID and Digest
// select the backend's immutable plan; replacing this DTO never changes it.
type ReviewedEditPreview struct {
	ID, Digest, Label, Text string
	// Evidence retains action provenance for the immutable journal. Ordinary
	// review UI renders Text, not hashes, wire metadata or internal identities.
	Evidence string
	Boundary types.InsightBoundary
	Files    []ReviewedEditFile
	// Message and URL are set only for a command action with no source edit.
	// They are presentation evidence; the command has already run exactly once.
	Message, URL string
}

type ReviewedEditFile struct {
	Path, BeforeSHA256, AfterSHA256 string
	Source                          types.InsightSource
	Mode                            uint32
	BeforeBytes, AfterBytes         int
	Created                         bool
}

// ReviewedEditResult distinguishes a refusal from an accepted disk effect whose
// canonical publication or durable completion needs inspection. It never means
// an ordinary per-author Ctrl+Z entry.
type ReviewedEditResult struct {
	ID, Digest, State, Detail string
	SafetyReference           string
	SafetySnapshotID          string
	Error                     string
}

type reviewedEdit struct {
	preview ReviewedEditPreview
	owner   document.Owner
	expires time.Time
	files   []workspaceEditFile
	edits   []FileEdit
	splices [][]document.Edit
	check   func() error // exact provider/context fence, never I/O or a source read
}

type reviewedEditJournal struct {
	Version int
	Owner   document.Owner
	Preview ReviewedEditPreview
	Result  ReviewedEditResult
}

func reviewedHash(text string) string {
	hash := sha256.Sum256([]byte(text))
	return hex.EncodeToString(hash[:])
}

func reviewedURIPath(root, raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "file" || u.User != nil || u.Opaque != "" || u.RawQuery != "" ||
		u.Fragment != "" || u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
		return "", errors.New("review requires a local file URI")
	}
	path := filepath.FromSlash(u.Path)
	if native := strings.TrimPrefix(path, string(filepath.Separator)); filepath.VolumeName(native) != "" {
		path = native
	}
	if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return "", errors.New("review requires an absolute file path")
	}
	path = filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("reviewed target is outside the workspace")
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if strings.EqualFold(part, ".git") {
			return "", errors.New("reviewed edits cannot target Git administration data")
		}
	}
	return path, nil
}

func cloneReviewedPreview(p ReviewedEditPreview) ReviewedEditPreview {
	p.Files = slices.Clone(p.Files)
	p.Boundary.Context.UnknownInputs = slices.Clone(p.Boundary.Context.UnknownInputs)
	return p
}

func (s *DocumentService) validateReviewedSourceLocked(owner document.Owner, source types.InsightSource) error {
	if s.closed || s.idleFenced {
		return context.Canceled
	}
	if s.stateErr != nil {
		return s.stateErr
	}
	d, err := s.owned(owner, source.DocumentID)
	if err != nil {
		return err
	}
	if source.Kind != "canonical" || s.analysisIdentityLocked(d) != source {
		return document.ErrRevision
	}
	if d.snapshot.Dirty || d.snapshot.Conflict || d.diskIntent != nil || d.relocation != nil {
		return errors.New("review requires clean reconciled shared source; save or reconcile and request it again")
	}
	return nil
}

// prepareReviewedEdit runs on a resource worker. The language coordinator owns
// boundary and check; no shell can provide a signed semantic fact or substitute
// file contents at Apply. Only explicit inert text edits are accepted here.
func (s *DocumentService) prepareReviewedEdit(ctx context.Context, owner document.Owner, label, summary string, boundary types.InsightBoundary, targets []lspedit.DocumentEdit, check func() error) (ReviewedEditPreview, error) {
	if err := ctx.Err(); err != nil {
		return ReviewedEditPreview{}, err
	}
	if label == "" || len(label) > 256 || !utf8.ValidString(label) || strings.ContainsAny(label, "\r\n\x00") ||
		len(targets) == 0 || len(targets) > reviewedEditMaxFiles || check == nil {
		return ReviewedEditPreview{}, errors.New("unsupported or oversized reviewed edit")
	}
	s.mu.Lock()
	err := s.validateReviewedSourceLocked(owner, boundary.Source)
	s.mu.Unlock()
	if err != nil {
		return ReviewedEditPreview{}, err
	}
	if err := check(); err != nil {
		return ReviewedEditPreview{}, err
	}
	plan := &reviewedEdit{owner: owner, expires: time.Now().Add(reviewedEditLifetime), check: check}
	plan.preview = ReviewedEditPreview{ID: "review-" + rand.Text(), Label: label, Evidence: summary, Boundary: boundary}
	plan.preview.Boundary.Context.UnknownInputs = slices.Clone(boundary.Context.UnknownInputs)
	seen := make(map[string]bool, len(targets))
	total, editCount := 0, 0
	if len(summary) > reviewedEditTextBytes {
		return ReviewedEditPreview{}, errors.New("edit evidence exceeds its review bound")
	}
	var text strings.Builder
	appendText := func(value string) error {
		if len(value) > reviewedEditTextBytes-text.Len()-len(summary) {
			return errors.New("exact edit preview exceeds its 64 KiB review bound")
		}
		text.WriteString(value)
		return nil
	}
	// Provider order cannot make one target's validation apply to a different
	// target. Sort only after validating the bounded outer declaration.
	targets = slices.Clone(targets)
	sort.Slice(targets, func(i, j int) bool { return targets[i].URI < targets[j].URI })
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return ReviewedEditPreview{}, err
		}
		if len(target.URI) == 0 || len(target.URI) > 4096 || len(target.Edits) == 0 && !target.Create ||
			len(target.Edits) > document.MaxEdits-editCount {
			return ReviewedEditPreview{}, errors.New("invalid or oversized reviewed target")
		}
		editCount += len(target.Edits)
		path, err := reviewedURIPath(s.root, target.URI)
		if err != nil {
			return ReviewedEditPreview{}, err
		}
		if seen[path] {
			return ReviewedEditPreview{}, errors.New("duplicate reviewed target")
		}
		seen[path] = true
		var file workspaceEditFile
		var before string
		mode := os.FileMode(0o644)
		if target.Create {
			file, err = absentWorkspaceEditFile(path)
			if err != nil {
				return ReviewedEditPreview{}, err
			}
		} else {
			// Bound the native read before allocating its full contents.
			info, err := os.Lstat(path)
			if err != nil {
				return ReviewedEditPreview{}, err
			}
			if !info.Mode().IsRegular() || info.Size() > reviewedEditFileBytes {
				return ReviewedEditPreview{}, errors.New("review requires a regular file within its 2 MiB source bound")
			}
			file, err = readWorkspaceEditFileBounded(path, reviewedEditFileBytes)
			if err != nil {
				return ReviewedEditPreview{}, err
			}
			before = string(file.data)
			mode = file.info.Mode()
		}
		for _, previous := range plan.files {
			if file.info != nil && previous.info != nil && os.SameFile(previous.info, file.info) {
				return ReviewedEditPreview{}, errors.New("review contains native file aliases")
			}
		}
		after, err := lspedit.ApplyOrdered(before, target.Edits)
		if err != nil {
			return ReviewedEditPreview{}, err
		}
		if len(after) > reviewedEditFileBytes {
			return ReviewedEditPreview{}, errors.New("reviewed result exceeds its recoverable 2 MiB file bound")
		}
		total += len(before) + len(after)
		if total > reviewedEditMaxBytes {
			return ReviewedEditPreview{}, errors.New("review exceeds its 16 MiB aggregate source bound")
		}
		s.mu.Lock()
		meta, sourceErr := s.reviewedFileSourceLocked(path, file.data, target)
		s.mu.Unlock()
		if sourceErr != nil {
			return ReviewedEditPreview{}, sourceErr
		}
		if before == after && !target.Create {
			continue
		}
		meta.Path, meta.BeforeSHA256, meta.AfterSHA256 = path, reviewedHash(before), reviewedHash(after)
		meta.Mode, meta.BeforeBytes, meta.AfterBytes = uint32(mode), len(before), len(after)
		meta.Created = target.Create
		displayPath, err := filepath.Rel(s.root, path)
		if err != nil {
			return ReviewedEditPreview{}, err
		}
		if target.Create && after == "" {
			// An empty new file has no lines to diff; name it so the review still shows it.
			err = appendText(strconv.Quote(filepath.ToSlash(displayPath)) + "  new empty file\n")
		} else {
			err = appendReviewedDiff(filepath.ToSlash(displayPath), before, after, appendText)
		}
		if err != nil {
			return ReviewedEditPreview{}, err
		}
		plan.files = append(plan.files, file)
		plan.edits = append(plan.edits, FileEdit{Path: path, OldContent: before, NewContent: after, Create: target.Create})
		splices := make([]document.Edit, 0, len(target.Edits))
		if !target.Create {
			for _, edit := range target.Edits {
				start, _ := lspedit.Offset(before, edit.Line, edit.Col)
				end, _ := lspedit.Offset(before, edit.EndLine, edit.EndCol)
				splices = append(splices, document.Edit{Start: start, End: end, Text: edit.NewText})
			}
		}
		plan.splices = append(plan.splices, splices)
		plan.preview.Files = append(plan.preview.Files, meta)
	}
	if len(plan.edits) == 0 {
		return ReviewedEditPreview{}, errors.New("provider proposed no source changes")
	}
	plan.preview.Text = text.String()
	data, err := json.Marshal(struct {
		Owner   document.Owner
		Preview ReviewedEditPreview
	}{owner, plan.preview})
	if err != nil {
		return ReviewedEditPreview{}, err
	}
	plan.preview.Digest = reviewedHash(string(data))
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validateReviewedPlanLocked(ctx, plan); err != nil {
		return ReviewedEditPreview{}, err
	}
	for id, older := range s.reviewed {
		if time.Now().After(older.expires) {
			delete(s.reviewed, id)
		}
	}
	if len(s.reviewed) >= reviewedEditMaxPending {
		return ReviewedEditPreview{}, errors.New("close an earlier edit preview before requesting another")
	}
	if s.reviewed == nil {
		s.reviewed = make(map[string]*reviewedEdit)
	}
	s.reviewed[plan.preview.ID] = plan
	return cloneReviewedPreview(plan.preview), nil
}

func (s *DocumentService) reviewedFileSourceLocked(path string, before []byte, target lspedit.DocumentEdit) (ReviewedEditFile, error) {
	d := s.documentAtPath(path)
	if target.Create {
		if d != nil {
			return ReviewedEditFile{}, errors.New("a new file cannot replace an open or retained document")
		}
		return ReviewedEditFile{}, nil
	}
	if d == nil {
		if target.Versioned && target.Version != nil {
			return ReviewedEditFile{}, errors.New("versioned target has no synchronized canonical source")
		}
		return ReviewedEditFile{}, nil
	}
	if target.Versioned && target.Version == nil {
		return ReviewedEditFile{}, errors.New("null-version target is already a canonical open or retained document")
	}
	if d.snapshot.Dirty || d.snapshot.Conflict || d.diskIntent != nil || d.relocation != nil ||
		!bytes.Equal(before, d.snapshot.Text) {
		return ReviewedEditFile{}, errors.New("target has unsaved, conflicted or changed shared source; review again after reconciliation")
	}
	return ReviewedEditFile{Source: s.analysisIdentityLocked(d)}, nil
}

func (s *DocumentService) validateReviewedPlanLocked(ctx context.Context, p *reviewedEdit) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if time.Now().After(p.expires) {
		return errors.New("edit preview expired; request a fresh review")
	}
	if err := s.validateReviewedSourceLocked(p.owner, p.preview.Boundary.Source); err != nil {
		return err
	}
	for i, expected := range p.preview.Files {
		if expected.Created {
			if s.documentAtPath(expected.Path) != nil {
				return document.ErrRevision
			}
			if err := p.files[i].checkAbsent(); err != nil {
				return err
			}
			continue
		}
		d := s.documentAtPath(expected.Path)
		if expected.Source.DocumentID == "" {
			if d != nil {
				return document.ErrRevision
			}
			continue
		}
		if d == nil || s.analysisIdentityLocked(d) != expected.Source ||
			d.snapshot.Dirty || d.snapshot.Conflict || d.diskIntent != nil || d.relocation != nil ||
			!bytes.Equal(d.snapshot.Text, p.files[i].data) {
			return document.ErrRevision
		}
		// Prove the canonical journal can accept the disk reconciliation before
		// any filesystem effect, including the new revision and persistence size.
		trial := stageDocument(d)
		if _, err := s.commit(trial, document.Change{DocumentID: d.snapshot.ID, RequestID: p.preview.ID,
			BaseRevision: d.snapshot.Revision, Edits: p.splices[i]}); err != nil {
			return err
		}
	}
	return p.check()
}

// ReleaseReviewedEdit closes only a pending preview. It cannot cancel or replay
// an admitted apply, delete safety evidence or alter another attachment's plan.
func (s *DocumentService) ReleaseReviewedEdit(owner document.Owner, id, digest string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.reviewed[id]
	if p == nil {
		return nil
	}
	if p.owner != owner || p.preview.Digest != digest {
		return document.ErrOwner
	}
	delete(s.reviewed, id)
	return nil
}

func (s *DocumentService) reviewTable() error {
	if s.stateDB == nil {
		return errors.New("reviewed edits require durable workspace document storage")
	}
	_, err := s.stateDB.Exec(`CREATE TABLE IF NOT EXISTS workspace_reviewed_edits (id TEXT PRIMARY KEY, payload BLOB NOT NULL)`)
	return err
}

func (s *DocumentService) saveReviewedJournal(j reviewedEditJournal) error {
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	_, err = s.stateDB.Exec(`INSERT INTO workspace_reviewed_edits(id,payload) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload`, j.Result.ID, data)
	return err
}

func (s *DocumentService) readReviewedJournal(clientID, id, digest string) (reviewedEditJournal, error) {
	var raw []byte
	err := s.stateDB.QueryRow(`SELECT payload FROM workspace_reviewed_edits WHERE id=?`, id).Scan(&raw)
	if err != nil {
		return reviewedEditJournal{}, err
	}
	var prior reviewedEditJournal
	if len(raw) > 2<<20 || json.Unmarshal(raw, &prior) != nil || prior.Version != 1 ||
		clientID == "" || prior.Owner.ClientID != clientID || prior.Owner.Epoch == 0 || prior.Result.Digest != digest || prior.Result.ID != id ||
		prior.Preview.ID != id || prior.Preview.Digest != digest {
		return reviewedEditJournal{}, errors.New("reviewed mutation identity mismatch")
	}
	switch prior.Result.State {
	case "prepared":
		prior.Result.State = "unconfirmed"
		prior.Result.Error = "an interrupted reviewed mutation requires inspection of its original receipt and retained safety reference; never replay"
	case "committed", "refused", "unconfirmed":
	default:
		return reviewedEditJournal{}, errors.New("invalid reviewed mutation outcome")
	}
	return prior, nil
}

// InspectReviewedEdit observes only the original durable operation. An absent
// intent is pending or never admitted, not evidence permitting another Apply.
func (s *DocumentService) InspectReviewedEdit(ctx context.Context, owner document.Owner, id, digest string) (ReviewedEditResult, error) {
	if err := ctx.Err(); err != nil {
		return ReviewedEditResult{}, err
	}
	if id == "" || len(id) > 128 || len(digest) != 64 {
		return ReviewedEditResult{}, errors.New("invalid reviewed mutation identity")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ReviewedEditResult{}, context.Canceled
	}
	if err := s.reviewTable(); err != nil {
		return ReviewedEditResult{}, err
	}
	journal, err := s.readReviewedJournal(owner.ClientID, id, digest)
	if errors.Is(err, sql.ErrNoRows) {
		return ReviewedEditResult{ID: id, Digest: digest, State: "pending",
			Detail: "No completed intent is visible yet. Check this original review again; do not apply it again."}, nil
	}
	return journal.Result, err
}

// ApplyReviewedEdit is the canonical document/disk exclusion. It records intent
// before snapshot/commit, keeps a durable non-agent safety reference, and uses
// the one FileOps staging/rollback writer. A prepared journal is never replayed.
func (s *DocumentService) ApplyReviewedEdit(ctx context.Context, owner document.Owner, id, digest string, store *localhistory.Store, files *FileOpsService) ReviewedEditResult {
	out := ReviewedEditResult{ID: id, Digest: digest, State: "refused"}
	fail := func(err error) ReviewedEditResult { out.Error = err.Error(); return out }
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if len(id) == 0 || len(id) > 128 || len(digest) != 64 || store == nil || files == nil {
		return fail(errors.New("mandatory review, history or file authority unavailable"))
	}
	s.mu.Lock()
	var changed []string
	var watch *reviewedWatchTxn
	defer func() {
		s.mu.Unlock()
		if watch != nil {
			s.lsp.replayReviewedWatch(watch)
		}
		for _, documentID := range changed {
			s.changed(documentID)
		}
	}()
	if err := s.reviewTable(); err != nil {
		return fail(err)
	}
	prior, err := s.readReviewedJournal(owner.ClientID, id, digest)
	if err == nil {
		if prior.Owner != owner {
			return fail(document.ErrOwner)
		}
		return prior.Result
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fail(err)
	}
	p := s.reviewed[id]
	if p == nil || p.owner != owner || p.preview.Digest != digest {
		return fail(errors.New("edit preview is absent, retired or belongs to another attachment"))
	}
	if err := s.validateReviewedPlanLocked(ctx, p); err != nil {
		return fail(err)
	}
	out.State, out.SafetyReference = "prepared", p.preview.ID
	journal := reviewedEditJournal{Version: 1, Owner: owner, Preview: p.preview, Result: out}
	if err := s.saveReviewedJournal(journal); err != nil {
		out.State = "refused"
		return fail(err)
	}
	delete(s.reviewed, id) // admission consumes this exact plan, even on failure
	preimages := make([]localhistory.RetainedFile, 0, len(p.edits))
	for i, edit := range p.edits {
		if p.preview.Files[i].Created {
			continue
		}
		rel, err := filepath.Rel(s.root, edit.Path)
		if err != nil {
			out.State = "refused"
			return fail(err)
		}
		preimages = append(preimages, localhistory.RetainedFile{Path: filepath.ToSlash(rel), Data: p.files[i].data, Mode: p.preview.Files[i].Mode})
	}
	var safety localhistory.Manifest
	var effectErr error
	if len(preimages) != 0 {
		safety, effectErr = store.SnapshotRetainedFiles(ctx, out.SafetyReference, "Before "+p.preview.Label, preimages)
		out.SafetySnapshotID = safety.ID
		if effectErr == nil {
			for _, meta := range p.preview.Files {
				if meta.Created {
					continue
				}
				rel, _ := filepath.Rel(s.root, meta.Path)
				entry, found := safety.Entries[filepath.ToSlash(rel)]
				if !found || entry.Type != localhistory.EntryFile || entry.Hash != meta.BeforeSHA256 || entry.Mode != meta.Mode {
					effectErr = errors.New("reviewed preimage changed before mandatory safety capture")
					break
				}
			}
		}
	}
	if effectErr == nil {
		effectErr = s.validateReviewedPlanLocked(ctx, p)
	}
	if effectErr == nil {
		// Persist the exact safety identity before the first possible disk effect.
		journal.Result = out
		effectErr = s.saveReviewedJournal(journal)
	}
	if effectErr == nil && s.lsp != nil {
		watch, effectErr = s.lsp.reviewWatch.begin(p.edits)
	}
	if effectErr != nil {
		out.State, out.Error = "refused", effectErr.Error()
	} else {
		result := files.applyWorkspaceEdit(p.edits, p.files, func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := p.check(); err != nil {
				return err
			}
			_, err := s.owned(owner, p.preview.Boundary.Source.DocumentID)
			return err
		}, s.lsp, watch)
		out.State, out.Detail = "committed", result.Detail
		effectErr = result.Err
		if result.Err != nil && result.NoEffect {
			out.State, out.Error = "refused", result.Err.Error()
			out.Detail = "The writer admitted no edit commit."
		} else {
			for i, meta := range p.preview.Files {
				if meta.Source.DocumentID != "" {
					changed = append(changed, meta.Source.DocumentID)
					if result.Err == nil {
						effectErr = errors.Join(effectErr, s.commitReviewedDocumentLocked(p, i))
					} else {
						effectErr = errors.Join(effectErr, s.reconcileLocked(meta.Source.DocumentID))
					}
				}
			}
			if effectErr != nil {
				out.State = "unconfirmed"
				out.Error = "reviewed disk mutation or reconciliation needs inspection; do not replay: " + effectErr.Error()
			}
		}
	}
	journal.Result = out
	if err := s.saveReviewedJournal(journal); err != nil {
		out.State = "unconfirmed"
		out.Error = "reviewed operation completion could not be persisted; inspect original receipt and retained safety reference; do not replay"
	} else if out.State == "refused" && out.SafetySnapshotID != "" {
		// Only positive no-effect evidence can relinquish a retained preimage.
		// Cancellation/shutdown/uncertainty are never liveness-based unpins.
		if err := store.ReleaseRetainedSnapshot(context.WithoutCancel(ctx), out.SafetyReference); err != nil {
			out.Detail += " Safety reference remains retained: " + err.Error()
			journal.Result = out
			if err := s.saveReviewedJournal(journal); err != nil {
				out.State = "unconfirmed"
				out.Error = "no edit was committed, but safety-disposition completion could not be persisted; inspect the original review"
			}
		}
	}
	return out
}

// The canonical source journal publishes the same sparse edit vector the
// provider reviewed, not a full-file delete/insert. That preserves each peer's
// private caret/selection and disjoint pending edits through ordinary rebasing.
func (s *DocumentService) commitReviewedDocumentLocked(p *reviewedEdit, index int) error {
	meta, edit := p.preview.Files[index], p.edits[index]
	d := s.docs[meta.Source.DocumentID]
	data, exists, err := readLiveDocument(edit.Path)
	if err != nil || !exists || !bytes.Equal(data, []byte(edit.NewContent)) {
		return errors.Join(errors.New("reviewed disk postimage changed before canonical publication"), err, s.reconcileLocked(meta.Source.DocumentID))
	}
	trial := stageDocument(d)
	if _, err := s.commit(trial, document.Change{DocumentID: d.snapshot.ID, RequestID: p.preview.ID,
		BaseRevision: d.snapshot.Revision, Edits: p.splices[index]}); err != nil {
		return err
	}
	trial.snapshot.Saved = bytes.Clone(trial.snapshot.Text)
	trial.snapshot.DiskExists = true
	trial.snapshot.DiskHash = sha256.Sum256(data)
	trial.snapshot.Dirty, trial.snapshot.Conflict = false, false
	if err := s.persistDocument(trial); err != nil {
		d.snapshot.Conflict = true
		return err
	}
	*d = *trial
	s.observeAnalysisLocked(d)
	return nil
}
