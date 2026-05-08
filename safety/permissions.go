package safety

import (
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
	Op    CRUDEOp
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

func (s *PermissionStore) IsAlwaysAllowed(op CRUDEOp, tool string, targetPath string) bool {
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

func (s *PermissionStore) GrantAlways(op CRUDEOp, tool string, targetPath string) {
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
		return true
	}

	if sr.Risk >= RiskDestructive {
		riskKey := PermissionKey{
			Scope: "risk_level",
			Tool:  "execute_command",
			Dir:   sr.Risk.String(),
		}
		return s.allowed[riskKey]
	}
	return false
}

// GrantCommandRisk allows a specific risk level for execute_command.
func (s *PermissionStore) GrantCommandRisk(risk CommandRisk) {
	s.mu.Lock()
	defer s.mu.Unlock()

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
