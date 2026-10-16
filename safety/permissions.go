package safety

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"crude/logger"
)

type PermissionScope string

const (
	ScopeDestructiveOp PermissionScope = "destructive_op"
	ScopeOutsideCwd    PermissionScope = "outside_cwd"
	ScopePathTree      PermissionScope = "path_tree"
)

type PermissionKey struct {
	Scope PermissionScope
	Tool  string
	Op    CRUDEOp
	Dir   string
}

type PermissionStore struct {
	mu      sync.Mutex
	allowed map[PermissionKey]bool
	workDir string
	logger  *logger.Logger
}

func NewPermissionStore() *PermissionStore {
	wd, _ := os.Getwd()
	return &PermissionStore{
		allowed: make(map[PermissionKey]bool),
		workDir: wd,
		logger:  nil, // set via SetLogger
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

	// 1. Check global destructive op permission (for commands)
	key := PermissionKey{Scope: ScopeDestructiveOp, Tool: tool, Op: op}
	if s.allowed[key] {
		s.logIf(func(lg *logger.Logger) {
			lg.Debug("PERM", "check", "op", op, "tool", tool, "target", targetPath, "result", "allowed_destructive_op")
		})
		return true
	}

	// 2. Check outside CWD permission
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

	// 3. Check path tree permission (for file operations: "allow for this directory and all children")
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

	// For commands: store global destructive op permission (existing behavior)
	if tool == "execute_command" {
		key := PermissionKey{Scope: ScopeDestructiveOp, Tool: tool, Op: op}
		s.allowed[key] = true

		if targetPath != "" && s.isOutsideWorkDir(targetPath) {
			absPath, err := filepath.Abs(targetPath)
			if err != nil {
				absPath = targetPath
			}
			dir := filepath.Dir(absPath)
			outsideKey := PermissionKey{Scope: ScopeOutsideCwd, Op: op, Tool: tool, Dir: dir}
			s.allowed[outsideKey] = true
		}
		return
	}

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

// IsCommandAllowed checks if a command safety result's risk level has been pre-approved.
func (s *PermissionStore) IsCommandAllowed(sr *CommandSafetyResult) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := PermissionKey{
		Scope: ScopeDestructiveOp,
		Tool:  "execute_command",
		Op:    OpExecute,
	}
	if s.allowed[key] {
		s.logIf(func(lg *logger.Logger) {
			lg.Debug("PERM", "command_check", "risk", sr.Risk.String(), "result", "allowed_global")
		})
		return true
	}

	if sr.Risk >= RiskDestructive {
		riskKey := PermissionKey{
			Scope: "risk_level",
			Tool:  "execute_command",
			Dir:   sr.Risk.String(),
		}
		allowed := s.allowed[riskKey]
		s.logIf(func(lg *logger.Logger) {
			lg.Debug("PERM", "command_check", "risk", sr.Risk.String(), "result", fmt.Sprintf("%v", allowed))
		})
		return allowed
	}
	s.logIf(func(lg *logger.Logger) {
		lg.Debug("PERM", "command_check", "risk", sr.Risk.String(), "result", "denied")
	})
	return false
}

// GrantCommandRisk allows a specific risk level for execute_command.
func (s *PermissionStore) GrantCommandRisk(risk CommandRisk) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.logIf(func(lg *logger.Logger) { lg.Info("PERM", "grant_risk", "risk", risk.String()) })

	riskKey := PermissionKey{
		Scope: "risk_level",
		Tool:  "execute_command",
		Dir:   risk.String(),
	}
	s.allowed[riskKey] = true
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
