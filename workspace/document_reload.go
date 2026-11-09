package workspace

import (
	"bytes"
	"context"
	"errors"

	"crude/document"
)

// Reload reads saved bytes without writing them. Replaced text is one ordinary
// author-owned undo group; a failed read never consumes the current draft.
func (s *DocumentService) Reload(ctx context.Context, owner document.Owner, id, request string, revision uint64) (document.Snapshot, error) {
	s.mu.Lock()
	defer func() { s.mu.Unlock(); s.changed(id) }()
	d, err := s.owned(owner, id)
	if err != nil {
		return document.Snapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return cloneDocument(d.snapshot), err
	}
	if d.snapshot.Revision != revision || d.snapshot.Path == "" || d.diskIntent != nil || d.relocation != nil {
		return cloneDocument(d.snapshot), document.ErrRevision
	}
	disk, err := readLiveDocument(d.snapshot.Path)
	if err != nil {
		return cloneDocument(d.snapshot), err
	}
	if !disk.Exists {
		return cloneDocument(d.snapshot), errors.New("file no longer exists. Save with a new name or restore it from Local History")
	}
	trial := stageDocument(d)
	if !bytes.Equal(trial.snapshot.Text, disk.Text) {
		index, err := s.commit(trial, document.Change{DocumentID: id, RequestID: request, Owner: owner, BaseRevision: revision, GroupID: request,
			Edits: []document.Edit{{Start: 0, End: len(trial.snapshot.Text), Text: string(disk.Text)}}})
		if err != nil {
			return cloneDocument(d.snapshot), err
		}
		trial.undo[author(owner)] = append(trial.undo[author(owner)], documentUndoUnit{index})
		delete(trial.redo, author(owner))
	}
	trial.snapshot.Saved = bytes.Clone(disk.Text)
	trial.snapshot.Encoding, trial.snapshot.SavedEncoding = disk.Encoding, disk.Encoding
	trial.snapshot.DiskHash, trial.snapshot.DiskExists = disk.Hash, true
	trial.snapshot.Dirty, trial.snapshot.Conflict = false, false
	if err := s.persistDocument(trial); err != nil {
		return cloneDocument(d.snapshot), err
	}
	*d = *trial
	s.observeAnalysisLocked(d)
	return cloneDocument(d.snapshot), nil
}
