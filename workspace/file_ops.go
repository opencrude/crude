package workspace

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"crude/fileio"
)

// ── File operation results ──
//
// File mutations are synchronous commands. Each operation returns its result
// directly to the caller, which owns any follow-up UI state changes. Results
// must not be echoed through EventBus: TUI callers execute inside Bubble Tea's
// Update loop, where a synchronous Program.Send would recursively block.

// FileOperationResultMsg is the returned result of a file mutation.
type FileOperationResultMsg struct {
	// Op identifies the operation type.
	Op FileOperation
	// Path is the primary path acted upon.
	Path string
	// OldPath is set for rename operations (the original path).
	OldPath string
	// NewPath is set for rename operations (the new path).
	NewPath string
	// IsDir is true for directory operations.
	IsDir bool
	// Err is non-nil if the operation failed.
	Err error
	// Detail provides additional context (e.g., "already exists").
	Detail string
}

// FileOperation identifies the type of file mutation.
type FileOperation int

const (
	FileOpCreate FileOperation = iota
	FileOpCreateFolder
	FileOpRename
	FileOpDelete
	FileOpApplyEdit
)

func (o FileOperation) String() string {
	switch o {
	case FileOpCreate:
		return "create"
	case FileOpCreateFolder:
		return "create-folder"
	case FileOpRename:
		return "rename"
	case FileOpDelete:
		return "delete"
	case FileOpApplyEdit:
		return "apply-edit"
	default:
		return "unknown"
	}
}

// Error implements the error interface for failed operations.
func (m FileOperationResultMsg) Error() string {
	if m.Err == nil {
		return ""
	}
	return fmt.Sprintf("%s %s: %s", m.Op, filepath.Base(m.Path), m.Err)
}

// Succeeded reports whether the operation completed without error.
func (m FileOperationResultMsg) Succeeded() bool {
	return m.Err == nil
}

// ── FileOpsService ──

// FileOpsService owns all filesystem mutations for a workspace.
// Every file creation, deletion, rename, and LSP workspace edit
// goes through this service. The invoking command receives the typed
// result and updates presentation state without owning filesystem handles.
type FileOpsService struct {
	rootPath string
	// eventBus remains part of workspace service wiring for compatibility,
	// but command-owned file operation results are never published through it.
	eventBus *EventBus
	logger   Logger
	// commitWorkspaceEdit is an instance-local fault-injection seam; nil uses the shared commit primitive.
	commitWorkspaceEdit func(from, to string) error
}

// NewFileOpsService creates a FileOpsService rooted at the given project path.
// File operation results are returned directly and are not sent to eventBus.
func NewFileOpsService(rootPath string, eventBus *EventBus, logger Logger) *FileOpsService {
	return &FileOpsService{
		rootPath: rootPath,
		eventBus: eventBus,
		logger:   logger,
	}
}

// CreateFile creates an empty file at the given path within the workspace.
// The path must be absolute and under the workspace root.
// The result is returned directly to the caller.
func (s *FileOpsService) CreateFile(path string) FileOperationResultMsg {
	msg := s.validatePath(path)
	if msg.Err != nil {
		return msg
	}
	unlock, ok := fileio.TryLockMutation()
	if !ok {
		msg.Err = fmt.Errorf("another file mutation is in progress")
		return msg
	}
	defer unlock()

	// Check if file already exists.
	if _, err := os.Stat(path); err == nil {
		msg.Err = fmt.Errorf("already exists: %s", filepath.Base(path))
		msg.Detail = "already exists"
		return msg
	}

	// Create the file.
	f, err := os.Create(path)
	if err != nil {
		msg.Err = err
		return msg
	}
	_ = f.Close()

	msg.Op = FileOpCreate
	if s.logger != nil {
		s.logger.Info("file-ops", "created file", "path", path)
	}
	return msg
}

// CreateFolder creates a directory (and parents) at the given path.
// The path must be absolute and under the workspace root.
// The result is returned directly to the caller.
func (s *FileOpsService) CreateFolder(path string) FileOperationResultMsg {
	msg := s.validatePath(path)
	if msg.Err != nil {
		return msg
	}
	unlock, ok := fileio.TryLockMutation()
	if !ok {
		msg.Err = fmt.Errorf("another file mutation is in progress")
		return msg
	}
	defer unlock()

	// Check if folder already exists.
	if _, err := os.Stat(path); err == nil {
		msg.Err = fmt.Errorf("already exists: %s", filepath.Base(path))
		msg.Detail = "already exists"
		return msg
	}

	// Create the directory.
	if err := os.MkdirAll(path, 0o755); err != nil {
		msg.Err = err
		return msg
	}

	msg.Op = FileOpCreateFolder
	msg.IsDir = true
	if s.logger != nil {
		s.logger.Info("file-ops", "created folder", "path", path)
	}
	return msg
}

// RenamePath renames a file or folder from oldPath to newPath.
// Both paths must be absolute and under the workspace root.
// The result is returned directly to the caller.
func (s *FileOpsService) RenamePath(oldPath, newPath string) FileOperationResultMsg {
	msg := FileOperationResultMsg{
		Path:    oldPath,
		OldPath: oldPath,
		NewPath: newPath,
	}

	// Validate both paths.
	if v := s.validatePath(oldPath); v.Err != nil {
		msg.Err = v.Err
		msg.Detail = "invalid source path"
		return msg
	}
	if v := s.validatePath(newPath); v.Err != nil {
		msg.Err = v.Err
		msg.Detail = "invalid target path"
		return msg
	}
	unlock, ok := fileio.TryLockMutation()
	if !ok {
		msg.Err = fmt.Errorf("another file mutation is in progress")
		return msg
	}
	defer unlock()

	// Check source exists.
	srcInfo, err := os.Stat(oldPath)
	if err != nil {
		msg.Err = fmt.Errorf("source does not exist: %s", filepath.Base(oldPath))
		msg.Detail = "source not found"
		return msg
	}
	msg.IsDir = srcInfo.IsDir()

	// Check target does not exist.
	if _, err := os.Stat(newPath); err == nil {
		msg.Err = fmt.Errorf("target already exists: %s", filepath.Base(newPath))
		msg.Detail = "target exists"
		return msg
	}

	// Perform the rename.
	if err := os.Rename(oldPath, newPath); err != nil {
		msg.Err = err
		return msg
	}

	msg.Op = FileOpRename
	if s.logger != nil {
		s.logger.Info("file-ops", "renamed", "from", oldPath, "to", newPath)
	}
	return msg
}

// DeletePath removes a file or directory at the given path.
// Directories are removed recursively.
// The path must be absolute and under the workspace root.
// The result is returned directly to the caller.
func (s *FileOpsService) DeletePath(path string) FileOperationResultMsg {
	msg := s.validatePath(path)
	if msg.Err != nil {
		return msg
	}
	unlock, ok := fileio.TryLockMutation()
	if !ok {
		msg.Err = fmt.Errorf("another file mutation is in progress")
		return msg
	}
	defer unlock()

	// Check if path exists.
	info, err := os.Stat(path)
	if err != nil {
		msg.Err = fmt.Errorf("does not exist: %s", filepath.Base(path))
		msg.Detail = "not found"
		return msg
	}
	msg.IsDir = info.IsDir()

	// Remove the path.
	if info.IsDir() {
		err = os.RemoveAll(path)
	} else {
		err = os.Remove(path)
	}
	if err != nil {
		msg.Err = err
		return msg
	}

	msg.Op = FileOpDelete
	if s.logger != nil {
		s.logger.Info("file-ops", "deleted", "path", path, "isDir", info.IsDir())
	}
	return msg
}

// ApplyWorkspaceEdit validates and stages the entire transaction before publishing
// any edit. It holds the shared mutation lease through commit and rollback.
func (s *FileOpsService) ApplyWorkspaceEdit(edits []FileEdit) FileOperationResultMsg {
	msg := FileOperationResultMsg{Op: FileOpApplyEdit, Path: "(workspace edit)"}
	if len(edits) == 0 {
		msg.Err = fmt.Errorf("no edits provided")
		return msg
	}
	seen := make(map[string]bool, len(edits))
	for i := range edits {
		if v := s.validatePath(edits[i].Path); v.Err != nil {
			msg.Err = v.Err
			return msg
		}
		path := filepath.Clean(edits[i].Path)
		key := path
		// Case-fold conservatively to reject aliases on case-insensitive filesystems.
		key = strings.ToLower(key)
		if seen[key] {
			msg.Err = fmt.Errorf("duplicate workspace edit path: %s", path)
			return msg
		}
		seen[key] = true
	}
	unlock, ok := fileio.TryLockMutation()
	if !ok {
		msg.Err = fmt.Errorf("another file mutation is in progress")
		return msg
	}
	defer unlock()

	prepared := make([]workspaceEditFile, 0, len(edits))
	defer func() {
		for _, p := range prepared {
			if p.next != "" {
				_ = os.Remove(p.next)
			}
			if p.backup != "" {
				_ = os.Remove(p.backup)
			}
		}
	}()
	var total int64
	for _, edit := range edits {
		p, err := readWorkspaceEditFile(filepath.Clean(edit.Path))
		if err != nil {
			msg.Err = err
			return msg
		}
		if string(p.data) != edit.OldContent {
			msg.Err = fmt.Errorf("content mismatch in %s (file changed on disk?)", edit.Path)
			return msg
		}
		total += int64(len(p.data)) + int64(len(edit.NewContent))
		if total > maxWorkspaceEditBytes {
			msg.Err = fmt.Errorf("workspace edit exceeds %d MiB transaction limit", maxWorkspaceEditBytes>>20)
			return msg
		}
		p.newData = []byte(edit.NewContent)
		prepared = append(prepared, p)
		target := &prepared[len(prepared)-1]
		target.backup, err = stageWorkspaceEditFile(p.path, p.data, p.info.Mode())
		if err == nil {
			target.next, err = stageWorkspaceEditFile(p.path, p.newData, p.info.Mode())
		}
		if err != nil {
			msg.Err = err
			return msg
		}
	}
	// Recheck all preimages after staging, not only the next file in sequence.
	for _, p := range prepared {
		if err := p.check(p.data, true); err != nil {
			msg.Err = err
			return msg
		}
	}
	commit := s.commitWorkspaceEdit
	if commit == nil {
		commit = fileio.CommitAtomicFile
	}
	for i := range prepared {
		p := &prepared[i]
		err := p.check(p.data, true)
		committed := false
		if err == nil {
			err = commit(p.next, p.path)
			committed = err == nil || fileio.WriteCommitted(err)
		}
		if err == nil {
			continue
		}
		msg.Err = fmt.Errorf("workspace edit commit %s: %w", p.path, err)
		last := i - 1
		if committed {
			last = i
		}
		for j := last; j >= 0; j-- {
			restore := &prepared[j]
			// Never overwrite an external actor's later content during recovery.
			rollbackErr := restore.check(restore.newData, false)
			if rollbackErr == nil {
				rollbackErr = fileio.CommitAtomicFile(restore.backup, restore.path)
			}
			if rollbackErr != nil {
				msg.Err = errors.Join(msg.Err, fmt.Errorf("rollback %s: %w (preimage retained at %s)", restore.path, rollbackErr, restore.backup))
				restore.backup = "" // Retain recovery evidence if rollback could not finish.
			}
		}
		msg.Detail = "workspace edit failed; rollback attempted"
		return msg
	}
	msg.Detail = fmt.Sprintf("applied %d edits", len(edits))
	if s.logger != nil {
		s.logger.Info("file-ops", "applied workspace edit", "count", len(edits))
	}
	return msg
}

const maxWorkspaceEditBytes int64 = 256 << 20

type workspaceEditFile struct {
	path          string
	data, newData []byte
	info          os.FileInfo
	parents       map[string]os.FileInfo
	next, backup  string
}

// Reject symlinks in every component, including parent directories. Capture
// directory identity so a replaced parent cannot redirect a later commit.
func readWorkspaceEditFile(path string) (workspaceEditFile, error) {
	p := workspaceEditFile{path: path, parents: make(map[string]os.FileInfo)}
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil {
			return p, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return p, fmt.Errorf("unsafe workspace edit parent: %s", parent)
		}
		p.parents[parent] = info
		if parent == filepath.Dir(parent) {
			break
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return p, err
	}
	if !info.Mode().IsRegular() {
		return p, fmt.Errorf("workspace edit requires regular non-symlink file: %s", path)
	}
	if info.Size() > maxWorkspaceEditBytes {
		return p, fmt.Errorf("workspace edit file exceeds size limit: %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return p, err
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil {
		return p, err
	}
	if !os.SameFile(info, opened) {
		return p, fmt.Errorf("workspace edit file changed: %s", path)
	}
	p.data, err = io.ReadAll(io.LimitReader(f, maxWorkspaceEditBytes+1))
	if err != nil {
		return p, err
	}
	if int64(len(p.data)) > maxWorkspaceEditBytes {
		return p, fmt.Errorf("workspace edit file exceeds size limit: %s", path)
	}
	p.info = info
	return p, nil
}

func (p workspaceEditFile) check(data []byte, identity bool) error {
	current, err := readWorkspaceEditFile(p.path)
	if err != nil {
		return err
	}
	for path, info := range p.parents {
		if !os.SameFile(info, current.parents[path]) {
			return fmt.Errorf("workspace edit parent changed: %s", path)
		}
	}
	if !bytes.Equal(data, current.data) || current.info.Mode() != p.info.Mode() || identity && !os.SameFile(p.info, current.info) {
		return fmt.Errorf("content mismatch or file identity/mode changed in %s", p.path)
	}
	return nil
}

func stageWorkspaceEditFile(path string, data []byte, mode os.FileMode) (name string, err error) {
	f, err := os.CreateTemp(filepath.Dir(path), ".crude-workspace-edit-*")
	if err != nil {
		return "", err
	}
	name = f.Name()
	defer func() {
		_ = f.Close()
		if err != nil {
			_ = os.Remove(name)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return name, err
	}
	if err = f.Chmod(fileio.MutationFileMode(mode)); err != nil {
		return name, err
	}
	if err = f.Sync(); err != nil {
		return name, err
	}
	err = f.Close()
	return name, err
}

// FileEdit represents a single text edit to apply to a file.
type FileEdit struct {
	// Path is the absolute path to the file.
	Path string
	// OldContent is the exact expected current content, including an empty file.
	OldContent string
	// NewContent is the content to write.
	NewContent string
}

// validatePath checks that a path is absolute and under the workspace root.
func (s *FileOpsService) validatePath(path string) FileOperationResultMsg {
	msg := FileOperationResultMsg{Path: path}
	if path == "" {
		msg.Err = fmt.Errorf("empty path")
		msg.Detail = "validation"
		return msg
	}
	if !filepath.IsAbs(path) {
		msg.Err = fmt.Errorf("path must be absolute: %s", path)
		msg.Detail = "validation"
		return msg
	}
	// Ensure the path is under the workspace root.
	rel, err := filepath.Rel(s.rootPath, path)
	if err != nil {
		msg.Err = fmt.Errorf("path not under workspace root: %s", path)
		msg.Detail = "validation"
		return msg
	}
	if rel == ".." || len(rel) > 2 && rel[:3] == ".."+string(filepath.Separator) {
		msg.Err = fmt.Errorf("path escapes workspace root: %s", path)
		msg.Detail = "validation"
		return msg
	}
	return msg
}
