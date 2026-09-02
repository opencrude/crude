package viewmodel

import (
	"database/sql"
	"os"
	"testing"

	"crude/sessiondb"
	"crude/tui/chatprojection"
	"crude/tui/ui"
	"crude/workspace"
)

// TestRealSessionColorPersistenceReplay is a reusable diagnosis gate for
// production session content. It stays skipped in hermetic CI and is run with:
//
//	CRUDE_REPLAY_SESSION_DB=/path/to/session.db go test ./tui/viewmodel \
//	  -run TestRealSessionColorPersistenceReplay -count=1
func TestRealSessionColorPersistenceReplay(t *testing.T) {
	dbPath := os.Getenv("CRUDE_REPLAY_SESSION_DB")
	if dbPath == "" {
		t.Skip("CRUDE_REPLAY_SESSION_DB not set")
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&_busy_timeout=1000")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	persisted, err := sessiondb.LoadMessagesWithMeta(db)
	if err != nil {
		t.Fatal(err)
	}
	views := workspace.ConvertMessagesForDisplay(persisted)
	if len(views) == 0 {
		t.Fatal("session contains no display messages")
	}

	app := newFullTestApp(t)
	app.AddOrchestrator("replay")
	node := app.AgentMgr.Get("replay")
	for _, view := range views {
		node.State.Messages.AppendMessage(workspace.Message(view))
	}
	app.syncAgentMessages("replay", false)
	app.paintShellFrame()
	projection := app.subPaneProjections.GetExisting("replay")
	if projection == nil || projection.renderer == nil {
		t.Fatal("missing replay renderer")
	}
	renderer := projection.renderer
	baseline := styledRowsByMessage(renderer, 100)
	if len(baseline) == 0 {
		t.Fatal("real session produced no styled message rows")
	}

	for frame := 0; frame < 12; frame++ {
		app.syncAgentMessages("replay", false)
		renderer.SetSpinFrame(frame * 117)
		if frame%3 == 0 {
			renderer.RebuildItems()
		}
		if frame%4 == 0 {
			renderer.InvalidateCache()
		}
		app.paintShellFrame()
		rows, _, _ := renderer.ExportMessageRows(100)
		for _, row := range rows {
			if len(row.Native) != len(row.Lines) {
				t.Fatalf("frame %d message %s text/native mismatch %d/%d", frame, row.ID, len(row.Lines), len(row.Native))
			}
		}
		current := styledRowsByMessage(renderer, 100)
		for id := range baseline {
			if current[id] == 0 {
				t.Fatalf("frame %d message %s lost all semantic styling", frame, id)
			}
		}
	}
}

func styledRowsByMessage(renderer *chatprojection.Renderer, width int) map[string]int {
	rows, _, _ := renderer.ExportMessageRows(width)
	result := make(map[string]int)
	for _, row := range rows {
		for _, line := range row.Native {
			for _, span := range line.Spans {
				if span.FGSet || span.BGSet || span.BgSet || span.Attr != 0 || span.Role != ui.ColorFg {
					result[row.ID]++
				}
			}
		}
	}
	return result
}
