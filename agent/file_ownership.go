package agent

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type FileOwnership struct {
	mu     sync.Mutex
	claims map[string]ClaimEntry
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
	fo.mu.Lock()
	defer fo.mu.Unlock()

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
}

func (fo *FileOwnership) ReadClaimsByAgent(agentID AgentID) []string {
	fo.mu.Lock()
	defer fo.mu.Unlock()
	var paths []string
	for path, entry := range fo.claims {
		if entry.AgentID == agentID && !entry.IsWrite {
			paths = append(paths, path)
		}
	}
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
	return paths
}
