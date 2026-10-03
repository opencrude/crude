package viewmodel

import (
	"testing"

	"crude/workspace"
)

// TestLauncherOpen_PaintsWithoutWorkspaceScan: openLauncher must never run
// the workspace DB scan on the update goroutine. Immediately after open, the
// launcher shows only the fixed actions (SetData(cwd, nil)) and a load is
// in flight (gen bumped); the listing arrives later via launcherWorkspacesMsg.
func TestLauncherOpen_PaintsWithoutWorkspaceScan(t *testing.T) {
	app := newTestApp(t)
	app.AgentMgr.ClearActive()
	app.AgentMgr.Remove("agent-1")
	app.width, app.height = 100, 30
	app.openLauncher()
	app.paintShellFrame()
	if !app.shellSlotActive(shellLauncherSlot) {
		t.Fatal("precondition: launcher slot active")
	}
	if app.launcherLoadGen.Load() == 0 {
		t.Fatal("openLauncher must kick an async workspace load")
	}
	if app.launcherScreen.TotalItems() != 2 {
		t.Fatalf("launcher must show only fixed actions until the async listing arrives, got %d items", app.launcherScreen.TotalItems())
	}
}

// TestLauncherAsyncLoad_AppliesLatestGen: the async listing applies only for
// the newest generation, only while the launcher is mounted, and lands in
// the workspace list without resetting the user's filter state.
func TestLauncherAsyncLoad_AppliesLatestGen(t *testing.T) {
	app := newTestApp(t)
	app.AgentMgr.ClearActive()
	app.AgentMgr.Remove("agent-1")
	app.openLauncher()
	gen := app.launcherLoadGen.Load()

	infos := []workspace.WorkspaceInfo{
		{ID: "w1", ProjectDir: "C:\\one", SessionCount: 2},
		{ID: "w2", ProjectDir: "C:\\two", SessionCount: 0},
	}

	// A stale generation must be dropped.
	_, _ = app.Update(launcherWorkspacesMsg{Screen: app.launcherScreen, Cwd: ".", Infos: infos, Gen: gen - 1})
	if app.launcherScreen.TotalItems() != 2 {
		t.Fatalf("stale generation applied; items=%d", app.launcherScreen.TotalItems())
	}

	// The current generation applies.
	_, _ = app.Update(launcherWorkspacesMsg{Screen: app.launcherScreen, Cwd: ".", Infos: infos, Gen: gen})
	if app.launcherScreen.TotalItems() != 4 {
		t.Fatalf("current generation not applied; items=%d", app.launcherScreen.TotalItems())
	}

	// A second kick invalidates the first: an old-gen message after a new
	// kick must not apply.
	app.openLauncher()
	gen2 := app.launcherLoadGen.Load()
	if gen2 == gen {
		t.Fatal("openLauncher must bump the load generation")
	}
	_, _ = app.Update(launcherWorkspacesMsg{Screen: app.launcherScreen, Cwd: ".", Infos: infos, Gen: gen})
	if app.launcherScreen.TotalItems() != 4 {
		t.Fatalf("stale generation applied after re-kick; items=%d", app.launcherScreen.TotalItems())
	}
	_, _ = app.Update(launcherWorkspacesMsg{Screen: app.launcherScreen, Cwd: ".", Infos: infos, Gen: gen2})
	if app.launcherScreen.TotalItems() != 4 {
		t.Fatalf("latest generation not applied; items=%d", app.launcherScreen.TotalItems())
	}
}

// TestLauncherAsyncLoad_DroppedWhenLauncherClosed: a listing that lands after
// the launcher was replaced by a session must not resurrect launcher state.
func TestLauncherAsyncLoad_DroppedWhenLauncherClosed(t *testing.T) {
	app := newTestApp(t)
	app.AgentMgr.ClearActive()
	app.AgentMgr.Remove("agent-1")
	app.openLauncher()
	gen := app.launcherLoadGen.Load()

	app.AgentMgr.ClearActive()
	app.launcherScreen = nil // simulate AddOrchestrator replacing the launcher

	_, _ = app.Update(launcherWorkspacesMsg{
		Cwd:   ".",
		Infos: []workspace.WorkspaceInfo{{ID: "w1", ProjectDir: "C:\\one"}},
		Gen:   gen,
	})
	if app.launcherScreen != nil {
		t.Fatal("async listing resurrected the launcher screen")
	}
}
