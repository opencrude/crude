package viewmodel

import (
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/ui"
	"crude/tui/wm"
)

// This immutable preview owns only consent to navigate, never an attachment.
// The switch lifecycle starts after approval and rechecks all live work.
type workspaceOpenPrompt struct {
	owner          shellport.Workspace
	generation     uint64
	root, resumeID string
}

func (m *App) requestWorkspaceOpen(root, resumeID string) tea.Cmd {
	if root == "" || m.asyncIOStopped.Load() || m.workspaceAttachPending || m.workspaceOpenPrompt != nil {
		return nil
	}
	if sameWorkspaceRoot(m.ws.RootPath(), root) {
		if resumeID != "" {
			return m.remoteSessionCommand(resumeID)
		}
		return nil
	}
	if m.overlayHost == nil || m.windowManager == nil {
		return nil
	}
	for _, entry := range m.overlayHost.All() {
		if entry.Policy.Modal {
			return nil
		}
	}
	p := &workspaceOpenPrompt{owner: m.ws, generation: m.workspaceSwitchGen.Load(), root: root, resumeID: resumeID}
	frame := ui.BuildConfirmFrame(m.Theme, ui.ConfirmFrameSpec{
		Title: "Switch workspace", Question: "Open " + filepath.Base(root) + "?",
		Detail:   root + "\nThis shell will leave the current workspace. Its agents and terminals keep running in the core. Unsaved files are reviewed next; composer drafts are saved before switching.",
		Severity: ui.ConfirmPromptQuestion, Width: min(72, max(36, m.width-4)), FocusedIndex: 1,
		Buttons: []ui.ConfirmButtonSpec{{Label: "Open workspace", ActionID: ui.ConfirmActionYes}, {Label: "Cancel", ActionID: ui.ConfirmActionCancel}},
	})
	w, h := frame.Measure(m.width, m.height)
	w, h = min(w, max(1, m.width)), min(h, max(1, m.height))
	fw := wm.NewFloatingWindow(string(ui.OverlayWorkspaceSwitch), "", frame, w, h,
		wm.WithMovable(true), wm.WithResizable(false), wm.WithClosable(false), wm.WithModal(true), wm.WithFocusable(true), wm.WithClickOutside(wm.ClickOutsideIgnore))
	wm.CenterOnScreen(fw, m.width, m.height)
	m.rootHost.CancelPointerCapture()
	m.workspaceOpenPrompt = p
	policy := ui.ModalDialogPolicy()
	policy.Movable = true
	m.overlayHost.Open(ui.OverlayWorkspaceSwitch, fw, policy, func() {
		if m.workspaceOpenPrompt == p {
			m.workspaceOpenPrompt = nil
		}
	})
	m.forceFrame()
	return nil
}

func (m *App) resolveWorkspaceOpenPrompt(approve bool) tea.Cmd {
	p := m.workspaceOpenPrompt
	if p == nil {
		return nil
	}
	top := m.overlayHost.Top()
	valid := p.owner == m.ws && p.generation == m.workspaceSwitchGen.Load() && top != nil && top.ID == ui.OverlayWorkspaceSwitch
	m.overlayHost.Close(ui.OverlayWorkspaceSwitch)
	if !approve || !valid || m.asyncIOStopped.Load() {
		return nil
	}
	return m.openWorkspaceConfirmed(p.root, p.resumeID)
}

func (m *App) handleWorkspaceOpenPromptInput(msg tea.Msg) (bool, tea.Cmd) {
	if m.workspaceOpenPrompt == nil {
		return false, nil
	}
	fw := m.overlayHost.Window(ui.OverlayWorkspaceSwitch)
	if fw == nil {
		m.workspaceOpenPrompt = nil
		return false, nil
	}
	if top := m.overlayHost.Top(); top == nil || top.Window != fw {
		return false, nil // a later owner-correlated modal owns its own input
	}
	// This route precedes ordinary update dispatch, so deliberate input must
	// invalidate the presentation gate here as well as mutating the dialog.
	switch msg.(type) {
	case tea.KeyPressMsg, tea.MouseClickMsg:
		m.forceFrame()
	}
	switch event := msg.(type) {
	case tea.KeyPressMsg:
		cw, ok := fw.Content.(*ui.ComponentWidget)
		if !ok {
			return true, m.resolveWorkspaceOpenPrompt(false)
		}
		switch event.Code {
		case tea.KeyEsc:
			return true, m.resolveWorkspaceOpenPrompt(false)
		case tea.KeyEnter:
			return true, m.resolveWorkspaceOpenPrompt(ui.ConfirmButtonIndexFromID(cw.Host().Focused()) == 0)
		case tea.KeyLeft, tea.KeyUp:
			ui.MoveConfirmFocus(cw, -1, 2)
		case tea.KeyRight, tea.KeyDown, tea.KeyTab:
			delta := 1
			if event.Code == tea.KeyTab && event.Mod.Contains(tea.ModShift) {
				delta = -1
			}
			ui.MoveConfirmFocus(cw, delta, 2)
		}
		m.forceFrame()
		return true, nil
	case tea.PasteMsg, tea.MouseWheelMsg:
		return true, nil
	case tea.MouseClickMsg:
		if event.Button != tea.MouseLeft {
			return true, nil
		}
	}
	return false, nil
}
