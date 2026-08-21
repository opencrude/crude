package viewmodel

import (
	"strings"
	"testing"
	"time"

	"crude/provider"
	ttypes "crude/tui/types"
	"crude/workspace"
)

// thinkingLines returns `n` lines of wordy text that wrap to `n` rows at
// chatprojection width (60 chars/line fits well under the ~64 content width).
func thinkingLines(n int) string {
	line := strings.Repeat("x", 60)
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// TestThinkingPromotionContinuesIntoPane drives the real event flow: a long
// thinking block crosses the promotion threshold, and subsequent thinking must
// keep streaming into the promoted pane rather than restarting inline in the
// main conversation.
func TestThinkingPromotionContinuesIntoPane(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator(
		"promote-agent",
		make(chan provider.UserInput, 1),
		make(chan provider.UserInput, 1),
		make(chan struct{}, 1),
		make(chan string, 1),
	)
	app.ChatAreaHeight = 6 // threshold = max(6/3, 5) = 5 wrapped lines

	node := app.AgentMgr.Get("promote-agent")
	if node == nil || node.State == nil {
		t.Fatal("agent not created")
	}
	agent := node.State

	// Stream enough thinking to exceed the 5-line threshold.
	app.handleAgentEvent(ttypes.AgentEventMsg{Type: "thinking", AgentID: "promote-agent", Data: thinkingLines(8)})

	if agent.ThinkingPaneID == "" {
		t.Fatal("thinking was not promoted to a pane")
	}

	// After promotion, subsequent thinking must continue into the pane,
	// not back into the main conversation.
	app.handleAgentEvent(ttypes.AgentEventMsg{Type: "thinking", AgentID: "promote-agent", Data: "EXTRA-THINKING"})

	for _, msg := range agent.Messages.Messages() {
		if msg.Role == workspace.MessageRoleThinking && msg.ThinkingActive {
			t.Fatalf("main conversation has an active inline thinking block after promotion: %q", msg.Content)
		}
	}

	sp := app.PaneMgr.Get(agent.ThinkingPaneID)
	if sp == nil {
		t.Fatal("thinking pane missing")
	}
	var paneContent strings.Builder
	for _, msg := range sp.Messages.Messages() {
		if msg.Role == workspace.MessageRoleThinking {
			paneContent.WriteString(msg.Content)
		}
	}
	if !strings.Contains(paneContent.String(), "EXTRA-THINKING") {
		t.Fatalf("extra thinking did not reach the pane: %q", paneContent.String())
	}
}

// TestThinkingPromotionStartTimeCoversWholeBlock verifies that the promoted
// thinking pane inherits the original block start time, so the eventual
// "thought for X" summary reflects the whole block and not just the
// post-promotion window.
func TestThinkingPromotionStartTimeCoversWholeBlock(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator(
		"promote-time",
		make(chan provider.UserInput, 1),
		make(chan provider.UserInput, 1),
		make(chan struct{}, 1),
		make(chan string, 1),
	)
	app.ChatAreaHeight = 6

	node := app.AgentMgr.Get("promote-time")
	if node == nil || node.State == nil {
		t.Fatal("agent not created")
	}
	agent := node.State

	// First thinking chunk seeds the block with its start time.
	app.handleAgentEvent(ttypes.AgentEventMsg{Type: "thinking", AgentID: "promote-time", Data: thinkingLines(8)})
	if agent.ThinkingPaneID == "" {
		t.Fatal("thinking was not promoted")
	}
	sp := app.PaneMgr.Get(agent.ThinkingPaneID)
	if sp == nil {
		t.Fatal("thinking pane missing")
	}

	// The thinking message in the pane keeps the block start time.
	var msgTime time.Time
	for _, msg := range sp.Messages.Messages() {
		if msg.Role == workspace.MessageRoleThinking {
			msgTime = msg.Time
			break
		}
	}
	if msgTime.IsZero() {
		t.Fatal("thinking pane has no thinking message")
	}
	// The pane's own clock must start at the block start (not at the later
	// promotion moment), so the "thought for X" summary covers the whole
	// block.
	if sp.StartTime.After(msgTime) {
		t.Fatalf("pane start time after block start: pane=%v block=%v", sp.StartTime, msgTime)
	}
	if msgTime.After(time.Now()) {
		t.Fatalf("thinking message start time in the future: %v", msgTime)
	}
}

// TestThinkingPaneIDsAreUniquePerAgent verifies two agents thinking in the
// same second get distinct thinking panes.
func TestThinkingPaneIDsAreUniquePerAgent(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator(
		"promote-a",
		make(chan provider.UserInput, 1),
		make(chan provider.UserInput, 1),
		make(chan struct{}, 1),
		make(chan string, 1),
	)
	app.AddOrchestrator(
		"promote-b",
		make(chan provider.UserInput, 1),
		make(chan provider.UserInput, 1),
		make(chan struct{}, 1),
		make(chan string, 1),
	)
	app.ChatAreaHeight = 6

	for _, id := range []string{"promote-a", "promote-b"} {
		app.handleAgentEvent(ttypes.AgentEventMsg{Type: "thinking", AgentID: id, Data: thinkingLines(8)})
		agent := app.AgentMgr.Get(id).State
		if agent.ThinkingPaneID == "" {
			t.Fatalf("agent %s: thinking not promoted", id)
		}
	}

	a := app.AgentMgr.Get("promote-a").State
	b := app.AgentMgr.Get("promote-b").State
	if a.ThinkingPaneID == b.ThinkingPaneID {
		t.Fatalf("two agents share the same thinking pane: %q", a.ThinkingPaneID)
	}
}

// TestThinkingPaneDemotesToSummary verifies that once thinking finishes, the
// promoted pane collapses to a single-line summary with a human duration.
func TestThinkingPaneDemotesToSummary(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator(
		"promote-done",
		make(chan provider.UserInput, 1),
		make(chan provider.UserInput, 1),
		make(chan struct{}, 1),
		make(chan string, 1),
	)
	app.ChatAreaHeight = 6

	node := app.AgentMgr.Get("promote-done")
	agent := node.State

	app.handleAgentEvent(ttypes.AgentEventMsg{Type: "thinking", AgentID: "promote-done", Data: thinkingLines(8)})
	if agent.ThinkingPaneID == "" {
		t.Fatal("thinking was not promoted")
	}
	paneID := agent.ThinkingPaneID
	// Thinking finishes: streaming begins.
	app.handleAgentEvent(ttypes.AgentEventMsg{Type: "stream_token", AgentID: "promote-done", Data: "hello"})

	sp := app.PaneMgr.Get(paneID)
	if sp == nil || !sp.Done {
		t.Fatalf("thinking pane not collapsed after stream started")
	}
	lines := app.subPaneProjections.Render(paneID, sp, 40, 5, 0)
	if len(lines) != 1 {
		t.Fatalf("expected single summary line after collapse, got %d", len(lines))
	}
	plain := lines[0].PlainText()
	if !strings.Contains(plain, "Thought") {
		t.Fatalf("summary missing 'Thought': %q", plain)
	}
	if !strings.Contains(plain, "s") {
		t.Fatalf("summary missing duration: %q", plain)
	}
}
