package workspace

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"

	"crude/localhistory"
)

// ApplyLSPRename runs off the UI thread. Its immutable reviewed preimages must
// match a mandatory recoverable safety snapshot before FileOps may commit.
func (w *Workspace) ApplyLSPRename(ctx context.Context, edits []FileEdit) (FileOperationResultMsg, string) {
	failure := func(err error) (FileOperationResultMsg, string) {
		return FileOperationResultMsg{Path: "LSP rename", Err: err}, ""
	}
	if w == nil || w.FileOps == nil || w.LocalHistory == nil {
		return failure(fmt.Errorf("local history is required for a recoverable rename"))
	}
	paths := make([]string, 0, len(edits))
	for _, edit := range edits {
		if !pathWithin(w.RootPath, edit.Path) {
			return failure(fmt.Errorf("rename target outside workspace"))
		}
		rel, err := filepath.Rel(w.RootPath, edit.Path)
		if err != nil {
			return failure(err)
		}
		paths = append(paths, filepath.ToSlash(rel))
	}
	snapshot, err := w.CaptureHistory(ctx, localhistory.SnapshotOptions{Kind: "safety", Label: "Before LSP rename", IncludePaths: paths, DisableGitIgnore: true})
	if err != nil {
		return failure(err)
	}
	for i, edit := range edits {
		entry, exists := snapshot.Entries[paths[i]]
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(edit.OldContent)))
		if !exists || entry.Type != localhistory.EntryFile || entry.Hash != hash {
			return failure(fmt.Errorf("rename preimage changed before safety snapshot: %s", edit.Path))
		}
	}
	if err := ctx.Err(); err != nil {
		return failure(err)
	}
	return w.ApplyWorkspaceEdit(edits), snapshot.ID
}
