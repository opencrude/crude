package viewmodel

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"crude/shellport"
	"crude/tui/editor"
	"crude/tui/screens/operationissue"
	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/types"
)

type operationIssueOwner struct {
	screen     *operationissue.Screen
	generation uint64
}

type repairConfigLoadedMsg struct {
	manager    *editor.BufferManager
	generation uint64
	path       string
	err        error
}

func (m *App) showOperationFailure(title, detail string) {
	m.presentOperationIssue(types.OperationIssue{
		Title: title, Summary: "The operation could not finish. Crude is still available.", Detail: detail,
		Guidance: "Review the error above. Check the relevant settings or storage access, then try the operation again. No retry runs automatically.", Target: types.RepairSettings,
	})
}

func (m *App) presentOperationIssue(issue types.OperationIssue) {
	m.notifyStatus(types.SeverityError, "operation", issue.Title, strings.Join([]string{issue.Summary, issue.Detail, issue.Guidance, issue.Path}, "\n\n"), types.ResourceRef{Kind: types.ResourceWorkspace, ID: m.ws.RootPath()})
	m.pendingOperationIssues = append(m.pendingOperationIssues, issue)
	m.showNextOperationIssue()
}

func (m *App) showNextOperationIssue() {
	if m.width <= 0 || m.height <= 0 || m.overlayHost == nil || len(m.pendingOperationIssues) == 0 || len(m.overlayHost.All()) != 0 {
		return
	}
	issue := m.pendingOperationIssues[0]
	m.pendingOperationIssues = m.pendingOperationIssues[1:]
	label := "Open Settings"
	switch issue.Target {
	case types.RepairAccounts:
		label = "Review accounts"
		if len(m.ws.AllAccounts()) == 0 {
			label = "Add account"
		}
	case types.RepairMainAgent:
		label = "Open Main Agent"
	case types.RepairSessionAccount:
		label = "Choose account"
	case types.RepairNetwork:
		label = "Network settings"
	case types.RepairConfigFile:
		label = "Edit config file"
	}
	screen := operationissue.New(m.Theme, issue, label)
	owner := &operationIssueOwner{screen: screen, generation: m.workspaceSwitchGen.Load()}
	m.operationIssue = owner
	w := min(78, max(1, m.width-4))
	h := min(max(1, m.height-4), min(18, max(8, screen.PreferredHeight(max(1, w-6)))))
	frame := ui.NewWindowFrameWidget(m.Theme, "operation-issue/frame", ui.FrameDialog, issue.Title, screen, nil, false, w, h)
	frame.Frame().SetBorderAccent(ui.NewTokenSet(m.Theme).Color(ui.ColorWarning))
	frame.Host().Focus(operationissue.ActionsID)
	window := wm.NewFloatingWindow(string(ui.OverlayOperationIssue), "", frame, w, h,
		wm.WithDecoration(true), wm.WithMovable(true), wm.WithResizable(true), wm.WithModal(true), wm.WithFocusable(true), wm.WithClosable(false), wm.WithClickOutside(wm.ClickOutsideIgnore))
	wm.CenterOnScreen(window, m.width, m.height)
	policy := ui.ModalDialogPolicy()
	policy.Decorated, policy.Movable, policy.Resizable = true, true, true
	m.overlayHost.Open(ui.OverlayOperationIssue, window, policy, func() {
		if m.operationIssue == owner {
			m.operationIssue = nil
		}
	})
	m.forceFrame()
}

func (m *App) handleOperationIssueAction(msg operationissue.ActionMsg) tea.Cmd {
	owner := m.operationIssue
	if owner == nil || owner.screen != msg.Screen || owner.generation != m.workspaceSwitchGen.Load() || !m.overlayHost.IsOpen(ui.OverlayOperationIssue) {
		return nil
	}
	issue := owner.screen.Issue()
	m.overlayHost.Close(ui.OverlayOperationIssue)
	if msg.Action != "repair" {
		return nil
	}
	switch issue.Target {
	case types.RepairAccounts:
		m.openApplicationAccounts()
	case types.RepairMainAgent:
		return m.openApplicationMainAgent()
	case types.RepairSessionAccount:
		if m.ActiveSession != nil && m.ws.BackendSession(m.ActiveSession.SessionID) != nil && len(m.ws.AllAccounts()) > 0 {
			return m.openSessionAccountPicker()
		}
		m.openApplicationAccounts()
	case types.RepairNetwork:
		m.openApplicationSettings()
		if fw := m.windowManager.Window(string(ui.OverlayWorkspaceSettings)); fw != nil {
			if frame, ok := fw.Content.(*ui.WindowFrameWidget); ok {
				if body, ok := frame.Body().(*settingsui.WorkspaceSettingsSurface); ok {
					body.ShowNetworkConnection()
					frame.Host().MarkLayout()
					frame.Host().Layout()
				}
			}
		}
	case types.RepairConfigFile:
		if _, remote := m.ws.(shellport.ClientFiles); remote {
			cmd, err := m.openEditorTab(issue.Path)
			if err != nil {
				m.showOperationFailure("Config file could not be opened", err.Error())
			}
			return cmd
		}
		if m.BufferMgr == nil {
			m.showOperationFailure("Config editor unavailable", "The document service is not initialized. Your original configuration is unchanged.")
			return nil
		}
		manager, generation, path := m.BufferMgr, m.workspaceSwitchGen.Load(), issue.Path
		return func() tea.Msg {
			_, err := manager.OpenFile(path)
			return repairConfigLoadedMsg{manager: manager, generation: generation, path: path, err: err}
		}
	default:
		m.openApplicationSettings()
	}
	return nil
}

func (m *App) applyRepairConfigLoaded(msg repairConfigLoadedMsg) tea.Cmd {
	if msg.manager != m.BufferMgr || msg.generation != m.workspaceSwitchGen.Load() {
		return nil
	}
	if msg.err != nil {
		m.showOperationFailure("Config file could not be opened", msg.err.Error())
		return nil
	}
	cmd, err := m.openEditorTab(msg.path) // buffer was loaded by the command, not on Update
	if err != nil {
		m.showOperationFailure("Config file could not be opened", err.Error())
	}
	return cmd
}
