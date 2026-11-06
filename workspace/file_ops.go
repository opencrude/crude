package workspace

import (
	"bytes"
	"crypto/rand"
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
	FileOpCopy
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
	case FileOpCopy:
		return "copy"
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

// CopyPath copies a file or folder to newPath, which must not exist. The copy
// is built in a hidden staging entry beside newPath and published with one
// rename, so a failure or cancellation never leaves a partial copy behind.
// Folders use os.CopyFS, which copies symbolic links as links and never
// follows them.
func (s *FileOpsService) CopyPath(oldPath, newPath string) FileOperationResultMsg {
	msg := FileOperationResultMsg{Op: FileOpCopy, Path: newPath, OldPath: oldPath, NewPath: newPath}
	for _, path := range []string{oldPath, newPath} {
		if v := s.validatePath(path); v.Err != nil {
			msg.Err, msg.Detail = v.Err, v.Detail
			return msg
		}
	}
	if rel, err := filepath.Rel(oldPath, newPath); err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		msg.Err, msg.Detail = fmt.Errorf("cannot copy %s into itself", filepath.Base(oldPath)), "target inside source"
		return msg
	}
	unlock, ok := fileio.TryLockMutation()
	if !ok {
		msg.Err = fmt.Errorf("another file mutation is in progress")
		return msg
	}
	defer unlock()
	info, err := os.Lstat(oldPath)
	if err != nil {
		msg.Err, msg.Detail = fmt.Errorf("source does not exist: %s", filepath.Base(oldPath)), "source not found"
		return msg
	}
	msg.IsDir = info.IsDir()
	if _, err := os.Lstat(newPath); err == nil {
		msg.Err, msg.Detail = fmt.Errorf("target already exists: %s", filepath.Base(newPath)), "target exists"
		return msg
	}
	staging, err := os.MkdirTemp(filepath.Dir(newPath), "."+filepath.Base(newPath)+".copy-*")
	if err != nil {
		msg.Err = err
		return msg
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(staging)
		}
	}()
	entry := staging
	switch {
	case info.IsDir():
		err = os.CopyFS(staging, os.DirFS(oldPath))
		if err == nil {
			err = os.Chmod(staging, info.Mode().Perm())
		}
	case info.Mode().IsRegular():
		entry = filepath.Join(staging, filepath.Base(newPath))
		err = copyRegularFile(oldPath, entry, info.Mode().Perm())
	default:
		err = fmt.Errorf("cannot copy %s: only files and folders can be copied", filepath.Base(oldPath))
	}
	if err != nil {
		msg.Err = err
		return msg
	}
	if _, err := os.Lstat(newPath); err == nil {
		msg.Err, msg.Detail = fmt.Errorf("target already exists: %s", filepath.Base(newPath)), "target exists"
		return msg
	}
	if err := os.Rename(entry, newPath); err != nil {
		msg.Err = err
		return msg
	}
	if entry == staging {
		published = true
	}
	if s.logger != nil {
		s.logger.Info("file-ops", "copied", "from", oldPath, "to", newPath)
	}
	return msg
}

func copyRegularFile(source, target string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
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
	return s.applyWorkspaceEdit(edits, nil, nil, nil, nil).FileOperationResultMsg
}

// NoEffect is positive evidence that the writer never admitted a commit.
// An attempted commit or rollback failure must not be reclassified from an
// error string, current equal bytes, or the caller's cancelled context.
type workspaceEditResult struct {
	FileOperationResultMsg
	NoEffect bool
}

// Reviewed edits retain native preimages from their immutable preview. This
// shares the ordinary staging/commit/rollback writer, but does not rebind equal
// bytes to a replacement inode, parent or mode between review and approval.
func (s *FileOpsService) applyWorkspaceEdit(edits []FileEdit, reviewed []workspaceEditFile, beforeCommit func() error, lsp *LSPManager, watch *reviewedWatchTxn) workspaceEditResult {
	msg := workspaceEditResult{FileOperationResultMsg: FileOperationResultMsg{Op: FileOpApplyEdit, Path: "(workspace edit)"}, NoEffect: true}
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
		if seen[path] {
			msg.Err = fmt.Errorf("duplicate workspace edit path: %s", path)
			return msg
		}
		seen[path] = true
	}
	unlock, ok := fileio.TryLockMutation()
	if !ok {
		msg.Err = fmt.Errorf("another file mutation is in progress")
		return msg
	}
	defer unlock()
	for _, expected := range reviewed {
		if err := expected.check(expected.data, expected.info); err != nil {
			msg.Err = err
			return msg
		}
	}

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
		path, limit := filepath.Clean(edit.Path), maxWorkspaceEditBytes
		if len(reviewed) != 0 {
			limit = 0
			for _, expected := range reviewed {
				if expected.path == path {
					limit = expected.limit
					break
				}
			}
		}
		var p workspaceEditFile
		var err error
		if edit.Create {
			if edit.OldContent != "" {
				msg.Err = fmt.Errorf("create target has a nonempty preimage: %s", edit.Path)
				return msg
			}
			p, err = absentWorkspaceEditFile(path)
		} else {
			p, err = readWorkspaceEditFileBounded(path, limit)
		}
		if err != nil {
			msg.Err = err
			return msg
		}
		if !edit.Create && string(p.data) != edit.OldContent {
			msg.Err = fmt.Errorf("content mismatch in %s (file changed on disk?)", edit.Path)
			return msg
		}
		for _, expected := range reviewed {
			if expected.path != p.path {
				continue
			}
			if expected.create != edit.Create {
				msg.Err = fmt.Errorf("reviewed workspace edit kind changed: %s", edit.Path)
				return msg
			}
			if edit.Create {
				if err := expected.checkAbsent(); err != nil {
					msg.Err = err
					return msg
				}
			} else {
				if !os.SameFile(expected.info, p.info) || expected.info.Mode() != p.info.Mode() {
					msg.Err = fmt.Errorf("reviewed workspace edit file was replaced: %s", edit.Path)
					return msg
				}
				for parent, info := range expected.parents {
					if current := p.parents[parent]; current == nil || !os.SameFile(info, current) {
						msg.Err = fmt.Errorf("reviewed workspace edit parent was replaced: %s", parent)
						return msg
					}
				}
			}
		}
		for _, previous := range prepared {
			if p.info != nil && previous.info != nil && os.SameFile(previous.info, p.info) {
				msg.Err = fmt.Errorf("duplicate native workspace edit file: %s", edit.Path)
				return msg
			}
		}
		total += int64(len(p.data)) + int64(len(edit.NewContent))
		if total > maxWorkspaceEditBytes {
			msg.Err = fmt.Errorf("workspace edit exceeds %d MiB transaction limit", maxWorkspaceEditBytes>>20)
			return msg
		}
		p.newData = []byte(edit.NewContent)
		prepared = append(prepared, p)
		target := &prepared[len(prepared)-1]
		if edit.Create {
			target.next, target.postimage, err = stageWorkspaceEditFile(p.path, p.newData, 0o644, lsp)
		} else {
			target.backup, target.backupImage, err = stageWorkspaceEditFile(p.path, p.data, p.info.Mode(), lsp)
			if err == nil {
				target.next, target.postimage, err = stageWorkspaceEditFile(p.path, p.newData, p.info.Mode(), lsp)
			}
		}
		if err != nil {
			msg.Err = err
			return msg
		}
	}
	// Recheck all preimages after staging, not only the next file in sequence.
	for _, p := range prepared {
		if err := p.check(p.data, p.info); err != nil {
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
		err := p.check(p.data, p.info)
		if err == nil && beforeCommit != nil {
			err = beforeCommit()
		}
		if err == nil {
			if p.create {
				err = checkWorkspaceEditStage(p.next, p.newData, p.postimage)
			} else {
				err = p.checkStage(p.next, p.newData, p.postimage)
			}
		}
		if err == nil && !p.create {
			err = p.checkStage(p.backup, p.data, p.backupImage)
		}
		if err == nil && watch != nil {
			err = watch.beginCommit(p.path)
		}
		committed := false
		if err == nil {
			msg.NoEffect = false
			err = commit(p.next, p.path)
			committed = err == nil || fileio.WriteCommitted(err)
			if watch != nil {
				watch.endCommit(p.path, committed)
			}
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
			rollbackErr := restore.check(restore.newData, restore.postimage)
			if restore.create {
				rollbackErr = restore.checkCreated(restore.newData, restore.postimage)
			}
			if rollbackErr == nil {
				if restore.create {
					rollbackErr = os.Remove(restore.path)
					if errors.Is(rollbackErr, os.ErrNotExist) {
						rollbackErr = nil
					}
				} else {
					rollbackErr = restore.checkStage(restore.backup, restore.data, restore.backupImage)
					if rollbackErr == nil {
						rollbackErr = fileio.CommitAtomicFile(restore.backup, restore.path)
					}
				}
			}
			if rollbackErr != nil {
				msg.Err = errors.Join(msg.Err, fmt.Errorf("rollback %s: %w (preimage retained at %s)", restore.path, rollbackErr, restore.backup))
				restore.backup = "" // Retain recovery evidence if rollback could not finish.
			}
		}
		msg.Detail = "workspace edit failed; rollback attempted"
		return msg
	}
	// Check the complete postimage once before claiming success. If another
	// writer changed a committed target, do not overwrite that later work.
	for _, p := range prepared {
		check := p.check
		if p.create {
			check = p.checkCreated
		}
		if err := check(p.newData, p.postimage); err != nil {
			msg.Err = fmt.Errorf("workspace edit postimage changed: %w", err)
			msg.Detail = "workspace edit result needs inspection"
			return msg
		}
	}
	if watch != nil {
		if err := watch.check(); err != nil {
			msg.Err = err
			msg.Detail = "workspace edit result needs inspection"
			return msg
		}
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
	limit         int64
	create        bool
	data, newData []byte
	info          os.FileInfo
	postimage     os.FileInfo
	backupImage   os.FileInfo
	parents       map[string]os.FileInfo
	next, backup  string
}

// Reject symlinks in every component, including parent directories. Capture
// directory identity so a replaced parent cannot redirect a later commit.
func readWorkspaceEditFileBounded(path string, limit int64) (workspaceEditFile, error) {
	p := workspaceEditFile{path: path, limit: limit, parents: make(map[string]os.FileInfo)}
	if limit <= 0 || limit > maxWorkspaceEditBytes {
		return p, errors.New("invalid workspace edit read bound")
	}
	if err := captureWorkspaceEditParents(&p); err != nil {
		return p, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return p, err
	}
	if !info.Mode().IsRegular() {
		return p, fmt.Errorf("workspace edit requires regular non-symlink file: %s", path)
	}
	if info.Size() > limit {
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
	p.data, err = io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return p, err
	}
	if int64(len(p.data)) > limit {
		return p, fmt.Errorf("workspace edit file exceeds size limit: %s", path)
	}
	p.info = info
	return p, nil
}

func (p workspaceEditFile) check(data []byte, identity os.FileInfo) error {
	if p.create {
		return p.checkAbsent()
	}
	current, err := readWorkspaceEditFileBounded(p.path, p.limit)
	if err != nil {
		return err
	}
	for path, info := range p.parents {
		if !os.SameFile(info, current.parents[path]) {
			return fmt.Errorf("workspace edit parent changed: %s", path)
		}
	}
	if identity == nil || !bytes.Equal(data, current.data) || current.info.Mode() != identity.Mode() ||
		!current.info.ModTime().Equal(identity.ModTime()) || !os.SameFile(identity, current.info) {
		return fmt.Errorf("content mismatch or file identity/mode changed in %s", p.path)
	}
	return nil
}

func (p workspaceEditFile) checkAbsent() error {
	current, err := absentWorkspaceEditFile(p.path)
	if err != nil {
		return err
	}
	for path, info := range p.parents {
		if !os.SameFile(info, current.parents[path]) {
			return fmt.Errorf("workspace edit parent changed: %s", path)
		}
	}
	return nil
}

func (p workspaceEditFile) checkCreated(data []byte, identity os.FileInfo) error {
	current, err := readWorkspaceEditFileBounded(p.path, maxWorkspaceEditBytes)
	if err != nil {
		return err
	}
	for path, info := range p.parents {
		if !os.SameFile(info, current.parents[path]) {
			return fmt.Errorf("workspace edit parent changed: %s", path)
		}
	}
	if identity == nil || !bytes.Equal(data, current.data) || !os.SameFile(identity, current.info) {
		return fmt.Errorf("created workspace edit file changed: %s", p.path)
	}
	return nil
}

func captureWorkspaceEditParents(p *workspaceEditFile) error {
	for parent := filepath.Dir(p.path); ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe workspace edit parent: %s", parent)
		}
		p.parents[parent] = info
		if parent == filepath.Dir(parent) {
			return nil
		}
	}
}

// absentWorkspaceEditFile captures the parent identity and proves that the
// create target is still absent. It never follows a link or reads a file.
func absentWorkspaceEditFile(path string) (workspaceEditFile, error) {
	p := workspaceEditFile{path: path, create: true, parents: make(map[string]os.FileInfo)}
	if err := captureWorkspaceEditParents(&p); err != nil {
		return p, err
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return p, fmt.Errorf("workspace edit create target already exists: %s", path)
		}
		return p, err
	}
	return p, nil
}

func (p workspaceEditFile) checkStage(path string, data []byte, identity os.FileInfo) error {
	p.path = path
	return p.check(data, identity)
}

func checkWorkspaceEditStage(path string, data []byte, identity os.FileInfo) error {
	current, err := readWorkspaceEditFileBounded(path, maxWorkspaceEditBytes)
	if err != nil {
		return err
	}
	if identity == nil || !bytes.Equal(data, current.data) || current.info.Mode() != identity.Mode() ||
		!current.info.ModTime().Equal(identity.ModTime()) || !os.SameFile(identity, current.info) {
		return fmt.Errorf("content mismatch or file identity/mode changed in staged workspace edit %s", path)
	}
	return nil
}

func stageWorkspaceEditFile(path string, data []byte, mode os.FileMode, lsp *LSPManager) (name string, identity os.FileInfo, err error) {
	var f *os.File
	for range 10 {
		name = filepath.Join(filepath.Dir(path), ".crude-workspace-edit-"+rand.Text())
		var stage *editStage
		if lsp != nil {
			stage = lsp.editStages.reserve(name)
			if stage == nil {
				continue
			}
		}
		f, err = os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
		if stage != nil {
			lsp.editStages.resolve(name, stage, err == nil)
		}
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return "", nil, err
		}
	}
	if f == nil {
		return "", nil, fmt.Errorf("cannot create a unique workspace edit staging file")
	}
	defer func() {
		_ = f.Close()
		if err != nil {
			_ = os.Remove(name)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return name, nil, err
	}
	if err = f.Chmod(fileio.MutationFileMode(mode)); err != nil {
		return name, nil, err
	}
	if err = f.Sync(); err != nil {
		return name, nil, err
	}
	// Stat the open handle: Windows Lstat may defer file identity lookup until
	// after the staged path has been renamed.
	identity, err = f.Stat()
	if err != nil {
		return name, nil, err
	}
	err = f.Close()
	return name, identity, err
}

// FileEdit represents a single text edit to apply to a file.
type FileEdit struct {
	// Path is the absolute path to the file.
	Path string
	// OldContent is the exact expected current content, including an empty file.
	OldContent string
	// NewContent is the content to write.
	NewContent string
	// Create requires the target to be absent before this transaction.
	Create bool
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
