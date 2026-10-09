package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/editor"
	"crude/tui/screens/operationissue"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

func paintedOperationIssue(t *testing.T, app *App) string {
	t.Helper()
	fw := app.windowManager.Window(string(ui.OverlayOperationIssue))
	if fw == nil {
		t.Fatal("actionable recovery dialog was not opened")
	}
	frame, ok := fw.Content.(*ui.WindowFrameWidget)
	if !ok {
		t.Fatal("recovery is not a native WindowFrame")
	}
	w, h := fw.Size()
	frame.Host().Resize(w, h)
	frame.Host().Layout()
	buf := surface.NewCellBuffer(w, h)
	frame.Host().Paint(buf)
	return bufferPlain(buf)
}

func TestOperationFailurePromptExplainsReasonWithoutLauncherAnnotation(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		app := newTestAppWithLauncher(t)
		app.width, app.height = size[0], size[1]
		wsOf(app).Status = workspace.NewStatusCenter(nil, &testLogger{})
		t.Cleanup(wsOf(app).Status.Close)
		app.registerOverlayHandlers()
		app.launcherNewSession() // the real failure entry point, no configured backend
		painted := paintedOperationIssue(t, app)
		for _, want := range []string{"Session could not be created", "Session configuration is unavailable", "Open Settings", "Close"} {
			if !strings.Contains(painted, want) {
				t.Fatalf("missing %q in recovery:\n%s", want, painted)
			}
		}
		app.paintShellFrame()
		for _, forbidden := range []string{"could not be created", "Open Notices", "unavailable"} {
			if strings.Contains(bufferPlain(app.mainSurf.Buffer()), forbidden) {
				t.Fatalf("launcher action was annotated with %q", forbidden)
			}
		}
		notices := app.ws.Status().Notifications(false)
		if len(notices) != 1 || !strings.Contains(notices[0].Detail, "Session configuration is unavailable") {
			t.Fatal("recovery did not independently register its notice")
		}
		if !app.handlePopupClickOutside(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft}) {
			t.Fatal("modal allowed an underlying launcher action")
		}
		_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		drainLauncherCommands(app, cmd)
		if app.overlayHost.IsOpen(ui.OverlayOperationIssue) || app.ws.AgentMgr().Count() != 0 || app.launcherScreen == nil {
			t.Fatal("default Close changed session or launcher state")
		}
	}
}

func TestStartupIssuesWaitForSizeAndRemainActionable(t *testing.T) {
	app := newTestAppWithLauncher(t)
	app.width, app.height = 0, 0
	wsOf(app).QueueStartupIssue(types.OperationIssue{Title: "Account setup required", Summary: "Selected connection unavailable", Detail: `account "5c7eb662d2f8abcca6eeeb1cf1acbc50" not found`, Guidance: "Add an account and review Main Agent.", Target: types.RepairAccounts})
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	text := paintedOperationIssue(t, app)
	if !strings.Contains(text, "5c7eb662d2f8abcca6eeeb1cf1acbc50") || !strings.Contains(text, "Add account") {
		t.Fatal(text)
	}
	app.Update(operationissue.ActionMsg{Screen: app.operationIssue.screen, Action: "repair"})
	if !app.overlayHost.IsOpen(ui.OverlayWorkspaceSettings) {
		t.Fatal("startup account repair did not open Settings")
	}
	if app.ws.AgentMgr().Count() != 0 {
		t.Fatal("repair action implicitly launched a session")
	}
}

func TestStartupResumeMissingSessionShowsPromptWithoutLeavingShell(t *testing.T) {
	fixtureHome := t.TempDir()
	t.Setenv("HOME", fixtureHome)
	t.Setenv("USERPROFILE", fixtureHome)
	app := newTestAppWithLauncher(t)
	wsOf(app).RootPath = t.TempDir()
	wsOf(app).SetBackendConfig(testAccountConfig(), t.TempDir())
	app.ws.SetEventSender(noopEventSender{})
	app.Update(types.ResumeStartupMsg{SessionID: "missing-session"})
	text := paintedOperationIssue(t, app)
	if !strings.Contains(text, "Session could not be resumed") || !strings.Contains(text, "session database unavailable") || !strings.Contains(app.operationIssue.screen.Issue().Detail, "missing-session") {
		t.Fatal(text)
	}
	if app.launcherScreen == nil || app.ws.AgentMgr().Count() != 0 {
		t.Fatal("startup resume failure left unusable shell")
	}
}

func TestNetworkRecoveryOpensPaintedNetworkForm(t *testing.T) {
	app := newTestAppWithLauncher(t)
	app.presentOperationIssue(types.OperationIssue{Title: "Network needs repair", Summary: "Outbound requests blocked", Detail: "invalid policy", Target: types.RepairNetwork})
	app.Update(operationissue.ActionMsg{Screen: app.operationIssue.screen, Action: "repair"})
	fw := app.overlayHost.Window(ui.OverlayWorkspaceSettings)
	if fw == nil {
		t.Fatal("repair did not open Settings")
	}
	frame := fw.Content.(*ui.WindowFrameWidget)
	w, h := fw.Size()
	buf := surface.NewCellBuffer(w, h)
	frame.Host().Paint(buf)
	if text := bufferPlain(buf); !strings.Contains(text, "Connect timeout") {
		t.Fatalf("network recovery opened blank/wrong form:\n%s", text)
	}
}

func TestConfigRepairLoadsOffUIAndPreservesOriginalBytes(t *testing.T) {
	app := newTestAppWithLauncher(t)
	app.BufferMgr = editor.NewBufferManager()
	path := filepath.Join(t.TempDir(), "config.json")
	raw := []byte(`{"accounts":{`)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	app.presentOperationIssue(types.OperationIssue{Title: "Configuration needs repair", Summary: "Saved configuration could not load.", Detail: "unexpected end of JSON input", Guidance: "Correct the syntax, save, then restart.", Target: types.RepairConfigFile, Path: path})
	old := app.operationIssue.screen
	_, cmd := app.Update(operationissue.ActionMsg{Screen: old, Action: "repair"})
	if app.EditorState.ActiveView != "" || cmd == nil {
		t.Fatal("config load did not stay command-owned")
	}
	drainLauncherCommands(app, cmd)
	if app.EditorState.ActiveView != path || app.EditorState.Editor.Value() != string(raw) {
		t.Fatal("repair did not open the exact malformed config")
	}
	if bytes, err := os.ReadFile(path); err != nil || string(bytes) != string(raw) {
		t.Fatal("opening repair rewrote original config")
	}
	app.presentOperationIssue(types.OperationIssue{Title: "Later failure", Summary: "Current issue", Detail: "new reason", Target: types.RepairSettings})
	app.Update(operationissue.ActionMsg{Screen: old, Action: "close"})
	if app.operationIssue == nil || app.operationIssue.screen.Issue().Title != "Later failure" {
		t.Fatal("stale action dismissed a newer recovery prompt")
	}
}
