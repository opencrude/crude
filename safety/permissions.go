package safety

import (
	"crude/dag"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type PermissionScope string

const (
	ScopeDestructiveOp PermissionScope = "destructive_op"
	ScopeOutsideCwd    PermissionScope = "outside_cwd"
)

type PermissionKey struct {
	Scope PermissionScope
	Tool  string
	Op    dag.CRUDEOp
	Dir   string
}

type PermissionStore struct {
	mu      sync.Mutex
	allowed map[PermissionKey]bool
	workDir string
}

func NewPermissionStore() *PermissionStore {
	wd, _ := os.Getwd()
	return &PermissionStore{
		allowed: make(map[PermissionKey]bool),
		workDir: wd,
	}
}

func (s *PermissionStore) IsAlwaysAllowed(op dag.CRUDEOp, tool string, targetPath string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := PermissionKey{Scope: ScopeDestructiveOp, Tool: tool, Op: op}
	if s.allowed[key] {
		return true
	}

	if targetPath != "" && s.isOutsideWorkDir(targetPath) {
		absPath, err := filepath.Abs(targetPath)
		if err != nil {
			absPath = targetPath
		}
		dir := filepath.Dir(absPath)
		outsideKey := PermissionKey{Scope: ScopeOutsideCwd, Op: op, Tool: tool, Dir: dir}
		if s.allowed[outsideKey] {
			return true
		}
	}

	return false
}

func (s *PermissionStore) GrantAlways(op dag.CRUDEOp, tool string, targetPath string) {
	s.mu.Lock()
	defer s.mu.Unlock()

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
}

func (s *PermissionStore) NeedsOutsideCwdConfirmation(targetPath string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return targetPath != "" && s.isOutsideWorkDir(targetPath)
}

func (s *PermissionStore) isOutsideWorkDir(targetPath string) bool {
	absPath, err := filepath.Abs(targetPath)
	if err != nil {
		return true
	}
	return !strings.HasPrefix(absPath, s.workDir+string(os.PathSeparator)) && absPath != s.workDir
}
