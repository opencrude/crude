package session

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// Info pairs one session with the orchestrator that serves it.
type Info struct {
	ID           string
	Session      *Session
	Orchestrator *Orchestrator
}

// Manager keeps the sessions of this process and hands out their ids.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Info
	nextID   atomic.Int64
}

// NewSessionManager returns an empty session manager.
func NewSessionManager() *Manager {
	return &Manager{
		sessions: make(map[string]*Info),
	}
}

// NewSession creates a session with the next id and registers its info.
func (sm *Manager) NewSession(cfg *Config, homeDir string) (*Info, error) {
	id := fmt.Sprintf("ses_%d", sm.nextID.Add(1))

	sess, err := New(id, cfg, homeDir)
	if err != nil {
		return nil, fmt.Errorf("new session: %w", err)
	}

	info := &Info{
		ID:      id,
		Session: sess,
	}

	sm.mu.Lock()
	sm.sessions[id] = info
	sm.mu.Unlock()

	return info, nil
}

// ResumeSession restores a session from its database file and registers it under a new id.
func (sm *Manager) ResumeSession(dbPath string, cfg *Config, homeDir string) (*Info, error) {
	id := fmt.Sprintf("ses_%d", sm.nextID.Add(1))

	sess, err := ResumeSession(dbPath, cfg, homeDir)
	if err != nil {
		return nil, fmt.Errorf("resume session: %w", err)
	}
	sess.ID = id

	info := &Info{
		ID:      id,
		Session: sess,
	}

	sm.mu.Lock()
	sm.sessions[id] = info
	sm.mu.Unlock()

	return info, nil
}

// StartOrchestrator builds the orchestrator for a registered session and returns it.
func (sm *Manager) StartOrchestrator(id string, sender EventSender) (*Orchestrator, error) {
	sm.mu.Lock()
	info, ok := sm.sessions[id]
	sm.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("session %s not found", id)
	}

	orch, err := info.Session.NewOrchestrator(sender)
	if err != nil {
		return nil, err
	}
	info.Orchestrator = orch

	return orch, nil
}

// Get returns the registered session info for an id.
func (sm *Manager) Get(id string) (*Info, bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	info, ok := sm.sessions[id]
	return info, ok
}

// First returns one registered session, or nil when the manager holds none.
func (sm *Manager) First() *Info {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	for _, info := range sm.sessions {
		return info
	}
	return nil
}

// Remove closes a session and forgets its info.
func (sm *Manager) Remove(id string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if info, ok := sm.sessions[id]; ok {
		info.Session.Close()
		delete(sm.sessions, id)
	}
}

// CloseAll closes every registered session and empties the manager.
func (sm *Manager) CloseAll() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	for id, info := range sm.sessions {
		info.Session.Close()
		delete(sm.sessions, id)
	}
}

// Count returns how many sessions the manager holds.
func (sm *Manager) Count() int {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return len(sm.sessions)
}
