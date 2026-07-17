package workspace

import (
	"fmt"
	"os"
	"path/filepath"
)

// ── File operation event messages ──
//
// These messages are published through the EventBus when a file operation
// completes (success or failure). The TUI handles them to update the
// file tree, editor tabs, and status bar.

// FileOperationResultMsg carries the result of a file mutation.
// Published through EventBus after every CreateFile, CreateFolder,
// RenamePath, DeletePath, or ApplyWorkspaceEdit call.
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
	FileOpCreate     FileOperation = iota
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
// goes through this service. The service publishes typed results
// through the EventBus so the TUI can update the file tree,
// editor tabs, and status bar without owning filesystem handles.
type FileOpsService struct {
	rootPath string
	eventBus *EventBus
	logger   Logger
}

// NewFileOpsService creates a FileOpsService rooted at the given project path.
func NewFileOpsService(rootPath string, eventBus *EventBus, logger Logger) *FileOpsService {
	return &FileOpsService{
		rootPath: rootPath,
		eventBus: eventBus,
		logger:   logger,
	}
}

// CreateFile creates an empty file at the given path within the workspace.
// The path must be absolute and under the workspace root.
// Results are published through EventBus as FileOperationResultMsg.
func (s *FileOpsService) CreateFile(path string) FileOperationResultMsg {
	msg := s.validatePath(path)
	if msg.Err != nil {
		s.publish(msg)
		return msg
	}

	// Check if file already exists.
	if _, err := os.Stat(path); err == nil {
		msg.Err = fmt.Errorf("already exists: %s", filepath.Base(path))
		msg.Detail = "already exists"
		s.publish(msg)
		return msg
	}

	// Create the file.
	f, err := os.Create(path)
	if err != nil {
		msg.Err = err
		s.publish(msg)
		return msg
	}
	_ = f.Close()

	msg.Op = FileOpCreate
	s.publish(msg)
	if s.logger != nil {
		s.logger.Info("file-ops", "created file", "path", path)
	}
	return msg
}

// CreateFolder creates a directory (and parents) at the given path.
// The path must be absolute and under the workspace root.
// Results are published through EventBus as FileOperationResultMsg.
func (s *FileOpsService) CreateFolder(path string) FileOperationResultMsg {
	msg := s.validatePath(path)
	if msg.Err != nil {
		s.publish(msg)
		return msg
	}

	// Check if folder already exists.
	if _, err := os.Stat(path); err == nil {
		msg.Err = fmt.Errorf("already exists: %s", filepath.Base(path))
		msg.Detail = "already exists"
		s.publish(msg)
		return msg
	}

	// Create the directory.
	if err := os.MkdirAll(path, 0o755); err != nil {
		msg.Err = err
		s.publish(msg)
		return msg
	}

	msg.Op = FileOpCreateFolder
	msg.IsDir = true
	s.publish(msg)
	if s.logger != nil {
		s.logger.Info("file-ops", "created folder", "path", path)
	}
	return msg
}

// RenamePath renames a file or folder from oldPath to newPath.
// Both paths must be absolute and under the workspace root.
// Results are published through EventBus as FileOperationResultMsg.
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
		s.publish(msg)
		return msg
	}
	if v := s.validatePath(newPath); v.Err != nil {
		msg.Err = v.Err
		msg.Detail = "invalid target path"
		s.publish(msg)
		return msg
	}

	// Check source exists.
	srcInfo, err := os.Stat(oldPath)
	if err != nil {
		msg.Err = fmt.Errorf("source does not exist: %s", filepath.Base(oldPath))
		msg.Detail = "source not found"
		s.publish(msg)
		return msg
	}
	msg.IsDir = srcInfo.IsDir()

	// Check target does not exist.
	if _, err := os.Stat(newPath); err == nil {
		msg.Err = fmt.Errorf("target already exists: %s", filepath.Base(newPath))
		msg.Detail = "target exists"
		s.publish(msg)
		return msg
	}

	// Perform the rename.
	if err := os.Rename(oldPath, newPath); err != nil {
		msg.Err = err
		s.publish(msg)
		return msg
	}

	msg.Op = FileOpRename
	s.publish(msg)
	if s.logger != nil {
		s.logger.Info("file-ops", "renamed", "from", oldPath, "to", newPath)
	}
	return msg
}

// DeletePath removes a file or directory at the given path.
// Directories are removed recursively.
// The path must be absolute and under the workspace root.
// Results are published through EventBus as FileOperationResultMsg.
func (s *FileOpsService) DeletePath(path string) FileOperationResultMsg {
	msg := s.validatePath(path)
	if msg.Err != nil {
		s.publish(msg)
		return msg
	}

	// Check if path exists.
	info, err := os.Stat(path)
	if err != nil {
		msg.Err = fmt.Errorf("does not exist: %s", filepath.Base(path))
		msg.Detail = "not found"
		s.publish(msg)
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
		s.publish(msg)
		return msg
	}

	msg.Op = FileOpDelete
	s.publish(msg)
	if s.logger != nil {
		s.logger.Info("file-ops", "deleted", "path", path, "isDir", info.IsDir())
	}
	return msg
}

// ApplyWorkspaceEdit applies a set of text edits to files on disk.
// This is used for LSP rename/refactor operations that modify multiple files.
// Each edit is a (filePath, oldContent, newContent) triple.
// Results are published through EventBus as FileOperationResultMsg.
func (s *FileOpsService) ApplyWorkspaceEdit(edits []FileEdit) FileOperationResultMsg {
	msg := FileOperationResultMsg{
		Path: "(workspace edit)",
	}

	if len(edits) == 0 {
		msg.Err = fmt.Errorf("no edits provided")
		s.publish(msg)
		return msg
	}

	applied := 0
	var firstErr error
	for _, edit := range edits {
		// Validate path.
		if v := s.validatePath(edit.Path); v.Err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("invalid path %s: %w", edit.Path, v.Err)
			}
			continue
		}

		// Read current content.
		data, err := os.ReadFile(edit.Path)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("read %s: %w", filepath.Base(edit.Path), err)
			}
			continue
		}

		// Verify old content matches if provided.
		if edit.OldContent != "" && string(data) != edit.OldContent {
			if firstErr == nil {
				firstErr = fmt.Errorf("content mismatch in %s (file changed on disk?)", filepath.Base(edit.Path))
			}
			continue
		}

		// Write new content.
		if err := os.WriteFile(edit.Path, []byte(edit.NewContent), 0o644); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("write %s: %w", filepath.Base(edit.Path), err)
			}
			continue
		}
		applied++
	}

	if firstErr != nil {
		msg.Err = firstErr
		msg.Detail = fmt.Sprintf("applied %d/%d edits", applied, len(edits))
	} else {
		msg.Op = FileOpApplyEdit
		msg.Detail = fmt.Sprintf("applied %d edits", applied)
	}

	s.publish(msg)
	if s.logger != nil {
		if firstErr != nil {
			s.logger.Warn("file-ops", "workspace edit partial failure", "applied", applied, "total", len(edits), "error", firstErr.Error())
		} else {
			s.logger.Info("file-ops", "applied workspace edit", "count", len(edits))
		}
	}
	return msg
}

// FileEdit represents a single text edit to apply to a file.
type FileEdit struct {
	// Path is the absolute path to the file.
	Path string
	// OldContent is the expected current content. If empty, the edit is applied
	// without content verification.
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

// publish sends the result through the EventBus.
func (s *FileOpsService) publish(msg FileOperationResultMsg) {
	if s.eventBus != nil {
		s.eventBus.Send(msg)
	}
}
