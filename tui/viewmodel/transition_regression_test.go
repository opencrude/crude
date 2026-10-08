package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	ttypes "crude/tui/types"
	"crude/workspace"
)

// newTransitionApp builds a real workspace app with an agent + messages so
// the chat/editor/launcher transitions can be inspected.
func newTransitionApp(t *testing.T) *App {
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
	ws.LSPMgr.SetRuntime(noopLSPRuntime{})
	_, _ = app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	app.AddOrchestrator(
		"agent-1")

	app.appendActiveMessage(ttypes.ChatMessage{
		Role:    ttypes.RoleAssistant,
		Content: "hello from the agent view",
		Time:    time.Now(),
	})
	app.syncAgentMessages("agent-1", false)
	return app
}

func chatAreaText(app *App) string {
	buf := app.mainSurf.Buffer()
	r := app.chatRect
	var b strings.Builder
	for y := r.Y; y < r.Y+r.H && y < buf.Height(); y++ {
		for x := r.X; x < r.X+r.W && x < buf.Width(); x++ {
			c := buf.Get(x, y)
			if c.Width > 0 && c.Char != 0 {
				b.WriteRune(c.Char)
			}
		}
	}
	return b.String()
}

// TestTransition_EditorCloseRestoresChat: agent chat with content → editor
// open → editor close → the chat must render again (not black).
func TestTransition_EditorCloseRestoresChat(t *testing.T) {
	app := newTransitionApp(t)
	t.Cleanup(func() {
		app.Close()
	})
	app.paintShellFrame()

	if !app.shellSlotActive(shellChatSlot) {
		t.Fatal("precondition: chat active")
	}
	text := chatAreaText(app)
	t.Logf("chat text: %q", text)
	if !strings.Contains(text, "hello from the agent view") {
		t.Fatal("precondition: chat content missing")
	}

	// Open the editor over the chat.
	if _, err := app.openEditorTab(filepath.Join(app.RootPath, "main.go")); err != nil {
		t.Fatal(err)
	}
	app.paintShellFrame()
	if !app.shellSlotActive(shellEditorSlot) || app.shellSlotActive(shellChatSlot) {
		t.Fatal("editor must take over the main content area")
	}

	// Close the editor: the chat must come back with content.
	app.closeEditorTab()
	app.paintShellFrame()

	if !app.shellSlotActive(shellChatSlot) {
		t.Fatal("chat slot must be active after editor close")
	}
	text = chatAreaText(app)
	if !strings.Contains(text, "hello from the agent view") {
		t.Fatalf("chat view black/stale after editor close; content=%q", text)
	}
}

// TestTransition_EditorCloseShowsCleanLauncher: launcher → editor → close →
// the launcher area must be clean (no stale editor/gutter cells in the left
// pad, the right pad, or anywhere the editor previously painted).
func TestTransition_EditorCloseShowsCleanLauncher(t *testing.T) {
	app := newTransitionApp(t)
	t.Cleanup(func() {
		app.Close()
	})
	app.RemoveOrchestrator("agent-1")
	app.openLauncher() // AddOrchestrator nils the launcher screen
	app.paintShellFrame()
	if !app.shellSlotActive(shellLauncherSlot) {
		t.Fatal("precondition: launcher active")
	}

	// Open an editor.
	if _, err := app.openEditorTab(filepath.Join(app.RootPath, "main.go")); err != nil {
		t.Fatal(err)
	}
	app.paintShellFrame()
	if !app.shellSlotActive(shellEditorSlot) {
		t.Fatal("precondition: editor active")
	}

	// Close it: launcher must come back clean.
	app.closeEditorTab()
	app.paintShellFrame()
	if !app.shellSlotActive(shellLauncherSlot) {
		t.Fatal("launcher must be active after editor close")
	}

	// Scan the main-content band (where the editor previously painted) for
	// cells that are neither clean background nor launcher content: the
	// launcher slot is inset by PadLR, so the pad columns and any row the
	// editor covered must be blank after the transition.
	lay := app.computeLayout()
	buf := app.mainSurf.Buffer()
	inLauncher := func(x, y int) bool {
		return x >= lay.SidebarW+lay.PadLR && x < lay.SidebarW+lay.PadLR+lay.InnerW &&
			y >= lay.ChatAreaTop && y < lay.ChatAreaTop+lay.ChatH
	}
	stale := 0
	for y := lay.ChatAreaTop; y < lay.ChatAreaTop+lay.ChatH && y < buf.Height(); y++ {
		for x := lay.SidebarW; x < lay.SidebarW+lay.ContentW && x < buf.Width(); x++ {
			c := buf.Get(x, y)
			if c.Width > 0 && c.Char != 0 && c.Char != ' ' && !inLauncher(x, y) {
				stale++
			}
		}
	}
	if stale > 0 {
		t.Fatalf("launcher transition left %d stale cells outside the launcher rect", stale)
	}
}

// TestTransition_TabSwitchEditorToAgentRestoresChat: agent tab + editor tab;
// switching back to the agent tab must render the chat (not black).
func TestTransition_TabSwitchEditorToAgentRestoresChat(t *testing.T) {
	app := newTransitionApp(t)
	t.Cleanup(func() {
		app.Close()
	})
	app.paintShellFrame()
	if !app.shellSlotActive(shellChatSlot) {
		t.Fatal("precondition: chat active")
	}

	// Open the editor: the agent tab stays in the tab bar.
	if _, err := app.openEditorTab(filepath.Join(app.RootPath, "main.go")); err != nil {
		t.Fatal(err)
	}
	app.paintShellFrame()
	if !app.shellSlotActive(shellEditorSlot) || app.shellSlotActive(shellChatSlot) {
		t.Fatal("editor must take over the main content area")
	}

	// Switch back to the agent tab.
	for i, tab := range app.Win.Tabs() {
		if app.isAgentTabTitle(tab.Title) {
			app.Win.SwitchTo(i)
			break
		}
	}
	app.handleTabSwitch()
	app.paintShellFrame()

	if !app.shellSlotActive(shellChatSlot) {
		t.Fatal("chat slot must be active after tab switch back to the agent")
	}
	text := chatAreaText(app)
	if !strings.Contains(text, "hello from the agent view") {
		t.Fatalf("agent chat black after tab switch back; content=%q", text)
	}
}

// TestTransition_EmptySessionToHydratedSessionPaintsFirstFrame covers the
// real launch flow: New Session first paints an empty chat projection, then a
// saved session is resumed from Workspace. Both retained renderers begin their
// local export generation at one, so the shared shell chat must still replace
// the empty list immediately rather than waiting for resize or another view
// invalidation.
func TestTransition_EmptySessionToHydratedSessionPaintsFirstFrame(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 100, 30

	app.AddOrchestrator("new-session")
	app.paintShellFrame()
	if text := chatAreaText(app); strings.Contains(text, "resumed history") {
		t.Fatalf("precondition: empty new session painted resumed content: %q", text)
	}

	app.AddOrchestrator("resumed-session")
	app.hydrateActiveMessages([]ttypes.ChatMessage{{
		InternalID: "history-1",
		Role:       ttypes.RoleAssistant,
		Content:    "resumed history paints immediately",
		Time:       time.Now(),
	}})
	app.paintShellFrame()

	if !app.shellSlotActive(shellChatSlot) {
		t.Fatal("resumed session did not activate the chat slot")
	}
	if text := chatAreaText(app); !strings.Contains(text, "resumed history paints immediately") {
		t.Fatalf("hydrated session stayed blank until another invalidation; content=%q", text)
	}
}
