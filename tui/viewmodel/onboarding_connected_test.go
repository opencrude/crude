package viewmodel

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"crude/agent/provider"
	"crude/agent/session"
	"crude/core"
	"crude/core/coreprotocol"
	"crude/internal/providersim"
	settingsui "crude/tui/screens/settingspages"
	"crude/tui/ui"
	"crude/types/setting"
	"crude/workspace"

	"github.com/charmbracelet/x/ansi"
)

// This owner implements the existing application seam. The test drives account
// and preference commits through the real framed core and native Settings forms.
type onboardingJourneyOwner struct {
	mu       sync.Mutex
	settings setting.App
	manager  *provider.AccountManager
	failSave bool
}

func (o *onboardingJourneyOwner) StartupSnapshot(context.Context) (coreprotocol.StartupSnapshot, error) {
	return coreprotocol.StartupSnapshot{}, nil
}

func (o *onboardingJourneyOwner) SaveStartup(context.Context, []setting.Edit) (setting.Onboarding, error) {
	return setting.Onboarding{}, errors.New("unexpected detached save")
}

func (o *onboardingJourneyOwner) ReadStartupSettings(context.Context) (setting.App, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return setting.CloneApp(o.settings), nil
}

func (o *onboardingJourneyOwner) account(change setting.AccountChange) (setting.App, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if change.After == nil {
		delete(o.settings.Accounts, change.ID)
	} else {
		o.settings.Accounts[change.ID] = provider.CloneAccount(*change.After)
	}
	o.manager.Replace(o.settings.Accounts)
	return setting.CloneApp(o.settings), nil
}

func (o *onboardingJourneyOwner) preferences(edits []setting.Edit) (setting.App, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.failSave {
		o.failSave = false
		return setting.App{}, errors.New("model choice could not be written")
	}
	if err := setting.ApplyEdits(&o.settings, edits); err != nil {
		return setting.App{}, err
	}
	return setting.CloneApp(o.settings), nil
}

func TestOnboardingConnectedAccountModelSaveAndExplicitSession(t *testing.T) {
	server := providersim.New(t, providersim.WithModels(providersim.Chat, "startup-model"))
	requests := func(method string) int {
		count := 0
		for _, request := range server.Requests() {
			if request.Method == method {
				count++
			}
		}
		return count
	}
	owner := &onboardingJourneyOwner{settings: setting.App{
		Accounts:     map[string]provider.Account{},
		Onboarding:   setting.Onboarding{IntroductionCompleted: true},
		LocalHistory: setting.LocalHistory{Enabled: "off"},
		Memory:       setting.Memory{AutoRecall: "off", AutoExtract: "off", Consolidation: "off"},
	}}
	owner.manager = provider.NewAccountManager(owner.settings.Accounts, nil)
	runtime, err := core.New(func(root string) (*workspace.Workspace, error) {
		settings, _ := owner.ReadStartupSettings(t.Context())
		ws := workspace.NewWorkspace(root, "", "tester", setting.Settings{}, noopLogger{})
		ws.SetAppDefaults(settings)
		ws.OnAppAccountCommit = owner.account
		ws.OnAppPreferencesCommit = owner.preferences
		ws.SetBackendConfig(&session.Config{Agents: session.DefaultAgentsConfig(), AccountManager: owner.manager}, t.TempDir())
		return ws, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.SetStartupOwner(owner); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Error(err)
		}
	})
	shell := newRemoteJourneyShell(t, runtime, t.TempDir())
	app := shell.app
	loadOnboarding(t, app)
	app.maybeShowAISetupReminder()
	if !strings.Contains(paintOnboarding(t, app), "No AI account is configured.") {
		t.Fatal("connected no-account state did not paint the reminder")
	}
	runAppCommand(t, app, app.activateAISetupReminder(1))
	window := app.overlayHost.Window(ui.OverlayWorkspaceSettings)
	body := app.settingsSurface(window)
	if body == nil || !strings.Contains(ansi.Strip(app.View().RenderContent()), "Add account") {
		t.Fatal("OK did not render Add Account directly")
	}
	model := body.Model()
	category := model.SelectedCategory()
	model.SetValueForTest(category, "account-label", "Local setup test")
	model.SetValueForTest(category, "account-endpoint", server.BaseURL(providersim.Chat))
	model.SetValueForTest(category, "api-key", "local-test-not-a-real-credential")
	submit := body.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.commit"})
	if submit.Cmd == nil {
		t.Fatal("valid native Add Account form did not emit its commit")
	}
	_, command := app.Update(submit.Cmd())
	runAppCommand(t, app, command)
	if !strings.Contains(body.PaneTitle(), "Main Agent") || requests(http.MethodGet) == 0 {
		t.Fatal("account commit did not open Main Agent and fetch the account's models")
	}
	committed, _ := owner.ReadStartupSettings(t.Context())
	if len(committed.Accounts) != 1 || committed.Agents.Categories["default"].Model != "" || app.ws.AgentMgr().Count() != 0 {
		t.Fatal("account save applied a model or created a session")
	}
	// The native form owns the model draft, including the populated dropdown.
	frame := window.Content.(*ui.WindowFrameWidget)
	frame.Host().MarkLayout()
	frame.Host().Layout()
	form := frame.Host().Component(settingsui.AgentSettingsFormID).(*ui.Form)
	if field := form.Field("model"); field == nil || len(field.FieldState().Options) == 0 {
		t.Fatal("account model list did not reach the rendered model control")
	}
	model.SetValueForTest(model.SelectedCategory(), "model", "startup-model")
	// The editor's Save applies the agent (this handler) and then persists
	// through ordinary Settings Save, which must retain the choice on failure.
	agents := frame.Host().Component("settings/agents").(ui.ActionHandler)
	agents.HandleAction(ui.EventContext{}, ui.Action{ID: "agents.save"})
	if model.AppSettings().Agents.Categories["default"].Model != "startup-model" {
		t.Fatal("agent Save did not apply the selected model")
	}
	owner.mu.Lock()
	owner.failSave = true
	owner.mu.Unlock()
	for attempt := 0; attempt < 2; attempt++ {
		save := body.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.save"})
		if save.Cmd == nil {
			t.Fatal("native Settings Save did not emit preferences")
		}
		_, command = app.Update(save.Cmd())
		runAppCommand(t, app, command)
		if attempt == 0 && (app.overlayHost.Window(ui.OverlayWorkspaceSettings) != window || model.SaveError() == "" || model.AppSettings().Agents.Categories["default"].Model != "startup-model") {
			t.Fatal("failed model Save lost its draft, error, or retry surface")
		}
	}
	committed, _ = owner.ReadStartupSettings(t.Context())
	if committed.Agents.Categories["default"].Model != "startup-model" || app.ws.AgentMgr().Count() != 0 || requests(http.MethodPost) != 0 {
		t.Fatal("model Save did not commit cleanly or setup started AI work")
	}
	current := app.settingsSurface(app.overlayHost.Window(ui.OverlayWorkspaceSettings))
	if current == nil || len(current.Model().Edits()) != 0 {
		t.Fatal("Save closed Settings or left the saved model dirty")
	}
	// The saved Main Agent moves setup on to Errand's model, with the reason
	// beside the field, and saves nothing for Errand by itself.
	if !strings.Contains(current.PaneTitle(), "Errand") {
		t.Fatalf("Main Agent save did not continue to Errand: %q", current.PaneTitle())
	}
	frame.Host().MarkLayout()
	frame.Host().Layout()
	form = frame.Host().Component(settingsui.AgentSettingsFormID).(*ui.Form)
	if field := form.Field("model"); field == nil || !strings.Contains(field.FieldState().Description, "fast, low-cost model") {
		t.Fatal("Errand model field does not explain the recommended model")
	}
	if committed, _ = owner.ReadStartupSettings(t.Context()); committed.Agents.Categories[setting.ErrandRoleID].Model != "" {
		t.Fatal("opening Errand saved a model")
	}
	errandModel := current.Model()
	errandModel.SetValueForTest(errandModel.SelectedCategory(), "model", "startup-model")
	agents = frame.Host().Component("settings/agents").(ui.ActionHandler)
	agents.HandleAction(ui.EventContext{}, ui.Action{ID: "agents.save"})
	save := current.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.save"})
	if save.Cmd == nil {
		t.Fatal("Errand Save did not emit preferences")
	}
	_, command = app.Update(save.Cmd())
	runAppCommand(t, app, command)
	if committed, _ = owner.ReadStartupSettings(t.Context()); committed.Agents.Categories[setting.ErrandRoleID].Model != "startup-model" {
		t.Fatal("Errand model was not saved")
	}
	if after := app.settingsSurface(app.overlayHost.Window(ui.OverlayWorkspaceSettings)); after == nil || len(after.Model().Edits()) != 0 {
		t.Fatal("Errand save left setup dirty")
	}
	remoteJourneyCreate(t, shell)
	if requests(http.MethodPost) != 0 {
		t.Fatal("explicit session creation sent a provider prompt")
	}
}
