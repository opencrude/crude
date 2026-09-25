package fileio

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"

	"crude/codexio"
)

// CodexChange describes a committed path image, including commits preceding a
// later failure. It is presentation-neutral; receipts remain the undo authority.
type CodexChange struct {
	Path          string
	Before, After MutationState
}

// ApplyCodexPatch applies the parsed upstream language under Crude's shared
// mutation lease. Each committed write/delete has its own durable receipt, so
// partial success and move-overwrite remain undoable without inventing rollback.
func (e *Editor) ApplyCodexPatch(ctx context.Context, root string, patch *codexio.Patch, mode codexio.Mode, prepare MutationPrepare) (changes []CodexChange, resultErr error) {
	if len(patch.Operations) == 0 {
		//nolint:staticcheck // Upstream Codex diagnostic.
		return nil, fmt.Errorf("No files were modified.")
	}
	if patch.EnvironmentID != "" {
		return nil, fmt.Errorf("environment %q is unavailable in this local session", patch.EnvironmentID)
	}
	unlock, err := LockMutationContext(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	resolve := func(path string) string {
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		return filepath.Clean(path)
	}
	// Observe committed receipts, not rereads of files another process can edit.
	tracked := func(expectedPath string, expected *MutationState) MutationPrepare {
		return func(receipt MutationReceipt) (MutationComplete, error) {
			if expected != nil {
				if err := validateMutationState(expectedPath, *expected); err != nil {
					return nil, err
				}
				for _, p := range receipt.Paths {
					if p.Path == expectedPath && (p.Before.Kind != expected.Kind || !bytes.Equal(p.Before.Bytes, expected.Bytes) || p.Before.Mode != expected.Mode) {
						return nil, fmt.Errorf("patch source changed: %s", expectedPath)
					}
				}
			}
			receipt.OperationName = "apply_patch"
			complete, err := prepareMutation(prepare, receipt)
			if err != nil {
				return nil, err
			}
			return func(id string, committed bool) error {
				if committed {
					for _, p := range receipt.Paths {
						if p.After.Kind != MutationDirectory {
							changes = append(changes, CodexChange(p))
						}
					}
				}
				if complete != nil {
					return complete(id, committed)
				}
				return nil
			}, nil
		}
	}
	for _, op := range patch.Operations {
		if err := ctx.Err(); err != nil {
			return changes, err
		}
		path, err := filepath.Abs(resolve(op.Path))
		if err != nil {
			return changes, err
		}
		switch op.Kind {
		case codexio.AddFile:
			err = e.writeBytesTracked(path, []byte(op.Contents), tracked("", nil))
		case codexio.DeleteFile:
			err = e.deleteTracked(path, tracked("", nil))
		case codexio.UpdateFile:
			var data []byte
			var info os.FileInfo
			data, info, err = readFileSecure(path, e.policy)
			if err != nil {
				//nolint:staticcheck // Upstream Codex diagnostic.
				return changes, fmt.Errorf("Failed to read file to update %s: %w", op.Path, err)
			}
			before := regularState(data, info.Mode())
			content, updateErr := mode.Update(op.Path, string(data), op.Diff)
			if updateErr != nil {
				return changes, updateErr
			}
			dest := path
			if op.MoveTo != "" {
				dest, err = filepath.Abs(resolve(op.MoveTo))
				if err != nil {
					return changes, err
				}
			}
			err = e.writeBytesTracked(dest, []byte(content), tracked(path, &before))
			if err == nil && op.MoveTo != "" {
				if filepath.Clean(dest) == path {
					before.Bytes = []byte(content)
				}
				err = e.deleteTracked(path, tracked(path, &before))
			}
		}
		if err != nil {
			return changes, err
		}
	}
	return changes, nil
}
