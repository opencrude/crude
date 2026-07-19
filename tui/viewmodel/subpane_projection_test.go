package viewmodel

import (
	"strings"
	"testing"
	"time"

	"crude/provider"
	ttypes "crude/tui/types"
	"crude/workspace"
)

// TestSubPaneProjectionCreation verifies that Get creates a projection with
// a fresh MessageList and that paneType is recorded for scrollbar routing.
func TestSubPaneProjectionCreation(t *testing.T) {
	app := newFullTestApp(t)
	p := app.subPaneProjections.Get("agent-x", workspace.PaneAgent)
	if p == nil || p.msgList == nil {
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
		"sync-agent",
		make(chan provider.UserInput, 1),
		make(chan provider.UserInput, 1),
		make(chan struct{}, 1),
		make(chan string, 1),
	)
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
	if len(proj.msgList.Messages()) != 1 || proj.msgList.Messages()[0].Content != "first" {
		t.Fatalf("projection did not sync: %#v", proj.msgList.Messages())
	}
	versionBefore := proj.msgList.SourceVersion()
	// A second sync with the same version must not bump SourceVersion.
	app.syncAgentMessages("sync-agent", false)
	if proj.msgList.SourceVersion() != versionBefore {
		t.Fatalf("sync with same version mutated SourceVersion: %d -> %d", versionBefore, proj.msgList.SourceVersion())
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
	lines := app.subPaneProjections.Render("think-1", sp, 40, 5, 0)
	if len(lines) == 0 {
		t.Fatal("Render produced zero lines")
	}
	// First line is the header; it should contain the title "Thinking".
	if !containsStr(lines[0], "Thinking") {
		t.Fatalf("header line %q does not contain 'Thinking'", lines[0])
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
	lines := app.subPaneProjections.Render("agent-1", sp, 40, 5, 0)
	if len(lines) < 2 {
		t.Fatalf("expected at least header + 1 tool line, got %d lines", len(lines))
	}
	// First line is the header; it should contain the agent title.
	if !containsStr(lines[0], "my-agent") {
		t.Fatalf("header %q does not contain 'my-agent'", lines[0])
	}
	// Some tool line should reference read_file.
	found := false
	for _, l := range lines[1:] {
		if containsStr(l, "read_file") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no tool line references 'read_file'")
	}
}

// TestSubPaneProjectionRenderDoneSummary verifies that a Done pane renders
// only the summary line.
func TestSubPaneProjectionRenderDoneSummary(t *testing.T) {
	app := newFullTestApp(t)
	sp := app.PaneMgr.GetOrCreate("done-1", workspace.PaneAgent, "my-agent")
	sp.TokenCount = 5200
	sp.Collapse()
	app.subPaneProjections.Get("done-1", workspace.PaneAgent)
	lines := app.subPaneProjections.Render("done-1", sp, 40, 5, 0)
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 summary line, got %d", len(lines))
	}
	if !containsStr(lines[0], "my-agent") {
		t.Fatalf("summary %q does not contain the agent title", lines[0])
	}
	if !containsStr(lines[0], "for") {
		t.Fatalf("summary %q does not contain 'for'", lines[0])
	}
}

// TestSubPaneProjectionScrollbarViewAfterCompactScroll verifies that
// compact scroll up/down affects the scrollbar view.
func TestSubPaneProjectionScrollbarViewAfterCompactScroll(t *testing.T) {
	app := newFullTestApp(t)
	sp := app.PaneMgr.GetOrCreate("scroll-1", workspace.PaneAgent, "agent")
	app.subPaneProjections.Get("scroll-1", workspace.PaneAgent)
	// Add enough tool messages to exceed a 2-row viewport.
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
	// Render once to populate compactContentH/compactVisibleH.
	app.subPaneProjections.Render("scroll-1", sp, 30, 3, 0)
	if !app.subPaneProjections.CanScrollCompact("scroll-1") {
		t.Fatalf("expected CanScrollCompact=true after rendering 5 tools in a 2-row viewport")
	}
	// Scroll up should decrement compactScrollY and disable auto-scroll.
	app.subPaneProjections.ScrollCompactDown("scroll-1")
	app.subPaneProjections.ScrollCompactUp("scroll-1")
	// Scrollbar view should be non-empty (compact scrollbar is visible).
	view := app.subPaneProjections.ScrollbarView("scroll-1", sp)
	if view == "" {
		t.Fatal("ScrollbarView returned empty for a compact pane with content")
	}
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
	app.subPaneProjections.Render("cache-1", sp, 30, 5, 0)
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
	app.subPaneProjections.Render("cache-1", sp, 30, 5, 0)
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
		"promote-agent",
		make(chan provider.UserInput, 1),
		make(chan provider.UserInput, 1),
		make(chan struct{}, 1),
		make(chan string, 1),
	)
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
	lines := app.subPaneProjections.Render(paneID, sp, 40, 5, 0)
	if len(lines) == 0 {
		t.Fatal("thinking pane rendered zero lines")
	}
	// Collapse and verify the summary path.
	sp.Collapse()
	doneLines := app.subPaneProjections.Render(paneID, sp, 40, 5, 0)
	if len(doneLines) != 1 {
		t.Fatalf("expected 1 summary line after Collapse, got %d", len(doneLines))
	}
}

func containsStr(s, sub string) bool {
	return strings.Contains(s, sub)
}