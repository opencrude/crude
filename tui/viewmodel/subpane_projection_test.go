package viewmodel

import (
	"strings"
	"testing"
	"time"

	"crude/tui/chatprojection"
	ttypes "crude/tui/types"
	"crude/tui/ui"
	"crude/workspace"
)

// TestSubPaneProjectionCreation verifies that Get creates a projection with
// a fresh MessageList and that paneType is recorded for scrollbar routing.
func TestSubPaneProjectionCreation(t *testing.T) {
	app := newFullTestApp(t)
	p := app.subPaneProjections.Get("agent-x", workspace.PaneAgent)
	if p == nil || p.renderer == nil {
		t.Fatal("Get did not create a projection with a MessageList")
	}
	if p.paneType != workspace.PaneAgent {
		t.Fatalf("paneType = %v, want PaneAgent", p.paneType)
	}
	// A second Get updates the type but returns the same projection.
	p2 := app.subPaneProjections.Get("agent-x", workspace.PaneThinking)
	if p2 != p {
		t.Fatal("Get returned a different projection for the same ID")
	}
	if p.paneType != workspace.PaneThinking {
		t.Fatalf("paneType not updated to PaneThinking")
	}
}

// TestSubPaneProjectionRemove verifies that Remove clears the projection.
func TestSubPaneProjectionRemove(t *testing.T) {
	app := newFullTestApp(t)
	app.subPaneProjections.Get("gone", workspace.PaneAgent)
	if !app.subPaneProjections.Has("gone") {
		t.Fatal("Get did not register the projection")
	}
	app.subPaneProjections.Remove("gone")
	if app.subPaneProjections.Has("gone") {
		t.Fatal("Remove did not clear the projection")
	}
}

// TestSubPaneProjectionSyncPreservesByVersion verifies that a second sync
// with the same Conversation version is a no-op (preserves retained caches).
func TestSubPaneProjectionSyncPreservesByVersion(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator(
		"sync-agent")

	// Append a message to the canonical Conversation and sync.
	node := app.AgentMgr.Get("sync-agent")
	if node == nil {
		t.Fatal("orchestrator not registered")
	}
	node.State.Messages.AppendMessage(workspace.Message{
		Role:    ttypes.RoleAssistant,
		Content: "first",
		Time:    time.Now(),
	})
	app.syncAgentMessages("sync-agent", false)
	proj := app.subPaneProjections.GetExisting("sync-agent")
	if proj == nil {
		t.Fatal("projection not created")
	}
	if len(proj.renderer.Messages()) != 1 || proj.renderer.Messages()[0].Content != "first" {
		t.Fatalf("projection did not sync: %#v", proj.renderer.Messages())
	}
	versionBefore := proj.renderer.SourceVersion()
	// A second sync with the same version must not bump SourceVersion.
	app.syncAgentMessages("sync-agent", false)
	if proj.renderer.SourceVersion() != versionBefore {
		t.Fatalf("sync with same version mutated SourceVersion: %d -> %d", versionBefore, proj.renderer.SourceVersion())
	}
}

// TestSubPaneProjectionRenderThinkingPane verifies that Render produces a
// header line plus content lines for a PaneThinking pane with messages.
func TestSubPaneProjectionRenderThinkingPane(t *testing.T) {
	app := newFullTestApp(t)
	sp := app.PaneMgr.GetOrCreate("think-1", workspace.PaneThinking, "Thinking")
	app.subPaneProjections.Get("think-1", workspace.PaneThinking)
	sp.Messages.AppendMessage(workspace.Message{
		Role:    ttypes.RoleAssistant,
		Content: "pondering the universe",
		Time:    time.Now(),
	})
	app.syncPaneMessages("think-1", false)
	lines := app.subPaneProjections.Render("think-1", sp, 40, 5, 0, true)
	if len(lines) == 0 {
		t.Fatal("Render produced zero lines")
	}
	// First line is the header; it should contain the title "Thinking".
	if !subPanePlainContains(lines, "Thinking") {
		t.Fatalf("header line does not contain 'Thinking'")
	}
}

// TestSubPaneProjectionRenderAgentPaneCompact verifies that Render produces
// a header + tool lines for a PaneAgent pane with tool messages.
func TestSubPaneProjectionRenderAgentPaneCompact(t *testing.T) {
	app := newFullTestApp(t)
	sp := app.PaneMgr.GetOrCreate("agent-1", workspace.PaneAgent, "my-agent")
	app.subPaneProjections.Get("agent-1", workspace.PaneAgent)
	sp.Messages.AppendMessage(workspace.Message{
		Role:             ttypes.RoleTool,
		ToolName:         "read_file",
		ToolArgs:         "main.go",
		ToolStatus:       "success",
		ToolPreviewPhase: "done",
		Time:             time.Now(),
	})
	app.syncPaneMessages("agent-1", false)
	lines := app.subPaneProjections.Render("agent-1", sp, 40, 5, 0, true)
	if len(lines) < 2 {
		t.Fatalf("expected at least header + 1 tool line, got %d lines", len(lines))
	}
	// First line is the header; it should contain the agent title.
	if !subPanePlainContains(lines, "my-agent") {
		t.Fatal("header does not contain 'my-agent'")
	}
	if !subPanePlainContains(lines, "Read main.go") {
		t.Fatal("no tool line names the read action and operand")
	}
	sp.Collapse()
	expanded := app.subPaneProjections.Render("agent-1", sp, 40, 5, 0, true)
	if len(expanded) == 0 || strings.TrimSpace(expanded[len(expanded)-1].PlainText()) == "" {
		t.Fatalf("expanded completed pane must end on content: %+v", expanded)
	}
}

// TestSubPaneProjectionRenderDoneSummary verifies that a Done pane renders
// its summary only. The transcript owns inter-item spacing.
func TestSubPaneProjectionRenderDoneSummary(t *testing.T) {
	app := newFullTestApp(t)
	sp := app.PaneMgr.GetOrCreate("done-1", workspace.PaneAgent, "my-agent")
	sp.TokenCount = 5200
	sp.Collapse()
	app.subPaneProjections.Get("done-1", workspace.PaneAgent)
	lines := app.subPaneProjections.Render("done-1", sp, 40, 5, 0, false)
	if len(lines) != 1 {
		t.Fatalf("expected summary only, got %d lines", len(lines))
	}
	if !subPanePlainContains(lines, "my-agent") {
		t.Fatalf("summary does not contain the agent title")
	}
	if !subPanePlainContains(lines, "for") {
		t.Fatalf("summary does not contain 'for'")
	}
}

// TestSubPaneProjectionCompactScrollModel verifies compact panes use ScrollModel.
func TestSubPaneProjectionCompactScrollModel(t *testing.T) {
	app := newFullTestApp(t)
	sp := app.PaneMgr.GetOrCreate("scroll-1", workspace.PaneAgent, "agent")
	app.subPaneProjections.Get("scroll-1", workspace.PaneAgent)
	for i := 0; i < 5; i++ {
		sp.Messages.AppendMessage(workspace.Message{
			Role:             ttypes.RoleTool,
			ToolName:         "read_file",
			ToolArgs:         "f" + string(rune('a'+i)) + ".go",
			ToolStatus:       "success",
			ToolPreviewPhase: "done",
			Time:             time.Now(),
		})
	}
	app.syncPaneMessages("scroll-1", false)
	app.subPaneProjections.Render("scroll-1", sp, 30, 3, 0, true)
	scroll := app.subPaneProjections.ScrollModel("scroll-1")
	if scroll == nil || scroll.MaxOffset() <= 0 {
		t.Fatalf("expected scrollable compact pane, scroll=%v max=%d", scroll, scrollMax(scroll))
	}
	scroll.SetAutoScroll(false)
	before := scroll.Offset()
	scroll.ScrollBy(-1)
	if scroll.Offset() >= before {
		t.Fatalf("ScrollBy(-1) should decrease offset before=%d after=%d", before, scroll.Offset())
	}
	st := app.subPaneProjections.PaneGutterState("scroll-1", sp)
	if !st.Visible || st.Scroll != scroll {
		t.Fatalf("PaneGutterState should expose visible scroll model: %+v", st)
	}
}

func scrollMax(s *ui.ScrollModel) int {
	if s == nil {
		return 0
	}
	return s.MaxOffset()
}

// TestSubPaneProjectionToolLineCacheInvalidation verifies that a version
// bump invalidates the compact tool-line cache.
func TestSubPaneProjectionToolLineCacheInvalidation(t *testing.T) {
	app := newFullTestApp(t)
	sp := app.PaneMgr.GetOrCreate("cache-1", workspace.PaneAgent, "agent")
	app.subPaneProjections.Get("cache-1", workspace.PaneAgent)
	sp.Messages.AppendMessage(workspace.Message{
		Role:             ttypes.RoleTool,
		ToolName:         "read_file",
		ToolArgs:         "first.go",
		ToolStatus:       "success",
		ToolPreviewPhase: "done",
		Time:             time.Now(),
	})
	app.syncPaneMessages("cache-1", false)
	// First render populates the cache.
	app.subPaneProjections.Render("cache-1", sp, 30, 5, 0, true)
	proj := app.subPaneProjections.GetExisting("cache-1")
	cachedVersion := proj.compactMsgVersion
	cachedCount := proj.compactMsgCount
	if cachedCount != 1 {
		t.Fatalf("expected cached count 1, got %d", cachedCount)
	}
	// Append another tool message; sync bumps the MessageList version.
	sp.Messages.AppendMessage(workspace.Message{
		Role:             ttypes.RoleTool,
		ToolName:         "write_file",
		ToolArgs:         "second.go",
		ToolStatus:       "streaming",
		ToolPreviewPhase: "streaming",
		Time:             time.Now(),
	})
	app.syncPaneMessages("cache-1", false)
	app.subPaneProjections.Render("cache-1", sp, 30, 5, 0, true)
	if proj.compactMsgVersion == cachedVersion {
		t.Fatal("compact cache version did not bump after sync")
	}
	if proj.compactMsgCount != 2 {
		t.Fatalf("expected cached count 2 after sync, got %d", proj.compactMsgCount)
	}
}

// TestThinkingPanePromotionRoundTrip verifies the full promotion flow:
// thinking content accumulates on the agent, a threshold promotes it to a
// PaneThinking, and completion collapses the pane.
func TestThinkingPanePromotionRoundTrip(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator(
		"promote-agent")

	node := app.AgentMgr.Get("promote-agent")
	if node == nil || node.State == nil {
		t.Fatal("agent not created")
	}
	// Simulate the promotion directly: create a thinking pane, move content.
	agent := node.State
	agent.Messages.AppendThinking("deep thought 1\n")
	agent.Messages.AppendThinking("deep thought 2\n")
	app.syncAgentMessages("promote-agent", false)
	// Force promote by setting a ThinkingPaneID and appending the marker.
	paneID := "thinking-test"
	sp := app.PaneMgr.GetOrCreate(paneID, workspace.PaneThinking, "Thinking")
	app.subPaneProjections.Get(paneID, workspace.PaneThinking)
	agent.ThinkingPaneID = paneID
	// Render the thinking pane; it should produce content.
	app.syncPaneMessages(paneID, false)
	lines := app.subPaneProjections.Render(paneID, sp, 40, 5, 0, true)
	if len(lines) == 0 {
		t.Fatal("thinking pane rendered zero lines")
	}
	// Collapse and verify the summary path.
	sp.Collapse()
	doneLines := app.subPaneProjections.Render(paneID, sp, 40, 5, 0, false)
	if len(doneLines) != 1 {
		t.Fatalf("expected summary only after Collapse, got %+v", doneLines)
	}
}

// TestThinkingSubpaneNativeRowsPreserveScrollPad pins thinking-subpane
// row-for-row output (including gravity topPad stripping and a non-zero
// scroll offset) and asserts content rows stay native.
func TestThinkingSubpaneNativeRowsPreserveScrollPad(t *testing.T) {
	app := newFullTestApp(t)
	sp := app.PaneMgr.GetOrCreate("think-native", workspace.PaneThinking, "Thinking")
	app.subPaneProjections.Get("think-native", workspace.PaneThinking)
	sp.Messages.AppendMessage(workspace.Message{
		Role:    ttypes.RoleThinking,
		Content: "thought line one\nthought line two\nthought line three",
		Time:    time.Now(),
	})
	app.syncPaneMessages("think-native", false)

	const width = 40
	const tallMaxH = 12 // taller than content → RenderVisible gravity pad, then topPad strip
	tall := app.subPaneProjections.Render("think-native", sp, width, tallMaxH, 0, true)
	if len(tall) < 2 {
		t.Fatalf("expected header+content, got %d lines", len(tall))
	}
	tallPlain := make([]string, len(tall))
	for i, l := range tall {
		tallPlain[i] = l.PlainText()
		if i == 0 {
			if l.Native == nil || !strings.Contains(tallPlain[0], "Thinking") {
				t.Fatalf("header line=%#v plain=%q", l, tallPlain[0])
			}
			continue
		}
		if l.Native == nil {
			t.Fatalf("content row %d degraded to ANSI Text=%q", i, l.Text)
		}
		if l.Native.PreferANSI() {
			t.Fatalf("content row %d PreferANSI", i)
		}
	}
	// Gravity topPad must be stripped: first content row is not an empty pad.
	if tallPlain[1] == "" && (len(tall) < 3 || !strings.Contains(strings.Join(tallPlain[2:], "\n"), "thought line one")) {
		// blank after thinking header is legitimate; body must still appear
		if !strings.Contains(strings.Join(tallPlain, "\n"), "thought line one") {
			t.Fatalf("topPad strip lost body: %#v", tallPlain)
		}
	}
	if !strings.Contains(strings.Join(tallPlain, "\n"), "thought line one") {
		t.Fatalf("missing body after topPad: %#v", tallPlain)
	}

	// Overflow viewport + non-zero scroll: pin which content rows appear.
	spOverflow := app.PaneMgr.GetOrCreate("think-scroll", workspace.PaneThinking, "Thinking")
	app.subPaneProjections.Get("think-scroll", workspace.PaneThinking)
	var b strings.Builder
	for i := 0; i < 12; i++ {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("scroll-line-")
		b.WriteByte(byte('a' + i))
	}
	spOverflow.Messages.AppendMessage(workspace.Message{
		Role:    ttypes.RoleThinking,
		Content: b.String(),
		Time:    time.Now(),
	})
	app.syncPaneMessages("think-scroll", false)

	const shortMaxH = 4
	// Establish heights / bind scroll.
	_ = app.subPaneProjections.Render("think-scroll", spOverflow, width, shortMaxH, 0, true)
	scroll := app.subPaneProjections.ScrollModel("think-scroll")
	if scroll == nil {
		t.Fatal("missing scroll model")
	}
	scroll.SetAutoScroll(false)
	scroll.ScrollTo(2)
	scrolled := app.subPaneProjections.Render("think-scroll", spOverflow, width, shortMaxH, 0, true)
	if len(scrolled) < 2 {
		t.Fatalf("scrolled render too short: %#v", scrolled)
	}
	scrolledPlain := make([]string, 0, len(scrolled)-1)
	for i, l := range scrolled {
		if i == 0 {
			continue
		}
		if l.Native == nil {
			t.Fatalf("scrolled content row %d not native: %q", i, l.Text)
		}
		scrolledPlain = append(scrolledPlain, l.PlainText())
	}
	joined := strings.Join(scrolledPlain, "\n")
	if !strings.Contains(joined, "scroll-line-") {
		t.Fatalf("scrolled window empty: %#v", scrolledPlain)
	}
	scroll.ScrollTo(0)
	top := app.subPaneProjections.Render("think-scroll", spOverflow, width, shortMaxH, 0, true)
	topPlain := make([]string, 0, len(top)-1)
	for i, l := range top {
		if i == 0 {
			continue
		}
		topPlain = append(topPlain, l.PlainText())
	}
	if strings.Join(topPlain, "\n") == joined {
		t.Fatalf("offset 0 and offset 2 produced identical content windows: %#v", scrolledPlain)
	}
	scroll.ScrollTo(2)
	// Re-render at same offset must be stable (row-for-row pin).
	again := app.subPaneProjections.Render("think-scroll", spOverflow, width, shortMaxH, 0, true)
	if len(again) != len(scrolled) {
		t.Fatalf("unstable length %d vs %d", len(again), len(scrolled))
	}
	for i := range scrolled {
		if again[i].PlainText() != scrolled[i].PlainText() {
			t.Fatalf("row %d plain changed %q -> %q", i, scrolled[i].PlainText(), again[i].PlainText())
		}
		if (again[i].Native == nil) != (scrolled[i].Native == nil) {
			t.Fatalf("row %d native presence changed", i)
		}
	}
}

func TestExpandedCompletedThinkingPaneRetainsScrollbar(t *testing.T) {
	app := newTestApp(t)
	sp := app.PaneMgr.GetOrCreate("thinking-scroll", workspace.PaneThinking, "Thinking")
	proj := app.subPaneProjections.Get("thinking-scroll", workspace.PaneThinking)
	proj.renderer.AppendMessage(ttypes.ChatMessage{
		InternalID: "thinking-body", Role: ttypes.RoleThinking,
		Content: strings.Repeat("reasoning body that wraps across rows ", 20),
	})
	sp.Collapse()
	_ = app.subPaneProjections.Render("thinking-scroll", sp, 36, 4, 0, true)
	gutter := app.subPaneProjections.PaneGutterState("thinking-scroll", sp)
	if !gutter.Visible || gutter.Scroll == nil || gutter.Scroll.MaxOffset() <= 0 {
		t.Fatalf("expanded completed pane gutter = %+v", gutter)
	}
	if got := app.subPaneProjections.ScrollbarLineCount("thinking-scroll", sp); got <= 4 {
		t.Fatalf("expanded completed pane content height = %d", got)
	}
}

func subPanePlainContains(lines []chatprojection.SubPaneLine, sub string) bool {
	for _, l := range lines {
		if strings.Contains(l.PlainText(), sub) {
			return true
		}
	}
	return false
}

func TestSubPaneEmbeddingOwnersRetainIndependentPresentation(t *testing.T) {
	app := newTestApp(t)
	root := app.subPaneProjections
	sp := app.PaneMgr.GetOrCreate("shared-child", workspace.PaneAgent, "child")
	for i := 0; i < 8; i++ {
		sp.Messages.AppendMessage(workspace.Message{
			Role: ttypes.RoleTool, ToolName: "read_file", ToolArgs: string(rune('a'+i)) + ".go",
			ToolStatus: "success", ToolPreviewPhase: "done",
		})
	}
	canonical := root.Get(sp.ID, sp.Type)
	left, right := root.ForOwner("left"), root.ForOwner("right")
	if root.ForOwner("left") != left {
		t.Fatal("embedding registry was not retained")
	}
	left.Render(sp.ID, sp, 30, 3, 0, true)
	leftScroll := left.ScrollModel(sp.ID)
	leftScroll.SetAutoScroll(false)
	leftScroll.ScrollTo(0)
	before := left.Render(sp.ID, sp, 30, 3, 0, true)
	right.Render(sp.ID, sp, 70, 6, 0, true)
	if left.ScrollModel(sp.ID) == right.ScrollModel(sp.ID) || left.GetExisting(sp.ID).renderer == canonical.renderer {
		t.Fatal("embedding projection shares canonical or other owner's presentation")
	}
	if leftScroll.Viewport() != 2 || leftScroll.Offset() != 0 || left.GetExisting(sp.ID).compactCacheWidth != 30 {
		t.Fatal("other owner overwrote left geometry, offset or cache")
	}
	if !subPanePlainContains(before, "Read a.go") || subPanePlainContains(before, "Read h.go") {
		t.Fatalf("left scroll window incorrect: %+v", before)
	}
	if !subPanePlainContains(right.Render(sp.ID, sp, 70, 6, 0, true), "Read h.go") {
		t.Fatal("right did not retain independent bottom window")
	}
	version := left.GetExisting(sp.ID).renderer.Version()
	left.Render(sp.ID, sp, 30, 3, 0, true)
	if left.GetExisting(sp.ID).renderer.Version() != version {
		t.Fatal("unchanged source caused projection republish")
	}
	sp.Messages.AppendMessage(workspace.Message{Role: ttypes.RoleTool, ToolName: "read_file", ToolArgs: "new.go", ToolStatus: "success", ToolPreviewPhase: "done"})
	if !subPanePlainContains(right.Render(sp.ID, sp, 70, 6, 0, true), "Read new.go") {
		t.Fatal("canonical source append missing from embedding")
	}
	left.Render(sp.ID, sp, 30, 3, 0, true)
	if leftScroll.Offset() != 0 || leftScroll.AutoScroll() {
		t.Fatal("source append reset disarmed embedding scroll")
	}
	sp.Collapse()
	right.Render(sp.ID, sp, 70, 6, 0, false)
	if right.PaneGutterState(sp.ID, sp).Visible || !left.PaneGutterState(sp.ID, sp).Visible {
		t.Fatal("one owner's disclosure changed another owner's gutter")
	}
	root.SetStreaming(sp.ID, true)
	root.SetActiveTyping(sp.ID, true)
	root.SetTheme(app.Theme)
	root.TickAll(400)
	root.InvalidateWidthProfile()
	root.InvalidateAllMessages(sp.ID)
	root.Remove(sp.ID)
	if left.Has(sp.ID) || right.Has(sp.ID) || root.Has(sp.ID) {
		t.Fatal("source removal retained embedding projection")
	}
}

func TestThinkingEmbeddingOwnersSyncAndScrollIndependently(t *testing.T) {
	app := newTestApp(t)
	sp := app.PaneMgr.GetOrCreate("shared-thought", workspace.PaneThinking, "Thinking")
	sp.Messages.AppendMessage(workspace.Message{Role: ttypes.RoleThinking, Content: "first\nsecond\nthird\nfourth\nfifth\nsixth"})
	left, right := app.subPaneProjections.ForOwner("left"), app.subPaneProjections.ForOwner("right")
	left.Render(sp.ID, sp, 30, 3, 0, true)
	left.ScrollModel(sp.ID).SetAutoScroll(false)
	left.ScrollModel(sp.ID).ScrollTo(0)
	before := left.Render(sp.ID, sp, 30, 3, 0, true)
	right.Render(sp.ID, sp, 60, 8, 0, true)
	after := left.Render(sp.ID, sp, 30, 3, 0, true)
	if len(before) != len(after) {
		t.Fatal("other embedding changed thinking viewport")
	}
	for i := range before {
		if before[i].PlainText() != after[i].PlainText() {
			t.Fatal("other embedding moved thinking rows")
		}
	}
	// Replacement conversations may have the same version, but not the same source.
	sp.Messages = workspace.NewConversation()
	sp.Messages.AppendMessage(workspace.Message{Role: ttypes.RoleThinking, Content: "replacement thought"})
	if !subPanePlainContains(left.Render(sp.ID, sp, 30, 8, 0, true), "replacement thought") {
		t.Fatal("same-version replacement retained stale source")
	}
}
