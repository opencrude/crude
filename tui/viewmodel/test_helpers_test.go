package viewmodel

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/session"
	"crude/tui/cell"
	"crude/tui/compositor"
	"crude/tui/editor"
	"crude/tui/filetree"
	"crude/tui/input"
	"crude/tui/screens/activitybar"
	"crude/tui/screens/activitylist"
	"crude/tui/screens/confirmation"
	"crude/tui/screens/launcher"
	"crude/tui/screens/question"
	"crude/tui/screens/shellchat"
	"crude/tui/screens/shelleditor"
	"crude/tui/screens/shellfooter"
	"crude/tui/screens/shellheader"
	"crude/tui/screens/shellinput"
	"crude/tui/screens/shelltabs"
	"crude/tui/screens/shellterm"
	"crude/tui/screens/shelltree"
	"crude/tui/screens/sourcecontrol"
	"crude/tui/screens/termdock"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/window"
	"crude/tui/wm"
	"crude/workspace"
)

// runAppCommand drives a command through the same message/update loop used by
// Bubble Tea. It deliberately understands only Bubble Tea's native BatchMsg;
// product-result messages must be handled by App.Update rather than injected or
// decoded by a test-specific acknowledgement path.
func runAppCommand(t *testing.T, app *App, command tea.Cmd) {
	t.Helper()
	queue := []tea.Cmd{command}
	for steps := 0; len(queue) > 0; steps++ {
		if steps >= 64 {
			t.Fatal("app command journey did not settle")
		}
		cmd := queue[0]
		queue = queue[1:]
		if cmd == nil {
			continue
		}
		message := cmd()
		if message == nil {
			continue
		}
		if batch, ok := message.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		model, next := app.Update(message)
		if model != app {
			t.Fatalf("command replaced App with %T", model)
		}
		if next != nil {
			queue = append(queue, next)
		}
	}
}

type noopEventSender struct{}

// openTestAgentPrompt mounts a request-specific screen for component-policy
// tests. Family routing tests enter through enqueueAgentPrompt instead.
func openTestAgentPrompt(t testing.TB, app *App, projection *sessionProjection, req agentPromptRequest) *agentPromptState {
	t.Helper()
	if req.agentID == "" {
		req.agentID = "agent"
	}
	if req.blockID == "" {
		req.blockID = "block-1"
	}
	if app.AgentMgr.Get(req.agentID) == nil {
		app.AgentMgr.AddOrchestrator(req.agentID, &workspace.AgentState{})
	}
	state := &agentPromptState{request: req, projection: projection}
	if req.kind == agentPromptQuestions {
		state.question = question.New(app.Theme)
		if count, err := state.question.ResetWithError(req.questionsJSON); err != nil || count == 0 {
			t.Fatalf("invalid question fixture: %v", err)
		}
	} else {
		state.confirmation = confirmation.New(app.Theme)
		state.confirmation.Reset(confirmation.Details{Question: req.question, Tool: req.tool})
	}
	app.agentPrompts = append(app.agentPrompts, state)
	app.openInlineAgentPrompt(state)
	return state
}

func (noopEventSender) SendAgentEvent(typ string, data any)                  {}
func (noopEventSender) SendAgentEventWithID(typ string, data any, id string) {}

// newTestApp builds an App with the full shell tree mounted (all shell
// screens under one rootHost painted into mainSurf) plus the workspace
// stubs. No compositor/renderCtx are created — tests that call View()
// should use newFullTestApp instead.
func newTestApp(t testing.TB) *App {
	t.Helper()
	th := theme.DefaultDark()
	wmgr := wm.NewWindowManager()
	im := input.NewModel(th)

	headerScreen := shellheader.New(th)
	footerScreen := shellfooter.New(th, ui.DefaultRegistry())
	tabScreen := shelltabs.New(th)
	inputScreen := shellinput.New(th)
	chatScreen := shellchat.New(th)
	editorScreen := shelleditor.New(th)
	sidebarScreen := shelltree.New(th, nil)
	activityBar := activitybar.New(th)
	activityList := activitylist.New(th)
	sourceControl := sourcecontrol.New(th)
	launcherScreen := launcher.New(th)
	launcherScreen.SetData(".", nil)
	termDock := termdock.New(th)

	deck := newDeckMount(nil)
	shell := newShellRoot(headerScreen, footerScreen, tabScreen, activityBar, sidebarScreen, chatScreen, inputScreen, editorScreen, launcherScreen, deck, termDock)
	rootHost := ui.NewHost(th, shell)

	app := &App{
		Workspace: &workspace.Workspace{
			AgentMgr:      workspace.NewAgentManager(),
			PaneMgr:       workspace.NewPaneManager(),
			Git:           workspace.NewGitService("."),
			TermMgr:       workspace.NewTerminalManager("."),
			FocusStack:    workspace.NewFocusStack(),
			StartTime:     time.Now(),
			ActiveSession: &workspace.Session{},
			LSPMgr:        workspace.NewLSPManager(nil),
			Status:        workspace.NewStatusCenter(nil, nil),
		},
		Theme:              th,
		Input:              im,
		Tree:               NewTreeState(nil, 30),
		SpinFrame:          0,
		OverlayState:       NewOverlayState(),
		LSPPopup:           NewLSPPopupState(),
		EditorState:        NewEditorState(),
		actionRegistry:     ui.DefaultRegistry(),
		windowManager:      wmgr,
		overlayHost:        ui.NewOverlayHost(wmgr),
		subPaneProjections: newSubPaneProjectionRegistry(th),
		sessionProjections: newSessionProjectionRegistry(th),
		headerScreen:       headerScreen,
		footerScreen:       footerScreen,
		tabScreen:          tabScreen,
		sidebarScreen:      sidebarScreen,
		activityBar:        activityBar,
		activityList:       activityList,
		sourceControl:      sourceControl,
		termScreens:        make(map[string]*shellterm.Screen),
		termDock:           termDock,
		termDockHeight:     12,
		inputScreen:        inputScreen,
		chatScreen:         chatScreen,
		editorScreen:       editorScreen,
		launcherScreen:     launcherScreen,
		shell:              shell,
		rootHost:           rootHost,
		mainSurf:           surface.NewSurface("main-shell", 80, 24, cell.TransparentBlank()),
		width:              80,
		height:             24,
	}
	app.Feedback = newFeedbackSink(app)
	app.initWorkspacePanels()
	deck.app = app
	app.Win = window.New(th)
	launcherScreen.SetFocusHandler(rootHost.Focus)
	sourceControl.SetFocusHandler(rootHost.Focus)
	t.Cleanup(func() {
		app.closeWorkspaceEditors()
		if app.Tree != nil && app.Tree.Model != nil {
			app.Tree.Model.StopWatcher()
		}
		if app.Workspace != nil {
			if app.TermMgr != nil {
				app.TermMgr.CloseAll()
			}
			if app.LSPMgr != nil {
				app.LSPMgr.Stop()
			}
		}
		resetAsyncIOStateForTest(app)
		if app.Workspace != nil {
			app.Close()
		}
	})
	return app
}

// newFullTestApp is newTestApp plus a compositor/renderCtx so View() works.
func newFullTestApp(t testing.TB) *App {
	t.Helper()
	app := newTestApp(t)
	app.compositor = compositor.NewCompositor(app.width, app.height)
	app.renderCtx = wm.NewRenderContext(app.Theme)
	return app
}

// addBackendTestOrchestrator exercises the real Workspace -> Session turn
// admission path while keeping provider events inert.
func addBackendTestOrchestrator(t *testing.T, app *App) string {
	t.Helper()
	previousActive := app.ActiveSession
	app.RootPath = t.TempDir()
	app.WorkspaceDir = t.TempDir()
	if app.SessionsByID == nil {
		app.SessionsByID = make(map[string]*workspace.Session)
	}
	if app.SessionsByAgent == nil {
		app.SessionsByAgent = make(map[string]*workspace.Session)
	}
	_, err := app.CreateSession(testAccountConfig(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	orchestrator, err := app.StartOrchestrator(noopEventSender{})
	if err != nil {
		t.Fatal(err)
	}
	app.AddOrchestrator(orchestrator.ID)
	if previousActive != nil {
		app.ActiveSession = previousActive
	}
	t.Cleanup(func() {
		app.CloseAgent(orchestrator.ID)
		app.Close()
	})
	return orchestrator.ID
}

// newEditorPaintApp builds a compositor-backed App with a real file tree so
// the DocumentEditor shell can be painted on the first View().
func newEditorPaintApp(t *testing.T, root string) *App {
	t.Helper()
	th := theme.DefaultDark()
	w, h := 100, 30
	app := newFullTestApp(t)
	app.Logger = stubLogger{}
	app.width = w
	app.height = h
	app.Theme = th
	app.BufferMgr = editor.NewBufferManager()

	treeModel := filetree.NewFileTreeModel(root)
	app.Tree = NewTreeState(treeModel, 24)
	app.Tree.Visible = true
	app.sidebarScreen.SetModel(treeModel)

	app.ChatAreaHeight = h - 6
	app.EditorState = NewEditorState()
	app.EditorState.Editor = editor.New(th)
	return app
}

// paintShellFrame runs the full per-frame shell configuration and paint for
// tests: geometry, slots, projections, and the root-host render into mainSurf.
func (m *App) paintShellFrame() {
	m.configureShell()
	m.renderShellToSurface()
	m.syncRootFocus()
}

// shellSlotActive reports whether the slot is currently mounted in the tree.
func (m *App) shellSlotActive(id shellSlotID) bool {
	if m.shell == nil {
		return false
	}
	return m.shell.slots[id].active
}

// rootHandleFocus sets the root-host focus and dispatches one message,
// returning the EventResult (mirrors dispatchKeyPress's per-target routing).
func (m *App) rootHandleFocus(focus ui.ComponentID, msg any) ui.EventResult {
	if m.rootHost == nil {
		return ui.EventResult{}
	}
	m.rootHost.Focus(focus)
	return m.rootHost.Handle(msg)
}

// testAccountConfig is an explicitly configured local test connection. Tests
// that exercise real session admission must not rely on credential fallbacks.
func testAccountConfig() *session.Config {
	agents := session.DefaultAgentsConfig()
	profile := agents.Profiles[agents.ActiveProfile]
	main := profile.Categories[session.CategoryDefault]
	main.AccountID, main.Model = "test", "test-model"
	profile.Categories[session.CategoryDefault] = main
	agents.Profiles[agents.ActiveProfile] = profile
	return &session.Config{Accounts: map[string]provider.Account{"test": {ID: "test", ProviderID: "openai", Label: "Test", APIKey: "test", Connection: provider.Template{Name: "OpenAI", ProviderURL: "http://127.0.0.1:1", APIStandard: "openai", AuthType: provider.AuthProtocol}}}, Agents: agents}
}
