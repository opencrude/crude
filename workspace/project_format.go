package workspace

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"crude/fileio"
	"crude/localhistory"
)

const (
	formatProjectMaxFiles  = 200
	formatProjectMaxBytes  = 16 << 20
	formatProjectFileBytes = reviewedEditFileBytes
)

// FormatProjectResult is presentation-neutral evidence for one explicit project
// format. Open documents are skipped because their live text has a different
// owner; the user can format those with Format Document.
type FormatProjectResult struct {
	Changed          int
	SkippedOpen      int
	Files            []string
	SafetySnapshotID string
}

// FormatProject formats saved files with the catalog formatter for each file's
// language. It captures one Local History safety snapshot before any write and
// applies every changed file through the one transactional workspace-edit owner.
func (w *Workspace) FormatProject(ctx context.Context) (FormatProjectResult, error) {
	if w == nil || w.FileOps == nil || w.LocalHistory == nil || w.LiveDocuments == nil {
		return FormatProjectResult{}, errors.New("project formatting requires file, history and document owners")
	}
	var result FormatProjectResult
	var edits []FileEdit
	preimages := make([]localhistory.RetainedFile, 0, formatProjectMaxFiles)
	total := 0
	err := fileio.WalkProjectFiles(ctx, w.RootPath, formatProjectFileBytes, func(path string, data []byte) error {
		w.LiveDocuments.mu.Lock()
		open := w.LiveDocuments.documentAtPath(path) != nil
		w.LiveDocuments.mu.Unlock()
		if open {
			result.SkippedOpen++
			return nil
		}
		formatted, err := FormatDocument(ctx, path, data)
		if err != nil {
			return err
		}
		if string(formatted) == string(data) {
			return nil
		}
		if len(edits) == formatProjectMaxFiles || total+len(data)+len(formatted) > formatProjectMaxBytes {
			return errors.New("project formatting would change more than 200 files or 16 MiB; format files individually")
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(w.RootPath, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		edits = append(edits, FileEdit{Path: path, OldContent: string(data), NewContent: string(formatted)})
		preimages = append(preimages, localhistory.RetainedFile{Path: key, Data: append([]byte(nil), data...), Mode: uint32(info.Mode())})
		result.Files = append(result.Files, key)
		total += len(data) + len(formatted)
		return nil
	})
	if err != nil {
		return result, err
	}
	if len(edits) == 0 {
		return result, nil
	}
	sort.Strings(result.Files)
	reference := "format-" + rand.Text()
	safety, err := w.LocalHistory.SnapshotRetainedFiles(ctx, reference, "Before Format Project", preimages)
	if err != nil {
		return result, err
	}
	result.SafetySnapshotID = safety.ID
	apply := w.FileOps.applyWorkspaceEdit(edits, nil, func() error {
		w.LiveDocuments.mu.Lock()
		defer w.LiveDocuments.mu.Unlock()
		for _, edit := range edits {
			if w.LiveDocuments.documentAtPath(edit.Path) != nil {
				return errors.New("a document opened during project formatting; no files changed")
			}
		}
		return nil
	}, nil, nil)
	if apply.Err != nil {
		if apply.NoEffect {
			// Only positive no-effect evidence relinquishes the retained preimage.
			if err := w.LocalHistory.ReleaseRetainedSnapshot(context.WithoutCancel(ctx), reference); err != nil {
				return result, errors.Join(apply.Err, err)
			}
			result.SafetySnapshotID = ""
		}
		return result, apply.Err
	}
	result.Changed = len(edits)
	return result, nil
}
