package coreprotocol

import (
	"crypto/sha256"
	"errors"
	"fmt"

	"crude/workspace"
)

// DocumentSnapshotSave commits a client's immutable buffer snapshot with exact
// disk preconditions. DocumentSave instead saves a core-owned live document.
const DocumentSnapshotSave = "document.snapshot_save"

type DocumentSnapshotSaveRequest struct {
	Request workspace.DocumentSaveRequest `json:"request"`
}

// DocumentSnapshotSaveResult preserves domain failures in the result rather
// than a transport fault, including successful replacement with durability errors.
type DocumentSnapshotSaveResult struct {
	Request   workspace.DocumentSaveRequest `json:"request"`
	DiskHash  [sha256.Size]byte             `json:"disk_hash"`
	Unchanged bool                          `json:"unchanged"`
	Committed bool                          `json:"committed"`
	Conflict  bool                          `json:"conflict"`
	Error     string                        `json:"error,omitempty"`
}

func NewDocumentSnapshotSaveResult(result workspace.DocumentSaveResult) DocumentSnapshotSaveResult {
	out := DocumentSnapshotSaveResult{Request: result.Request, DiskHash: result.DiskHash,
		Unchanged: result.Unchanged, Committed: result.Committed, Conflict: errors.Is(result.Err, workspace.ErrDocumentConflict)}
	if result.Err != nil {
		out.Error = result.Err.Error()
	}
	return out
}

func (result DocumentSnapshotSaveResult) DocumentResult() workspace.DocumentSaveResult {
	out := workspace.DocumentSaveResult{Request: result.Request, DiskHash: result.DiskHash,
		Unchanged: result.Unchanged, Committed: result.Committed}
	if result.Conflict {
		out.Err = fmt.Errorf("%s: %w", result.Error, workspace.ErrDocumentConflict)
	} else if result.Error != "" {
		out.Err = errors.New(result.Error)
	}
	return out
}
