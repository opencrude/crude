package workspace

import (
	"bytes"
	"slices"

	"crude/document"
	"crude/worktree"
)

// mergeDiskLocked joins a changed file into a document with unsaved edits
// when the two sets of changes do not touch: base is the saved text, ours the
// document text and theirs the file. It commits the external changes as one
// disk change, makes the file the save baseline and reports true. It reports
// false, changing nothing, when the changes overlap, the file is gone, its
// encoding changed or the text cannot be merged exactly.
func (s *DocumentService) mergeDiskLocked(d *liveDocument, id string, disk documentDiskSource) bool {
	if !disk.Exists || !d.snapshot.DiskExists || disk.Encoding != d.snapshot.SavedEncoding {
		return false
	}
	edits, err := mergeDiskEdits(d.snapshot.Saved, d.snapshot.Text, disk.Text)
	if err != nil {
		return false
	}
	if len(edits) > 0 {
		if _, err := s.commit(d, document.Change{DocumentID: id, RequestID: document.DiskRequestID, BaseRevision: d.snapshot.Revision, Edits: edits}); err != nil {
			return false
		}
	}
	d.snapshot.Saved = bytes.Clone(disk.Text)
	d.snapshot.DiskHash = disk.Hash
	d.snapshot.SavedEncoding = disk.Encoding
	d.snapshot.DiskExists = true
	d.snapshot.Dirty = d.snapshot.ContentDirty()
	d.snapshot.Conflict, d.snapshot.DiskSeen = false, [32]byte{}
	return true
}

// mergeDiskEdits returns the edits that apply the external changes (base to
// theirs) to ours, which already holds the unsaved changes (base to ours). A
// change made the same way on both sides is already in ours. Changes that
// overlap or touch fail with document.ErrOverlap: the user decides those.
func mergeDiskEdits(base, ours, theirs []byte) ([]document.Edit, error) {
	if bytes.Equal(ours, theirs) {
		return nil, nil
	}
	mine, err := worktree.TextEdits(base, ours)
	if err != nil {
		return nil, err
	}
	external, err := worktree.TextEdits(base, theirs)
	if err != nil {
		return nil, err
	}
	external = slices.DeleteFunc(external, func(e document.Edit) bool { return slices.Contains(mine, e) })
	if len(external) == 0 {
		return nil, nil
	}
	edits, err := document.Rebase(external, mine)
	if err != nil {
		return nil, err
	}
	// Prove the result before any commit: exact application, nothing lost.
	if _, _, err := document.Apply(ours, edits); err != nil {
		return nil, err
	}
	return edits, nil
}
