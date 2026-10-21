package viewmodel

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/shellport"
	settingsui "crude/tui/settings"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/types"
)

func onboardingTestApp(t *testing.T) *App {
	t.Helper()
	m := newTestAppWithLauncher(t)
	m.ActiveSession = nil
	m.width, m.height = 100, 30
	return m
}

func loadOnboarding(t *testing.T, m *App) {
	t.Helper()
	cmd := m.onboardingCmd()
	if cmd == nil {
		t.Fatal("missing committed onboarding read")
	}
	m.handleOnboardingMessage(cmd())
}

func paintOnboarding(t *testing.T, m *App) string {
	t.Helper()
	window := m.overlayHost.Window(aiSetupReminderOverlay)
	if window == nil {
		t.Fatal("no native reminder")
	}
	frame := window.Content.(*ui.ComponentWidget)
	if _, ok := frame.Root().(*ui.WindowFrame); !ok {
		t.Fatal("reminder is not a native WindowFrame")
	}
	w, h := window.Size()
	frame.Host().Resize(w, h)
	frame.Host().Layout()
	buffer := surface.NewCellBuffer(w, h)
	frame.Host().Paint(buffer)
	return bufferPlain(buffer)
}

func TestOnboardingReminderNativeActionsAndEscDefersOnce(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		m := onboardingTestApp(t)
		m.width, m.height = size[0], size[1]
		writes := 0
		wsOf(m).OnAppPreferencesCommit = func([]types.SettingEdit) (types.AppSettings, error) {
			writes++
			return types.AppSettings{}, nil
		}
		loadOnboarding(t, m)
		m.Update(types.TickMsg{})
		painted := paintOnboarding(t, m)
		for _, want := range []string{"No AI account is configured.", "Do not show this again", "OK"} {
			if !strings.Contains(painted, want) {
				t.Fatalf("missing %q at %v:\n%s", want, size, painted)
			}
		}
		frame := m.overlayHost.Window(aiSetupReminderOverlay).Content.(*ui.ComponentWidget)
		if got := ui.ConfirmButtonIndexFromID(frame.Host().Focused()); got != 1 {
			t.Fatal("OK was not the default focus")
		}
		m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
		if got := ui.ConfirmButtonIndexFromID(frame.Host().Focused()); got != 0 {
			t.Fatal("native focus did not move in place")
		}
		if !m.handlePopupClickOutside(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft}) {
			t.Fatal("reminder allowed outside activation")
		}
		m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
		m.Update(types.TickMsg{})
		if m.overlayHost.IsOpen(aiSetupReminderOverlay) || writes != 0 || m.onboardingCmd() != nil {
			t.Fatal("Esc persisted suppression or showed twice in one launch")
		}
	}
}

func TestOnboardingReminderDefersAndRejectsStaleReads(t *testing.T) {
	m := onboardingTestApp(t)
	cmd := m.onboardingCmd()
	stale := cmd()
	m.onboardingCmd() // supersede the first read
	m.handleOnboardingMessage(stale)
	m.maybeShowAISetupReminder()
	if m.onboarding.loaded || m.onboarding.prompt != nil {
		t.Fatal("stale committed read admitted reminder")
	}
	loadOnboarding(t, m)
	m.width = 0
	m.maybeShowAISetupReminder()
	if m.onboarding.shown {
		t.Fatal("reminder opened before shell geometry")
	}
	m.width = 100
	m.pendingOperationIssues = []types.OperationIssue{{Title: "Startup repair"}}
	m.maybeShowAISetupReminder()
	if m.onboarding.shown {
		t.Fatal("reminder displaced startup repair")
	}
	m.pendingOperationIssues = nil
	m.sessionLifecyclePending = true
	m.maybeShowAISetupReminder()
	if m.onboarding.shown {
		t.Fatal("reminder interrupted startup resume")
	}
	m.sessionLifecyclePending = false
	m.openApplicationSettings()
	m.maybeShowAISetupReminder()
	if m.onboarding.shown {
		t.Fatal("reminder interrupted another modal")
	}
	m.overlayHost.Close(ui.OverlayWorkspaceSettings)
	m.maybeShowAISetupReminder()
	if !m.aiSetupReminderOnTop() {
		t.Fatal("deferred reminder never became available")
	}
}

func TestOnboardingReminderHiddenAccountsAndReadErrorsStayQuiet(t *testing.T) {
	for _, reason := range []string{"hidden", "account", "read error", "workspace generation"} {
		t.Run(reason, func(t *testing.T) {
			m := onboardingTestApp(t)
			switch reason {
			case "hidden":
				wsOf(m).SetAppDefaults(types.AppSettings{Onboarding: types.OnboardingSettings{AISetupReminderHidden: true}})
			case "account":
				wsOf(m).SetAppDefaults(types.AppSettings{Accounts: map[string]provider.Account{"one": {ID: "one"}}})
			}
			cmd := m.onboardingCmd()
			msg := cmd().(onboardingLoadedMsg)
			if reason == "read error" {
				msg.err = errors.New("unavailable")
			}
			m.handleOnboardingMessage(msg)
			if reason == "workspace generation" {
				m.workspaceSwitchGen.Add(1)
			}
			m.maybeShowAISetupReminder()
			if m.onboarding.prompt != nil {
				t.Fatal("proactive prompt ignored", reason)
			}
		})
	}
}

func TestOnboardingReminderSuppressionFailureAndRetry(t *testing.T) {
	m := onboardingTestApp(t)
	writes := 0
	wsOf(m).OnAppPreferencesCommit = func(edits []types.SettingEdit) (types.AppSettings, error) {
		writes++
		if len(edits) != 1 || !reflect.DeepEqual(edits[0].Path, []string{"onboarding", "ai_setup_reminder_hidden"}) || string(edits[0].After) != "true" {
			t.Fatal("suppression wrote unrelated preferences", edits)
		}
		if writes == 1 {
			return types.AppSettings{}, errors.New("disk full")
		}
		return types.AppSettings{Onboarding: types.OnboardingSettings{AISetupReminderHidden: true}}, nil
	}
	loadOnboarding(t, m)
	m.maybeShowAISetupReminder()
	cmd := m.activateAISetupReminder(0)
	if writes != 0 || cmd == nil || !m.onboarding.prompt.busy {
		t.Fatal("suppression performed I/O on Update or failed to own pending save")
	}
	if m.activateAISetupReminder(0) != nil {
		t.Fatal("duplicate save was admitted")
	}
	m.handleOnboardingMessage(cmd())
	painted := paintOnboarding(t, m)
	for _, want := range []string{"disk full", "Do not show this again", "OK"} {
		if !strings.Contains(painted, want) {
			t.Fatal("failure lost modal actions or reason:", painted)
		}
	}
	if m.ws.AppDefaults().Onboarding.AISetupReminderHidden {
		t.Fatal("failed save changed persistent preference")
	}
	frame := m.overlayHost.Window(aiSetupReminderOverlay).Content.(*ui.ComponentWidget)
	if ui.ConfirmButtonIndexFromID(frame.Host().Focused()) != 0 {
		t.Fatal("failed suppression moved keyboard focus to OK")
	}
	_, retry := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if retry == nil {
		t.Fatal("Enter did not retry the focused suppression action")
	}
	m.handleOnboardingMessage(retry())
	if m.overlayHost.IsOpen(aiSetupReminderOverlay) || !m.ws.AppDefaults().Onboarding.AISetupReminderHidden || writes != 2 {
		t.Fatal("successful retry did not commit and close")
	}
}

type freshOnboardingPort struct {
	shellport.Workspace
	settings types.AppSettings
	reads    int
	err      error
}

func (p *freshOnboardingPort) RefreshOnboarding(context.Context) (types.AppSettings, error) {
	p.reads++
	return types.CloneAppSettings(p.settings), p.err
}

func (p *freshOnboardingPort) ApplyOnboarding(settings types.AppSettings) {
	if underlying := shellport.Unwrap(p.Workspace); underlying != nil {
		underlying.SetAppDefaults(settings)
	}
}

func TestOnboardingRechecksBeforeQueuedReminderAndOK(t *testing.T) {
	for _, timing := range []string{"queued", "OK"} {
		t.Run(timing, func(t *testing.T) {
			m := onboardingTestApp(t)
			port := &freshOnboardingPort{Workspace: m.ws}
			m.ws = port
			loadOnboarding(t, m)
			if timing == "OK" {
				m.maybeShowAISetupReminder()
			}
			// Another shell committed an account. No publication has reached this
			// shell; the explicit read must win over the old no-account snapshot.
			port.settings = types.AppSettings{Accounts: map[string]provider.Account{"other": {ID: "other", ProviderID: "openai", Label: "Other", Connection: provider.DefaultTemplates()["openai"]}}}
			if timing == "queued" {
				handled, cmd := m.handleOnboardingMessage(types.TickMsg{})
				if handled || cmd == nil {
					t.Fatal("queued reminder did not request committed state")
				}
				m.handleOnboardingMessage(cmd())
				if m.onboarding.prompt != nil {
					t.Fatal("obsolete no-account reminder appeared")
				}
			} else {
				cmd := m.activateAISetupReminder(1)
				if cmd == nil {
					t.Fatal("OK did not refresh committed state")
				}
				m.handleOnboardingMessage(cmd())
				body := m.settingsSurface(m.overlayHost.Window(ui.OverlayWorkspaceSettings))
				if body == nil || !strings.Contains(body.PaneTitle(), "Main Agent") || m.onboarding.setupModel != nil {
					t.Fatal("OK opened Add Account despite committed account")
				}
			}
		})
	}
}

func TestOnboardingOKTracksOnlyItsAccountAndForwardsMainAgentFetch(t *testing.T) {
	m := onboardingTestApp(t)
	agentCount := m.ws.AgentMgr().Count()
	loadOnboarding(t, m)
	m.maybeShowAISetupReminder()
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	window := m.overlayHost.Window(ui.OverlayWorkspaceSettings)
	body := m.settingsSurface(window)
	if body == nil || body.Model() != m.onboarding.setupModel || m.overlayHost.IsOpen(aiSetupReminderOverlay) {
		t.Fatal("OK did not open and own the native Add Account flow")
	}
	model := body.Model()
	account := provider.Account{ID: "new", ProviderID: "openai", Label: "New account", APIKey: "secret", Connection: provider.DefaultTemplates()["openai"]}
	result := settingsui.AccountChangeResultMsg{Target: model, Change: types.AccountChange{ID: "new", ProviderID: "openai", After: &account}, Settings: types.AppSettings{Accounts: map[string]provider.Account{"new": account}}, WorkspaceGeneration: m.workspaceSwitchGen.Load()}
	failed := result
	failed.Err = errors.New("disk full")
	if m.continueOnboardingAccount(failed) != nil || m.onboarding.setupModel != model {
		t.Fatal("failed account save advanced or consumed setup owner")
	}
	edit := result
	edit.Change.Before = &account
	if m.continueOnboardingAccount(edit) != nil || m.onboarding.setupModel != model {
		t.Fatal("ordinary account edit triggered onboarding")
	}
	stale := result
	stale.WorkspaceGeneration++
	if m.continueOnboardingAccount(stale) != nil || m.onboarding.setupModel != model {
		t.Fatal("stale account save triggered onboarding")
	}
	// Match production ordering: account result first updates the retained model.
	m.forwardEventToWindowContent(window, result)
	cmd := m.continueOnboardingAccount(result)
	if cmd == nil {
		t.Fatal("successful Add dropped Main Agent model discovery")
	}
	request := cmd().(settingsui.FetchModelsMsg)
	if request.AccountID != "new" || request.ProviderID != "openai" || !strings.Contains(body.PaneTitle(), "Main Agent") {
		t.Fatalf("wrong handoff: request=%+v title=%q", request, body.PaneTitle())
	}
	if cfg := model.AppSettings().Agents.Categories["default"]; cfg.AccountID != "" || cfg.Model != "" {
		t.Fatal("handoff silently saved Main Agent choice")
	}
	if m.ws.AgentMgr().Count() != agentCount {
		t.Fatal("setup changed session inventory")
	}
	if m.continueOnboardingAccount(result) != nil {
		t.Fatal("duplicate account result navigated again")
	}
}

func TestOnboardingReminderMouseOKAndHoverUseNativeHitGeometry(t *testing.T) {
	m := onboardingTestApp(t)
	loadOnboarding(t, m)
	m.maybeShowAISetupReminder()
	paintOnboarding(t, m)
	window := m.overlayHost.Window(aiSetupReminderOverlay)
	frame := window.Content.(*ui.ComponentWidget)
	w, h := window.Size()
	buttonX, buttonY := -1, -1
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if frame.Host().HitTest(x, y) == ui.ConfirmButtonID(1) {
				buttonX, buttonY = x, y
				break
			}
		}
		if buttonX >= 0 {
			break
		}
	}
	if buttonX < 0 {
		t.Fatal("painted OK has no native hit target")
	}
	ui.SetConfirmFocus(frame, 0)
	sx, sy := window.Position()
	m.handlePopupContentHover(window, sx+buttonX, sy+buttonY, tea.MouseNone)
	if frame.Host().Focused() != ui.ConfirmButtonID(0) || frame.Host().Hovered() != ui.ConfirmButtonID(1) {
		t.Fatal("hover failed or stole keyboard focus")
	}
	m.handlePopupContentClick(window, tea.MouseClickMsg{X: sx + buttonX, Y: sy + buttonY, Button: tea.MouseLeft})
	if m.settingsSurface(m.overlayHost.Window(ui.OverlayWorkspaceSettings)) == nil || m.overlayHost.IsOpen(aiSetupReminderOverlay) {
		t.Fatal("mouse OK did not open native account setup")
	}
}

func TestOnboardingLateSuppressionDoesNotPublishAcrossWorkspaceBoundary(t *testing.T) {
	m := onboardingTestApp(t)
	loadOnboarding(t, m)
	m.maybeShowAISetupReminder()
	cmd := m.activateAISetupReminder(0)
	m.workspaceSwitchGen.Add(1)
	m.overlayHost.Close(aiSetupReminderOverlay)
	m.handleOnboardingMessage(cmd())
	if m.ws.AppDefaults().Onboarding.AISetupReminderHidden || m.onboarding.prompt != nil {
		t.Fatal("late save result published into replacement generation")
	}
}

func TestOnboardingExplicitSetupWithAccountsSkipsAddAndKeepsDraft(t *testing.T) {
	m := onboardingTestApp(t)
	wsOf(m).SetAppDefaults(types.AppSettings{Onboarding: types.OnboardingSettings{AISetupReminderHidden: true}, Accounts: map[string]provider.Account{"one": {ID: "one", ProviderID: "openai", Label: "One", Connection: provider.DefaultTemplates()["openai"]}}})
	m.openAISetup()
	window := m.overlayHost.Window(ui.OverlayWorkspaceSettings)
	body := m.settingsSurface(window)
	if body == nil || !strings.Contains(body.PaneTitle(), "Main Agent") || m.onboarding.setupModel != nil {
		t.Fatal("existing accounts did not route explicit setup directly to Main Agent")
	}
	m.openAISetup()
	if m.overlayHost.Window(ui.OverlayWorkspaceSettings) != window {
		t.Fatal("repeated setup replaced retained Settings draft")
	}
}

func TestOnboardingCommittedReadFollowsWorkspaceSwitchUntilShown(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	shell := newRemoteJourneyShell(t, runtime, t.TempDir())
	m := shell.app
	// A pending read for the old attachment must not suppress checks forever.
	m.onboarding.checking = true
	old := m.ws
	runAppCommand(t, m, confirmWorkspaceSwitchTest(t, m, t.TempDir()))
	if m.ws == old || m.onboarding.owner != m.ws || m.onboarding.generation != m.workspaceSwitchGen.Load() || m.onboarding.checking {
		t.Fatal("successful switch did not settle old checking state and refresh the new owner")
	}
	// Once deferred or accepted, a workspace switch must not show it again.
	m.onboarding.shown = true
	runAppCommand(t, m, confirmWorkspaceSwitchTest(t, m, t.TempDir()))
	if !m.onboarding.shown || m.onboarding.prompt != nil || m.onboardingCmd() != nil {
		t.Fatal("workspace switch reset the shell-lifetime reminder choice")
	}
}

func TestOnboardingAccountCommitSurvivesFailedFollowUpRead(t *testing.T) {
	m := onboardingTestApp(t)
	loadOnboarding(t, m)
	m.maybeShowAISetupReminder()
	m.activateAISetupReminder(1)
	body := m.settingsSurface(m.overlayHost.Window(ui.OverlayWorkspaceSettings))
	model := body.Model()
	model.SetValueForTest(model.SelectedCategory(), "account-label", "Saved account")
	model.SetValueForTest(model.SelectedCategory(), "api-key", "local-only-fixture")
	submit := body.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.commit"})
	if submit.Cmd == nil {
		t.Fatal("Add Account did not prepare its commit")
	}
	request := submit.Cmd().(settingsui.AccountChangeMsg)
	writes := 0
	wsOf(m).OnAppAccountChange = func(types.AccountChange) error {
		writes++
		return nil
	}
	m.ws = &freshOnboardingPort{Workspace: m.ws, err: errors.New("connection lost after account commit")}
	commands := m.handleAppMessages(request)
	if len(commands) != 1 {
		t.Fatal("account operation did not capture one commit command")
	}
	result := commands[0]().(settingsui.AccountChangeResultMsg)
	if writes != 1 || result.Err != nil || result.RefreshError == nil || len(result.Settings.Accounts) != 1 {
		t.Fatal("follow-up read failure was reported as a failed or repeated account commit")
	}
	// Inspect the request result before model discovery can run. The ordinary
	// result path must settle Add Account and keep the acknowledged account.
	m.handleAppMessages(result)
	if _, exists := model.AppSettings().Accounts[request.Change.ID]; !exists || !strings.Contains(body.PaneTitle(), "Main Agent") {
		t.Fatal("acknowledged account was lost or Add Account remained open for a duplicate retry")
	}
	found := false
	for _, notice := range m.ws.Status().Notifications(true) {
		found = found || strings.Contains(notice.Summary, "Account saved. The configuration could not be refreshed")
	}
	if !found {
		t.Fatal("follow-up read failure was silent")
	}
}
