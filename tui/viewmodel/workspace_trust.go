package viewmodel

import (
	"encoding/json"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

type workspaceTrustState struct {
	askedOwner shellport.Workspace
	askedGen   uint64
	prompt     *workspaceTrustPrompt
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

func (m *App) openWorkspaceTrustPrompt() tea.Cmd {
	if m.ws == nil || m.width <= 0 || m.height <= 0 || m.overlayHost == nil || len(m.overlayHost.All()) != 0 ||
		m.workspaceAttachPending || m.sessionLifecyclePending {
		return nil
	}
	state := &m.onboarding.trust
	prompt := &workspaceTrustPrompt{owner: m.ws, generation: m.workspaceSwitchGen.Load(), undecided: m.ws.LoadAppSettings().Analysis.DecisionNeeded()}
	state.prompt, state.askedOwner, state.askedGen = prompt, prompt.owner, prompt.generation
	m.openConfirmModal(ui.OverlayWorkspaceTrust, m.workspaceTrustFrame(prompt), m.activateWorkspaceTrust, func() {
		if state.prompt == prompt {
			state.prompt = nil
		}
	})
	return nil
}

func (m *App) workspaceTrustFrame(prompt *workspaceTrustPrompt) *ui.ComponentWidget {
	detail := prompt.owner.RootPath() + "\n\nTrusted folders can run project tools, such as language servers. These tools run code from the folder. In restricted mode, you can edit files, but Crude does not run project tools. You can change this later in Workspace settings."
	if prompt.busy {
		detail += "\n\nSaving your choice…"
	} else if prompt.err != "" {
		detail += "\n\n" + prompt.err
	}
	frame := ui.BuildConfirmFrame(m.Theme, ui.ConfirmFrameSpec{
		Title: "Trust this folder?", Question: "Do you trust the authors of the files in this folder?", Detail: detail,
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
	value := types.AnalysisRestricted
	if choice == 0 {
		value = types.AnalysisTrusted
	}
	if frame, ok := m.overlayHost.Window(ui.OverlayWorkspaceTrust).Content.(*ui.ComponentWidget); ok {
		ui.SetConfirmFocus(frame, choice)
	}
	prompt.busy, prompt.err = true, ""
	m.refreshConfirmModal(ui.OverlayWorkspaceTrust, m.workspaceTrustFrame(prompt))
	raw, _ := json.Marshal(value)
	run := prompt.owner.PreparePreferencesSave(types.SettingsScopeWorkspace, []types.SettingEdit{{Path: []string{"analysis", "trust"}, After: raw}})
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
	case types.TickMsg:
		if p := state.prompt; p != nil && p.undecided && !p.busy && p.owner == m.ws && p.generation == m.workspaceSwitchGen.Load() && !m.ws.LoadAppSettings().Analysis.DecisionNeeded() {
			m.overlayHost.Close(ui.OverlayWorkspaceTrust)
		}
		if m.ws != nil && m.width > 0 && m.height > 0 && m.overlayHost != nil && len(m.overlayHost.All()) == 0 &&
			!m.workspaceAttachPending && !m.sessionLifecyclePending && len(m.pendingOperationIssues) == 0 &&
			(state.askedOwner != m.ws || state.askedGen != m.workspaceSwitchGen.Load()) && m.ws.LoadAppSettings().Analysis.DecisionNeeded() {
			m.openWorkspaceTrustPrompt()
		}
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
