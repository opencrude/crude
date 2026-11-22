package safety

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"crude/internal/logger"
)

type PermissionScope string

const (
	ScopeOutsideCwd PermissionScope = "outside_cwd"
	ScopePathTree   PermissionScope = "path_tree"
)

type PermissionKey struct {
	Scope PermissionScope
	Tool  string
	Op    CRUDEOp
	Dir   string
}

// PermissionStore holds the session's "Always allow" answers. File
// approvals cover a directory tree; command approvals cover exact commands
// (see CommandSafetyResult.ApprovalKeys). Nothing here outlives the session.
type PermissionStore struct {
	mu       sync.Mutex
	allowed  map[PermissionKey]bool
	commands map[string]bool
	workDir  string
	logger   *logger.Logger
}

func NewPermissionStore() *PermissionStore {
	wd, _ := os.Getwd()
	return &PermissionStore{
		allowed:  make(map[PermissionKey]bool),
		commands: make(map[string]bool),
		workDir:  wd,
		logger:   nil, // set via SetLogger
	}
}

func (s *PermissionStore) SetLogger(lg *logger.Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logger = lg
}

func (s *PermissionStore) IsAlwaysAllowed(op CRUDEOp, tool string, targetPath string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. Check outside CWD permission
	if targetPath != "" && s.isOutsideWorkDir(targetPath) {
		absPath, err := filepath.Abs(targetPath)
		if err != nil {
			absPath = targetPath
		}
		dir := filepath.Dir(absPath)
		outsideKey := PermissionKey{Scope: ScopeOutsideCwd, Op: op, Tool: tool, Dir: dir}
		if s.allowed[outsideKey] {
			s.logIf(func(lg *logger.Logger) {
				lg.Debug("PERM", "check", "op", op, "tool", tool, "target", targetPath, "result", "allowed_outside_cwd")
			})
			return true
		}
	}

	// 2. Check path tree permission (for file operations: "allow for this directory and all children")
	if targetPath != "" {
		absPath, err := filepath.Abs(targetPath)
		if err == nil {
			for pk := range s.allowed {
				if pk.Scope == ScopePathTree && pk.Tool == tool && pk.Op == op {
					// Check if the target path is within the allowed directory tree
					if strings.HasPrefix(absPath, pk.Dir+string(os.PathSeparator)) || absPath == pk.Dir {
						s.logIf(func(lg *logger.Logger) {
							lg.Debug("PERM", "check", "op", op, "tool", tool, "target", targetPath, "result", "allowed_path_tree")
						})
						return true
					}
				}
			}
		}
	}

	s.logIf(func(lg *logger.Logger) {
		lg.Debug("PERM", "check", "op", op, "tool", tool, "target", targetPath, "result", "denied")
	})
	return false
}

func (s *PermissionStore) GrantAlways(op CRUDEOp, tool string, targetPath string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.logIf(func(lg *logger.Logger) { lg.Info("PERM", "grant", "op", op, "tool", tool, "target", targetPath) })

	// For file tools: store path-tree permission scoped to the project working directory.
	if targetPath != "" {
		absPath, err := filepath.Abs(targetPath)
		if err != nil {
			absPath = targetPath
		}
		scopeDir := s.workDir
		if s.isOutsideWorkDir(absPath) {
			scopeDir = filepath.Dir(absPath)
		}
		treeKey := PermissionKey{Scope: ScopePathTree, Op: op, Tool: tool, Dir: scopeDir}
		s.allowed[treeKey] = true
	}
}

func (s *PermissionStore) NeedsOutsideCwdConfirmation(targetPath string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	outside := targetPath != "" && s.isOutsideWorkDir(targetPath)
	s.logIf(func(lg *logger.Logger) { lg.Debug("PERM", "outside_check", "target", targetPath, "outside", outside) })
	return outside
}

// CommandApproved reports whether the session approved every key of a
// command (CommandSafetyResult.ApprovalKeys). No keys approve nothing.
func (s *PermissionStore) CommandApproved(keys []string) bool {
	if s == nil || len(keys) == 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, key := range keys {
		if !s.commands[key] {
			return false
		}
	}
	return true
}

// ApproveCommand records an "Always allow" answer for each key of a command.
func (s *PermissionStore) ApproveCommand(keys []string) {
	if s == nil || len(keys) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, key := range keys {
		s.commands[key] = true
	}
	s.logIf(func(lg *logger.Logger) { lg.Info("PERM", "grant_command", "keys", len(keys)) })
}

func (s *PermissionStore) isOutsideWorkDir(targetPath string) bool {
	absPath, err := filepath.Abs(targetPath)
	if err != nil {
		return true
	}
	return !strings.HasPrefix(absPath, s.workDir+string(os.PathSeparator)) && absPath != s.workDir
}

func (s *PermissionStore) logIf(fn func(lg *logger.Logger)) {
	if s.logger != nil {
		fn(s.logger)
	}
}
