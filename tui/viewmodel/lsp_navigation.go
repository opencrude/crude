package viewmodel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/document"
	"crude/shellport"
	"crude/tui/editor"
	"crude/tui/screens/navigation"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/types"
	"crude/workspace"
)

const navigationOverlay = ui.OverlayNavigation

type navigationState struct {
	workspace            shellport.Workspace
	editor               *editor.Editor
	source               types.InsightSource
	selection            codeActionSelection
	path, kind           string
	line, col            int
	revision, generation uint64
	port                 shellport.Navigation
	screen               *navigation.Screen
	ctx                  context.Context
	cancel               context.CancelFunc
	deadline             time.Time
	result               *types.LSPNavigation
	shown, reading       bool
	landing              bool
	selected             int
	focus                FocusTarget
}

type navigationResultMsg struct {
	owner  *navigationState
	result types.LSPNavigation
	err    error
}

type navigationTargetMsg struct {
	owner   *navigationState
	index   int
	landing types.LSPNavigationLanding
	err     error
}

// paneNavigation retains the explicit invocation through the existing async
// file-open owner. A retired result cannot create a tab or move the old editor.
type paneNavigation struct {
	owner   *navigationState
	landing types.LSPNavigationLanding
	hash    string // computed off Update from the actually opened snapshot
}

func (m *App) closeNavigation() {
	if s := m.navigation; s != nil {
		m.navigation = nil
		s.cancel()
	}
	if m.overlayHost != nil {
		m.overlayHost.Close(navigationOverlay)
	}
}

func navigationTitle(kind string) string {
	switch kind {
	case types.LSPNavigationDefinition:
		return "Go to definition"
	case types.LSPNavigationTypeDefinition:
		return "Go to type definition"
	case types.LSPNavigationImplementation:
		return "Go to implementation"
	case types.LSPNavigationReferences:
		return "Find references"
	default:
		return "Navigation"
	}
}

func (m *App) requestNavigationCmd(kind string) tea.Cmd {
	m.closeNavigation()
	m.closeSignatureHelp()
	m.closeCodeActions()
	ed := m.EditorState.Editor
	port, ok := m.ws.(shellport.Navigation)
	if !ok || ed == nil || !ed.IsOpen() || !m.manualLSPAvailable() {
		m.Feedback.NotifyInfo("The language server is not running for this file. Open Language Servers to start it.")
		return nil
	}
	source, current := ed.CurrentInsightSource()
	if !current || source.Kind != "canonical" {
		m.Feedback.NotifyInfo("The file is still syncing. Try again in a moment.")
		return nil
	}
	base, generation := m.lspRequestContext()
	ctx, cancel := context.WithCancel(base)
	line, col := ed.LSPCursorPosition()
	s := &navigationState{
		workspace: m.ws, editor: ed, source: source, selection: currentCodeActionSelection(ed),
		path: ed.FilePath(), kind: kind, line: line, col: col, revision: ed.Revision(),
		generation: generation, port: port, screen: navigation.New(m.Theme), ctx: ctx, cancel: cancel,
		deadline: time.Now().Add(workspace.ManualLSPRequestTimeout + 5*time.Second), selected: -1,
	}
	if m.FocusStack != nil {
		s.focus = m.FocusStack.Top()
	}
	s.screen.SetLoading(kind)
	m.navigation = s
	w, h := min(104, max(1, m.width-4)), min(28, max(1, m.height-4))
	frame := ui.NewWindowFrameWidget(m.Theme, "navigation/frame", ui.FramePopover, navigationTitle(kind), s.screen, nil, true, w, h)
	frame.Focus(s.screen.DefaultFocus())
	fw := wm.NewFloatingWindow(string(navigationOverlay), "", frame, w, h, wm.WithDecoration(false),
		wm.WithMovable(false), wm.WithResizable(false), wm.WithClosable(true), wm.WithFocusable(true),
		wm.WithClickOutside(wm.ClickOutsideCloseRoute))
	m.placeEditorPicker(fw, frame, ed)
	m.overlayHost.Open(navigationOverlay, fw, ui.PopoverPolicy(true), func() {
		if m.navigation == s && !s.landing {
			m.navigation = nil
			s.cancel()
		}
	})
	return func() tea.Msg {
		request, stop := context.WithTimeout(s.ctx, workspace.ManualLSPRequestTimeout)
		defer stop()
		result, err := port.RequestLSPNavigation(request, source, kind, line, col, kind == types.LSPNavigationReferences)
		return navigationResultMsg{owner: s, result: result, err: err}
	}
}

func (m *App) navigationCurrent(s *navigationState) bool {
	if s == nil || m.navigation != s || m.ws != s.workspace || m.asyncIOStopped.Load() ||
		!m.acceptsLSPDocument(s.generation, s.path, s.editor, s.revision) ||
		!s.editor.MatchesInsightSource(s.source) || currentCodeActionSelection(s.editor) != s.selection ||
		m.ws.LSPMgr() == nil {
		return false
	}
	if m.termDockFocused || m.sessionDeckCapturesInput() || m.FocusStack != nil && m.FocusStack.Top() != s.focus {
		return false
	}
	if s.landing {
		// Opening a target changes the aggregate active-file status. The source
		// provider must still be the exact instance that qualified the selected
		// target, even while a different destination server starts.
		if s.result == nil || s.result.Boundary.Producer.InstanceID == "" {
			return false
		}
		owner, _ := m.ws.LSPMgr().ContextCursor()
		if owner != s.source.WorkspaceGeneration {
			return false
		}
		found := false
		for _, health := range m.ws.LSPMgr().ServerHealthSnapshots() {
			if health.InstanceID == s.result.Boundary.Producer.InstanceID && health.Connected {
				found = true
				break
			}
		}
		if !found {
			return false
		}
		return m.focusTarget() == FocusEditor && !m.termDockOwnsKeys() && !m.sessionDeckCapturesInput()
	}
	if s.shown && (s.result == nil || !m.lspProducerConnected(s.result.Boundary.Producer.InstanceID)) {
		return false
	}
	if m.overlayHost == nil {
		return false
	}
	fw := m.overlayHost.Window(navigationOverlay)
	if fw == nil || m.focusedPopup() != fw {
		return false
	}
	frame, ok := fw.Content.(*ui.WindowFrameWidget)
	return ok && frame.Body() == s.screen
}

func (m *App) markNavigationDirty(s *navigationState) {
	if m.navigation != s || m.overlayHost == nil {
		return
	}
	if fw := m.overlayHost.Window(navigationOverlay); fw != nil {
		frame := fw.Content.(*ui.WindowFrameWidget)
		title := s.screen.Title()
		if title == "" {
			title = navigationTitle(s.kind)
		}
		frame.Frame().SetTitle(title)
		m.placeEditorPicker(fw, frame, s.editor)
		frame.Host().MarkLayout()
		frame.Host().Layout()
		frame.Focus(s.screen.DefaultFocus())
		fw.MarkContentDirty()
		m.forceFrame()
	}
}

func (m *App) handleNavigationResult(msg navigationResultMsg) {
	m.syncNavigation()
	s := msg.owner
	if !m.navigationCurrent(s) || s.ctx.Err() != nil {
		return
	}
	if msg.err != nil {
		s.deadline = time.Time{}
		m.logLSPFailure("navigation", msg.err)
		s.screen.SetError("Could not search. The language server returned an error. Try again.")
		m.markNavigationDirty(s)
		return
	}
	if msg.result.Kind != s.kind || msg.result.Line != s.line || msg.result.Col != s.col ||
		msg.result.IncludeDeclaration != (s.kind == types.LSPNavigationReferences) {
		s.screen.SetError("The answer does not match this search. Search again.")
		s.deadline = time.Time{}
		m.markNavigationDirty(s)
		return
	}
	s.result = &msg.result
	m.syncNavigation()
}

func (m *App) syncNavigation() {
	s := m.navigation
	if s == nil {
		return
	}
	if !m.navigationCurrent(s) {
		m.closeNavigation()
		return
	}
	if !s.deadline.IsZero() && time.Now().After(s.deadline) {
		if s.landing {
			m.closeNavigation()
			m.Feedback.NotifyInfo("Opening the file took too long. Search again.")
			return
		}
		s.cancel()
		s.deadline = time.Time{}
		s.result = nil
		s.screen.SetError("The language server did not answer in time. It may still be loading the project. Try again in a moment.")
		m.markNavigationDirty(s)
		return
	}
	if s.result == nil || s.landing {
		return
	}
	owner, generation := m.ws.LSPMgr().ContextCursor()
	boundary := s.result.Boundary
	if owner == "" || owner != boundary.Source.WorkspaceGeneration ||
		generation > boundary.Context.Generation || boundary.Source != s.source {
		m.closeNavigation()
		return
	}
	if generation < boundary.Context.Generation || s.shown {
		return
	}
	// The context can be published while its replacement server is still
	// starting. Wait for that exact producer's connected publication too; do
	// not show an answer and retire it on the next Update's older readiness.
	if !m.lspProducerConnected(boundary.Producer.InstanceID) {
		return
	}
	s.shown, s.deadline = true, time.Time{}
	s.screen.SetResult(*s.result)
	m.markNavigationDirty(s)
	// A single in-project destination is the normal direct-jump case. Multiple
	// and external destinations always need an explicit native chooser action.
	if s.kind != types.LSPNavigationReferences && len(s.result.Targets) == 1 &&
		s.result.Targets[0].Path != "" && s.result.Targets[0].UnavailableReason == "" && !s.result.Targets[0].External {
		m.runDetachedAsyncCmd(m.openNavigationTarget(navigation.OpenMsg{Target: s.screen, Index: 0}))
	}
}

func (m *App) openNavigationTarget(msg navigation.OpenMsg) tea.Cmd {
	s := m.navigation
	if !m.navigationCurrent(s) || s.ctx.Err() != nil || !s.shown || s.result == nil || s.reading ||
		msg.Target != s.screen || msg.Index < 0 || msg.Index >= len(s.result.Targets) {
		return nil
	}
	target := s.result.Targets[msg.Index]
	if target.Path == "" || target.UnavailableReason != "" {
		return nil
	}
	s.reading, s.selected, s.deadline = true, msg.Index, time.Now().Add(10*time.Second)
	s.screen.SetBusy(true)
	m.markNavigationDirty(s)
	result := *s.result
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
		defer cancel()
		landing, err := s.port.InspectLSPNavigationTarget(ctx, result.Boundary, result.WireVersion, result.Kind, target)
		return navigationTargetMsg{owner: s, index: msg.Index, landing: landing, err: err}
	}
}

func (m *App) handleNavigationTarget(msg navigationTargetMsg) tea.Cmd {
	m.syncNavigation()
	s := msg.owner
	if !m.navigationCurrent(s) || !s.reading || s.ctx.Err() != nil || msg.index != s.selected {
		return nil
	}
	s.reading, s.deadline = false, time.Time{}
	if msg.err != nil {
		m.logLSPFailure("navigation-open", msg.err)
		s.screen.SetError("Could not open the file. It may have moved or changed. Search again.")
		m.markNavigationDirty(s)
		return nil
	}
	if !reflect.DeepEqual(msg.landing.Boundary, s.result.Boundary) ||
		!reflect.DeepEqual(msg.landing.Target, s.result.Targets[msg.index]) || len(msg.landing.TextHash) != 64 {
		s.screen.SetError("That place changed while it was opened. Search again.")
		m.markNavigationDirty(s)
		return nil
	}
	s.landing = true
	s.deadline = time.Now().Add(10 * time.Second)
	m.overlayHost.Close(navigationOverlay) // keep the owner until async open settles
	openCtx, stop := context.WithTimeout(s.ctx, 10*time.Second)
	cmd := m.openPaneFilesContext(openCtx, []string{msg.landing.Target.Path}, m.Win.ActiveGroup())
	if cmd == nil {
		stop()
		m.closeNavigation()
		return nil
	}
	return func() tea.Msg {
		defer stop()
		if s.ctx.Err() != nil {
			return nil
		}
		opened := cmd().(paneFilesOpenedMsg)
		n := &paneNavigation{owner: s, landing: msg.landing}
		if len(opened.files) == 1 && opened.files[0].snapshot != nil && opened.files[0].err == nil {
			sum := sha256.Sum256(opened.files[0].snapshot.Text)
			n.hash = hex.EncodeToString(sum[:])
		}
		opened.navigation = n
		return opened
	}
}

func navigationOpenedSource(s document.Snapshot) types.InsightSource {
	return types.InsightSource{Kind: "canonical", WorkspaceGeneration: s.AnalysisGeneration,
		DocumentID: s.ID, Lifetime: s.AnalysisLifetime, Path: s.Path, Revision: s.Revision}
}

// Refuse a changed target before installing or activating any tab. The hash is
// computed in the async open command, never by exporting source on Update.
func (m *App) navigationLandingMatches(msg paneFilesOpenedMsg) bool {
	n := msg.navigation
	if len(msg.files) != 1 || msg.files[0].err != nil || msg.files[0].snapshot == nil {
		return false
	}
	snapshot := *msg.files[0].snapshot
	source := navigationOpenedSource(snapshot)
	if snapshot.Conflict || snapshot.Path != n.landing.Target.Path || n.hash != n.landing.TextHash ||
		n.landing.Source.Kind == "canonical" && source != n.landing.Source {
		return false
	}
	if tab := m.EditorState.Tabs[snapshot.Path]; tab != nil {
		if !tab.Editor.MatchesInsightSource(source) {
			return false
		}
		if state := m.liveEditors[tab.Editor]; state != nil && state.refs != nil {
			return true // An already installed, settled private view wins.
		}
	}
	// Ordinary file-open is allowed to restore a private recovery over the
	// canonical snapshot. Navigation must not install that tab before discovering
	// its observed target is no longer the actual editable source. Leave recovery
	// untouched and let the ordinary open/recovery flow resolve it explicitly.
	return msg.files[0].recoveryErr == nil && len(msg.files[0].recovery.Payload) == 0
}

func (m *App) applyNavigationLanding(msg paneFilesOpenedMsg) {
	n := msg.navigation
	defer m.closeNavigation()
	ed := m.EditorState.Editor
	if ed == nil || len(msg.files) != 1 || msg.files[0].snapshot == nil {
		return
	}
	source := navigationOpenedSource(*msg.files[0].snapshot)
	if n.hash != n.landing.TextHash || !ed.MatchesInsightSource(source) ||
		(n.landing.Source.Kind == "canonical" && !ed.MatchesInsightSource(n.landing.Source)) {
		m.Feedback.NotifyInfo("The file changed. Search again.")
		return
	}
	r := n.landing.Target.Selection
	if err := ed.SetLSPNavigationRange(r); err != nil {
		m.Feedback.NotifyError(err.Error())
		return
	}
	ed.ScrollToCursor()
}
