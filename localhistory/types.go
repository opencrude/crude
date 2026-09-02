// Package localhistory stores immutable workspace snapshots and applies
// precondition-checked restores. It is presentation-neutral and never writes
// history artifacts inside the project tree.
package localhistory

import "time"

const FormatVersion = 1

type EntryType string

const (
	EntryFile      EntryType = "file"
	EntryDirectory EntryType = "directory"
	EntrySymlink   EntryType = "symlink"
	EntryHardlink  EntryType = "hardlink"
	EntrySpecial   EntryType = "special"
)

type ChunkRef struct {
	Hash string `json:"hash"`
	Size int64  `json:"size"`
}

type FileObject struct {
	Version int        `json:"version"`
	Hash    string     `json:"hash"`
	Size    int64      `json:"size"`
	Chunks  []ChunkRef `json:"chunks"`
}

type Entry struct {
	Type           EntryType `json:"type"`
	Mode           uint32    `json:"mode"`
	Hash           string    `json:"hash,omitempty"`
	Size           int64     `json:"size,omitempty"`
	SymlinkTarget  string    `json:"symlink_target,omitempty"`
	HardlinkTarget string    `json:"hardlink_target,omitempty"`
	SpecialMode    string    `json:"special_mode,omitempty"`
}

type Manifest struct {
	Version   int              `json:"version"`
	ID        string           `json:"id"`
	ParentID  string           `json:"parent_id,omitempty"`
	CreatedAt int64            `json:"created_at"`
	Kind      string           `json:"kind"`
	Label     string           `json:"label,omitempty"`
	Entries   map[string]Entry `json:"entries"`
}

func (m Manifest) Time() time.Time { return time.Unix(0, m.CreatedAt) }

type SnapshotOptions struct {
	Kind                string   `json:"kind,omitempty"`
	Label               string   `json:"label,omitempty"`
	ParentID            string   `json:"parent_id,omitempty"`
	UseGitIgnore        bool     `json:"use_gitignore,omitempty"`
	DisableGitIgnore    bool     `json:"disable_gitignore,omitempty"`
	ExtraIgnorePatterns []string `json:"extra_ignore_patterns,omitempty"`
	MaxFileSize         int64    `json:"max_file_size,omitempty"`
}

type ChangeAction string

const (
	ChangeCreate  ChangeAction = "create"
	ChangeRestore ChangeAction = "restore"
	ChangeDelete  ChangeAction = "delete"
)

type Change struct {
	Path           string       `json:"path"`
	Action         ChangeAction `json:"action"`
	Target         *Entry       `json:"target,omitempty"`
	Current        *Entry       `json:"current,omitempty"`
	UnsafeReason   string       `json:"unsafe_reason,omitempty"`
	ConflictReason string       `json:"conflict_reason,omitempty"`
	ExactAllowed   bool         `json:"exact_allowed,omitempty"`
	ExactTarget    *Entry       `json:"exact_target,omitempty"`
	AutoMerged     bool         `json:"auto_merged,omitempty"`
	TextDiff       string       `json:"text_diff,omitempty"`
	MovePeer       string       `json:"move_peer,omitempty"`
}

type RestorePlan struct {
	Version          int             `json:"version"`
	ID               string          `json:"id"`
	TargetSnapshotID string          `json:"target_snapshot_id"`
	CreatedAt        int64           `json:"created_at"`
	CaptureOptions   SnapshotOptions `json:"capture_options"`
	SessionID        string          `json:"session_id,omitempty"`
	MessageID        string          `json:"message_id,omitempty"`
	Changes          []Change        `json:"changes"`
}

type RestoreChoice struct {
	Path  string
	Exact bool
}

type RestoreResult struct {
	PlanID           string
	TargetSnapshotID string
	SafetySnapshotID string
	AfterSnapshotID  string
	Paths            []string
}

type Stats struct {
	ChunkObjects  int
	FileObjects   int
	Manifests     int
	Snapshots     int
	PhysicalBytes int64
	LogicalBytes  int64
}

type SnapshotRecord struct {
	ID           string
	ParentID     string
	CreatedAt    int64
	Kind         string
	Label        string
	LogicalBytes int64
}

type MessageScope struct {
	SessionID      string
	MessageID      string
	BeforeSnapshot string
	AfterSnapshot  string
	OpenedAt       int64
	ClosedAt       int64
	State          string
	ChangedPaths   []string
	CollisionPaths []string
}

type RestoreEvent struct {
	ID               int64
	PlanID           string
	TargetSnapshotID string
	SafetySnapshotID string
	AfterSnapshotID  string
	CreatedAt        int64
	Paths            []string
}

type RetentionPolicy struct {
	KeepHours    int
	KeepDaily    int
	MaxSnapshots int
	BudgetBytes  int64
}

type PruneResult struct {
	RemovedSnapshots int
	RemovedObjects   int
	FreedBytes       int64
}
