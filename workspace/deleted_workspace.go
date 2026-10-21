package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"crude/fileio"
	"crude/types"
)

// WorkspaceDeletion is an immutable preview of one saved workspace, not its
// project directory. Deletion moves the complete saved data into recovery
// storage. No session, history object, or project file is unlinked.
type WorkspaceDeletion struct {
	// Token names a connection-owned core preview. Local adapters keep the
	// private directory preimage instead; neither is a caller-authored plan.
	Token                string `json:"token,omitempty"`
	ID, Name, ProjectDir string
	SessionCount         int
	Issue                *types.OperationIssue `json:"issue,omitempty"`
	source               string
	identity             os.FileInfo
	stamp                [32]byte
	used                 *atomic.Bool
}

type DeletedWorkspace struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	ProjectDir   string    `json:"project_dir"`
	DeletedAt    time.Time `json:"deleted_at"`
	RecoveryPath string    `json:"recovery_path"`
}

func PreviewWorkspaceDeletion(ctx context.Context, id, projectDir, currentProject string) (WorkspaceDeletion, error) {
	var plan WorkspaceDeletion
	unlock, err := lockWorkspaceStorage(ctx, true)
	if err != nil {
		return plan, err
	}
	defer unlock()
	decoded, err := hex.DecodeString(id)
	if err != nil || len(decoded) != 8 || id != WorkspaceID(projectDir) {
		return plan, errors.New("workspace identity does not match its project")
	}
	if canonicalizePath(projectDir) == canonicalizePath(currentProject) {
		return plan, errors.New("the current workspace cannot be deleted")
	}
	root, err := WorkspaceRoot()
	if err != nil {
		return plan, err
	}
	source := filepath.Join(root, id)
	if err = realDirectoryPath(source); err != nil {
		return plan, err
	}
	identity, err := os.Lstat(source)
	if err != nil {
		return plan, err
	}
	info := readWorkspaceMetaUnlocked(ctx, filepath.Join(source, "workspace.db"))
	if err = ctx.Err(); err != nil {
		return plan, err
	}
	if info == nil || canonicalizePath(info.ProjectDir) != canonicalizePath(projectDir) {
		return plan, errors.New("saved workspace no longer matches the selected project")
	}
	stamp, err := workspaceDeletionStamp(ctx, source)
	if err != nil {
		return plan, err
	}
	return WorkspaceDeletion{ID: id, Name: WorkspaceDisplayName(*info), ProjectDir: projectDir, SessionCount: info.SessionCount, source: source, identity: identity, stamp: stamp, used: new(atomic.Bool)}, nil
}

// DeleteSavedWorkspace commits exactly the previewed directory identity. The
// recovery manifest is durable before the atomic rename, and an error before
// that rename leaves the source untouched. Later writes are preserved as part
// of the same directory rather than overwritten with a stale snapshot.
func DeleteSavedWorkspace(ctx context.Context, plan WorkspaceDeletion) (DeletedWorkspace, error) {
	var deleted DeletedWorkspace
	releaseStorage, err := lockWorkspaceStorage(ctx, true)
	if err != nil {
		return deleted, err
	}
	defer releaseStorage()
	unlock, err := fileio.LockMutationContext(ctx)
	if err != nil {
		return deleted, err
	}
	defer unlock()
	if plan.identity == nil || plan.source == "" || plan.used == nil || plan.used.Load() {
		return deleted, errors.New("workspace deletion has no preview")
	}
	if err = realDirectoryPath(plan.source); err != nil {
		return deleted, err
	}
	current, err := os.Lstat(plan.source)
	if err != nil || !os.SameFile(current, plan.identity) {
		return deleted, errors.New("saved workspace changed since preview")
	}
	stamp, err := workspaceDeletionStamp(ctx, plan.source)
	if err != nil {
		return deleted, err
	}
	if stamp != plan.stamp {
		return deleted, errors.New("saved workspace content changed since preview; review deletion again")
	}
	root := filepath.Join(filepath.Dir(filepath.Dir(plan.source)), "deleted-workspaces")
	if err = os.MkdirAll(root, 0700); err != nil {
		return deleted, err
	}
	if err = realDirectoryPath(root); err != nil {
		return deleted, err
	}
	dir, err := os.MkdirTemp(root, plan.ID+"-")
	if err != nil {
		return deleted, err
	}
	deleted = DeletedWorkspace{ID: plan.ID, Name: plan.Name, ProjectDir: plan.ProjectDir, DeletedAt: time.Now().UTC(), RecoveryPath: dir}
	data, err := json.MarshalIndent(deleted, "", "  ")
	if err != nil {
		return DeletedWorkspace{}, err
	}
	manifest, err := os.OpenFile(filepath.Join(dir, "workspace.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return DeletedWorkspace{}, err
	}
	_, writeErr := manifest.Write(data)
	if writeErr == nil {
		writeErr = manifest.Sync()
	}
	closeErr := manifest.Close()
	if err = errors.Join(writeErr, closeErr, ctx.Err()); err != nil {
		return DeletedWorkspace{}, err
	}
	if !plan.used.CompareAndSwap(false, true) {
		return DeletedWorkspace{}, errors.New("workspace deletion preview was already used")
	}
	if err = fileio.MoveDirectoryNoReplace(plan.source, filepath.Join(dir, "data")); err != nil && !fileio.WriteCommitted(err) {
		return DeletedWorkspace{}, err
	}
	return deleted, err
}

// RestoreDeletedWorkspace never overwrites a workspace created after deletion.
// Recovery data remains in place on conflict and can be inspected manually.
func RestoreDeletedWorkspace(ctx context.Context, deleted DeletedWorkspace) error {
	releaseStorage, err := lockWorkspaceStorage(ctx, true)
	if err != nil {
		return err
	}
	defer releaseStorage()
	unlock, err := fileio.LockMutationContext(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	root, err := WorkspaceRoot()
	if err != nil {
		return err
	}
	if deleted.ID != WorkspaceID(deleted.ProjectDir) {
		return errors.New("invalid recovery identity")
	}
	recoveryRoot := filepath.Join(filepath.Dir(root), "deleted-workspaces")
	if filepath.Dir(deleted.RecoveryPath) != recoveryRoot {
		return errors.New("recovery path is outside workspace recovery storage")
	}
	if err = realDirectoryPath(deleted.RecoveryPath); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(deleted.RecoveryPath, "workspace.json"))
	if err != nil {
		return err
	}
	var record DeletedWorkspace
	if err = json.Unmarshal(data, &record); err != nil {
		return err
	}
	if record.ID != deleted.ID || record.ProjectDir != deleted.ProjectDir || !record.DeletedAt.Equal(deleted.DeletedAt) {
		return errors.New("recovery manifest does not match this deletion")
	}
	target := filepath.Join(root, deleted.ID)
	if _, err = os.Lstat(target); !os.IsNotExist(err) {
		return errors.New("a workspace already exists at this identity; recovery was preserved")
	}
	if err = realDirectoryPath(root); err != nil {
		return err
	}
	source := filepath.Join(deleted.RecoveryPath, "data")
	if err = realDirectoryPath(source); err != nil {
		return err
	}
	return fileio.MoveDirectoryNoReplace(source, target)
}

func workspaceDeletionStamp(ctx context.Context, root string) ([32]byte, error) {
	hash := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if transientWorkspaceSQLiteFile(path, info) {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		size, modified := info.Size(), info.ModTime().UnixNano()
		if info.IsDir() {
			// Directory timestamps may settle after a read-only SQLite/WAL
			// inspection on Windows. Membership is already stamped by the walk.
			size, modified = 0, 0
		}
		fmt.Fprintf(hash, "%s\x00%d\x00%d\x00%d\n", rel, size, info.Mode(), modified)
		return nil
	})
	var stamp [32]byte
	copy(stamp[:], hash.Sum(nil))
	return stamp, err
}

// SQLite's SHM is a derived WAL index, not saved content. Even read-only
// catalog access rewrites its timestamps on Windows. An empty WAL also has no
// frames and may appear/disappear on read. Only recognize regular sidecars of
// an actual SQLite database; nonempty WALs (including committed newer work),
// journals, unrelated files and symlinks remain part of the exact preimage.
func transientWorkspaceSQLiteFile(path string, info os.FileInfo) bool {
	if !info.Mode().IsRegular() {
		return false
	}
	var database string
	switch {
	case strings.HasSuffix(path, ".db-shm"):
		database = strings.TrimSuffix(path, "-shm")
	case strings.HasSuffix(path, ".db-wal") && info.Size() == 0:
		database = strings.TrimSuffix(path, "-wal")
	default:
		return false
	}
	base, err := os.Lstat(database)
	if err != nil || !base.Mode().IsRegular() {
		return false
	}
	f, err := os.Open(database)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	var header [16]byte
	_, err = io.ReadFull(f, header[:])
	return err == nil && string(header[:]) == "SQLite format 3\x00"
}

func realDirectoryPath(path string) error {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("workspace storage is not a real directory: %s", current)
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}
