package workspace

import (
	"bytes"
	"context"
	"crude/textencoding"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"crude/document"
)

// SaveUntitled creates a new file for an exact unnamed document revision.
// The disk writer atomically refuses an existing target, including a target
// created after its precondition check. This operation never overwrites a file.
func (s *DocumentService) SaveUntitled(ctx context.Context, owner document.Owner, id string, revision uint64, path string, encoding textencoding.Encoding) (document.Snapshot, error) {
	s.mu.Lock()
	defer func() { s.mu.Unlock(); s.changed(id) }()
	d, err := s.owned(owner, id)
	if err != nil {
		return document.Snapshot{}, err
	}
	if revision != d.snapshot.Revision {
		return cloneDocument(d.snapshot), document.ErrRevision
	}
	data, err := textencoding.EncodeText(d.snapshot.Text, encoding)
	if err != nil {
		return cloneDocument(d.snapshot), err
	}
	if len(data) > document.MaxBytes {
		return cloneDocument(d.snapshot), document.ErrLimit
	}
	if !document.IsUntitledID(id) || d.snapshot.Path != "" || d.diskIntent != nil || d.relocation != nil || d.snapshot.Conflict {
		return cloneDocument(d.snapshot), errors.New("unnamed document requires reconciliation before saving")
	}
	if path == "" || len(path) > 4096 || s.saves == nil {
		return cloneDocument(d.snapshot), errors.New("a file destination is required")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(s.root, path)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return cloneDocument(d.snapshot), err
	}
	if s.documentAtPath(path) != nil || s.docs[path] != nil {
		return cloneDocument(d.snapshot), ErrDocumentConflict
	}
	if _, err := os.Lstat(path); err == nil {
		return cloneDocument(d.snapshot), ErrDocumentConflict
	} else if !errors.Is(err, os.ErrNotExist) {
		return cloneDocument(d.snapshot), err
	}
	if err := s.watch.add(filepath.Dir(path)); err != nil {
		return cloneDocument(d.snapshot), err
	}
	ctx, cancel := context.WithCancel(ctx)
	s.ownerMu.Lock()
	if s.detached[owner] || s.epochs[owner.ClientID] != owner.Epoch {
		s.ownerMu.Unlock()
		cancel()
		return cloneDocument(d.snapshot), document.ErrOwner
	}
	s.saveCancels[owner] = cancel
	s.ownerMu.Unlock()
	defer func() {
		cancel()
		s.ownerMu.Lock()
		delete(s.saveCancels, owner)
		s.ownerMu.Unlock()
	}()
	intent := stageDocument(d)
	intent.diskIntent = &documentDiskIntent{Revision: revision, Hash: sha256.Sum256(data), Exists: true, Target: path}
	if err := s.persistDocument(intent); err != nil {
		return cloneDocument(d.snapshot), err
	}
	*d = *intent
	result := s.saves.Save(ctx, DocumentSaveRequest{
		BufferID: id, Path: path, Data: data, Revision: revision,
		ExpectedExist: false, Reason: DocumentSaveManual,
	})
	trial := stageDocument(d)
	trial.diskIntent = nil
	if result.Committed {
		trial.snapshot.Path = path
		trial.snapshot.Encoding = encoding
		trial.snapshot.SavedEncoding = encoding
		trial.snapshot.Saved = bytes.Clone(d.snapshot.Text)
		trial.snapshot.DiskHash = result.DiskHash
		trial.snapshot.DiskExists = true
		trial.snapshot.Dirty = false
		// A post-commit verification failure cannot authorize another write.
		trial.snapshot.Conflict = result.Err != nil
	}
	if err := s.persistDocument(trial); err != nil {
		d.snapshot.Conflict = true
		return cloneDocument(d.snapshot), fmt.Errorf("%w: %v", ErrDocumentSaveUncertain, err)
	}
	*d = *trial
	if result.Committed && s.lsp != nil && !d.lspOpen {
		s.lsp.OpenFile(context.Background(), s.analysisIdentityLocked(d), string(d.snapshot.Text))
		d.lspOpen = true
	}
	if result.Committed && result.Err != nil {
		return cloneDocument(d.snapshot), fmt.Errorf("%w: %v", ErrDocumentSaveUncertain, result.Err)
	}
	return cloneDocument(d.snapshot), result.Err
}
