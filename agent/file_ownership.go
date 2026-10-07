package agent

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type FileOwnership struct {
	mu     sync.Mutex
	claims map[string]ClaimEntry
	reads  map[AgentID]map[string]struct{}
}

type ClaimEntry struct {
	AgentID   AgentID
	AgentName string
	ClaimedAt time.Time
	IsWrite   bool
}

func NewFileOwnership() *FileOwnership {
	return &FileOwnership{
		claims: make(map[string]ClaimEntry),
		reads:  make(map[AgentID]map[string]struct{}),
	}
}

func (fo *FileOwnership) CheckOrClaim(agentID AgentID, agentName string, path string, isWrite bool) error {
	path = normalizeClaimPath(path)
	fo.mu.Lock()
	defer fo.mu.Unlock()

	for claimedPath, entry := range fo.claims {
		if claimPathsOverlap(path, claimedPath) {
			if entry.AgentID != agentID {
				return fmt.Errorf("file %q is owned by agent %q (claimed at %s)", path, entry.AgentName, entry.ClaimedAt.Format("15:04:05"))
			}
		}
	}

	fo.claims[path] = ClaimEntry{
		AgentID:   agentID,
		AgentName: agentName,
		ClaimedAt: time.Now(),
		IsWrite:   isWrite,
	}
	return nil
}

func (fo *FileOwnership) PreClaim(agentID AgentID, agentName string, paths []string) error {
	return fo.ReplaceClaims(agentID, agentName, paths)
}

// ReplaceClaims atomically publishes one agent's complete current write scope.
// Validation happens before the previous projection is removed, so a failed
// repair cannot create a release gap or retain a partially replaced scope.
func (fo *FileOwnership) ReplaceClaims(agentID AgentID, agentName string, paths []string) error {
	fo.mu.Lock()
	defer fo.mu.Unlock()
	return fo.replaceClaimsLocked(agentID, agentName, paths)
}

// CommitReplaceClaims holds the ownership projection gate while durable claim
// replacement commits, then publishes the exact committed scope locally. This
// prevents file effects from observing a release gap or racing a durable repair.
func (fo *FileOwnership) CommitReplaceClaims(agentID AgentID, agentName string, paths []string, commit func() error) error {
	fo.mu.Lock()
	defer fo.mu.Unlock()
	if err := fo.validateReplacementLocked(agentID, paths); err != nil {
		return err
	}
	if commit == nil {
		return fmt.Errorf("claim replacement commit is unavailable")
	}
	if err := commit(); err != nil {
		return err
	}
	return fo.replaceClaimsLocked(agentID, agentName, paths)
}

func (fo *FileOwnership) replaceClaimsLocked(agentID AgentID, agentName string, paths []string) error {
	if err := fo.validateReplacementLocked(agentID, paths); err != nil {
		return err
	}

	for path, entry := range fo.claims {
		if entry.AgentID == agentID {
			delete(fo.claims, path)
		}
	}
	now := time.Now()
	for _, path := range paths {
		path = normalizeClaimPath(path)
		fo.claims[path] = ClaimEntry{
			AgentID:   agentID,
			AgentName: agentName,
			ClaimedAt: now,
			IsWrite:   true,
		}
	}
	return nil
}

func (fo *FileOwnership) validateReplacementLocked(agentID AgentID, paths []string) error {
	for _, path := range paths {
		path = normalizeClaimPath(path)
		for claimedPath, entry := range fo.claims {
			if claimPathsOverlap(path, claimedPath) {
				if entry.AgentID != agentID {
					return fmt.Errorf("pre-claim conflict: file %q is owned by agent %q", path, entry.AgentName)
				}
			}
		}
	}
	return nil
}

func normalizeClaimPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	return filepath.Clean(path)
}

func claimPathsOverlap(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	return left == right || pathWithin(left, right) || pathWithin(right, left)
}

func pathWithin(path, parent string) bool {
	relative, err := filepath.Rel(parent, path)
	if err != nil || relative == "." || relative == ".." {
		return relative == "."
	}
	return !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (fo *FileOwnership) Release(agentID AgentID) {
	fo.mu.Lock()
	defer fo.mu.Unlock()
	for path, entry := range fo.claims {
		if entry.AgentID == agentID {
			delete(fo.claims, path)
		}
	}
	delete(fo.reads, agentID)
}

func (fo *FileOwnership) RecordRead(agentID AgentID, path string) {
	path = normalizeClaimPath(path)
	if path == "" {
		return
	}
	fo.mu.Lock()
	defer fo.mu.Unlock()
	if fo.reads[agentID] == nil {
		fo.reads[agentID] = make(map[string]struct{})
	}
	fo.reads[agentID][path] = struct{}{}
}

func (fo *FileOwnership) ReadClaimsByAgent(agentID AgentID) []string {
	fo.mu.Lock()
	defer fo.mu.Unlock()
	paths := make([]string, 0, len(fo.reads[agentID]))
	for path := range fo.reads[agentID] {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func (fo *FileOwnership) WriteClaimsByAgent(agentID AgentID) []string {
	fo.mu.Lock()
	defer fo.mu.Unlock()
	var paths []string
	for path, entry := range fo.claims {
		if entry.AgentID == agentID && entry.IsWrite {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths
}
