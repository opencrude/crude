package session

import (
	"testing"
	"time"
)

// Preparing a compaction never blocks its caller (the core actor or the
// Bubble Tea update loop), even while a turn owns the gate; only Run waits.
func TestPrepareCompactionNeverBlocksBehindTheTurnGate(t *testing.T) {
	session := &Session{sessionID: "gate", activeContext: compactableContext()}
	session.turnGate.RLock()
	defer session.turnGate.RUnlock()

	prepared := make(chan PreparedCompaction, 1)
	go func() { p, _ := session.PrepareCompaction("gate"); prepared <- p }()
	select {
	case p := <-prepared:
		if p.Run == nil {
			t.Fatal("a held gate refused /compact instead of admitting it")
		}
		session.releaseUnstartedCompaction(p.OperationID)
	case <-time.After(time.Second):
		t.Fatal("PrepareCompaction blocked behind the turn gate")
	}
}
