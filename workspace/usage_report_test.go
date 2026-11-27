package workspace

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"crude/agent/sessiondb"
)

func writeUsage(t *testing.T, path string, records ...sessiondb.UsageRecord) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sessiondb.OpenAndMigrate(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, record := range records {
		if err := sessiondb.UpsertUsageRecord(db, record); err != nil {
			t.Fatal(err)
		}
	}
}

func usageRecord(id, model string, cost float64, known bool, at time.Time) sessiondb.UsageRecord {
	return sessiondb.UsageRecord{RequestID: id, SessionID: "s", AgentID: "a", ProviderID: "proxy", ModelID: model,
		PromptTokens: 1000, CompletionTokens: 100, CachedTokens: 400, CostUSD: cost, CostKnown: known, CreatedAt: at.UnixNano(), UpdatedAt: at.UnixNano()}
}

// The report sums the root session database and each sub-agent database
// once, keeps an unknown cost unknown, and reads a changed database again.
func TestUsageReportSumsSessionsAndSubagentsOnce(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	root := filepath.Join(dir, "sessions", "one.db")
	writeUsage(t, root, usageRecord("r1", "opus", 2.5, true, now), usageRecord("r2", "opus", 0, false, now.Add(-48*time.Hour)))
	writeUsage(t, filepath.Join(dir, "sessions", "one", "subagents", "child.db"), usageRecord("c1", "flash", 0.25, true, now))
	writeUsage(t, filepath.Join(dir, "sessions", "two.db"))
	reader := NewUsageReader(dir)
	report, err := reader.Report(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if report.Requests != 3 || report.CostUSD != 2.75 || report.UnknownCost != 1 || report.Unreadable != 0 {
		t.Fatalf("totals = %+v", report)
	}
	if len(report.Models) != 2 || report.Models[0].ModelID != "opus" || report.Models[0].Requests != 2 || report.Models[0].InputTokens != 2000 || report.Models[1].CostUSD != 0.25 {
		t.Fatalf("models = %+v", report.Models)
	}
	if len(report.Sessions) != 1 || report.Sessions[0].SessionID != "one" || report.Sessions[0].Requests != 3 {
		t.Fatalf("sub-agent use must count toward its session: %+v", report.Sessions)
	}
	if len(report.Days) != UsageReportDays || report.Days[len(report.Days)-1].Day != now.Format("2006-01-02") || report.Days[len(report.Days)-1].CostUSD != 2.75 || report.Days[len(report.Days)-3].Requests != 1 {
		t.Fatalf("days = %+v", report.Days[len(report.Days)-3:])
	}
	writeUsage(t, root, usageRecord("r3", "opus", 1, true, now))
	if report, err = reader.Report(t.Context()); err != nil || report.Requests != 4 || report.CostUSD != 3.75 {
		t.Fatalf("a written database was not read again: %+v %v", report, err)
	}
}

// A database Crude cannot read is counted, not hidden and not guessed.
func TestUsageReportCountsUnreadableDatabase(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sessions", "broken.db"), []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := NewUsageReader(dir).Report(t.Context())
	if err != nil || report.Unreadable != 1 || report.Requests != 0 {
		t.Fatalf("report = %+v, %v", report, err)
	}
}

func writeSized(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Storage names what holds the space; only closed sessions' temporary files
// are reclaimable, and the cleanup keeps an open session's folder.
func TestStorageReportAndTemporaryCleanupKeepOpenSessions(t *testing.T) {
	dir := t.TempDir()
	writeSized(t, filepath.Join(dir, "sessions", "open.db"), 100)
	writeSized(t, filepath.Join(dir, "sessions", "closed.db"), 200)
	writeSized(t, filepath.Join(dir, "sessions", "open", "tmp", "build.bin"), 50)
	writeSized(t, filepath.Join(dir, "sessions", "closed", "tmp", "nested", "log.txt"), 70)
	writeSized(t, filepath.Join(dir, "sessions", "closed", "subagents", "child.db"), 30)
	writeSized(t, filepath.Join(dir, "sessions", "closed", "references", "image.png"), 10)
	writeSized(t, filepath.Join(dir, "local-history", "history.db"), 20)
	writeSized(t, filepath.Join(dir, "executions", "e1.json"), 4)
	writeSized(t, filepath.Join(dir, "executions", "e1", "main.go"), 40)
	writeSized(t, filepath.Join(dir, "workspace.db"), 5)
	writeSized(t, filepath.Join(dir, "collaboration-locks", "lock"), 1)
	open := map[string]bool{"open": true}
	report, err := MeasureStorage(t.Context(), dir, open)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][3]int64{ // bytes, items, reclaimable
		StorageSessions:  {310, 2, 0},
		StorageSubagents: {30, 1, 0},
		StorageTemporary: {120, 2, 70},
		StorageHistory:   {20, 0, 0},
		StorageWorktrees: {44, 0, 0},
		StorageIndex:     {5, 0, 0},
		StorageOther:     {1, 0, 0},
	}
	for _, category := range report.Categories {
		got := [3]int64{category.Bytes, int64(category.Items), category.Reclaimable}
		if got != want[category.ID] {
			t.Fatalf("%s = %v, want %v", category.ID, got, want[category.ID])
		}
	}
	if report.Bytes != 530 || len(report.Categories) != 7 {
		t.Fatalf("total = %d", report.Bytes)
	}
	// Only the execution owner knows which records are checkouts.
	report.SetItems(StorageWorktrees, 3)
	for _, category := range report.Categories {
		if category.ID == StorageWorktrees && category.Items != 3 {
			t.Fatalf("worktrees = %d, want the owner's count", category.Items)
		}
	}
	result, err := RemoveTemporaryFiles(t.Context(), dir, open)
	if err != nil || result.Removed != 1 || result.FreedBytes != 70 || result.Kept != 0 {
		t.Fatalf("cleanup = %+v, %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sessions", "open", "tmp", "build.bin")); err != nil {
		t.Fatal("an open session lost its temporary files")
	}
	if _, err := os.Stat(filepath.Join(dir, "sessions", "closed", "references", "image.png")); err != nil {
		t.Fatal("cleanup removed more than the temporary folder")
	}
}
