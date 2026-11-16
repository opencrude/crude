package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"crude/document"
)

// Reload makes the saved file the document's save baseline without writing
// it. Without a resolution the text becomes the file text, which discards
// unsaved changes; an unnamed document has no file and becomes empty. With a
// resolution the text stays or becomes resolution.Text, and it stays unsaved
// where it differs from the file. Either way a disk conflict is settled.
// Replaced text is one ordinary author-owned undo group; a failed read never
// consumes the current draft.
func (s *DocumentService) Reload(ctx context.Context, owner document.Owner, id, request string, revision uint64, resolve *document.Resolution) (document.Snapshot, error) {
	s.mu.Lock()
	defer func() { s.mu.Unlock(); s.changed(id) }()
	d, err := s.owned(owner, id)
	if err != nil {
		return document.Snapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return cloneDocument(d.snapshot), err
	}
	if d.snapshot.Revision != revision || d.diskIntent != nil || d.relocation != nil {
		return cloneDocument(d.snapshot), document.ErrRevision
	}
	if resolve != nil && resolve.Replace && (len(resolve.Text) > document.MaxBytes || !utf8.Valid(resolve.Text)) {
		return cloneDocument(d.snapshot), errors.New("resolved text is not valid UTF-8 within the document limit")
	}
	var disk documentDiskSource
	if d.snapshot.Path != "" {
		if disk, err = readLiveDocument(d.snapshot.Path); err != nil {
			return cloneDocument(d.snapshot), err
		}
	} else if resolve != nil {
		return cloneDocument(d.snapshot), errors.New("an unnamed document has no file to resolve against")
	}
	if resolve != nil && resolve.DiskHash != ([32]byte{}) && resolve.DiskHash != disk.Hash {
		return cloneDocument(d.snapshot), fmt.Errorf("%w again. Compare the versions again", ErrDocumentConflict)
	}
	if d.snapshot.Path != "" && !disk.Exists && resolve == nil {
		return cloneDocument(d.snapshot), errors.New("file no longer exists. Save with a new name or restore it from Local History")
	}
	trial := stageDocument(d)
	target := disk.Text
	if resolve != nil {
		target = trial.snapshot.Text
		if resolve.Replace {
			target = resolve.Text
		}
	}
	if !bytes.Equal(trial.snapshot.Text, target) {
		index, err := s.commit(trial, document.Change{DocumentID: id, RequestID: request, Owner: owner, BaseRevision: revision, GroupID: request,
			Edits: []document.Edit{{Start: 0, End: len(trial.snapshot.Text), Text: string(target)}}})
		if err != nil {
			return cloneDocument(d.snapshot), err
		}
		trial.undo[author(owner)] = append(trial.undo[author(owner)], documentUndoUnit{index})
		delete(trial.redo, author(owner))
	}
	if d.snapshot.Path != "" {
		trial.snapshot.Saved = bytes.Clone(disk.Text)
		if resolve == nil {
			trial.snapshot.Encoding = disk.Encoding
		}
		trial.snapshot.SavedEncoding = disk.Encoding
		if !disk.Exists {
			// A deleted file has no encoding; keeping the text recreates it
			// with the document's own encoding.
			trial.snapshot.SavedEncoding = trial.snapshot.Encoding
		}
		trial.snapshot.DiskHash, trial.snapshot.DiskExists = disk.Hash, disk.Exists
	}
	trial.snapshot.Conflict, trial.snapshot.Removed, trial.snapshot.DiskSeen = false, false, [32]byte{}
	trial.snapshot.Dirty = trial.snapshot.ContentDirty()
	if err := s.persistDocument(trial); err != nil {
		return cloneDocument(d.snapshot), err
	}
	*d = *trial
	s.observeAnalysisLocked(d)
	return cloneDocument(d.snapshot), nil
}
