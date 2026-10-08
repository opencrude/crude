package viewmodel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	ttypes "crude/tui/types"
	model "crude/types"
	"crude/workspace"
)

type regressionLogger struct{}

func (regressionLogger) Info(string, string, ...any)  {}
func (regressionLogger) Warn(string, string, ...any)  {}
func (regressionLogger) Error(string, string, ...any) {}
func (regressionLogger) Debug(string, string, ...any) {}

func newRealShellApp(t *testing.T) (*App, *workspace.Workspace) {
	t.Helper()
	dir := t.TempDir()
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws := workspace.NewWorkspace(dir, "openrouter", "test", ttypes.Settings{}, regressionLogger{})
	ws.SetBackendConfig(testAccountConfig(), home)
	ws.SetAppDefaults(ttypes.AppSettings{Accounts: ws.AllAccounts()})
	ws.SetEventSender(noopEventSender{})
	t.Setenv("MODELS_DEV_URL", "http://127.0.0.1:9/models-dev-unreachable")
	app := NewApp(ws, nil)
	// These tests exercise the tab-close/launcher flow, not LSP. A real
	// language server subprocess would inherit the workspace DB handle on
	// Windows and wedge the temp-dir cleanup; NewApp installs the editor
	// runtime, so the no-op runtime must be swapped in after.
	ws.LSPMgr.SetRuntime(noopLSPRuntime{})
	_, _ = app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	_ = app.View()
	return app, ws
}

// noopLSPRuntime never spawns a server; it keeps session flows deterministic
// in tests that do not exercise LSP.
type noopLSPRuntime struct{}

type noopLSPClient struct{}

func (noopLSPRuntime) NewClient(model.LSPServerSpec, string) workspace.LSPClient {
	return noopLSPClient{}
}

func (noopLSPClient) OnDiagnostics(func(string, []workspace.LSPDiagnostic)) {}
func (noopLSPClient) Start(context.Context) error                           { return nil }
func (noopLSPClient) Stop()                                                 {}
func (noopLSPClient) Done() <-chan struct{}                                 { return nil }
func (noopLSPClient) OpenFile(context.Context, string, string, string)      {}
func (noopLSPClient) UpdateFile(context.Context, string, string)            {}
func (noopLSPClient) CloseFile(context.Context, string)                     {}
func (noopLSPClient) NotifySave(context.Context, string, string, int, bool) {}
func (noopLSPClient) NotifyWatchedFilesChanged(context.Context, string, int) {
}
func (noopLSPClient) SyncFileFromDisk(context.Context, string, int) {}
func (noopLSPClient) RequestDefinition(context.Context, string, int, int) []workspace.LSPLocation {
	return nil
}
func (noopLSPClient) FindReferences(context.Context, string, int, int, bool) []workspace.LSPLocation {
	return nil
}
func (noopLSPClient) RequestDocumentSymbols(context.Context, string) []workspace.LSPSymbol {
	return nil
}
func (noopLSPClient) RequestHover(context.Context, string, int, int) (string, error) {
	return "", nil
}
func (noopLSPClient) RequestRename(context.Context, string, int, int, string) (interface{}, error) {
	return nil, nil
}
func (noopLSPClient) RequestCompletions(context.Context, string, int, int) []workspace.CompletionItem {
	return nil
}

// clickLastTabClose clicks the close glyph of the LAST tab through the real
// dispatch path and drains any returned cmd.
func clickLastTabClose(t *testing.T, app *App) {
	t.Helper()
	app.paintShellFrame()
	tabs := app.tabScreen.TabsControl()
	closeLocal := tabs.CloseLocalX(0)
	if closeLocal < 0 {
		t.Fatal("no close local X for the last tab")
	}
	// Screen X of the close glyph: the strip's rail column plus the local X
	// the control reports (the bar reserves one separator column by the nav
	// rail; tabBarRailCol owns that offset).
	screenX := app.tabBarRailCol() + closeLocal
	idx, closeHit := app.tabBarHitTest(screenX)
	if idx != 0 || !closeHit {
		t.Fatalf("tabBarHitTest(%d)=(%d,%v) want (0,true)", screenX, idx, closeHit)
	}
	_, cmd := app.dispatchMouseClick(tea.MouseClickMsg{X: screenX, Y: app.shellTopRow(), Button: tea.MouseLeft})
	if cmd != nil {
		_ = cmd
	}
}

// assertLauncherBack checks the launcher took the screen back after the last
// tab closed. retainedAgents is how many agents stay in the manager: closing
// an agent tab minimizes it, so it is still listed in the Agents panel and
// still restorable, while an editor tab close leaves nothing behind.
func assertLauncherBack(t *testing.T, app *App, retainedAgents int) {
	t.Helper()
	if app.Win.TabCount() != 0 {
		t.Fatalf("tabs=%d want 0 after closing last tab", app.Win.TabCount())
	}
	if app.AgentMgr.VisibleCount() != 0 {
		t.Fatalf("surfaced agents=%d want 0", app.AgentMgr.VisibleCount())
	}
	if got := app.AgentMgr.Count(); got != retainedAgents {
		t.Fatalf("retained agents=%d want %d", got, retainedAgents)
	}
	// A minimized idle agent remains in the aggregate agent report, which is
	// how the status bar advertises backgrounded work from the launcher.
	if got := testIdleAgentCount(app); got != retainedAgents {
		t.Fatalf("idle agents=%d want %d after closing last tab", got, retainedAgents)
	}
	if got := testActiveAgentCount(app); got != 0 {
		t.Fatalf("active agents=%d want 0 after closing last tab", got)
	}
	items := app.agentSidebarItems()
	if retainedAgents == 0 {
		if len(items) != 2 || items[0].ID != sidebarItemNewAgent || items[1].Label != "No agents" {
			t.Fatalf("Agents panel disagrees with closed lifecycle: %#v", items)
		}
	} else {
		got := 0
		for _, item := range items {
			if item.Command || item.ID == "none" {
				continue
			}
			got++
			if !item.Selectable() {
				t.Fatalf("a minimized agent must stay clickable to restore: %#v", item)
			}
			if !strings.Contains(item.Detail, "minimized") {
				t.Fatalf("minimized agent row should say so: %#v", item)
			}
		}
		if got != retainedAgents {
			t.Fatalf("Agents panel must list minimized agents: %#v", items)
		}
	}
	app.paintShellFrame()
	if !app.shellSlotActive(shellLauncherSlot) {
		t.Fatal("launcher slot must be active after last tab close")
	}
	if app.shellSlotActive(shellChatSlot) || app.shellSlotActive(shellEditorSlot) {
		t.Fatal("chat/editor slot must be inactive after last tab close")
	}
	for i := 0; i < 5; i++ {
		app.paintShellFrame()
	}
	if n := countNonBlankGlyphs(app.mainSurf.Buffer()); n < 50 {
		t.Fatalf("launcher frame after close too sparse: %d glyphs", n)
	}
	_, _ = app.handleTick(ttypes.TickMsg(time.Now()))
	_ = app.View()
}

// TestRealSessionLastTabCloseSwitchesToLauncher drives the REAL
// launcher session flow (NewSessionFromLauncher — session DB + orchestrator)
// then closes the last tab via the tab-bar close click.
func TestRealSessionLastTabCloseSwitchesToLauncher(t *testing.T) {
	app, ws := newRealShellApp(t)
	t.Cleanup(func() { ws.Close() })

	cmd := app.launcherNewSession()
	if cmd != nil {
		_ = cmd
	}
	if app.Win.TabCount() != 1 {
		t.Fatalf("agent tab count=%d want 1 after launcherNewSession", app.Win.TabCount())
	}
	if app.AgentMgr.Count() != 1 {
		t.Fatalf("agents=%d want 1", app.AgentMgr.Count())
	}
	app.paintShellFrame()
	if !app.shellSlotActive(shellChatSlot) || app.shellSlotActive(shellLauncherSlot) {
		t.Fatalf("after real session open: chat=%v launcher=%v", app.shellSlotActive(shellChatSlot), app.shellSlotActive(shellLauncherSlot))
	}

	clickLastTabClose(t, app)
	assertLauncherBack(t, app, 1)
	t.Log("real-session last-tab close OK")
	ws.Close()
}

// TestAgentLastTabCloseSwitchesToLauncher: session agent tab open →
// close the LAST tab via the tab-bar close click → launcher comes back.
func TestAgentLastTabCloseSwitchesToLauncher(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	if !app.shellSlotActive(shellLauncherSlot) {
		t.Fatal("precondition: launcher active at startup")
	}
	app.AddOrchestrator(
		"agent-1")

	if app.Win.TabCount() != 1 {
		t.Fatalf("agent tab count=%d want 1", app.Win.TabCount())
	}
	app.paintShellFrame()
	if !app.shellSlotActive(shellChatSlot) || app.shellSlotActive(shellLauncherSlot) {
		t.Fatalf("after session open: chat=%v launcher=%v", app.shellSlotActive(shellChatSlot), app.shellSlotActive(shellLauncherSlot))
	}
	clickLastTabClose(t, app)
	assertLauncherBack(t, app, 1)
	t.Log("agent last-tab close OK")
}

// TestEditorLastTabCloseSwitchesToLauncher: editor tab open → close
// the LAST tab via the tab-bar close click → launcher comes back.
func TestEditorLastTabCloseSwitchesToLauncher(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	path := filepath.Join(app.RootPath, "main.go")
	if _, err := app.openEditorTab(path); err != nil {
		t.Fatal(err)
	}
	if app.Win.TabCount() != 1 {
		t.Fatalf("editor tab count=%d want 1", app.Win.TabCount())
	}
	app.paintShellFrame()
	if !app.shellSlotActive(shellEditorSlot) || app.shellSlotActive(shellLauncherSlot) {
		t.Fatalf("after editor open: editor=%v launcher=%v", app.shellSlotActive(shellEditorSlot), app.shellSlotActive(shellLauncherSlot))
	}
	clickLastTabClose(t, app)
	assertLauncherBack(t, app, 0)
	t.Log("editor last-tab close OK")
}
