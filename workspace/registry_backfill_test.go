package workspace

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"crude/sessiondb"
	ttypes "crude/types"
)

// TestBackfillSessionRegistryFromDisk inserts missing registry rows from
// workspace-local session DBs and is idempotent.
func TestBackfillSessionRegistryFromDisk(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	project := t.TempDir()
	wsDir, err := WorkspaceDir(project)
	if err != nil {
		t.Fatal(err)
	}
	wdb, err := OpenWorkspaceDB(project)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = wdb.Close() })

	sessRoot := filepath.Join(wsDir, "sessions")
	if err := os.MkdirAll(sessRoot, 0o700); err != nil {
		t.Fatal(err)
	}

	const sid = "2026-07-30_backfill_ok"
	dbPath := filepath.Join(sessRoot, sid+".db")
	sdb, err := sessiondb.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.Create(sdb, sessiondb.SessionMeta{
		ID:           sid,
		ProjectPath:  project,
		ProjectName:  "proj",
		Model:        "m1",
		Provider:     "prov",
		CreatedAt:    42,
		MessageCount: 3,
		TurnCount:    2,
	}); err != nil {
		t.Fatal(err)
	}
	_ = sessiondb.UpdateFirstPrompt(sdb, sid, "hello backfill")
	_ = sdb.Close()

	ws := &Workspace{
		RootPath:     project,
		WorkspaceDir: wsDir,
		WorkspaceDB:  wdb,
		Logger:       nopLogger{},
	}

	n := ws.BackfillSessionRegistry()
	if n != 1 {
		t.Fatalf("inserted=%d, want 1", n)
	}
	metas, err := wdb.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 || metas[0].ID != sid {
		t.Fatalf("ListSessions: %+v", metas)
	}
	if metas[0].Model != "m1" || metas[0].TurnCount != 2 {
		t.Fatalf("meta fields: %+v", metas[0])
	}
	if !metas[0].FirstPrompt.Valid || metas[0].FirstPrompt.String != "hello backfill" {
		t.Fatalf("first_prompt: %+v", metas[0].FirstPrompt)
	}

	// Idempotent: second run inserts 0 and does not duplicate.
	n2 := ws.BackfillSessionRegistry()
	if n2 != 0 {
		t.Fatalf("second insert=%d, want 0", n2)
	}
	metas2, _ := wdb.ListSessions()
	if len(metas2) != 1 {
		t.Fatalf("after second backfill: %+v", metas2)
	}
}

// TestBackfillDoesNotOverwriteRicherExistingRow ensures a sparse derived
// insert cannot clobber an already-populated registry row.
func TestBackfillDoesNotOverwriteRicherExistingRow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	project := t.TempDir()
	wsDir, err := WorkspaceDir(project)
	if err != nil {
		t.Fatal(err)
	}
	wdb, err := OpenWorkspaceDB(project)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = wdb.Close() })

	const sid = "2026-07-30_rich_row"
	if err := wdb.RegisterSession(sid, project, "rich-name", "rich-model", "rich-prov"); err != nil {
		t.Fatal(err)
	}
	if err := wdb.UpdateSessionMeta(sid, 9, 12, "kept-first", "kept-last", 1.5); err != nil {
		t.Fatal(err)
	}

	sessRoot := filepath.Join(wsDir, "sessions")
	_ = os.MkdirAll(sessRoot, 0o700)
	dbPath := filepath.Join(sessRoot, sid+".db")
	sdb, err := sessiondb.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// Poorer session DB meta — must not overwrite registry.
	if err := sessiondb.Create(sdb, sessiondb.SessionMeta{
		ID:          sid,
		ProjectPath: project,
		ProjectName: "poor",
		Model:       "poor-model",
		Provider:    "poor",
		CreatedAt:   1,
	}); err != nil {
		t.Fatal(err)
	}
	_ = sdb.Close()

	ws := &Workspace{RootPath: project, WorkspaceDir: wsDir, WorkspaceDB: wdb, Logger: nopLogger{}}
	if n := ws.BackfillSessionRegistry(); n != 0 {
		t.Fatalf("inserted=%d, want 0 (row already exists)", n)
	}
	tc, mc, fp, lp, cost := registryRow(t, wdb, sid)
	if tc != 9 || mc != 12 || fp != "kept-first" || lp != "kept-last" || cost != 1.5 {
		t.Fatalf("richer row clobbered: tc=%d mc=%d fp=%q lp=%q cost=%v", tc, mc, fp, lp, cost)
	}
	var model string
	_ = wdb.DB.QueryRow(`SELECT model FROM workspace_sessions WHERE session_id = ?`, sid).Scan(&model)
	if model != "rich-model" {
		t.Fatalf("model overwritten: %q", model)
	}
}

// TestBackfillSkipsCorruptSessionDB ensures a bad file does not abort backfill
// of other sessions.
func TestBackfillSkipsCorruptSessionDB(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	project := t.TempDir()
	wsDir, err := WorkspaceDir(project)
	if err != nil {
		t.Fatal(err)
	}
	wdb, err := OpenWorkspaceDB(project)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = wdb.Close() })

	sessRoot := filepath.Join(wsDir, "sessions")
	_ = os.MkdirAll(sessRoot, 0o700)
	if err := os.WriteFile(filepath.Join(sessRoot, "corrupt.db"), []byte("not a sqlite db"), 0o600); err != nil {
		t.Fatal(err)
	}

	const sid = "2026-07-30_good_peer"
	goodPath := filepath.Join(sessRoot, sid+".db")
	sdb, err := sessiondb.OpenAndMigrate(goodPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.Create(sdb, sessiondb.SessionMeta{
		ID: sid, ProjectPath: project, ProjectName: "p", CreatedAt: 99,
	}); err != nil {
		t.Fatal(err)
	}
	_ = sdb.Close()

	ws := &Workspace{RootPath: project, WorkspaceDir: wsDir, WorkspaceDB: wdb, Logger: nopLogger{}}
	n := ws.BackfillSessionRegistry()
	if n != 1 {
		t.Fatalf("inserted=%d, want 1 (corrupt skipped)", n)
	}
	metas, _ := wdb.ListSessions()
	if len(metas) != 1 || metas[0].ID != sid {
		t.Fatalf("ListSessions: %+v", metas)
	}
}

// TestBackfillUsesMtimeWhenCreatedAtMissing covers the last-resort timestamp.
func TestBackfillUsesMtimeWhenCreatedAtMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	project := t.TempDir()
	wsDir, err := WorkspaceDir(project)
	if err != nil {
		t.Fatal(err)
	}
	wdb, err := OpenWorkspaceDB(project)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = wdb.Close() })

	const sid = "2026-07-30_mtime_fallback"
	sessRoot := filepath.Join(wsDir, "sessions")
	_ = os.MkdirAll(sessRoot, 0o700)
	dbPath := filepath.Join(sessRoot, sid+".db")
	sdb, err := sessiondb.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.Create(sdb, sessiondb.SessionMeta{
		ID: sid, ProjectPath: project, ProjectName: "p", CreatedAt: 0,
	}); err != nil {
		t.Fatal(err)
	}
	_ = sdb.Close()

	mtime := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(dbPath, mtime, mtime); err != nil {
		t.Fatal(err)
	}

	ws := &Workspace{RootPath: project, WorkspaceDir: wsDir, WorkspaceDB: wdb, Logger: nopLogger{}}
	if n := ws.BackfillSessionRegistry(); n != 1 {
		t.Fatalf("inserted=%d", n)
	}
	var created int64
	if err := wdb.DB.QueryRow(
		`SELECT created_at FROM workspace_sessions WHERE session_id = ?`, sid,
	).Scan(&created); err != nil {
		t.Fatal(err)
	}
	want := mtime.UnixNano()
	if created != want {
		t.Fatalf("created_at=%d, want mtime nano %d", created, want)
	}
}

// TestEnrichMarksMissingDoesNotDelete verifies TASK 2: apparent absence marks
// the row unavailable instead of DELETE, and reappearance clears the flag.
func TestEnrichMarksMissingDoesNotDelete(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	project := t.TempDir()
	wsDir, err := WorkspaceDir(project)
	if err != nil {
		t.Fatal(err)
	}
	wdb, err := OpenWorkspaceDB(project)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = wdb.Close() })

	const sid = "2026-07-30_mark_missing"
	if err := wdb.RegisterSession(sid, project, "p", "m", "prov"); err != nil {
		t.Fatal(err)
	}

	ws := &Workspace{
		RootPath:     project,
		WorkspaceDir: wsDir,
		WorkspaceDB:  wdb,
		Logger:       nopLogger{},
	}

	// No session DB on disk — enrich must mark, not delete.
	got := ws.enrichSessionMeta(sessiondb.SessionMeta{ID: sid})
	if got.ID != "" {
		t.Fatalf("expected empty meta when missing, got %+v", got)
	}
	var missing int
	if err := wdb.DB.QueryRow(
		`SELECT db_missing FROM workspace_sessions WHERE session_id = ?`, sid,
	).Scan(&missing); err != nil {
		t.Fatalf("row was deleted or unreadable: %v", err)
	}
	if missing != 1 {
		t.Fatalf("db_missing=%d, want 1", missing)
	}
	listed, _ := wdb.ListSessions()
	if len(listed) != 0 {
		t.Fatalf("ListSessions should exclude missing: %+v", listed)
	}

	// Reappear: create session DB and enrich again.
	sessRoot := filepath.Join(wsDir, "sessions")
	_ = os.MkdirAll(sessRoot, 0o700)
	dbPath := filepath.Join(sessRoot, sid+".db")
	sdb, err := sessiondb.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.Create(sdb, sessiondb.SessionMeta{
		ID: sid, ProjectPath: project, ProjectName: "p",
		Model: "m", Provider: "prov", CreatedAt: 1, TurnCount: 1,
	}); err != nil {
		t.Fatal(err)
	}
	_ = sdb.Close()

	got = ws.enrichSessionMeta(sessiondb.SessionMeta{ID: sid, Model: "m", Provider: "prov"})
	if got.ID != sid {
		t.Fatalf("expected restored meta, got %+v", got)
	}
	if err := wdb.DB.QueryRow(
		`SELECT db_missing FROM workspace_sessions WHERE session_id = ?`, sid,
	).Scan(&missing); err != nil {
		t.Fatal(err)
	}
	if missing != 0 {
		t.Fatalf("db_missing after reappear=%d, want 0", missing)
	}
	listed, _ = wdb.ListSessions()
	if len(listed) != 1 || listed[0].ID != sid {
		t.Fatalf("ListSessions after reappear: %+v", listed)
	}
}

// TestBackfillClearsMissingFlagWhenDBReappears covers the backfill path for
// restoring a previously marked-missing row without overwriting fields.
func TestBackfillClearsMissingFlagWhenDBReappears(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	project := t.TempDir()
	wsDir, err := WorkspaceDir(project)
	if err != nil {
		t.Fatal(err)
	}
	wdb, err := OpenWorkspaceDB(project)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = wdb.Close() })

	const sid = "2026-07-30_clear_missing"
	if err := wdb.RegisterSession(sid, project, "kept", "kept-m", "kept-p"); err != nil {
		t.Fatal(err)
	}
	if err := wdb.UpdateSessionMeta(sid, 5, 7, "fp", "lp", 0.25); err != nil {
		t.Fatal(err)
	}
	if err := wdb.MarkSessionDBMissing(sid, true); err != nil {
		t.Fatal(err)
	}

	sessRoot := filepath.Join(wsDir, "sessions")
	_ = os.MkdirAll(sessRoot, 0o700)
	dbPath := filepath.Join(sessRoot, sid+".db")
	sdb, err := sessiondb.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.Create(sdb, sessiondb.SessionMeta{
		ID: sid, ProjectPath: project, ProjectName: "other",
		Model: "other", Provider: "x", CreatedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	_ = sdb.Close()

	ws := &Workspace{RootPath: project, WorkspaceDir: wsDir, WorkspaceDB: wdb, Logger: nopLogger{}}
	if n := ws.BackfillSessionRegistry(); n != 0 {
		t.Fatalf("should not re-insert existing row, got %d", n)
	}
	var missing int
	var model string
	_ = wdb.DB.QueryRow(
		`SELECT db_missing, model FROM workspace_sessions WHERE session_id = ?`, sid,
	).Scan(&missing, &model)
	if missing != 0 {
		t.Fatalf("db_missing=%d after backfill, want 0", missing)
	}
	if model != "kept-m" {
		t.Fatalf("model clobbered: %q", model)
	}
	tc, mc, fp, lp, cost := registryRow(t, wdb, sid)
	if tc != 5 || mc != 7 || fp != "fp" || lp != "lp" || cost != 0.25 {
		t.Fatalf("fields changed: tc=%d mc=%d fp=%q lp=%q cost=%v", tc, mc, fp, lp, cost)
	}
}

// TestNewWorkspaceRunsBackfill ensures the workspace-open hook populates the
// registry so ListSessions works without a separate call.
func TestNewWorkspaceRunsBackfill(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	project := t.TempDir()
	wsDir, err := WorkspaceDir(project)
	if err != nil {
		t.Fatal(err)
	}
	sessRoot := filepath.Join(wsDir, "sessions")
	_ = os.MkdirAll(sessRoot, 0o700)
	const sid = "2026-07-30_open_hook"
	dbPath := filepath.Join(sessRoot, sid+".db")
	sdb, err := sessiondb.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.Create(sdb, sessiondb.SessionMeta{
		ID: sid, ProjectPath: project, ProjectName: "p", CreatedAt: 7, TurnCount: 1,
	}); err != nil {
		t.Fatal(err)
	}
	_ = sdb.Close()

	ws := NewWorkspace(project, "model", "user", ttypes.Settings{}, nopLogger{})
	t.Cleanup(func() {
		if ws.WorkspaceDB != nil {
			_ = ws.WorkspaceDB.Close()
		}
	})
	listed := ws.ListSessions()
	if len(listed) != 1 || listed[0].ID != sid {
		t.Fatalf("ListSessions after NewWorkspace: %+v", listed)
	}
}
