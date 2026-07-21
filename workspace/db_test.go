package workspace

import (
	"database/sql"
	"path/filepath"
	"testing"

	"crude/provider"
	"crude/sessiondb"
)

// openTestWorkspaceDB creates a workspace DB in a temp dir and returns it.
// The caller does not need to close it — t.Cleanup handles that.
func openTestWorkspaceDB(t *testing.T) *WorkspaceDB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "workspace.db")
	wdb, err := openWorkspaceDBAt(dbPath)
	if err != nil {
		t.Fatalf("openWorkspaceDBAt: %v", err)
	}
	t.Cleanup(func() { _ = wdb.Close() })
	return wdb
}

// openTestSessionDB creates a session DB in a temp dir, seeds a single
// session row, and returns the open handle plus the session ID.
func openTestSessionDB(t *testing.T, sessionID string) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, sessionID+".db")
	db, err := sessiondb.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate session db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sessiondb.Create(db, sessiondb.SessionMeta{
		ID:          sessionID,
		ProjectPath: "/tmp/project",
		ProjectName: "project",
		Model:       "test-model",
		Provider:    "test",
		CreatedAt:   1000,
	}); err != nil {
		t.Fatalf("sessiondb.Create: %v", err)
	}
	return db
}

// registryCost reads total_cost back from workspace_sessions for assertions.
func registryCost(t *testing.T, wdb *WorkspaceDB, sessionID string) float64 {
	t.Helper()
	var got float64
	err := wdb.DB.QueryRow(
		`SELECT total_cost FROM workspace_sessions WHERE session_id = ?`, sessionID,
	).Scan(&got)
	if err != nil {
		t.Fatalf("registry cost read: %v", err)
	}
	return got
}

// registryRow returns the full workspace_sessions row for assertions.
func registryRow(t *testing.T, wdb *WorkspaceDB, sessionID string) (
	turnCount, messageCount int, firstPrompt, lastPrompt string, totalCost float64,
) {
	t.Helper()
	var fp, lp sql.NullString
	err := wdb.DB.QueryRow(`
		SELECT turn_count, message_count, first_prompt, last_prompt, total_cost
		FROM workspace_sessions WHERE session_id = ?
	`, sessionID).Scan(&turnCount, &messageCount, &fp, &lp, &totalCost)
	if err != nil {
		t.Fatalf("registry row read: %v", err)
	}
	if fp.Valid {
		firstPrompt = fp.String
	}
	if lp.Valid {
		lastPrompt = lp.String
	}
	return
}

func TestUpdateSessionCost(t *testing.T) {
	wdb := openTestWorkspaceDB(t)
	const sid = "sess-cost-1"
	if err := wdb.RegisterSession(sid, "/tmp/project", "project", "m", "p"); err != nil {
		t.Fatalf("RegisterSession: %v", err)
	}

	// Initial cost is 0.0.
	if got := registryCost(t, wdb, sid); got != 0.0 {
		t.Fatalf("initial cost: expected 0.0, got %v", got)
	}

	// Push a live cost update.
	if err := wdb.UpdateSessionCost(sid, 0.42); err != nil {
		t.Fatalf("UpdateSessionCost: %v", err)
	}
	if got := registryCost(t, wdb, sid); got != 0.42 {
		t.Fatalf("after first push: expected 0.42, got %v", got)
	}

	// Push again — cumulative cost replaces (not adds). The workspace tracks
	// the running total in memory and pushes the snapshot.
	if err := wdb.UpdateSessionCost(sid, 0.71); err != nil {
		t.Fatalf("UpdateSessionCost #2: %v", err)
	}
	if got := registryCost(t, wdb, sid); got != 0.71 {
		t.Fatalf("after second push: expected 0.71, got %v", got)
	}
}

func TestUpdateSessionCostUnknownSessionNoOp(t *testing.T) {
	wdb := openTestWorkspaceDB(t)
	// Updating a session that was never registered should not error —
	// the UPDATE just matches zero rows.
	if err := wdb.UpdateSessionCost("never-registered", 1.0); err != nil {
		t.Fatalf("UpdateSessionCost on unknown session: %v", err)
	}
}

func TestSyncSessionMetaFromDBPullsAllFields(t *testing.T) {
	wdb := openTestWorkspaceDB(t)
	const sid = "sess-sync-1"
	if err := wdb.RegisterSession(sid, "/tmp/project", "project", "m", "p"); err != nil {
		t.Fatalf("RegisterSession: %v", err)
	}

	// Build a session DB with realistic data: prompts, turn/message counts,
	// and two cost snapshots (the second is the latest cumulative).
	sdb := openTestSessionDB(t, sid)
	if err := sessiondb.UpdateFirstPrompt(sdb, sid, "first prompt text"); err != nil {
		t.Fatalf("UpdateFirstPrompt: %v", err)
	}
	if err := sessiondb.UpdateLastPrompt(sdb, sid, "last prompt text"); err != nil {
		t.Fatalf("UpdateLastPrompt: %v", err)
	}
	// ReplaceMessages updates message_count and turn_count on the sessions row.
	if _, err := sessiondb.ReplaceMessages(sdb, sid, []provider.ChatMessage{
		{Role: "user", Content: provider.TextContent("hello")},
		{Role: "user", Content: provider.TextContent("hi again")},
	}, []int{1, 2}); err != nil {
		t.Fatalf("ReplaceMessages: %v", err)
	}
	if err := sessiondb.InsertCostSnapshot(sdb, 100, 50, 10, 0.05, nil); err != nil {
		t.Fatalf("InsertCostSnapshot #1: %v", err)
	}
	if err := sessiondb.InsertCostSnapshot(sdb, 200, 100, 20, 0.123, nil); err != nil {
		t.Fatalf("InsertCostSnapshot #2: %v", err)
	}

	if err := wdb.syncSessionMetaFromDB(sdb, sid); err != nil {
		t.Fatalf("syncSessionMetaFromDB: %v", err)
	}

	turnCount, messageCount, firstPrompt, lastPrompt, totalCost :=
		registryRow(t, wdb, sid)
	if turnCount != 2 {
		t.Errorf("turn_count: expected 2, got %d", turnCount)
	}
	if messageCount != 2 {
		t.Errorf("message_count: expected 2, got %d", messageCount)
	}
	if firstPrompt != "first prompt text" {
		t.Errorf("first_prompt: expected %q, got %q", "first prompt text", firstPrompt)
	}
	if lastPrompt != "last prompt text" {
		t.Errorf("last_prompt: expected %q, got %q", "last prompt text", lastPrompt)
	}
	if totalCost != 0.123 {
		t.Errorf("total_cost: expected 0.123 (latest snapshot), got %v", totalCost)
	}
}

func TestSyncSessionMetaFromDBNoCostSnapshots(t *testing.T) {
	wdb := openTestWorkspaceDB(t)
	const sid = "sess-sync-2"
	if err := wdb.RegisterSession(sid, "/tmp/project", "project", "m", "p"); err != nil {
		t.Fatalf("RegisterSession: %v", err)
	}
	sdb := openTestSessionDB(t, sid)

	if err := wdb.syncSessionMetaFromDB(sdb, sid); err != nil {
		t.Fatalf("syncSessionMetaFromDB: %v", err)
	}
	// No cost snapshots → cost should be 0.0, not an error.
	if got := registryCost(t, wdb, sid); got != 0.0 {
		t.Errorf("total_cost with no snapshots: expected 0.0, got %v", got)
	}
}

// TestRecordAgentUsagePushesCostToRegistry verifies the push path: after
// RecordAgentUsage updates the in-memory session cost, the cumulative cost
// is written to the workspace_sessions registry without re-opening the
// per-session DB.
func TestRecordAgentUsagePushesCostToRegistry(t *testing.T) {
	wdb := openTestWorkspaceDB(t)
	const sid = "sess-usage-1"
	if err := wdb.RegisterSession(sid, "/tmp/project", "project", "m", "p"); err != nil {
		t.Fatalf("RegisterSession: %v", err)
	}

	mgr := NewAgentManager()
	mgr.AddOrchestrator("orch", nil, nil, nil, nil, &AgentState{})
	sess := &Session{SessionID: sid, AgentID: "orch"}
	ws := &Workspace{
		AgentMgr:        mgr,
		WorkspaceDB:     wdb,
		SessionsByAgent: map[string]*Session{"orch": sess},
		Logger:          nopLogger{},
	}

	// First usage event: cost 0.25.
	if _, ok := ws.RecordAgentUsage("orch", map[string]any{
		"prompt":     float64(10),
		"completion": float64(4),
		"cost":       0.25,
	}); !ok {
		t.Fatal("RecordAgentUsage #1 rejected valid event")
	}
	if got := registryCost(t, wdb, sid); got != 0.25 {
		t.Fatalf("after first usage: registry cost expected 0.25, got %v", got)
	}

	// Second usage event: cost 0.30 → cumulative 0.55.
	if _, ok := ws.RecordAgentUsage("orch", map[string]any{
		"prompt":     float64(20),
		"completion": float64(8),
		"cost":       0.30,
	}); !ok {
		t.Fatal("RecordAgentUsage #2 rejected valid event")
	}
	if got := registryCost(t, wdb, sid); got != 0.55 {
		t.Fatalf("after second usage: registry cost expected 0.55 (cumulative), got %v", got)
	}
	if sess.SessionCost != 0.55 {
		t.Fatalf("in-memory session cost: expected 0.55, got %v", sess.SessionCost)
	}
}

// TestRecordAgentUsageWithoutWorkspaceDBDoesNotPanic verifies the push path
// is a no-op when the workspace has no DB (e.g., in unit tests, or if the DB
// failed to open). Must not panic and must still return the usage snapshot.
func TestRecordAgentUsageWithoutWorkspaceDBDoesNotPanic(t *testing.T) {
	mgr := NewAgentManager()
	mgr.AddOrchestrator("orch", nil, nil, nil, nil, &AgentState{})
	sess := &Session{SessionID: "sess-x", AgentID: "orch"}
	ws := &Workspace{
		AgentMgr:        mgr,
		WorkspaceDB:     nil, // no DB
		SessionsByAgent: map[string]*Session{"orch": sess},
		Logger:          nopLogger{},
	}
	got, ok := ws.RecordAgentUsage("orch", map[string]any{
		"prompt":     float64(1),
		"completion": float64(1),
		"cost":       0.01,
	})
	if !ok {
		t.Fatal("RecordAgentUsage rejected valid event without DB")
	}
	if got.Cost != 0.01 {
		t.Fatalf("usage snapshot cost: expected 0.01, got %v", got.Cost)
	}
}

// nopLogger is a Logger that discards everything. Used in tests where the
// workspace code may emit warnings (e.g., sync failures) but the test does
// not care about log output.
type nopLogger struct{}

func (nopLogger) Debug(tag, msg string, pairs ...any) {}
func (nopLogger) Info(tag, msg string, pairs ...any)  {}
func (nopLogger) Warn(tag, msg string, pairs ...any)  {}
func (nopLogger) Error(tag, msg string, pairs ...any) {}
