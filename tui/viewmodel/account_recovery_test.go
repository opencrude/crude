package viewmodel

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/session"
	"crude/sessiondb"
	"crude/tui/surface"
	"crude/tui/ui"
	ttypes "crude/types"
	"crude/workspace"
)

func TestNewSessionWithoutAccountShowsPaintedRecoveryAndOpensAccounts(t *testing.T) {
	app := newTestAppWithLauncher(t)
	app.RootPath = t.TempDir()
	app.WorkspaceDir = t.TempDir()
	app.registerOverlayHandlers()
	cfg := testAccountConfig()
	cfg.Accounts = nil
	app.SetBackendConfig(cfg, t.TempDir())

	priorSession := app.ActiveSession
	result := app.NewSessionFromLauncher()
	if result == nil || !result.NeedsAccount || !strings.Contains(result.Error, session.ErrAccountRequired.Error()) {
		t.Fatalf("no-account create result = %+v, want an explicit account-required failure", result)
	}
	if len(app.Sessions) != 0 || len(app.SessionsByID) != 0 || app.ActiveSession != priorSession {
		t.Fatal("account-required failure created or activated a session")
	}

	if cmd := app.launcherNewSession(); cmd != nil {
		t.Fatal("failed creation should not enqueue an agent")
	}
	if app.AgentMgr.Count() != 0 || len(app.Sessions) != 0 || len(app.SessionsByID) != 0 || app.ActiveSession != priorSession || app.launcherScreen == nil || !app.overlayHost.IsOpen(ui.OverlayAccountRecovery) {
		t.Fatal("failed creation must leave launcher intact and open recovery")
	}
	fw := app.windowManager.Window(string(ui.OverlayAccountRecovery))
	cw, ok := fw.Content.(*ui.ComponentWidget)
	if !ok {
		t.Fatal("warning has no retained component frame")
	}
	w, h := fw.Size()
	cw.Host().Resize(w, h)
	cw.Host().Layout()
	buf := surface.NewCellBuffer(w, h)
	cw.Host().Paint(buf)
	painted := bufferPlain(buf)
	for _, want := range []string{"Account required", "Add account", "Not now"} {
		if !strings.Contains(painted, want) {
			t.Fatalf("missing painted warning %q:\n%s", want, painted)
		}
	}
	if strings.Contains(painted, "Failed to create session") || strings.Contains(painted, `account ""`) {
		t.Fatalf("recovery dialog exposed backend error text instead of user guidance:\n%s", painted)
	}
	if app.accountRecoveryChoice != 1 {
		t.Fatal("safe dismissal is not the default action")
	}
	app.handleAccountRecoveryKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if app.overlayHost.IsOpen(ui.OverlayAccountRecovery) || app.AgentMgr.Count() != 0 || app.launcherScreen == nil {
		t.Fatal("dismissing warning must not create a session or lose launcher")
	}

	app.launcherNewSession()
	app.handleAccountRecoveryKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	app.handleAccountRecoveryKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !app.overlayHost.IsOpen(ui.OverlayWorkspaceSettings) {
		t.Fatal("account action did not open application settings")
	}
	settingsWindow := app.windowManager.Window(string(ui.OverlayWorkspaceSettings))
	settingsWidget := settingsWidgetForTest(t, settingsWindow)
	model := settingsWidget.ModelForTest()
	selected := model.SelectedCategory()
	for _, item := range model.CategoryItems() {
		if item.Label == "Accounts" && item.Payload == selected {
			return
		}
	}
	t.Fatal("recovery opened settings without selecting Accounts")
}

func TestNewSessionWithSavedAccountsOpensMainAgentSelection(t *testing.T) {
	app := newTestAppWithLauncher(t)
	app.RootPath, app.WorkspaceDir = t.TempDir(), t.TempDir()
	app.registerOverlayHandlers()
	cfg := testAccountConfig()
	profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	main := profile.Categories[session.CategoryDefault]
	main.AccountID = ""
	profile.Categories[session.CategoryDefault] = main
	cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
	app.SetBackendConfig(cfg, t.TempDir())
	app.SetAppDefaults(ttypes.AppSettings{Providers: cfg.Providers, Accounts: cfg.Accounts,
		Agents: ttypes.AgentsSettings{Categories: map[string]ttypes.CategorySettings{session.CategoryDefault: {Model: "test-model"}}}})
	app.launcherNewSession()
	if app.AgentMgr.Count() != 0 || !app.overlayHost.IsOpen(ui.OverlayAccountRecovery) {
		t.Fatal("missing Main Agent account did not keep launcher and show recovery")
	}
	if fw := app.windowManager.Window(string(ui.OverlayAccountRecovery)); fw != nil {
		cw := fw.Content.(*ui.ComponentWidget)
		w, h := fw.Size()
		cw.Host().Resize(w, h)
		cw.Host().Layout()
		buf := surface.NewCellBuffer(w, h)
		cw.Host().Paint(buf)
		if !strings.Contains(bufferPlain(buf), "Open Main Agent") {
			t.Fatal("existing saved accounts were treated as missing accounts")
		}
	}
	app.activateAccountRecovery(0)
	fw := app.windowManager.Window(string(ui.OverlayWorkspaceSettings))
	if fw == nil {
		t.Fatal("Main Agent account action did not open settings")
	}
	model := settingsWidgetForTest(t, fw).ModelForTest()
	mainSelected := false
	for _, item := range model.CategoryItems() {
		if item.Label == "Agents" && item.Payload == model.SelectedCategory() {
			mainSelected = true
		}
	}
	if !mainSelected || app.AgentMgr.Count() != 0 {
		t.Fatal("recovery chose an account automatically or opened the wrong settings page")
	}
}

func TestResumeUnpinnedSessionKeepsHistoryAndOpensConnectionPicker(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := newTestAppWithLauncher(t)
	root := t.TempDir()
	app.RootPath = root
	app.SessionsByID = make(map[string]*workspace.Session)
	app.SessionsByAgent = make(map[string]*workspace.Session)
	dataDir, err := workspace.WorkspaceDir(root)
	if err != nil {
		t.Fatal(err)
	}
	app.WorkspaceDir = dataDir
	app.SetEventSender(noopEventSender{})
	cfg := testAccountConfig()
	home := t.TempDir()
	app.SetBackendConfig(cfg, home)
	sessionDir := filepath.Join(dataDir, "sessions")
	original, err := session.NewWithSessionDir("", cfg, home, sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	id := original.SessionID()
	if err := original.AppendWorkspaceHistoryEvent("previous work", "Prior work"); err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.UpdateConnection(original.DB(), id, "openai", ""); err != nil {
		t.Fatal(err)
	}
	original.Close()

	app.launcherResumeSession(id, &sessiondb.SessionMeta{ID: id, ProjectPath: root})
	if app.ActiveSession == nil || !errors.Is(app.ActiveSessionConnectionIssue(), session.ErrAccountRequired) {
		t.Fatal("old transcript silently adopted today's account or failed to open")
	}
	if !app.overlayHost.IsOpen(ui.OverlayAccountRecovery) || !app.accountRecoverySession {
		t.Fatal("resumed history did not show connection recovery")
	}
	if _, err := os.Stat(filepath.Join(sessionDir, id+".db")); err != nil {
		t.Fatal("recovery discarded existing transcript", err)
	}
	app.activateAccountRecovery(0)
	projection := app.activeSessionDeck()
	if projection == nil || projection.deck.Active() == nil || projection.deck.Active().ID != sessionSettingsPanelID {
		t.Fatal("choose connection did not open the session settings panel")
	}
	if app.overlayHost.IsOpen(ui.OverlayAccountRecovery) {
		t.Fatal("recovery dialog still blocks the session picker")
	}
	// Startup --resume already has an active backend before the TUI receives
	// its first measured terminal size; that route must show the same warning.
	app.width, app.height = 0, 0
	_, _ = app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if !app.overlayHost.IsOpen(ui.OverlayAccountRecovery) {
		t.Fatal("startup resume did not present connection recovery")
	}
	app.CloseAllSessions()
}
