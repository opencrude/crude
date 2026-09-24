package viewmodel

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/tui/wm"
)

type settingsPromptState struct {
	request settingsui.PromptMsg
	parent  *wm.FloatingWindow
}

func (m *App) settingsPromptOnTop() bool {
	if m.settingsPrompt == nil || m.overlayHost == nil {
		return false
	}
	top := m.overlayHost.Top()
	return top != nil && top.ID == ui.OverlaySettingsPrompt
}

func (m *App) settingsSurface(fw *wm.FloatingWindow) *settingsui.WorkspaceSettingsSurface {
	if fw == nil {
		return nil
	}
	frame, ok := fw.Content.(*ui.WindowFrameWidget)
	if !ok {
		return nil
	}
	body, _ := frame.Body().(*settingsui.WorkspaceSettingsSurface)
	return body
}

func (m *App) settingsPromptOwner(state *settingsPromptState) *settingsui.WorkspaceSettingsSurface {
	if state == nil || m.overlayHost == nil || m.windowManager == nil ||
		m.overlayHost.Window(ui.OverlayWorkspaceSettings) != state.parent ||
		m.windowManager.Window(string(ui.OverlayWorkspaceSettings)) != state.parent {
		return nil
	}
	body := m.settingsSurface(state.parent)
	if body == nil || body.Model() != state.request.Target {
		return nil
	}
	return body
}

func (m *App) showSettingsPrompt(msg settingsui.PromptMsg) {
	if m.overlayHost == nil || m.windowManager == nil || m.overlayHost.IsOpen(ui.OverlaySettingsPrompt) {
		return
	}
	parent := m.overlayHost.Window(ui.OverlayWorkspaceSettings)
	state := &settingsPromptState{request: msg, parent: parent}
	body := m.settingsPromptOwner(state)
	if body == nil || !body.PromptRelevant(msg) {
		return
	}

	spec := ui.ConfirmFrameSpec{
		Severity: ui.ConfirmPromptWarning,
		Width:    minInt(64, maxInt(36, m.width-4)), DetailMaxLines: 5,
		FocusedIndex: 1,
	}
	switch msg.Kind {
	case settingsui.PromptTemplateInUse:
		spec.Title, spec.Question = "Accounts use this template", "This template cannot be removed"
		plural := "s"
		if msg.RelatedCount == 1 {
			plural = ""
		}
		spec.Detail = fmt.Sprintf("%q is used by %d account%s. Review linked accounts before removing it.", msg.Label, msg.RelatedCount, plural)
		spec.Buttons = []ui.ConfirmButtonSpec{{Label: "Open Accounts", ActionID: ui.ConfirmActionYes}, {Label: "Not now", ActionID: ui.ConfirmActionCancel}}
	case settingsui.PromptRemoveAccount:
		spec.Title, spec.Question = "Remove account", "Remove this account?"
		spec.Detail = fmt.Sprintf("Account %q will be removed. Existing sessions will not be reassigned.", msg.Label)
		spec.Severity = ui.ConfirmPromptDanger
		spec.Buttons = []ui.ConfirmButtonSpec{{Label: "Remove account", ActionID: ui.ConfirmActionYes}, {Label: "Keep account", ActionID: ui.ConfirmActionCancel}}
	case settingsui.PromptRemoveTemplate:
		spec.Title, spec.Question = "Remove template", "Remove this provider template?"
		spec.Detail = fmt.Sprintf("Template %q will be removed. This cannot be undone.", msg.Label)
		spec.Severity = ui.ConfirmPromptDanger
		spec.Buttons = []ui.ConfirmButtonSpec{{Label: "Remove template", ActionID: ui.ConfirmActionYes}, {Label: "Keep template", ActionID: ui.ConfirmActionCancel}}
	case settingsui.PromptDiscardEditor:
		spec.Title, spec.Question = "Discard changes", "Discard unsaved account or template changes?"
		spec.Detail = "The current editor draft will be lost. Saved settings are unchanged."
		spec.Severity = ui.ConfirmPromptDanger
		spec.Buttons = []ui.ConfirmButtonSpec{{Label: "Discard changes", ActionID: ui.ConfirmActionDiscard}, {Label: "Keep editing", ActionID: ui.ConfirmActionCancel}}
	case settingsui.PromptDiscardSettings:
		spec.Title, spec.Question = "Discard drafts", "Discard unsaved settings drafts?"
		spec.Detail = "Unsaved preferences will be lost. Saved settings are unchanged."
		spec.Buttons = []ui.ConfirmButtonSpec{{Label: "Discard drafts", ActionID: ui.ConfirmActionDiscard}, {Label: "Keep settings open", ActionID: ui.ConfirmActionCancel}}
	default:
		return
	}
	frame := ui.BuildConfirmFrame(m.Theme, spec)
	w, h := frame.Measure(m.width, m.height)
	fw := wm.NewFloatingWindow(string(ui.OverlaySettingsPrompt), "", frame, w, h,
		wm.WithDecoration(false), wm.WithMovable(true), wm.WithResizable(false),
		wm.WithClosable(false), wm.WithModal(true), wm.WithFocusable(true),
		wm.WithClickOutside(wm.ClickOutsideIgnore))
	px, py := parent.Position()
	pw, ph := parent.Size()
	x, y := clampPopupPosition(px+(pw-w)/2, py+(ph-h)/2, w, h, m.width, m.height)
	fw.SetPosition(x, y)
	if m.rootHost != nil {
		m.rootHost.CancelPointerCapture()
	}
	m.settingsPrompt = state
	m.overlayHost.Open(ui.OverlaySettingsPrompt, fw, ui.ModalDialogPolicy(), func() {
		if m.settingsPrompt == state {
			m.settingsPrompt = nil
		}
	})
}

func (m *App) activateSettingsPrompt(choice int) tea.Cmd {
	state := m.settingsPrompt
	if state == nil || m.overlayHost == nil || !m.overlayHost.IsOpen(ui.OverlaySettingsPrompt) {
		return nil
	}
	body := m.settingsPromptOwner(state)
	m.overlayHost.Close(ui.OverlaySettingsPrompt)
	if body == nil {
		return nil
	}
	if choice != 0 {
		if state.request.Kind == settingsui.PromptDiscardEditor || state.request.Kind == settingsui.PromptDiscardSettings {
			body.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.discard.stay"})
		}
		return nil
	}
	if !body.PromptRelevant(state.request) {
		m.Feedback.NotifyError("Settings changed; review and retry")
		return nil
	}
	var result ui.EventResult
	switch state.request.Kind {
	case settingsui.PromptTemplateInUse:
		body.ShowAccountsUsingTemplate(state.request.ID)
	case settingsui.PromptRemoveAccount:
		result = body.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.confirm-remove", Payload: state.request.ID})
	case settingsui.PromptRemoveTemplate:
		result = body.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.template.confirm-remove", Payload: state.request.ID})
	case settingsui.PromptDiscardEditor, settingsui.PromptDiscardSettings:
		result = body.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.discard.confirm", Payload: state.request.Kind})
	}
	if frame, ok := state.parent.Content.(*ui.WindowFrameWidget); ok {
		frame.Host().MarkLayout()
		if state.request.Kind == settingsui.PromptTemplateInUse {
			// The Accounts table was not mounted while Providers was active.
			// Mount it before restoring keyboard focus from the modal.
			frame.Host().Layout()
			body.FocusAccountList()
		}
	}
	state.parent.MarkContentDirty()
	return result.Cmd
}

func (m *App) handleSettingsPromptKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.settingsPrompt == nil || m.windowManager == nil {
		return nil
	}
	fw := m.windowManager.Window(string(ui.OverlaySettingsPrompt))
	if fw == nil {
		return nil
	}
	cw, ok := fw.Content.(*ui.ComponentWidget)
	if !ok {
		return nil
	}
	switch msg.Code {
	case tea.KeyEsc:
		return m.activateSettingsPrompt(1)
	case tea.KeyEnter:
		return m.activateSettingsPrompt(ui.ConfirmButtonIndexFromID(cw.Host().Focused()))
	case tea.KeyLeft, tea.KeyUp:
		ui.MoveConfirmFocus(cw, -1, 2)
	case tea.KeyRight, tea.KeyDown, tea.KeyTab:
		delta := 1
		if msg.Code == tea.KeyTab && msg.Mod.Contains(tea.ModShift) {
			delta = -1
		}
		ui.MoveConfirmFocus(cw, delta, 2)
	}
	return nil
}
