package viewmodel

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
	"crude/tui/wm"
)

// showAccountRecovery keeps the launcher or resumed transcript intact. Opening
// settings is deliberate; saving an account never silently retries a turn.
func (m *App) showAccountRecovery(resumed bool) {
	if m.overlayHost == nil || m.windowManager == nil {
		return
	}
	m.accountRecoveryChoice = 1 // Enter must not dismiss into an implicit action.
	m.accountRecoverySession = resumed
	label := "Add account"
	question := "Main Agent needs an account"
	detail := "Add an account, select it for Main Agent, then create the session."
	if !resumed && len(m.AllAccounts()) > 0 {
		label = "Open Main Agent"
		detail = "Select one of your saved accounts for Main Agent, then create the session."
	}
	if resumed {
		question = "This session needs an account"
		detail = "Your conversation is still available. Choose an account, Apply it, then Save the session settings."
		label = "Choose account"
		if len(m.AllAccounts()) == 0 {
			label = "Add account"
			detail = "Your conversation is still available. Add an account, then select and Save it in Session Settings."
		} else if m.ActiveSession == nil {
			label = "Review accounts"
			detail = "The session could not open. Review its saved accounts, then try resuming again."
		}
	}
	frame := ui.BuildConfirmFrame(m.Theme, ui.ConfirmFrameSpec{
		Title: "Account required", Question: question, Detail: detail,
		Severity: ui.ConfirmPromptWarning, Width: minInt(68, maxInt(36, m.width-4)), DetailMaxLines: 6,
		Buttons: []ui.ConfirmButtonSpec{
			{Label: label, ActionID: ui.ConfirmActionYes},
			{Label: "Not now", ActionID: ui.ConfirmActionCancel},
		}, FocusedIndex: 1,
	})
	w, h := frame.Measure(m.width, m.height)
	fw := wm.NewFloatingWindow(string(ui.OverlayAccountRecovery), "", frame, w, h,
		wm.WithDecoration(false), wm.WithMovable(true), wm.WithResizable(false),
		wm.WithClosable(false), wm.WithModal(true), wm.WithFocusable(true),
		wm.WithClickOutside(wm.ClickOutsideIgnore),
	)
	wm.CenterOnScreen(fw, m.width, m.height)
	m.overlayHost.Open(ui.OverlayAccountRecovery, fw, ui.ModalDialogPolicy(), nil)
}

func (m *App) activateAccountRecovery(choice int) tea.Cmd {
	if m.overlayHost == nil || !m.overlayHost.IsOpen(ui.OverlayAccountRecovery) {
		return nil
	}
	m.overlayHost.Close(ui.OverlayAccountRecovery)
	if choice != 0 {
		return nil
	}
	if m.accountRecoverySession && m.ActiveSession != nil && len(m.AllAccounts()) > 0 {
		return m.openSessionAccountPicker()
	}
	if m.accountRecoverySession {
		m.openApplicationAccounts()
		return nil
	}
	if len(m.AllAccounts()) > 0 {
		m.openApplicationMainAgent()
		return nil
	}
	m.openApplicationAccounts()
	return nil
}

func (m *App) handleAccountRecoveryKey(msg tea.KeyPressMsg) tea.Cmd {
	fw := m.windowManager.Window(string(ui.OverlayAccountRecovery))
	if fw == nil {
		return nil
	}
	cw, ok := fw.Content.(*ui.ComponentWidget)
	if !ok {
		return nil
	}
	switch msg.Code {
	case tea.KeyEsc:
		return m.activateAccountRecovery(1)
	case tea.KeyEnter:
		return m.activateAccountRecovery(m.accountRecoveryChoice)
	case tea.KeyLeft, tea.KeyUp:
		if choice := ui.MoveConfirmFocus(cw, -1, 2); choice >= 0 {
			m.accountRecoveryChoice = choice
		}
	case tea.KeyRight, tea.KeyDown, tea.KeyTab:
		delta := 1
		if msg.Code == tea.KeyTab && msg.Mod.Contains(tea.ModShift) {
			delta = -1
		}
		if choice := ui.MoveConfirmFocus(cw, delta, 2); choice >= 0 {
			m.accountRecoveryChoice = choice
		}
	}
	return nil
}
