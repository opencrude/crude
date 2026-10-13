package viewmodel

import (
	"strings"
	"testing"
	"time"

	"crude/types"
	"crude/types/reasoning"
	"crude/workspace"
)

func TestThinkingPaneIDsAreNotReusedAfterRemoval(t *testing.T) {
	app := newFullTestApp(t)
	first := app.nextThinkingPaneID("same-agent")
	second := app.nextThinkingPaneID("same-agent")
	if first == second {
		t.Fatalf("unmounted pane identity was reused: %q", first)
	}
}

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
	app.AddOrchestrator(
		"probe-agent")

	node := app.ws.AgentMgr().Get("probe-agent")
	agent := node.State
	app.handleAgentEvent(types.AgentEventMsg{Type: "thinking", AgentID: "probe-agent", Data: reasoning.Delta{Text: thinkingLines(40)}})
	if app.thinkingPaneID(agent) == "" {
		t.Fatal("thinking was not promoted")
	}
	for i := 0; i < 20; i++ {
		app.handleAgentEvent(types.AgentEventMsg{Type: "thinking", AgentID: "probe-agent", Data: reasoning.Delta{Text: thinkingLines(5)}})
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
	app.AddOrchestrator(
		"repeat-agent")

	agent := app.ws.AgentMgr().Get("repeat-agent").State
	fillAgentChat(app, "repeat-agent", 6, "history")
	seen := make(map[string]bool)

	for cycle := 0; cycle < 4; cycle++ {
		app.handleAgentEvent(types.AgentEventMsg{Type: "turn_started", AgentID: "repeat-agent", Data: types.TurnIdentity{Generation: uint64(cycle + 1), ModelID: "model"}})
		app.handleAgentEvent(types.AgentEventMsg{Type: "thinking", AgentID: "repeat-agent", Data: reasoning.Delta{Text: thinkingLines(40)}})
		paneID := app.thinkingPaneID(agent)
		if paneID == "" {
			t.Fatalf("cycle %d: thinking was not promoted", cycle)
		}
		if seen[paneID] {
			t.Fatalf("cycle %d reused completed thinking pane %q", cycle, paneID)
		}
		seen[paneID] = true
		for i := 0; i < 4; i++ {
			app.handleAgentEvent(types.AgentEventMsg{Type: "thinking", AgentID: "repeat-agent", Data: reasoning.Delta{Text: thinkingLines(5)}})
		}

		app.paintShellFrame()
		sp := app.Get(paneID)
		gutter := app.subPaneProjections.ForOwner(app.AgentView.ActiveID()).PaneGutterState(paneID, sp)
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

		app.handleAgentEvent(types.AgentEventMsg{Type: "stream_token", AgentID: "repeat-agent", Data: "reply"})
		app.handleAgentEvent(types.AgentEventMsg{Type: "done", AgentID: "repeat-agent", Data: map[string]any{
			"content": "reply",
			"reason":  "completed",
		}})
		if app.thinkingPaneID(agent) != "" {
			t.Fatalf("cycle %d: thinking pane remained active after completion", cycle)
		}
	}
}

func TestThinkingScrollbarPaintsOnFirstFrameAfterReturningToActivePane(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 80
	app.height = 24
	app.AddOrchestrator(
		"scrolled-agent")

	fillAgentChat(app, "scrolled-agent", 12, "history")
	app.paintShellFrame()
	mainScroll := app.chatScreen.ScrollModel()
	mainScroll.SetAutoScroll(false)
	mainScroll.ScrollTo(0)

	app.handleAgentEvent(types.AgentEventMsg{Type: "thinking", AgentID: "scrolled-agent", Data: reasoning.Delta{Text: thinkingLines(50)}})
	paneID := app.thinkingPaneID(app.ws.AgentMgr().Get("scrolled-agent").State)
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

	node := app.ws.AgentMgr().Get("promote-agent")
	if node == nil || node.State == nil {
		t.Fatal("agent not created")
	}
	agent := node.State

	// Stream enough thinking to exceed the 5-line threshold.
	app.handleAgentEvent(types.AgentEventMsg{Type: "thinking", AgentID: "promote-agent", Data: reasoning.Delta{Text: thinkingLines(8)}})

	if app.thinkingPaneID(agent) == "" {
		t.Fatal("thinking was not promoted to a pane")
	}

	// After promotion, subsequent thinking must continue into the pane,
	// not back into the main conversation.
	app.handleAgentEvent(types.AgentEventMsg{Type: "thinking", AgentID: "promote-agent", Data: reasoning.Delta{Text: "EXTRA-THINKING"}})

	thought, active := agent.Messages.ActiveThinking()
	if !active || !strings.Contains(thought.Content, "EXTRA-THINKING") {
		t.Fatal("canonical thought lost streamed continuation")
	}

	sp := app.Get(app.thinkingPaneID(agent))
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

	node := app.ws.AgentMgr().Get("promote-time")
	if node == nil || node.State == nil {
		t.Fatal("agent not created")
	}
	agent := node.State

	// First thinking chunk seeds the block with its start time.
	app.handleAgentEvent(types.AgentEventMsg{Type: "thinking", AgentID: "promote-time", Data: reasoning.Delta{Text: thinkingLines(8)}})
	if app.thinkingPaneID(agent) == "" {
		t.Fatal("thinking was not promoted")
	}
	sp := app.Get(app.thinkingPaneID(agent))
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
		app.handleAgentEvent(types.AgentEventMsg{Type: "thinking", AgentID: id, Data: reasoning.Delta{Text: thinkingLines(8)}})
		agent := app.ws.AgentMgr().Get(id).State
		if app.thinkingPaneID(agent) == "" {
			t.Fatalf("agent %s: thinking not promoted", id)
		}
	}

	a := app.ws.AgentMgr().Get("promote-a").State
	b := app.ws.AgentMgr().Get("promote-b").State
	if app.thinkingPaneID(a) == app.thinkingPaneID(b) {
		t.Fatalf("two agents share the same thinking pane: %q", app.thinkingPaneID(a))
	}
}

// TestThinkingPaneDemotesToSummary verifies that once thinking finishes, the
// promoted body returns to an ordinary thought with its identity and duration.
func TestThinkingPaneDemotesToSummary(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator(
		"promote-done")

	app.ChatAreaHeight = 6

	node := app.ws.AgentMgr().Get("promote-done")
	agent := node.State

	app.handleAgentEvent(types.AgentEventMsg{Type: "thinking", AgentID: "promote-done", Data: reasoning.Delta{Text: thinkingLines(8)}})
	if app.thinkingPaneID(agent) == "" {
		t.Fatal("thinking was not promoted")
	}
	paneID := app.thinkingPaneID(agent)
	// Thinking finishes: streaming begins.
	time.Sleep(20 * time.Millisecond)
	app.handleAgentEvent(types.AgentEventMsg{Type: "stream_token", AgentID: "promote-done", Data: "hello"})

	if app.ws.PaneMgr().Has(paneID) || app.subPaneProjections.Has(paneID) {
		t.Fatal("completed thinking kept a competing pane owner")
	}
	thought := agent.Messages.Messages()[0]
	if thought.Role != types.RoleThinking || thought.ThinkingActive || thought.ThinkingDuration <= 0 || thought.Content != thinkingLines(8) || thought.SubPaneID != "" {
		t.Fatalf("thought was not restored: %+v", thought)
	}
}

func TestThinkingPaneDemotesWhenTurnEndsDirectly(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator(
		"direct-done")

	app.ChatAreaHeight = 6

	agent := app.ws.AgentMgr().Get("direct-done").State
	app.handleAgentEvent(types.AgentEventMsg{Type: "thinking", AgentID: "direct-done", Data: reasoning.Delta{Text: thinkingLines(8)}})
	paneID := app.thinkingPaneID(agent)
	if paneID == "" {
		t.Fatal("thinking was not promoted")
	}

	app.handleAgentEvent(types.AgentEventMsg{Type: "done", AgentID: "direct-done", Data: map[string]any{
		"content": "",
		"reason":  "completed",
	}})

	if app.ws.PaneMgr().Has(paneID) || app.subPaneProjections.Has(paneID) {
		t.Fatal("thinking pane survived direct completion")
	}
	if app.thinkingPaneID(agent) != "" {
		t.Fatalf("active thinking pane ID remained %q after done", app.thinkingPaneID(agent))
	}
	thought := agent.Messages.Messages()[0]
	if thought.Role != types.RoleThinking || thought.ThinkingActive || thought.Content != thinkingLines(8) {
		t.Fatalf("direct completion lost thought: %+v", thought)
	}
}
