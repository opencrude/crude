package memory

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	db := openTestDB(t)
	st, err := Attach(db)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	return st
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "memory.db")
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestAttachEnablesFTS5(t *testing.T) {
	st := openTestStore(t)
	if !st.FTSEnabled() {
		fault, _ := st.GetMeta(context.Background(), MetaFTSFault)
		t.Fatalf("FTS5 unavailable: %s", fault)
	}
}

func TestLegacyV0SchemaUpgrades(t *testing.T) {
	db := openTestDB(t)
	// v0 schema as created by the pre-v2 workspace migration.
	_, err := db.Exec(`
		CREATE TABLE workspace_memories (
			id                TEXT PRIMARY KEY,
			kind              TEXT NOT NULL DEFAULT 'fact',
			title             TEXT NOT NULL,
			content           TEXT NOT NULL,
			source_session_id TEXT NOT NULL DEFAULT '',
			pinned            INTEGER NOT NULL DEFAULT 0,
			created_at        INTEGER NOT NULL,
			updated_at        INTEGER NOT NULL
		);
		INSERT INTO workspace_memories
			(id, kind, title, content, source_session_id, pinned, created_at, updated_at)
		VALUES ('mem-legacy1', 'convention', 'Validation command',
		        'Run go test ./... in moveWordLeft before handoff.', 's1', 1, 100, 100);
	`)
	if err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}

	st, err := Attach(db)
	if err != nil {
		t.Fatalf("Attach over legacy schema: %v", err)
	}
	if !st.FTSEnabled() {
		t.Fatal("FTS should be enabled after upgrade")
	}
	// Attach must be idempotent.
	if _, err := Attach(db); err != nil {
		t.Fatalf("second Attach: %v", err)
	}

	// Legacy row is FTS-searchable, including camelCase identifier parts.
	results, err := st.Recall(context.Background(), RecallQuery{Text: "word left climb"})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 1 || results[0].ID != "mem-legacy1" {
		t.Fatalf("legacy recall results = %+v", results)
	}
	if results[0].Status != StatusActive {
		t.Fatalf("legacy row status = %q", results[0].Status)
	}
}

func TestRememberRecallIdentifierSearch(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := st.Remember(ctx, Record{
		Kind:  KindLesson,
		Title: "moveWordLeft must climb blank lines",
		Content: "Editor.moveWordLeft in tui/editor/movement.go must keep climbing " +
			"across whitespace-only lines; a swallowed continue parks the cursor.",
		SourceSessionID: "s1",
	}); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if _, err := st.Remember(ctx, Record{
		Kind: KindFact, Title: "Unrelated fact", Content: "The build uses Go workspaces.",
	}); err != nil {
		t.Fatalf("Remember 2: %v", err)
	}

	for _, query := range []string{
		"moveWordLeft",              // exact identifier
		"move word left",            // split parts
		"movement.go cursor climbs", // path + prose
	} {
		results, err := st.Recall(ctx, RecallQuery{Text: query})
		if err != nil {
			t.Fatalf("Recall(%q): %v", query, err)
		}
		if len(results) == 0 || results[0].Kind != KindLesson {
			t.Fatalf("Recall(%q) top result = %+v", query, results)
		}
	}
}

func TestSupersedeExcludesFromRecallAndJournals(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	saved, err := st.Remember(ctx, Record{
		Kind: KindDecision, Title: "Use sqlite", Content: "Persist agent state in sqlite.",
	})
	if err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if err := st.Supersede(ctx, saved.ID, "", ActorTool, "s1"); err != nil {
		t.Fatalf("Supersede: %v", err)
	}
	results, err := st.Recall(ctx, RecallQuery{Text: "sqlite"})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("superseded memory recalled: %+v", results)
	}
	// Still fetchable by ID for provenance.
	got, err := st.Get(ctx, saved.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusSuperseded {
		t.Fatalf("status = %q", got.Status)
	}
	entries, err := st.JournalForMemory(ctx, saved.ID, 10)
	if err != nil {
		t.Fatalf("JournalForMemory: %v", err)
	}
	if len(entries) != 2 || entries[0].Op != "remember" || entries[1].Op != "supersede" {
		t.Fatalf("journal = %+v", entries)
	}
	// Superseding twice errors (already superseded).
	if err := st.Supersede(ctx, saved.ID, "", ActorTool, "s1"); err == nil {
		t.Fatal("double supersede should error")
	}
}

func TestUpdateCreatesSupersedeChain(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	old, err := st.Remember(ctx, Record{
		Kind: KindConvention, Title: "Test command", Content: "Run go test ./...",
	})
	if err != nil {
		t.Fatalf("Remember: %v", err)
	}
	// Same kind+title: revise in place.
	revised, err := st.Update(ctx, old.ID, Record{
		Content: "Run go test -race ./...",
	}, ActorTool, "s2")
	if err != nil {
		t.Fatalf("Update in place: %v", err)
	}
	if revised.ID != old.ID {
		t.Fatalf("in-place update changed ID: %q != %q", revised.ID, old.ID)
	}
	// New title: supersede-and-replace.
	replaced, err := st.Update(ctx, old.ID, Record{
		Title: "Validation gate", Content: "Run go test -race ./... and golangci-lint.",
	}, ActorTool, "s2")
	if err != nil {
		t.Fatalf("Update replace: %v", err)
	}
	if replaced.ID == old.ID {
		t.Fatal("replacement should have a new ID")
	}
	oldRec, err := st.Get(ctx, old.ID)
	if err != nil {
		t.Fatalf("Get old: %v", err)
	}
	if oldRec.Status != StatusSuperseded || oldRec.SupersededBy != replaced.ID {
		t.Fatalf("old record = %+v", oldRec)
	}
}

func TestArchiveExcludedFromAutoInjectButExplicitSearchFinds(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	saved, err := st.Remember(ctx, Record{
		Kind: KindFact, Title: "Old detail", Content: "The legacy parser lived in parser.go.",
	})
	if err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if err := st.Archive(ctx, []string{saved.ID}, ActorConsolidation, "decayed"); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	autoInject, err := st.Recall(ctx, RecallQuery{Text: "legacy parser"})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(autoInject) != 0 {
		t.Fatalf("archived memory auto-injected: %+v", autoInject)
	}
	explicit, err := st.Recall(ctx, RecallQuery{Text: "legacy parser", IncludeArchived: true})
	if err != nil {
		t.Fatalf("Recall archived: %v", err)
	}
	if len(explicit) != 1 {
		t.Fatalf("explicit search results = %+v", explicit)
	}
}

func TestTouchReinforcesUsage(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	saved, err := st.Remember(ctx, Record{
		Kind: KindFact, Title: "Theme file", Content: "Colors live in tui/theme.",
	})
	if err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if err := st.Touch(ctx, []string{saved.ID}); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	got, err := st.Get(ctx, saved.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.UseCount != 1 || got.LastUsedAt == 0 {
		t.Fatalf("usage not reinforced: %+v", got)
	}
}

func TestHistoryMirrorAndSearch(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	err := st.RecordTurn(ctx, TurnRecord{
		SessionID: "s1", Turn: 1,
		UserText:      "fix the scrollbar artifact in the editor",
		AssistantText: "Fixed by matching VisibleWidth to ultraviolet in rendering.go.",
		ToolNames:     []string{"read_file", "edit_file"},
		Files:         []string{"tui/editor/render.go"},
	})
	if err != nil {
		t.Fatalf("RecordTurn: %v", err)
	}
	// Upsert same turn (re-finalize) must not duplicate.
	if err := st.RecordTurn(ctx, TurnRecord{
		SessionID: "s1", Turn: 1,
		UserText:      "fix the scrollbar artifact in the editor",
		AssistantText: "Fixed by matching VisibleWidth to ultraviolet in rendering.go (final).",
	}); err != nil {
		t.Fatalf("RecordTurn upsert: %v", err)
	}
	if err := st.SaveSessionSummary(ctx, SessionSummary{
		SessionID: "s1", Title: "Scrollbar artifact fix",
		Summary:  "Fixed the editor scrollbar artifact by unifying width measurement.",
		KeyFiles: []string{"tui/editor/render.go"}, Outcome: "completed", TurnCount: 1,
	}); err != nil {
		t.Fatalf("SaveSessionSummary: %v", err)
	}

	hits, summaries, err := st.SearchHistory(ctx, "scrollbar VisibleWidth", 10)
	if err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}
	if len(hits) != 1 || hits[0].SessionID != "s1" || hits[0].Turn != 1 {
		t.Fatalf("history hits = %+v", hits)
	}
	if !strings.Contains(hits[0].AssistantText, "(final)") {
		t.Fatalf("upsert did not replace turn text: %+v", hits[0])
	}
	if len(summaries) != 1 || summaries[0].Title != "Scrollbar artifact fix" {
		t.Fatalf("summaries = %+v", summaries)
	}

	latest, err := st.LatestSummaries(ctx, "other-session", 2)
	if err != nil {
		t.Fatalf("LatestSummaries: %v", err)
	}
	if len(latest) != 1 {
		t.Fatalf("latest = %+v", latest)
	}
	excluded, err := st.LatestSummaries(ctx, "s1", 2)
	if err != nil {
		t.Fatalf("LatestSummaries exclude: %v", err)
	}
	if len(excluded) != 0 {
		t.Fatalf("own session not excluded: %+v", excluded)
	}
}

func TestRecallBudgetPacking(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	long := strings.Repeat("scrollbar rendering detail ", 40) // ~1000 chars ≈ 250 tokens
	for i, title := range []string{"First scrollbar note", "Second scrollbar note", "Third scrollbar note"} {
		if _, err := st.Remember(ctx, Record{
			Kind: KindFact, Title: title, Content: long, Importance: 90 - i*10,
		}); err != nil {
			t.Fatalf("Remember %d: %v", i, err)
		}
	}
	results, err := st.Recall(ctx, RecallQuery{Text: "scrollbar", BudgetTokens: 300})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("budget packing returned %d results, want 1: %+v", len(results), results)
	}
	unbounded, err := st.Recall(ctx, RecallQuery{Text: "scrollbar"})
	if err != nil {
		t.Fatalf("Recall unbounded: %v", err)
	}
	if len(unbounded) != 3 {
		t.Fatalf("unbounded results = %d, want 3", len(unbounded))
	}
}

func TestPinnedAlwaysCandidate(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := st.Remember(ctx, Record{
		Kind: KindConvention, Title: "Always run lint", Content: "golangci-lint run ./... gate.",
		Pinned: true,
	}); err != nil {
		t.Fatalf("Remember pinned: %v", err)
	}
	// Query that does not match the pinned memory textually.
	results, err := st.Recall(ctx, RecallQuery{Text: "completely unrelated zig comptime"})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	found := false
	for _, r := range results {
		if r.Pinned {
			found = true
		}
	}
	if !found {
		t.Fatalf("pinned memory missing from recall: %+v", results)
	}
}

func TestTryLock(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	ok, err := st.TryLock(ctx, "consolidate", 60_000_000_000)
	if err != nil || !ok {
		t.Fatalf("first TryLock: ok=%v err=%v", ok, err)
	}
	ok, err = st.TryLock(ctx, "consolidate", 60_000_000_000)
	if err != nil || ok {
		t.Fatalf("second TryLock should be held: ok=%v err=%v", ok, err)
	}
	if err := st.Unlock(ctx, "consolidate"); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	ok, err = st.TryLock(ctx, "consolidate", 60_000_000_000)
	if err != nil || !ok {
		t.Fatalf("TryLock after unlock: ok=%v err=%v", ok, err)
	}
}
