package logger

import (
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	defaultMaxAge   = 30 * 24 * time.Hour // 30 days
	defaultMaxCount = 100
)

// CleanOldSessions removes expired session directories.
// Sessions older than maxAge are deleted. If more than maxCount remain,
// the oldest are removed first. Zero values use defaults.
func CleanOldSessions(sessionsDir string, maxAge time.Duration, maxCount int) (int, error) {
	if maxAge <= 0 {
		maxAge = defaultMaxAge
	}
	if maxCount <= 0 {
		maxCount = defaultMaxCount
	}

	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	type info struct {
		name    string
		modTime time.Time
		isDB    bool
	}
	var sessions []info
	for _, entry := range entries {
		fi, err := entry.Info()
		if err != nil {
			continue
		}
		isDB := !entry.IsDir() && filepath.Ext(entry.Name()) == ".db"
		if !entry.IsDir() && !isDB {
			continue
		}
		sessions = append(sessions, info{name: entry.Name(), modTime: fi.ModTime(), isDB: isDB})
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].modTime.Before(sessions[j].modTime)
	})

	cutoff := time.Now().Add(-maxAge)
	removed := 0

	var kept []info
	for _, s := range sessions {
		if s.modTime.Before(cutoff) {
			if os.RemoveAll(filepath.Join(sessionsDir, s.name)) == nil {
				removed++
			}
		} else {
			kept = append(kept, s)
		}
	}

	if len(kept) > maxCount {
		excess := len(kept) - maxCount
		for i := 0; i < excess; i++ {
			if os.RemoveAll(filepath.Join(sessionsDir, kept[i].name)) == nil {
				removed++
			}
		}
	}

	return removed, nil
}
