package viewmodel

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/prompt"
	"crude/shellport"
	"crude/tui/screens/toolsetup"
	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/types"
)

const projectToolsOverlay ui.OverlayID = "project-tool-setup"

type projectToolsState struct {
	workspace  shellport.Workspace
	generation uint64
	checked    uint64
	request    *projectToolsRequest
	notified   string
}

type projectToolsRequest struct {
	workspace  shellport.Workspace
	generation uint64
	port       shellport.ProjectTools
	preview    types.ProjectToolSetupReview
	screen     *toolsetup.Screen
	ctx        context.Context
	cancel     context.CancelFunc
	selection  types.ProjectToolSetupRequest
	origin     *settingsui.OpenProjectToolsMsg
	submitted  bool
}

type projectToolsReviewedMsg struct {
	request *projectToolsRequest
	preview types.ProjectToolSetupReview
	err     error
}

type projectToolsDecidedMsg struct {
	request *projectToolsRequest
	result  types.ProjectToolSetupResult
	err     error
	// quiet means Aeron took the work, so Crude's own record needs no notice.
	quiet bool
}

// projectToolSetupTask turns the user's switches into the work order for
// Aeron: switched-on tools are set up, the others are left alone.
func projectToolSetupTask(project string, items []types.ProjectToolCandidate, selected []string) prompt.ToolSetupTask {
	on := make(map[string]bool, len(selected))
	for _, id := range selected {
		on[id] = true
	}
	task := prompt.ToolSetupTask{Project: project}
	for _, item := range items {
		entry := prompt.ToolSetupItem{Languages: item.Languages, Package: item.Package, Version: item.Version,
			Source: item.Source, Command: item.Command, Status: item.Reason}
		if on[item.ID] {
			task.Setup = append(task.Setup, entry)
		} else {
			task.Skip = append(task.Skip, entry)
		}
	}
	return task
}

func (m *App) projectToolsCurrent(request *projectToolsRequest) bool {
	return request != nil && m.projectTools.request == request && request.workspace == m.ws &&
		request.generation == m.workspaceSwitchGen.Load() && request.ctx.Err() == nil
}

func (m *App) closeProjectTools() {
	if request := m.projectTools.request; request != nil {
		m.projectTools.request = nil
		request.cancel()
	}
	if m.overlayHost != nil {
		m.overlayHost.Close(projectToolsOverlay)
	}
}

// requestProjectTools is a joined, cancellable read. A later language or catalog
// change gets a quiet notice; only the first project choice opens automatically.
func (m *App) requestProjectTools(selection types.ProjectToolSetupRequest, origin *settingsui.OpenProjectToolsMsg) tea.Cmd {
	if m.ws == nil || m.projectTools.request != nil {
		return nil
	}
	if !m.ws.LoadAppSettings().Analysis.Trusted() {
		if selection.Explicit && m.Feedback != nil {
			m.Feedback.NotifyInfo("Trust this folder before setting up project tools.")
		}
		return nil
	}
	port, ok := m.ws.(shellport.ProjectTools)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	request := &projectToolsRequest{workspace: m.ws, generation: m.workspaceSwitchGen.Load(),
		port: port, ctx: ctx, cancel: cancel, selection: selection, origin: origin}
	m.projectTools.request = request
	return m.clientIOCommand(ctx, func(ctx context.Context) tea.Msg {
		ctx, stop := context.WithTimeout(ctx, 30*time.Second)
		defer stop()
		preview, err := port.ReviewProjectTools(ctx, request.selection)
		return projectToolsReviewedMsg{request: request, preview: preview, err: err}
	})
}

func (m *App) showProjectTools(request *projectToolsRequest) {
	request.screen = toolsetup.New(m.Theme, request.preview.Review)
	w, h := min(100, max(1, m.width-4)), min(30, max(1, m.height-4))
	frame := ui.NewWindowFrameWidget(m.Theme, "project-tools/frame", ui.FrameDialog,
		"Set up code intelligence", request.screen, nil, true, w, h)
	frame.Focus(toolsetup.ActionsID)
	window := wm.NewFloatingWindow(string(projectToolsOverlay), "", frame, w, h,
		wm.WithDecoration(true), wm.WithMovable(true), wm.WithResizable(true),
		wm.WithClosable(true), wm.WithFocusable(true), wm.WithModal(true),
		wm.WithClickOutside(wm.ClickOutsideIgnore))
	wm.CenterOnScreen(window, m.width, m.height)
	if m.rootHost != nil {
		m.rootHost.CancelPointerCapture()
	}
	m.overlayHost.Open(projectToolsOverlay, window, ui.ModalDialogPolicy(), func() {
		if !request.submitted && m.projectTools.request == request {
			m.projectTools.request = nil
			request.cancel()
		}
	})
	m.forceFrame()
}

func (m *App) handleProjectToolsMessage(msg tea.Msg) (bool, tea.Cmd) {
	switch msg := msg.(type) {
	case projectToolsReviewedMsg:
		request := msg.request
		if !m.projectToolsCurrent(request) {
			return true, nil
		}
		if request.origin != nil {
			var body *settingsui.WorkspaceSettingsSurface
			if m.overlayHost != nil {
				body = m.settingsSurface(m.overlayHost.Window(ui.OverlayWorkspaceSettings))
			}
			if body == nil || !body.ProjectToolsRequestRelevant(*request.origin) {
				m.closeProjectTools()
				return true, nil
			}
		}
		if msg.err != nil || !msg.preview.NeedsDecision || msg.preview.ID == "" {
			m.projectTools.request = nil
			request.cancel()
			if msg.err != nil && request.selection.Explicit && m.Feedback != nil {
				m.Feedback.NotifyError("Could not review project tools. " + msg.err.Error())
			} else if request.selection.Explicit && m.Feedback != nil {
				m.Feedback.NotifyInfo(msg.preview.Message)
			}
			return true, nil
		}
		request.preview = msg.preview
		if !request.selection.Explicit && (msg.preview.Quiet || m.overlayHost == nil || len(m.overlayHost.All()) != 0) {
			m.projectTools.request = nil
			request.cancel()
			if m.projectTools.notified != msg.preview.Review.Token && m.Feedback != nil {
				m.projectTools.notified = msg.preview.Review.Token
				m.Feedback.NotifyInfo("Code intelligence tools are available. Choose Set up tools in the Workspace panel.")
			}
			return true, nil
		}
		m.showProjectTools(request)
		return true, nil
	case toolsetup.CloseMsg:
		if request := m.projectTools.request; m.projectToolsCurrent(request) && request.screen == msg.Target && !request.submitted {
			m.closeProjectTools()
		}
		return true, nil
	case toolsetup.DecisionMsg:
		request := m.projectTools.request
		if !m.projectToolsCurrent(request) || request.screen != msg.Target || request.submitted {
			return true, nil
		}
		request.submitted = true
		m.overlayHost.Close(projectToolsOverlay)
		var task tea.Cmd
		if len(msg.Selected) > 0 {
			// Aeron does the work. Crude only records that the user answered,
			// so the question is not asked again for this project.
			task = m.taskWorkspaceAeron(prompt.ToolSetupMessage(projectToolSetupTask(m.ws.RootPath(), msg.Target.Items(), msg.Selected)))
			if m.Feedback != nil {
				m.Feedback.NotifyInfo("Aeron is setting up the tools. You can keep editing; open Aeron in the Agents panel to watch.")
			}
		}
		decide := m.clientIOCommand(request.ctx, func(ctx context.Context) tea.Msg {
			result, err := request.port.DecideProjectTools(ctx, request.preview.ID, nil)
			return projectToolsDecidedMsg{request: request, result: result, err: err, quiet: len(msg.Selected) > 0}
		})
		return true, tea.Batch(task, decide)
	case projectToolsDecidedMsg:
		if !m.projectToolsCurrent(msg.request) {
			return true, nil
		}
		m.projectTools.request = nil
		msg.request.cancel()
		if m.Feedback != nil {
			if msg.err != nil {
				m.Feedback.NotifyError("Could not finish code intelligence setup. " + msg.err.Error())
			} else if msg.result.Message != "" && !msg.quiet {
				m.Feedback.NotifyInfo(msg.result.Message)
			}
		}
		return true, nil
	case types.TickMsg:
		state := &m.projectTools
		if state.workspace != m.ws || state.generation != m.workspaceSwitchGen.Load() {
			m.closeProjectTools()
			*state = projectToolsState{workspace: m.ws, generation: m.workspaceSwitchGen.Load()}
		}
		if state.request != nil || m.ws == nil || m.ws.LSPMgr() == nil || !m.ws.LoadAppSettings().Analysis.Trusted() ||
			m.width == 0 || m.height == 0 || m.overlayHost == nil || len(m.overlayHost.All()) != 0 ||
			m.workspaceAttachPending || m.sessionLifecyclePending {
			return false, nil
		}
		plan := m.ws.LSPMgr().Assembly()
		if plan.State == types.AssemblyReady && !plan.Limited && plan.Generation != state.checked {
			state.checked = plan.Generation
			return false, m.requestProjectTools(types.ProjectToolSetupRequest{}, nil)
		}
	}
	return false, nil
}
