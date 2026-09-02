package viewmodel

import (
	"strings"
	"testing"
	"time"

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

// TestThinkingPaneShowsScrollbarOnFirstFrame pins the nested-gutter ordering
// fix: the nesting level (which reserves a scrollbar column for an overflowing
// pane) must be computed after the panes render, so a newly promoted thinking
// pane paints its scrollbar on the very first frame instead of one frame late.
func TestThinkingPaneShowsScrollbarOnFirstFrame(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24
	app.agentChatScroll = make(map[string]agentMainChatScrollSnapshot)
	app.AddOrchestrator(
		"probe-agent")

	node := app.AgentMgr.Get("probe-agent")
	agent := node.State
	app.handleAgentEvent(ttypes.AgentEventMsg{Type: "thinking", AgentID: "probe-agent", Data: thinkingLines(40)})
	if agent.ThinkingPaneID == "" {
		t.Fatal("thinking was not promoted")
	}
	for i := 0; i < 20; i++ {
		app.handleAgentEvent(ttypes.AgentEventMsg{Type: "thinking", AgentID: "probe-agent", Data: thinkingLines(5)})
	}

	app.paintShellFrame()

	// The nested gutter paints '│' track cells. The thinking block cursor is
	// also '█', but '│' is unambiguous — its presence proves the pane reserved
	// and painted a scrollbar column on this first frame.
	buf := app.mainSurf.Buffer()
	track := 0
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			if buf.Get(x, y).Char == '│' {
				track++
			}
		}
	}
	if track == 0 {
		t.Fatal("thinking pane scrollbar not painted on the first frame")
	}
}

func TestRepeatedThinkingBlocksEachKeepTheirScrollbar(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24
	app.agentChatScroll = make(map[string]agentMainChatScrollSnapshot)
	app.AddOrchestrator(
		"repeat-agent")

	agent := app.AgentMgr.Get("repeat-agent").State
	fillAgentChat(app, "repeat-agent", 6, "history")
	seen := make(map[string]bool)

	for cycle := 0; cycle < 4; cycle++ {
		app.handleAgentEvent(ttypes.AgentEventMsg{Type: "thinking", AgentID: "repeat-agent", Data: thinkingLines(40)})
		paneID := agent.ThinkingPaneID
		if paneID == "" {
			t.Fatalf("cycle %d: thinking was not promoted", cycle)
		}
		if seen[paneID] {
			t.Fatalf("cycle %d reused completed thinking pane %q", cycle, paneID)
		}
		seen[paneID] = true
		for i := 0; i < 4; i++ {
			app.handleAgentEvent(ttypes.AgentEventMsg{Type: "thinking", AgentID: "repeat-agent", Data: thinkingLines(5)})
		}

		app.paintShellFrame()
		sp := app.PaneMgr.Get(paneID)
		gutter := app.subPaneProjections.PaneGutterState(paneID, sp)
		if !gutter.Visible || gutter.Scroll == nil || gutter.Scroll.MaxOffset() <= 0 {
			t.Fatalf("cycle %d pane %q lacks an overflowing scrollbar: %+v", cycle, paneID, gutter)
		}
		layout := app.computeContentLayout()
		mainScrollbarX := app.chatRect.X + app.chatRect.W - layout.PadLR - 1
		paneScrollbarX := mainScrollbarX - 2
		painted := false
		paneRows := 0
		for y, info := range app.chatScreen.ViewportPaneLineMap() {
			if info.PaneID != paneID {
				continue
			}
			paneRows++
			ch := app.mainSurf.Buffer().Get(paneScrollbarX, app.chatRect.Y+y).Char
			if ch == '│' || ch == '┃' {
				painted = true
			}
		}
		if paneRows == 0 || !painted {
			t.Fatalf("cycle %d pane %q has state but no painted scrollbar: rows=%d x=%d", cycle, paneID, paneRows, paneScrollbarX)
		}

		app.handleAgentEvent(ttypes.AgentEventMsg{Type: "stream_token", AgentID: "repeat-agent", Data: "reply"})
		app.handleAgentEvent(ttypes.AgentEventMsg{Type: "done", AgentID: "repeat-agent", Data: map[string]any{
			"content": "reply",
			"reason":  "completed",
		}})
		if agent.ThinkingPaneID != "" {
			t.Fatalf("cycle %d: thinking pane remained active after completion", cycle)
		}
	}
}

func TestThinkingScrollbarPaintsOnFirstFrameAfterReturningToActivePane(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24
	app.agentChatScroll = make(map[string]agentMainChatScrollSnapshot)
	app.AddOrchestrator(
		"scrolled-agent")

	fillAgentChat(app, "scrolled-agent", 12, "history")
	app.paintShellFrame()
	mainScroll := app.chatScreen.ScrollModel()
	mainScroll.SetAutoScroll(false)
	mainScroll.ScrollTo(0)

	app.handleAgentEvent(ttypes.AgentEventMsg{Type: "thinking", AgentID: "scrolled-agent", Data: thinkingLines(50)})
	paneID := app.AgentMgr.Get("scrolled-agent").State.ThinkingPaneID
	if paneID == "" {
		t.Fatal("thinking was not promoted while main chat was scrolled away")
	}
	// Materialize the active pane while it is outside the parent viewport.
	app.paintShellFrame()
	mainScroll.ScrollTo(mainScroll.MaxOffset())
	app.paintShellFrame()

	layout := app.computeContentLayout()
	mainScrollbarX := app.chatRect.X + app.chatRect.W - layout.PadLR - 1
	paneScrollbarX := mainScrollbarX - 2
	paneRows := 0
	painted := false
	for y, info := range app.chatScreen.ViewportPaneLineMap() {
		if info.PaneID != paneID {
			continue
		}
		paneRows++
		ch := app.mainSurf.Buffer().Get(paneScrollbarX, app.chatRect.Y+y).Char
		if ch == '│' || ch == '┃' {
			painted = true
		}
	}
	if paneRows == 0 || !painted {
		t.Fatalf("active pane %q did not paint scrollbar on return: rows=%d x=%d", paneID, paneRows, paneScrollbarX)
	}
}

// TestThinkingPromotionContinuesIntoPane drives the real event flow: a long
// thinking block crosses the promotion threshold, and subsequent thinking must
// keep streaming into the promoted pane rather than restarting inline in the
// main conversation.
func TestThinkingPromotionContinuesIntoPane(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator(
		"promote-agent")

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
		"promote-time")

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
		"promote-a")

	app.AddOrchestrator(
		"promote-b")

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
		"promote-done")

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
	lines := app.subPaneProjections.Render(paneID, sp, 40, 5, 0, false)
	if len(lines) != 2 {
		t.Fatalf("expected summary plus separator after collapse, got %d lines", len(lines))
	}
	plain := lines[0].PlainText()
	if !strings.Contains(plain, "Thought") {
		t.Fatalf("summary missing 'Thought': %q", plain)
	}
	if !strings.Contains(plain, "s") {
		t.Fatalf("summary missing duration: %q", plain)
	}
	if !lines[1].Separator || lines[1].PlainText() != "" {
		t.Fatalf("collapsed summary lacks an empty separator: %+v", lines[1])
	}
}

func TestThinkingPaneDemotesWhenTurnEndsDirectly(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator(
		"direct-done")

	app.ChatAreaHeight = 6

	agent := app.AgentMgr.Get("direct-done").State
	app.handleAgentEvent(ttypes.AgentEventMsg{Type: "thinking", AgentID: "direct-done", Data: thinkingLines(8)})
	paneID := agent.ThinkingPaneID
	if paneID == "" {
		t.Fatal("thinking was not promoted")
	}

	app.handleAgentEvent(ttypes.AgentEventMsg{Type: "done", AgentID: "direct-done", Data: map[string]any{
		"content": "",
		"reason":  "completed",
	}})

	sp := app.PaneMgr.Get(paneID)
	if sp == nil || !sp.Done {
		t.Fatalf("thinking pane remained expanded after direct done: %+v", sp)
	}
	if agent.ThinkingPaneID != "" {
		t.Fatalf("active thinking pane ID remained %q after done", agent.ThinkingPaneID)
	}
	lines := app.subPaneProjections.Render(paneID, sp, 40, 5, 0, false)
	if len(lines) != 2 || !strings.Contains(lines[0].PlainText(), "Thought") || !lines[1].Separator {
		t.Fatalf("direct-done pane did not demote to Thought plus separator: %+v", lines)
	}
}
