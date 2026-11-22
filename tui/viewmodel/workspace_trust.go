package viewmodel

import (
	"encoding/json"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/core/shellport"
	"crude/tui/ui"
	"crude/types/setting"
	"crude/workspace"
)

type workspaceTrustState struct {
	askedOwner        shellport.Workspace
	askedGen          uint64
	prompt            *workspaceTrustPrompt
	awaitingFirstSize bool
}

type workspaceTrustPrompt struct {
	owner      shellport.Workspace
	generation uint64
	busy       bool
	err        string
	undecided  bool
}

type workspaceTrustSavedMsg struct {
	prompt *workspaceTrustPrompt
	result workspace.PreferencesSaveResult
}

func (m *App) canOpenWorkspaceTrustPrompt() bool {
	return m.ws != nil && m.width > 0 && m.height > 0 && m.overlayHost != nil && len(m.overlayHost.All()) == 0 &&
		!m.workspaceAttachPending && !m.sessionLifecyclePending
}

func (m *App) openWorkspaceTrustPrompt() tea.Cmd {
	if !m.canOpenWorkspaceTrustPrompt() {
		return nil
	}
	m.showWorkspaceTrustPrompt(m.ws.AnalysisSettings().DecisionNeeded())
	return nil
}

func (m *App) showWorkspaceTrustPrompt(undecided bool) {
	state := &m.onboarding.trust
	prompt := &workspaceTrustPrompt{owner: m.ws, generation: m.workspaceSwitchGen.Load(), undecided: undecided}
	state.prompt, state.askedOwner, state.askedGen = prompt, prompt.owner, prompt.generation
	m.openConfirmModal(ui.OverlayWorkspaceTrust, m.workspaceTrustFrame(prompt), m.activateWorkspaceTrust, func() {
		if state.prompt == prompt {
			state.prompt = nil
		}
	})
}

func (m *App) maybeOpenWorkspaceTrustPrompt() {
	state := &m.onboarding.trust
	if m.canOpenWorkspaceTrustPrompt() && len(m.pendingOperationIssues) == 0 &&
		(state.askedOwner != m.ws || state.askedGen != m.workspaceSwitchGen.Load()) &&
		m.ws.AnalysisSettings().DecisionNeeded() {
		m.showWorkspaceTrustPrompt(true)
	}
}

// Only an initialized shell starts onboarding. Assigning a viewport alone
// (for a render or embedded model) is not a workspace-opening event.
func (m *App) checkInitialWorkspaceTrust() {
	state := &m.onboarding.trust
	if !state.awaitingFirstSize || m.width <= 0 || m.height <= 0 {
		return
	}
	state.awaitingFirstSize = false
	// Preserve the existing priority of other dialogs and AI setup.
	defaults := m.ws.AppDefaults()
	if defaults.Onboarding.AISetupReminderHidden || len(defaults.Accounts) != 0 {
		m.maybeOpenWorkspaceTrustPrompt()
	}
}

func (m *App) workspaceTrustFrame(prompt *workspaceTrustPrompt) *ui.ComponentWidget {
	var detail []string
	if path := prompt.owner.RootPath(); path != "" {
		detail = append(detail, path)
	}
	detail = append(detail, "Restricted mode allows editing, but not project tools.", "Change this in Workspace settings.")
	if prompt.busy {
		detail = append(detail, "Saving your choice…")
	} else if prompt.err != "" {
		detail = append(detail, prompt.err)
	}
	frame := ui.BuildConfirmFrame(m.Theme, ui.ConfirmFrameSpec{
		Title: "Trust this folder?", Question: "Trust lets project tools run code.", Detail: strings.Join(detail, "\n"),
		Severity: ui.ConfirmPromptQuestion, Width: min(70, max(36, m.width-4)), FocusedIndex: 1,
		Buttons: []ui.ConfirmButtonSpec{{Label: "Trust folder", ActionID: ui.ConfirmActionYes}, {Label: "Open in restricted mode", ActionID: ui.ConfirmActionNo}},
	})
	if prompt.busy {
		disableConfirmButtons(frame)
	}
	return frame
}

func (m *App) activateWorkspaceTrust(choice int) tea.Cmd {
	prompt := m.onboarding.trust.prompt
	if prompt == nil || prompt.busy || prompt.owner != m.ws || prompt.generation != m.workspaceSwitchGen.Load() || choice < 0 || choice > 1 {
		return nil
	}
	value := setting.AnalysisRestricted
	if choice == 0 {
		value = setting.AnalysisTrusted
	}
	if frame, ok := m.overlayHost.Window(ui.OverlayWorkspaceTrust).Content.(*ui.ComponentWidget); ok {
		ui.SetConfirmFocus(frame, choice)
	}
	prompt.busy, prompt.err = true, ""
	m.refreshConfirmModal(ui.OverlayWorkspaceTrust, m.workspaceTrustFrame(prompt))
	raw, _ := json.Marshal(value)
	run := prompt.owner.PreparePreferencesSave(setting.ScopeWorkspace, []setting.Edit{{Path: []string{"analysis", "trust"}, After: raw}})
	return func() tea.Msg { return workspaceTrustSavedMsg{prompt: prompt, result: run()} }
}

func (m *App) handleWorkspaceTrustMessage(msg tea.Msg) (bool, tea.Cmd) {
	state := &m.onboarding.trust
	switch msg := msg.(type) {
	case workspaceTrustSavedMsg:
		p := msg.prompt
		if p == nil || p != state.prompt || p.owner != m.ws || p.generation != m.workspaceSwitchGen.Load() {
			return true, nil
		}
		p.busy = false
		if msg.result.Err != nil {
			p.err = "Could not save your choice: " + msg.result.Err.Error() + ". Try again."
			m.refreshConfirmModal(ui.OverlayWorkspaceTrust, m.workspaceTrustFrame(p))
		} else {
			m.ws.ApplyPreferencesSave(msg.result)
			m.overlayHost.Close(ui.OverlayWorkspaceTrust)
		}
		return true, nil
	case TickMsg:
		if p := state.prompt; p != nil && p.undecided && !p.busy && p.owner == m.ws && p.generation == m.workspaceSwitchGen.Load() && !m.ws.AnalysisSettings().DecisionNeeded() {
			m.overlayHost.Close(ui.OverlayWorkspaceTrust)
		}
		m.maybeOpenWorkspaceTrustPrompt()
	case tea.KeyPressMsg:
		if state.prompt != nil {
			if state.prompt.busy {
				return true, nil
			}
			return m.confirmModalInput(ui.OverlayWorkspaceTrust, msg, state.prompt.busy, m.activateWorkspaceTrust)
		}
	}
	return false, nil
}
