package session

import (
	"strings"
	"testing"
)

func TestNewWithSessionDirEmptyIDUsesLoggerSessionID(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	sess, err := NewWithSessionDir("", testConfig(), home, dir)
	if err != nil {
		t.Fatalf("NewWithSessionDir: %v", err)
	}
	defer sess.Close()

	if sess.ID == "" {
		t.Fatal("Session.ID must not be empty")
	}
	if sess.ID != sess.SessionID() {
		t.Fatalf("Session.ID=%q SessionID()=%q want equal", sess.ID, sess.SessionID())
	}
	sender := newMockEventSender()
	orch, err := sess.NewOrchestrator(sender)
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}
	if strings.HasPrefix(orch.ID, "_orch_") {
		t.Fatalf("orchestrator id leaked empty session prefix: %q", orch.ID)
	}
	wantPrefix := sess.ID + "_orch_"
	if !strings.HasPrefix(orch.ID, wantPrefix) {
		t.Fatalf("orch.ID=%q want prefix %q", orch.ID, wantPrefix)
	}
}
