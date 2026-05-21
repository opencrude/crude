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
}

func NewFileOwnership() *FileOwnership {
	return &FileOwnership{
		claims: make(map[string]ClaimEntry),
	}
}

func (fo *FileOwnership) CheckOrClaim(agentID AgentID, agentName string, path string) error {
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

func (fo *FileOwnership) Query(path string) AgentID {
	fo.mu.Lock()
	defer fo.mu.Unlock()
	if entry, ok := fo.claims[path]; ok {
		return entry.AgentID
	}
	return ""
}

func (fo *FileOwnership) ClaimsByAgent(agentID AgentID) []string {
	fo.mu.Lock()
	defer fo.mu.Unlock()
	var paths []string
	for path, entry := range fo.claims {
		if entry.AgentID == agentID {
			paths = append(paths, path)
		}
	}
	return paths
}

func (fo *FileOwnership) AllClaims() map[string]ClaimEntry {
	fo.mu.Lock()
	defer fo.mu.Unlock()
	cp := make(map[string]ClaimEntry, len(fo.claims))
	for k, v := range fo.claims {
		cp[k] = v
	}
	return cp
}
