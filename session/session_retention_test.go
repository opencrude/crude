package session

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"crude/collaboration"
	"crude/sessiondb"
)

func retentionSession(t *testing.T, home, id string, store *collaboration.Store) (*Session, string) {
	t.Helper()
	sessions := filepath.Join(home, "sessions")
	s, err := NewWithSessionDir(id, testConfig(), home, sessions)
	if err != nil {
		t.Fatal(err)
	}
	s.SetWorkDir(t.TempDir())
	s.SetCollaboration(store, "retention-owner")
	o, err := s.NewOrchestrator(newMockEventSender())
	if err != nil {
		t.Fatal(err)
	}
	s.CloseOrchestrator(o.ID)
	return s, o.ID
}

func collaborationAgents(t *testing.T, db *sql.DB, sessionID string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM collaboration_agents WHERE session_id = ?`, sessionID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// An empty session prunes its storage only when nothing durable refers to it:
// a peer's delivery addressed to it lives in the workspace database and must
// keep the session resumable instead of stranding the message.
func TestEmptySessionRetentionHasOneOwner(t *testing.T) {
	home := t.TempDir()
	workspaceDB := sessiondb.OpenAndMigrateAt(t.TempDir())
	t.Cleanup(func() { _ = workspaceDB.Close() })
	store, err := collaboration.Attach(workspaceDB, t.TempDir(), filepath.Join(home, "locks"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	empty, _ := retentionSession(t, home, "retention-empty", store)
	emptyID, emptyDB := empty.SessionID(), empty.dbPath
	empty.Close()
	if !empty.Pruned() {
		t.Fatal("an unreferenced empty session was kept")
	}
	if _, err := os.Stat(emptyDB); !os.IsNotExist(err) {
		t.Fatalf("pruned session database still exists: %v", err)
	}
	if n := collaborationAgents(t, workspaceDB, emptyID); n != 0 {
		t.Fatalf("pruned session left %d directory agents", n)
	}

	addressed, agentID := retentionSession(t, home, "retention-addressed", store)
	addressedID, addressedDB := addressed.SessionID(), addressed.dbPath
	if _, err := store.RegisterAgent(context.Background(), collaboration.AgentRegistration{ID: "peer", Name: "Peer", SessionID: "peer-session"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish(context.Background(), collaboration.PublishRequest{SenderID: "peer", RecipientIDs: []string{agentID}, Origin: collaboration.OriginPeer, Kind: collaboration.EventPeerNote, Body: "please review", IdempotencyKey: "retention"}); err != nil {
		t.Fatal(err)
	}
	addressed.Close()
	if addressed.Pruned() {
		t.Fatal("a session holding a peer delivery was pruned")
	}
	if _, err := os.Stat(addressedDB); err != nil {
		t.Fatalf("addressed session database was removed: %v", err)
	}
	if n := collaborationAgents(t, workspaceDB, addressedID); n != 1 {
		t.Fatalf("addressed session directory agents = %d; want 1", n)
	}
}
