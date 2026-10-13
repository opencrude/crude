// Package worktree owns explicit managed execution artifacts. It never changes
// the logical checkout's branch, index or files. Integration is a neutral plan:
// the workspace document and Local History owners must commit it transactionally.
package worktree

import "time"

const FormatVersion = 1

type Mode string

const (
	Auto     Mode = "auto"
	Shared   Mode = "shared"
	Isolated Mode = "worktree"
)

type Owner struct {
	AgentID string `json:"agent_id"`
	RunID   string `json:"run_id"`
}

// Document is accepted shared text, not a private shell draft. Deleted files
// have Exists=false; Revision must be supplied even for accepted deletion.
type Document struct {
	Revision uint64 `json:"revision"`
	Exists   bool   `json:"exists"`
	Data     []byte `json:"data,omitempty"`
	Mode     uint32 `json:"mode,omitempty"`
}

type File struct {
	Type             string `json:"type"`
	Mode             uint32 `json:"mode"`
	Hash             string `json:"hash"`
	Data             []byte `json:"data"`
	DocumentRevision uint64 `json:"document_revision,omitempty"`
}

type Options struct {
	MaxContexts  int
	MaxFiles     int
	MaxBytes     int64
	MaxFileBytes int64
}

type Request struct {
	Owner Owner
	// SourceContextID selects exact current checkout bytes, without logical document overlays.
	SourceContextID string
	Mode            Mode
	BaseCommit      string
	// Independent selects isolation for Auto; caller owns task classification.
	Independent bool
	Documents   map[string]Document
	// IncludeIgnored contains exact regular-file paths explicitly authorized by
	// the caller. No directory globbing or setup scripts are performed.
	IncludeIgnored []string
}

type Context struct {
	Version           int               `json:"version"`
	ID                string            `json:"id"`
	SourceContextID   string            `json:"source_context_id,omitempty"`
	LogicalRoot       string            `json:"logical_root"`
	Root              string            `json:"root"`
	CommonGitDir      string            `json:"common_git_dir,omitempty"`
	Mode              Mode              `json:"mode"`
	Reason            string            `json:"reason"`
	BaseCommit        string            `json:"base_commit,omitempty"`
	Owner             Owner             `json:"owner"`
	CreatedAt         time.Time         `json:"created_at"`
	Baseline          map[string]File   `json:"baseline"`
	DocumentRevisions map[string]uint64 `json:"document_revisions,omitempty"`
	Pins              []Owner           `json:"pins"`
	Kept              bool              `json:"kept"`
	Ready             bool              `json:"ready"`
	Removed           bool              `json:"removed"`
	Integration       *Receipt          `json:"integration,omitempty"`
	IntegratedSource  string            `json:"integrated_source,omitempty"`
	ArchivedResult    map[string]File   `json:"archived_result,omitempty"`
	ArchiveCommit     string            `json:"archive_commit,omitempty"`
}

type Change struct {
	Path   string `json:"path"`
	Before *File  `json:"before,omitempty"`
	After  *File  `json:"after,omitempty"`
}

type PlanDelta struct {
	ContextID    string   `json:"context_id"`
	Owner        Owner    `json:"owner"`
	Digest       string   `json:"digest"`
	SourceDigest string   `json:"source_digest,omitempty"`
	Changes      []Change `json:"changes"`
}

// Receipt is evidence supplied by the existing integration owner, not a claim
// that publishing a receipt itself applies files or makes cleanup safe.
type Receipt struct {
	PlanDigest       string `json:"plan_digest"`
	OperationID      string `json:"operation_id"`
	SafetySnapshotID string `json:"safety_snapshot_id"`
}
