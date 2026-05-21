package agent

import (
	"fmt"
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
	fo.mu.Lock()
	defer fo.mu.Unlock()

	for claimedPath, entry := range fo.claims {
		if strings.HasPrefix(path, claimedPath) || strings.HasPrefix(claimedPath, path) {
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
		for claimedPath, entry := range fo.claims {
			if strings.HasPrefix(path, claimedPath) || strings.HasPrefix(claimedPath, path) {
				if entry.AgentID != agentID {
					return fmt.Errorf("pre-claim conflict: file %q is owned by agent %q", path, entry.AgentName)
				}
			}
		}
	}

	now := time.Now()
	for _, path := range paths {
		fo.claims[path] = ClaimEntry{
			AgentID:   agentID,
			AgentName: agentName,
			ClaimedAt: now,
			IsWrite:   true,
		}
	}
	return nil
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


