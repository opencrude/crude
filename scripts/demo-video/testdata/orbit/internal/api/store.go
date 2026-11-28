package api

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Launch is one scheduled launch.
type Launch struct {
	ID      string    `json:"id"`
	Vehicle string    `json:"vehicle"`
	Site    string    `json:"site"`
	NET     time.Time `json:"net"`
}

// Store keeps launches in memory.
type Store struct {
	mu       sync.RWMutex
	launches map[string]Launch
}

// NewStore returns a store with sample data.
func NewStore() *Store {
	s := &Store{launches: map[string]Launch{}}
	now := time.Now().UTC()
	for i, v := range []string{"Falcon 9", "Electron", "New Glenn", "Vega-C"} {
		id := v[:3] + "-" + string(rune('A'+i))
		s.launches[id] = Launch{ID: id, Vehicle: v, Site: "LC-39A", NET: now.Add(time.Duration(i+1) * 36 * time.Hour)}
	}
	return s
}

// Upcoming lists launches by time.
func (s *Store) Upcoming(ctx context.Context) []Launch {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Launch, 0, len(s.launches))
	for _, l := range s.launches {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NET.Before(out[j].NET) })
	return out
}
