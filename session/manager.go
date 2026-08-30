package session

import (
	"fmt"
	"sync"
	"sync/atomic"
)

type SessionInfo struct {
	ID           string
	Session      *Session
	Orchestrator *Orchestrator
}

type SessionManager struct {
	mu       sync.Mutex
	sessions map[string]*SessionInfo
	nextID   atomic.Int64
}

func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*SessionInfo),
	}
}

func (sm *SessionManager) NewSession(cfg *Config, homeDir string) (*SessionInfo, error) {
	id := fmt.Sprintf("ses_%d", sm.nextID.Add(1))

	sess, err := New(id, cfg, homeDir)
	if err != nil {
		return nil, fmt.Errorf("new session: %w", err)
	}

	info := &SessionInfo{
		ID:      id,
		Session: sess,
	}

	sm.mu.Lock()
	sm.sessions[id] = info
	sm.mu.Unlock()

	return info, nil
}

func (sm *SessionManager) ResumeSession(dbPath string, cfg *Config, homeDir string) (*SessionInfo, error) {
	id := fmt.Sprintf("ses_%d", sm.nextID.Add(1))

	sess, err := ResumeSession(dbPath, cfg, homeDir)
	if err != nil {
		return nil, fmt.Errorf("resume session: %w", err)
	}
	sess.ID = id

	info := &SessionInfo{
		ID:      id,
		Session: sess,
	}

	sm.mu.Lock()
	sm.sessions[id] = info
	sm.mu.Unlock()

	return info, nil
}

func (sm *SessionManager) StartOrchestrator(id string, sender EventSender) (*Orchestrator, error) {
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

func (sm *SessionManager) Get(id string) (*SessionInfo, bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	info, ok := sm.sessions[id]
	return info, ok
}

func (sm *SessionManager) First() *SessionInfo {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	for _, info := range sm.sessions {
		return info
	}
	return nil
}

func (sm *SessionManager) Remove(id string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if info, ok := sm.sessions[id]; ok {
		info.Session.Close()
		delete(sm.sessions, id)
	}
}

func (sm *SessionManager) CloseAll() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	for id, info := range sm.sessions {
		info.Session.Close()
		delete(sm.sessions, id)
	}
}

func (sm *SessionManager) Count() int {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return len(sm.sessions)
}
