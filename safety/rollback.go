package safety

import (
	"fmt"
	"log"

	"crude/backend"
	"crude/dag"
)

// RollbackHandler captures pre-mutation state and provides undo operations.
type RollbackHandler struct {
	backend backend.Executor
	logger  *log.Logger
}

// NewRollbackHandler creates a rollback handler backed by the given executor.
func NewRollbackHandler(b backend.Executor) *RollbackHandler {
	return &RollbackHandler{
		backend: b,
		logger:  log.Default(),
	}
}

// CapturePreState reads the current state needed to undo a mutation.
// Stores it in node.RollbackData.
func (rh *RollbackHandler) CapturePreState(node *dag.Node) error {
	switch node.Tool {
	case "write_file", "edit_file":
		path, _ := node.Parameters["path"].(string)
		if path == "" {
			return nil
		}
		content, err := rh.backend.ReadFile(path)
		if err != nil {
			// File doesn't exist yet — that's fine for write_file (create).
			node.RollbackData = map[string]any{"path": path, "existed": false}
			return nil
		}
		node.RollbackData = map[string]any{
			"path":    path,
			"content": content,
			"existed": true,
		}

	case "delete_file":
		path, _ := node.Parameters["path"].(string)
		if path == "" {
			return nil
		}
		content, err := rh.backend.ReadFile(path)
		if err != nil {
			node.RollbackData = map[string]any{"path": path, "existed": false}
			return nil
		}
		node.RollbackData = map[string]any{
			"path":    path,
			"content": content,
			"existed": true,
		}

	case "move_file":
		src, _ := node.Parameters["source"].(string)
		dst, _ := node.Parameters["destination"].(string)
		if src == "" || dst == "" {
			return nil
		}
		node.RollbackData = map[string]any{
			"source":      src,
			"destination": dst,
		}

	case "copy_file":
		dst, _ := node.Parameters["destination"].(string)
		if dst == "" {
			return nil
		}
		node.RollbackData = map[string]any{
			"destination": dst,
		}
	}
	return nil
}

// Rollback undoes a completed operation using the captured pre-state.
func (rh *RollbackHandler) Rollback(node *dag.Node) error {
	if node.RollbackData == nil {
		return nil
	}

	switch node.Tool {
	case "write_file":
		return rh.rollbackWriteFile(node)
	case "edit_file":
		return rh.rollbackEditFile(node)
	case "delete_file":
		return rh.rollbackDeleteFile(node)
	case "move_file":
		return rh.rollbackMoveFile(node)
	case "copy_file":
		return rh.rollbackCopyFile(node)
	default:
		rh.logger.Printf("[rollback] no rollback handler for tool %s", node.Tool)
		return nil
	}
}

func (rh *RollbackHandler) rollbackWriteFile(node *dag.Node) error {
	path, _ := node.RollbackData["path"].(string)
	existed, _ := node.RollbackData["existed"].(bool)

	if !existed {
		// File was created — delete it.
		if err := rh.backend.DeleteFile(path); err != nil {
			return fmt.Errorf("rollback write_file delete: %w", err)
		}
		rh.logger.Printf("[rollback] deleted created file %s", path)
		return nil
	}

	// File was overwritten — restore original content.
	content, _ := node.RollbackData["content"].(string)
	if err := rh.backend.WriteFile(path, content); err != nil {
		return fmt.Errorf("rollback write_file restore: %w", err)
	}
	rh.logger.Printf("[rollback] restored %s", path)
	return nil
}

func (rh *RollbackHandler) rollbackEditFile(node *dag.Node) error {
	path, _ := node.RollbackData["path"].(string)
	content, _ := node.RollbackData["content"].(string)
	if path == "" || content == "" {
		return nil
	}
	if err := rh.backend.WriteFile(path, content); err != nil {
		return fmt.Errorf("rollback edit_file: %w", err)
	}
	rh.logger.Printf("[rollback] restored %s from edit", path)
	return nil
}

func (rh *RollbackHandler) rollbackDeleteFile(node *dag.Node) error {
	path, _ := node.RollbackData["path"].(string)
	content, _ := node.RollbackData["content"].(string)
	existed, _ := node.RollbackData["existed"].(bool)

	if !existed || path == "" || content == "" {
		return nil
	}
	if err := rh.backend.WriteFile(path, content); err != nil {
		return fmt.Errorf("rollback delete_file: %w", err)
	}
	rh.logger.Printf("[rollback] restored deleted file %s", path)
	return nil
}

func (rh *RollbackHandler) rollbackMoveFile(node *dag.Node) error {
	src, _ := node.RollbackData["source"].(string)
	dst, _ := node.RollbackData["destination"].(string)
	if src == "" || dst == "" {
		return nil
	}
	if err := rh.backend.MoveFile(dst, src); err != nil {
		return fmt.Errorf("rollback move_file: %w", err)
	}
	rh.logger.Printf("[rollback] moved %s back to %s", dst, src)
	return nil
}

func (rh *RollbackHandler) rollbackCopyFile(node *dag.Node) error {
	dst, _ := node.RollbackData["destination"].(string)
	if dst == "" {
		return nil
	}
	if err := rh.backend.DeleteFile(dst); err != nil {
		return fmt.Errorf("rollback copy_file: %w", err)
	}
	rh.logger.Printf("[rollback] deleted copied file %s", dst)
	return nil
}
