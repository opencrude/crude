package workspace

import (
	"bytes"
	"context"
	"crude/textencoding"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"crude/document"
	"crude/internal/fileuri"
	"crude/localhistory"
	"crude/types"
)

// PrepareUntitledOverwrite retains one immutable, owner-bound review. Applying
// it uses the same journal, safety snapshot and transactional writer as other
// reviewed edits. Previewing never changes the destination or draft.
func (s *DocumentService) PrepareUntitledOverwrite(ctx context.Context, owner document.Owner, id string, revision uint64, path string, encoding textencoding.Encoding) (ReviewedEditPreview, error) {
	return s.prepareDraftOverwrite(ctx, owner, id, revision, path, encoding, false)
}

// SaveWithEncoding uses the same immutable preimage, safety history, receipt and
// writer as first-save replacement. It never decodes the existing text again or
// changes line endings. The core RPC supplies the exact operation identity.
func (s *DocumentService) SaveWithEncoding(ctx context.Context, owner document.Owner, id string, revision uint64, encoding textencoding.Encoding, store *localhistory.Store, files *FileOpsService) (document.Snapshot, error) {
	snap, err := s.Snapshot(id)
	if err != nil {
		return snap, err
	}
	if snap.Path == "" {
		return snap, errors.New("choose a destination for this unnamed file")
	}
	if encoding == snap.Encoding {
		return s.save(ctx, owner, id, revision, DocumentSaveManual, &encoding)
	}
	preview, err := s.prepareDraftOverwrite(ctx, owner, id, revision, snap.Path, encoding, true)
	if err != nil {
		return snap, err
	}
	result := s.ApplyReviewedEdit(ctx, owner, preview.ID, preview.Digest, store, files)
	if result.State != "committed" {
		if result.State == "refused" {
			return snap, errors.New(result.Error)
		}
		return snap, fmt.Errorf("%w: %s", ErrDocumentSaveUncertain, result.Error)
	}
	return s.Snapshot(id)
}

func (s *DocumentService) prepareDraftOverwrite(ctx context.Context, owner document.Owner, id string, revision uint64, path string, encoding textencoding.Encoding, named bool) (ReviewedEditPreview, error) {
	if err := ctx.Err(); err != nil {
		return ReviewedEditPreview{}, err
	}
	if !filepath.IsAbs(path) {
		return ReviewedEditPreview{}, errors.New("choose an absolute destination")
	}
	path, err := reviewedURIPath(s.root, fileuri.FromPath(path))
	if err != nil {
		return ReviewedEditPreview{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.owned(owner, id)
	if err != nil {
		return ReviewedEditPreview{}, err
	}
	if s.stateErr != nil || s.idleFenced || s.stateDB == nil {
		return ReviewedEditPreview{}, errors.New("review requires durable workspace storage")
	}
	if (!named && (!document.IsUntitledID(id) || d.snapshot.Path != "")) || (named && d.snapshot.Path != path) || d.snapshot.Revision != revision ||
		d.snapshot.Conflict || d.diskIntent != nil || d.relocation != nil {
		return ReviewedEditPreview{}, document.ErrRevision
	}
	if other := s.documentAtPath(path); other != nil && other != d {
		return ReviewedEditPreview{}, errors.New("the destination is already an open or retained document")
	}
	encoded, err := textencoding.EncodeText(d.snapshot.Text, encoding)
	if err != nil {
		return ReviewedEditPreview{}, err
	}
	if len(encoded) > reviewedEditFileBytes {
		return ReviewedEditPreview{}, errors.New("the draft is too large for a safe replacement review")
	}
	file, err := readWorkspaceEditFileBounded(path, reviewedEditFileBytes)
	if err != nil {
		return ReviewedEditPreview{}, err
	}
	if named && (!d.snapshot.DiskExists || sha256.Sum256(file.data) != d.snapshot.DiskHash) {
		return ReviewedEditPreview{}, ErrDocumentConflict
	}
	before, after := string(file.data), string(encoded)
	rel, err := filepath.Rel(s.root, path)
	if err != nil {
		return ReviewedEditPreview{}, err
	}
	preview := ReviewedEditPreview{
		ID: "save-review-" + rand.Text(), Label: "Replace file",
		Text:     fmt.Sprintf("%s\nEncoding: %s", filepath.ToSlash(rel), encoding.Label()),
		Boundary: types.InsightBoundary{Source: s.analysisIdentityLocked(d)},
		Files: []ReviewedEditFile{{
			Path: path, BeforeSHA256: reviewedHash(before), AfterSHA256: reviewedHash(after),
			Mode: uint32(file.info.Mode()), BeforeBytes: len(before), AfterBytes: len(after),
		}},
	}
	if named {
		preview.Label = "Save with encoding"
	}
	raw, err := json.Marshal(struct {
		Owner   document.Owner
		Preview ReviewedEditPreview
	}{owner, preview})
	if err != nil {
		return ReviewedEditPreview{}, err
	}
	preview.Digest = reviewedHash(string(raw))
	plan := &reviewedEdit{
		preview: preview, owner: owner, expires: time.Now().Add(reviewedEditLifetime),
		files: []workspaceEditFile{file},
		edits: []FileEdit{{Path: path, OldContent: before, NewContent: after}},
		check: func() error { return nil }, draftSaveID: id, draftSaveEncoding: encoding,
	}
	if err := file.check(file.data, file.info); err != nil {
		return ReviewedEditPreview{}, err
	}
	if err := s.validateReviewedPlanLocked(ctx, plan); err != nil {
		return ReviewedEditPreview{}, err
	}
	for key, older := range s.reviewed {
		if time.Now().After(older.expires) {
			delete(s.reviewed, key)
		}
	}
	if len(s.reviewed) >= reviewedEditMaxPending {
		return ReviewedEditPreview{}, errors.New("close an earlier edit review before requesting another")
	}
	if s.reviewed == nil {
		s.reviewed = make(map[string]*reviewedEdit)
	}
	s.reviewed[preview.ID] = plan
	return cloneReviewedPreview(preview), nil
}

// The document mutex and reviewed writer's native preimage fence are held.
func (s *DocumentService) commitReviewedDraftSaveLocked(p *reviewedEdit) error {
	d := s.docs[p.draftSaveID]
	path := p.edits[0].Path
	disk, err := readLiveDocument(path)
	if err != nil || !disk.Exists || !bytes.Equal(disk.Text, d.snapshot.Text) || disk.Encoding != p.draftSaveEncoding {
		d.snapshot.Conflict = true
		return errors.Join(errors.New("saved file needs inspection"), err)
	}
	trial := stageDocument(d)
	trial.snapshot.Path = path
	trial.snapshot.Saved = bytes.Clone(trial.snapshot.Text)
	trial.snapshot.DiskHash = disk.Hash
	trial.snapshot.Encoding = disk.Encoding
	trial.snapshot.SavedEncoding = disk.Encoding
	trial.snapshot.DiskExists = true
	trial.snapshot.Dirty, trial.snapshot.Conflict = false, false
	trial.diskIntent = nil
	if err := s.persistDocument(trial); err != nil {
		d.snapshot.Conflict = true
		return err
	}
	*d = *trial
	if s.lsp != nil && !d.lspOpen {
		s.lsp.OpenFile(context.Background(), s.analysisIdentityLocked(d), string(d.snapshot.Text))
		d.lspOpen = true
	}
	return nil
}

// UntitledOverwriteComparison is detached text from the exact retained plan,
// not a second read of a target that may have changed since confirmation.
type UntitledOverwriteComparison struct {
	Path          string
	Before, After []byte
}

func (s *DocumentService) ReadUntitledOverwrite(ctx context.Context, owner document.Owner, id, digest string) (UntitledOverwriteComparison, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.reviewed[id]
	if p == nil || p.owner != owner || p.preview.Digest != digest || p.draftSaveID == "" {
		return UntitledOverwriteComparison{}, document.ErrOwner
	}
	if err := s.validateReviewedPlanLocked(ctx, p); err != nil {
		return UntitledOverwriteComparison{}, err
	}
	before, _, err := textencoding.DecodeText(p.files[0].data)
	if err != nil {
		return UntitledOverwriteComparison{}, err
	}
	after, _, err := textencoding.DecodeText([]byte(p.edits[0].NewContent))
	return UntitledOverwriteComparison{Path: p.edits[0].Path, Before: before, After: after}, err
}
