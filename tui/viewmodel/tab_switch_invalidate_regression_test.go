package viewmodel

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"crude/tui/editor"
	ttypes "crude/types"
	"crude/workspace"
)

// tabSwitchCacheProbe returns the cached-render state of the given content in
// the projection's retained messages. ok=false when the message is absent.
func tabSwitchCacheProbe(app *App, paneID, content string) (cached bool, ok bool) {
	proj := app.subPaneProjections.GetExisting(paneID)
	if proj == nil {
		return false, false
	}
	for _, msg := range proj.renderer.Messages() {
		if msg.Content == content {
			return msg.CachedLines != nil, true
		}
	}
	return false, false
}

// TestTabSwitchSteadyStateKeepsRenderCache: handleTabSwitch re-derives the
// active view on every tick (syncAgentWindowTabs calls it unconditionally).
// Re-deriving the SAME agent tab must not drop the projection's retained
// render cache; that turned every tick into a full-session re-render.
func TestTabSwitchSteadyStateKeepsRenderCache(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	proj := app.subPaneProjections.Get("root", workspace.PaneAgent)
	if proj == nil {
		t.Fatal("root projection missing")
	}
	const body = "steady state cached body"
	app.appendActiveMessage(ttypes.ChatMessage{
		Role:    ttypes.RoleAssistant,
		Content: body,
		Time:    time.Now(),
	})
	app.handleTabSwitch()
	app.paintShellFrame()

	cached, ok := tabSwitchCacheProbe(app, "root", body)
	if !ok {
		t.Fatal("message not present in projection")
	}
	if !cached {
		t.Fatal("first render did not populate the render cache")
	}

	// Steady state: repeated re-derivations of the same agent tab.
	for range 10 {
		app.handleTabSwitch()
	}
	cached, _ = tabSwitchCacheProbe(app, "root", body)
	if !cached {
		t.Fatal("steady-state handleTabSwitch dropped the render cache")
	}
	if got := app.AgentView.ActiveID(); got != "root" {
		t.Fatalf("active agent drifted: %q", got)
	}
}

// TestTabSwitchAwayAndBackKeepsRenderCache: focus is not source damage.
func TestTabSwitchAwayAndBackKeepsRenderCache(t *testing.T) {
	app := newFullTestApp(t)
	app.EditorState = NewEditorState()
	app.EditorState.Editor = editor.New(app.Theme)
	app.BufferMgr = editor.NewBufferManager()
	app.AddOrchestrator("root")
	const body = "transition cached body"
	app.appendActiveMessage(ttypes.ChatMessage{
		Role:    ttypes.RoleAssistant,
		Content: body,
		Time:    time.Now(),
	})
	app.handleTabSwitch()
	app.paintShellFrame()

	if cached, _ := tabSwitchCacheProbe(app, "root", body); !cached {
		t.Fatal("precondition: cache not populated")
	}

	main := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(main, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.openEditorTab(main); err != nil {
		t.Fatal(err)
	}
	app.handleTabSwitch()
	if cached, _ := tabSwitchCacheProbe(app, "root", body); !cached {
		t.Fatal("editor tab must not invalidate the inactive agent projection")
	}

	// Close the editor tab: falls back to the still-warm agent projection.
	if tab := app.Win.ActiveTab(); tab != nil {
		app.Win.CloseTab(app.Win.ActiveIndex())
	}
	app.handleTabSwitch()
	if got := app.AgentView.ActiveID(); got != "root" {
		t.Fatalf("agent not re-activated after editor close: %q", got)
	}
	if cached, _ := tabSwitchCacheProbe(app, "root", body); !cached {
		t.Fatal("returning to the agent tab dropped a source-valid render cache")
	}
	app.paintShellFrame()
	if cached, _ := tabSwitchCacheProbe(app, "root", body); !cached {
		t.Fatal("post-switch render did not repopulate the cache")
	}
}
