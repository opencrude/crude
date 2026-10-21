package viewmodel

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/types"
	"crude/workspace"
)

const aiSetupReminderOverlay = ui.OverlayAISetup

type onboardingState struct {
	sequence   uint64
	loaded     bool
	shown      bool // shell lifetime, deliberately not reset on workspace switches
	settings   types.AppSettings
	owner      shellport.Workspace
	generation uint64
	prompt     *aiSetupReminder
	setupModel *settingsui.Model
	setupGen   uint64
	checking   bool
}

type aiSetupReminder struct {
	owner      shellport.Workspace
	generation uint64
	busy       bool
	err        string
}

type onboardingLoadedMsg struct {
	owner      shellport.Workspace
	generation uint64
	sequence   uint64
	settings   types.AppSettings
	err        error
	explicit   bool
	verified   bool
}

type onboardingSavedMsg struct {
	prompt *aiSetupReminder
	result workspace.PreferencesSaveResult
}

// onboardingCmd reads committed application choices, not a Settings draft.
// Connected shells refresh their redacted replica off the Update goroutine.
func (m *App) onboardingCmd() tea.Cmd {
	if m.ws == nil || m.onboarding.shown {
		return nil
	}
	m.onboarding.sequence++
	m.onboarding.loaded = false
	owner, generation, sequence := m.ws, m.workspaceSwitchGen.Load(), m.onboarding.sequence
	return func() tea.Msg {
		var settings types.AppSettings
		var err error
		if reader, ok := owner.(interface {
			RefreshOnboarding(context.Context) (types.AppSettings, error)
		}); ok {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			settings, err = reader.RefreshOnboarding(ctx)
		} else {
			settings = owner.AppDefaults()
		}
		return onboardingLoadedMsg{owner: owner, generation: generation, sequence: sequence, settings: settings, err: err}
	}
}

func (m *App) handleOnboardingMessage(msg tea.Msg) (bool, tea.Cmd) {
	switch msg := msg.(type) {
	case onboardingLoadedMsg:
		if msg.owner == m.ws && msg.generation == m.workspaceSwitchGen.Load() && msg.sequence == m.onboarding.sequence {
			m.onboarding.checking = false
			m.onboarding.settings, m.onboarding.loaded = msg.settings, msg.err == nil
			m.onboarding.owner, m.onboarding.generation = msg.owner, msg.generation
			if msg.err != nil && m.Feedback != nil {
				m.Feedback.NotifyError("AI setup state could not be read: " + msg.err.Error())
			}
			if msg.err == nil {
				if installer, ok := m.ws.(interface{ ApplyOnboarding(types.AppSettings) }); ok {
					installer.ApplyOnboarding(msg.settings)
				}
				if msg.explicit {
					main := msg.settings.Agents.Categories["default"]
					if _, exists := msg.settings.Accounts[main.AccountID]; exists && main.Model != "" {
						m.Feedback.NotifyInfo("AI setup is configured.")
						return true, nil
					}
					return true, m.showAISetup(msg.settings)
				}
				if msg.verified {
					m.maybeShowAISetupReminder()
				}
			}
		}
		return true, nil
	case onboardingSavedMsg:
		prompt := msg.prompt
		if prompt == nil || prompt != m.onboarding.prompt || prompt.owner != m.ws || prompt.generation != m.workspaceSwitchGen.Load() {
			return true, nil
		}
		prompt.busy = false
		if msg.result.Err != nil {
			prompt.err = "The reminder preference could not be saved: " + msg.result.Err.Error()
			m.refreshAISetupReminder()
			return true, nil
		}
		m.ws.ApplyPreferencesSave(msg.result)
		m.overlayHost.Close(aiSetupReminderOverlay)
		return true, nil
	case types.TickMsg:
		if m.canShowAISetupReminder() && !m.onboarding.checking {
			if reader, ok := m.ws.(interface {
				RefreshOnboarding(context.Context) (types.AppSettings, error)
			}); ok {
				m.onboarding.checking = true
				m.onboarding.sequence++
				owner, generation, sequence := m.ws, m.workspaceSwitchGen.Load(), m.onboarding.sequence
				return false, func() tea.Msg {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					settings, err := reader.RefreshOnboarding(ctx)
					return onboardingLoadedMsg{owner: owner, generation: generation, sequence: sequence, settings: settings, err: err, verified: true}
				}
			}
		}
		m.maybeShowAISetupReminder()
	case tea.KeyPressMsg:
		if !m.aiSetupReminderOnTop() {
			break
		}
		window := m.overlayHost.Window(aiSetupReminderOverlay)
		frame := window.Content.(*ui.ComponentWidget)
		if m.onboarding.prompt.busy && msg.Code != tea.KeyEsc {
			return true, nil
		}
		switch msg.Code {
		case tea.KeyEsc:
			m.overlayHost.Close(aiSetupReminderOverlay) // defer, never persist
		case tea.KeyEnter:
			return true, m.activateAISetupReminder(ui.ConfirmButtonIndexFromID(frame.Host().Focused()))
		case tea.KeyLeft, tea.KeyUp:
			ui.MoveConfirmFocus(frame, -1, 2)
		case tea.KeyRight, tea.KeyDown, tea.KeyTab:
			delta := 1
			if msg.Code == tea.KeyTab && msg.Mod.Contains(tea.ModShift) {
				delta = -1
			}
			ui.MoveConfirmFocus(frame, delta, 2)
		}
		m.forceFrame()
		return true, nil
	}
	return false, nil
}

func (m *App) aiSetupReminderOnTop() bool {
	if m.onboarding.prompt == nil || m.overlayHost == nil {
		return false
	}
	top := m.overlayHost.Top()
	return top != nil && top.ID == aiSetupReminderOverlay
}

func (m *App) canShowAISetupReminder() bool {
	state := &m.onboarding
	if !state.loaded || state.owner != m.ws || state.generation != m.workspaceSwitchGen.Load() || state.shown || state.settings.Onboarding.AISetupReminderHidden || len(state.settings.Accounts) != 0 ||
		m.ws == nil || m.width <= 0 || m.height <= 0 || m.overlayHost == nil || len(m.overlayHost.All()) != 0 ||
		len(m.pendingOperationIssues) != 0 || m.sessionLifecyclePending || m.workspaceAttachPending || m.ActiveSession != nil {
		return false
	}
	// A newer replica/account commit must also suppress an obsolete read.
	current := m.ws.AppDefaults()
	if current.Onboarding.AISetupReminderHidden || len(current.Accounts) != 0 {
		return false
	}
	return true
}

func (m *App) maybeShowAISetupReminder() {
	if !m.canShowAISetupReminder() || m.onboarding.checking {
		return
	}
	state := &m.onboarding
	state.shown = true
	prompt := &aiSetupReminder{owner: m.ws, generation: m.workspaceSwitchGen.Load()}
	state.prompt = prompt
	frame := m.aiSetupReminderFrame()
	w, h := frame.Measure(m.width, m.height)
	window := wm.NewFloatingWindow(string(aiSetupReminderOverlay), "", frame, w, h,
		wm.WithDecoration(true), wm.WithMovable(true), wm.WithModal(true), wm.WithFocusable(true),
		wm.WithClosable(false), wm.WithResizable(false), wm.WithClickOutside(wm.ClickOutsideIgnore))
	wm.CenterOnScreen(window, m.width, m.height)
	policy := ui.ModalDialogPolicy()
	policy.Decorated, policy.Movable = true, true
	m.overlayHost.RegisterContentHandler(aiSetupReminderOverlay, func(_ *ui.OverlayHost, fw *wm.FloatingWindow, x, y int, _ tea.Msg) tea.Cmd {
		return m.activateAISetupReminder(confirmFrameButtonAt(fw, x, y))
	})
	m.overlayHost.RegisterHoverHandler(aiSetupReminderOverlay, func(_ *ui.OverlayHost, fw *wm.FloatingWindow, x, y int, _ tea.MouseButton) {
		if frame, ok := fw.Content.(*ui.ComponentWidget); ok {
			ui.SetConfirmHover(frame, confirmFrameButtonAt(fw, x, y))
		}
	})
	if m.rootHost != nil {
		m.rootHost.CancelPointerCapture()
	}
	m.overlayHost.Open(aiSetupReminderOverlay, window, policy, func() {
		if state.prompt == prompt {
			state.prompt = nil
		}
	})
	m.forceFrame()
}

func (m *App) aiSetupReminderFrame() *ui.ComponentWidget {
	detail := "Add your LLM provider account. Select the model for Main Agent. AI sessions are not available until setup is completed. You can use the file editor without AI."
	if prompt := m.onboarding.prompt; prompt != nil {
		if prompt.busy {
			detail = "Saving your reminder preference…"
		} else if prompt.err != "" {
			detail = prompt.err
		}
	}
	frame := ui.BuildConfirmFrame(m.Theme, ui.ConfirmFrameSpec{
		Title: "Set up AI", Question: "No AI account is configured.", Detail: detail,
		Severity: ui.ConfirmPromptInfo, Width: min(70, max(36, m.width-4)), FocusedIndex: 1, DetailMaxLines: 5,
		Buttons: []ui.ConfirmButtonSpec{
			{Label: "Do not show this again", ActionID: ui.ConfirmActionNo},
			{Label: "OK", ActionID: ui.ConfirmActionYes},
		},
	})
	if m.onboarding.prompt != nil && m.onboarding.prompt.busy {
		var disable func(ui.Component)
		disable = func(component ui.Component) {
			if button, ok := component.(*ui.Button); ok {
				button.SetDisabled(true)
			}
			if parent, ok := component.(ui.Parent); ok {
				for _, child := range parent.Children() {
					disable(child)
				}
			}
		}
		disable(frame.Root())
	}
	return frame
}

func (m *App) refreshAISetupReminder() {
	if window := m.overlayHost.Window(aiSetupReminderOverlay); window != nil {
		focus := 1
		if old, ok := window.Content.(*ui.ComponentWidget); ok {
			focus = ui.ConfirmButtonIndexFromID(old.Host().Focused())
		}
		frame := m.aiSetupReminderFrame()
		ui.SetConfirmFocus(frame, focus)
		w, h := frame.Measure(m.width, m.height)
		window.Content = frame
		frame.SetMarkContentDirty(window.MarkContentDirty)
		window.SetSize(w, h)
		window.MarkContentDirty()
		m.forceFrame()
	}
}

func (m *App) activateAISetupReminder(choice int) tea.Cmd {
	prompt := m.onboarding.prompt
	if !m.aiSetupReminderOnTop() || prompt.busy || choice < 0 || choice > 1 || prompt.owner != m.ws || prompt.generation != m.workspaceSwitchGen.Load() {
		return nil
	}
	if choice == 1 {
		m.overlayHost.Close(aiSetupReminderOverlay)
		return m.openAISetup()
	}
	if frame, ok := m.overlayHost.Window(aiSetupReminderOverlay).Content.(*ui.ComponentWidget); ok {
		ui.SetConfirmFocus(frame, choice)
	}
	prompt.busy, prompt.err = true, ""
	m.refreshAISetupReminder()
	run := m.ws.PreparePreferencesSave(types.SettingsScopeApplication, []types.SettingEdit{{
		Path: []string{"onboarding", "ai_setup_reminder_hidden"}, After: []byte("true"),
	}})
	return func() tea.Msg { return onboardingSavedMsg{prompt: prompt, result: run()} }
}

// openAISetup is an explicit action, so the hidden-reminder preference never
// blocks it. Do not replace a retained Settings window and discard its drafts.
func (m *App) openAISetup() tea.Cmd {
	if m.ws == nil || m.overlayHost == nil || m.windowManager == nil {
		return nil
	}
	if m.overlayHost.IsOpen(ui.OverlayWorkspaceSettings) {
		return nil
	}
	m.onboarding.shown = true
	if reader, ok := m.ws.(interface {
		RefreshOnboarding(context.Context) (types.AppSettings, error)
	}); ok {
		m.onboarding.sequence++
		owner, generation, sequence := m.ws, m.workspaceSwitchGen.Load(), m.onboarding.sequence
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			settings, err := reader.RefreshOnboarding(ctx)
			return onboardingLoadedMsg{owner: owner, generation: generation, sequence: sequence, settings: settings, err: err, explicit: true}
		}
	}
	return m.showAISetup(m.ws.AppDefaults())
}

func (m *App) showAISetup(settings types.AppSettings) tea.Cmd {
	if m.overlayHost.IsOpen(ui.OverlayWorkspaceSettings) {
		return nil
	}
	if len(settings.Accounts) != 0 {
		return m.openApplicationMainAgent()
	}
	m.openApplicationAccounts()
	if body := m.settingsSurface(m.overlayHost.Window(ui.OverlayWorkspaceSettings)); body != nil {
		m.onboarding.setupModel, m.onboarding.setupGen = body.Model(), m.workspaceSwitchGen.Load()
	}
	return nil
}

// continueOnboardingAccount runs after ordinary account-result forwarding has
// installed the newly saved account in this exact Settings model.
func (m *App) continueOnboardingAccount(result settingsui.AccountChangeResultMsg) tea.Cmd {
	if result.Err != nil || result.Change.Before != nil || result.Change.After == nil ||
		result.Target == nil || result.Target != m.onboarding.setupModel || result.WorkspaceGeneration != m.onboarding.setupGen ||
		result.WorkspaceGeneration != m.workspaceSwitchGen.Load() || m.overlayHost == nil {
		return nil
	}
	window := m.overlayHost.Window(ui.OverlayWorkspaceSettings)
	body := m.settingsSurface(window)
	if body == nil || body.Model() != result.Target {
		return nil
	}
	m.onboarding.setupModel = nil
	cmd := body.ShowMainAgentForAccount(result.Change.ID)
	if frame, ok := window.Content.(*ui.WindowFrameWidget); ok {
		frame.Host().MarkLayout()
		frame.Host().Layout()
	}
	window.MarkContentDirty()
	return cmd
}
