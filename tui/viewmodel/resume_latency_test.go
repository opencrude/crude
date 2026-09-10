package viewmodel

import (
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"crude/sessiondb"
	"crude/tui/tools"
	ttypes "crude/tui/types"
	"crude/workspace"
)

// Opt-in read-only reproduction against a real archive. No backend session is
// started, so this cannot write history or contact a configured provider.
func TestResumeLatencyArchive(t *testing.T) {
	path := os.Getenv("CRUDE_TEST_ARCHIVE")
	if path == "" {
		t.Skip("set CRUDE_TEST_ARCHIVE to a session database")
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	start := time.Now()
	messages, err := sessiondb.LoadMessagesWithMeta(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("archive load: %s, %d messages", time.Since(start), len(messages))
	views := ChatMessagesFromViews(workspace.ConvertMessagesForDisplay(messages))
	var files, rows, longestPath int
	for _, view := range views {
		if view.Role != ttypes.RoleTool {
			continue
		}
		for _, file := range tools.ParseUnifiedDiffStruct(view.Content) {
			files++
			rows += len(tools.BuildDiffRows(file))
			longestPath = max(longestPath, len(file.Path))
		}
	}
	t.Logf("diff workload: %d files, %d rows, longest path %d bytes", files, rows, longestPath)
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	enableIdleTicks(t, app)
	start = time.Now()
	app.handleAppMessages(ttypes.HistoryMessagesMsg(views))
	t.Logf("hydrate: %s, %d rows", time.Since(start), len(views))
	start = time.Now()
	app.paintShellFrame()
	t.Logf("first paint: %s", time.Since(start))
	start = time.Now()
	for range 10 {
		app.handleTick(ttypes.TickMsg(time.Now()))
		app.paintShellFrame()
	}
	t.Logf("ten settled frames: %s", time.Since(start))
	for _, phase := range []string{"preparing", "waiting_provider"} {
		start = time.Now()
		app.handleAgentEvent(ttypes.AgentEventMsg{AgentID: "root", Type: "phase", Data: map[string]any{"name": phase, "progress": 100, "provider": "ollama"}})
		app.paintShellFrame()
		t.Logf("%s update + paint: %s", phase, time.Since(start))
	}
	start = time.Now()
	for range 200 {
		app.handleTick(ttypes.TickMsg(time.Now()))
		app.paintShellFrame()
	}
	t.Logf("200 provider-wait frames: %s", time.Since(start))
}

func TestProviderWaitWithLargeDiffRemainsResponsive(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	enableIdleTicks(t, app)
	raw := "--- a/main.go\n+++ b/main.go\n@@ -0,0 +1,3000 @@\n" + strings.Repeat("+var number = 123456789 // wrapped source code\n", 3000)
	app.handleAppMessages(ttypes.HistoryMessagesMsg{{Role: ttypes.RoleTool, ToolName: "edit_file", ToolStatus: "success", Content: raw}})
	start := time.Now()
	app.handleAgentEvent(ttypes.AgentEventMsg{AgentID: "root", Type: "phase", Data: map[string]any{"name": "waiting_provider", "progress": 100, "provider": "ollama"}})
	app.paintShellFrame()
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("provider-wait first frame froze on completed diff: %s", elapsed)
	}
	for range 20 {
		start = time.Now()
		app.handleTick(ttypes.TickMsg(time.Now()))
		app.paintShellFrame()
		if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
			t.Fatalf("provider-wait tick froze: %s", elapsed)
		}
	}
	rows := app.AgentMgr.Get("root").State.Messages.Messages()
	if len(rows) == 0 || rows[len(rows)-1].LifecyclePhase != "waiting_provider" {
		t.Fatal("wait status disappeared instead of remaining live")
	}
}

func BenchmarkResumeHydration(b *testing.B) {
	app := newFullTestApp(b)
	app.AddOrchestrator("root")
	messages := seedBudgetSession(1000, 4)
	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		conversation := workspace.NewConversation()
		app.PaneMgr.Get("root").Messages = conversation
		app.AgentMgr.Get("root").State.Messages = conversation
		app.subPaneProjections.GetExisting("root").renderer.SyncMessages(nil, 0)
		b.StartTimer()
		app.handleAppMessages(ttypes.HistoryMessagesMsg(messages))
	}
}
