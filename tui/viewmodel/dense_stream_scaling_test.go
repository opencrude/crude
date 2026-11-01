package viewmodel

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"crude/types"
)

// denseStreamHistory resembles a long-running agent session: request rounds of
// command results (JSON envelopes the activity grouping must classify), file
// reads, edits and prose answers.
func denseStreamHistory(rounds int) []types.ChatMessage {
	output := strings.Repeat("ok  \tcrude/pkg\t0.42s\n", 40)
	messages := make([]types.ChatMessage, 0, rounds*6)
	for round := range rounds {
		roundID := fmt.Sprint("round-", round)
		messages = append(messages,
			types.ChatMessage{InternalID: fmt.Sprint("u", round), Role: types.RoleUser, Content: "continue the investigation"},
			types.ChatMessage{InternalID: fmt.Sprint("c", round), Role: types.RoleTool, ToolName: "exec_command", ToolStatus: types.ToolStatusSuccess, ToolRoundID: roundID,
				ToolArgs: "go test ./...", Content: fmt.Sprintf(`{"exit_code":0,"output":%q}`, output)},
			types.ChatMessage{InternalID: fmt.Sprint("r", round), Role: types.RoleTool, ToolName: "read_file", ToolStatus: types.ToolStatusSuccess, ToolRoundID: roundID,
				ToolArgs: "main.go", Content: strings.Repeat("func main() {}\n", 60)},
			types.ChatMessage{InternalID: fmt.Sprint("e", round), Role: types.RoleTool, ToolName: "edit_file", ToolStatus: types.ToolStatusSuccess, ToolRoundID: roundID,
				ToolArgs: "main.go", Content: "Replaced 1 occurrence"},
			types.ChatMessage{InternalID: fmt.Sprint("a", round), Role: types.RoleAssistant, Content: "Updated the handler and the tests pass."},
		)
	}
	return messages
}

// streamTokenAllocs is the allocation count of one streamed token: event
// reduction, transcript projection and the presented frame.
func streamTokenAllocs(t *testing.T, rounds int) float64 {
	t.Helper()
	app := newFullTestApp(t)
	app.width, app.height = 160, 48
	app.AddOrchestrator("root")
	enableIdleTicks(t, app)
	app.handleAppMessages(types.HistoryMessagesMsg(denseStreamHistory(rounds)))
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "root", Type: "turn_started", Data: types.TurnIdentity{Generation: 7}})
	app.paintShellFrame()
	for i := range 30 {
		app.handleAgentEvent(types.AgentEventMsg{AgentID: "root", Type: "stream_token", Data: fmt.Sprintf("warm%d ", i)})
		app.handleTick(types.TickMsg(time.Now()))
		app.paintShellFrame()
	}
	// Offscreen history materializes within a fixed per-frame budget; that is
	// bounded background work, not per-token cost, so settle it on idle frames
	// (the streamed answer, whose rewrap cost is its own length, stays short).
	for i := 0; app.activeMsgList().Materializing(); i++ {
		if i > 5000 {
			t.Fatal("offscreen materialization never settled")
		}
		app.handleTick(types.TickMsg(time.Now()))
		app.paintShellFrame()
	}
	const tokens = 40
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for i := range tokens {
		app.handleAgentEvent(types.AgentEventMsg{AgentID: "root", Type: "stream_token", Data: fmt.Sprintf("word%d ", i)})
		app.paintShellFrame()
	}
	runtime.ReadMemStats(&after)
	if got := app.ws.AgentMgr().Get("root").State.Messages.Messages(); !strings.Contains(got[len(got)-1].Content, "word39") {
		t.Fatal("streamed tokens did not reach the transcript")
	}
	return float64(after.Mallocs-before.Mallocs) / tokens
}

// A streamed token must cost the same whether the session holds a hundred
// messages or many thousands: before the incremental projection every token
// reclassified (JSON-decoded) every settled tool result and exported the
// complete transcript, so a 17k-message session spent ~250 ms per token.
func TestDenseSessionStreamTokenCostIndependentOfHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("dense history fixture")
	}
	small := streamTokenAllocs(t, 40)
	large := streamTokenAllocs(t, 1600) // 8,000 messages
	t.Logf("allocations per streamed token: %d rounds=%.0f, %d rounds=%.0f", 40, small, 1600, large)
	// Measured flat (395 vs 396 allocations from 200 to 16,000 messages); a
	// small allowance absorbs map growth, never a per-row term.
	if large > small*1.15+20 {
		t.Fatalf("per-token work scales with history: %.0f allocs at 8,000 messages vs %.0f at 200", large, small)
	}
}
