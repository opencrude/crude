package viewmodel

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/memory"
	"crude/shellport"
	memorybrowser "crude/tui/screens/memorybrowser"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
)

const memoryBrowserTimeout = 5 * time.Second

type memoryBrowserController struct {
	owner           shellport.Workspace
	generation      uint64
	searchCancel    context.CancelFunc
	detailCancel    context.CancelFunc
	mutationCancel  context.CancelFunc
	searchRequest   memorybrowser.SearchMsg
	detailRequest   memorybrowser.DetailMsg
	mutationRequest memorybrowser.MutationMsg
}

var memoryBrowserControllers sync.Map

type memoryBrowserSearchResultMsg struct {
	Request             memorybrowser.SearchMsg
	Owner               shellport.Workspace
	WorkspaceGeneration uint64
	Records             []memory.ScoredRecord
	Episodes            []memory.Episode
	Err                 error
}

type memoryBrowserDetailResultMsg struct {
	Request             memorybrowser.DetailMsg
	Owner               shellport.Workspace
	WorkspaceGeneration uint64
	Detail              memorybrowser.DetailData
	Err                 error
}

type memoryBrowserMutationResultMsg struct {
	Request             memorybrowser.MutationMsg
	Owner               shellport.Workspace
	WorkspaceGeneration uint64
	Record              memory.Record
	Err                 error
}

func (m *App) memoryBrowserController() *memoryBrowserController {
	value, loaded := memoryBrowserControllers.LoadOrStore(m, &memoryBrowserController{owner: m.ws, generation: m.workspaceSwitchGen.Load()})
	controller := value.(*memoryBrowserController)
	if loaded && (controller.owner != m.ws || controller.generation != m.workspaceSwitchGen.Load()) {
		controller.cancel()
		controller.owner, controller.generation = m.ws, m.workspaceSwitchGen.Load()
	}
	return controller
}

func (controller *memoryBrowserController) cancel() {
	for _, cancel := range []context.CancelFunc{controller.searchCancel, controller.detailCancel, controller.mutationCancel} {
		if cancel != nil {
			cancel()
		}
	}
	controller.searchCancel, controller.detailCancel, controller.mutationCancel = nil, nil, nil
	controller.searchRequest, controller.detailRequest, controller.mutationRequest = memorybrowser.SearchMsg{}, memorybrowser.DetailMsg{}, memorybrowser.MutationMsg{}
}

func (m *App) memoryBrowserVisible() bool {
	return m.memoryBrowser != nil && m.memoryBrowser.IsOpen() && m.termDock != nil && m.panelShown(termdock.PanelMemory)
}

func (m *App) toggleMemoryBrowser() (tea.Model, tea.Cmd) {
	if m.termDock == nil {
		return m, nil
	}
	m.syncMemoryBrowserLifecycle()
	if m.memoryBrowser != nil && m.memoryBrowser.IsOpen() && m.revealPlacedPanel(termdock.PanelMemory) {
		return m, nil
	}
	if m.memoryBrowserVisible() && m.termDockFocused {
		m.closeMemoryBrowser()
		return m, nil
	}
	m.memoryBrowserController()
	if m.memoryBrowser == nil || m.termDock.Panel(termdock.PanelMemory) != m.memoryBrowser {
		m.memoryBrowser = memorybrowser.New(m.Theme)
		m.memoryBrowser.SetFocusHandler(func(id ui.ComponentID) {
			if m.rootHost != nil {
				m.rootHost.Focus(id)
			}
		})
		m.memoryBrowser.SetClipboardCopy(func(text string) tea.Cmd {
			request := m.prepareClipboardAsyncRequest(clipboardAsyncRequestMsg{operation: clipboardAsyncWriteText, text: text})
			return m.clipboardAsyncCommand(request)
		})
		m.termDock.SetPanel(termdock.PanelMemory, m.memoryBrowser)
	}
	wasVisible := m.memoryBrowserVisible()
	if !wasVisible {
		m.termDockHeight = defaultWorkspacePanelHeight(termdock.PanelMemory)
		if height := m.workspacePanelHeights[termdock.PanelMemory]; height > 0 {
			m.termDockHeight = height
		}
	}
	m.termDock.ShowPanel(termdock.PanelMemory)
	m.setSessionDeckFocused(false)
	m.setTermDockFocused(true)
	m.FocusStack.Clear()
	var command tea.Cmd
	if !wasVisible {
		command = m.memoryBrowser.Open()
	}
	m.markMemoryBrowserDirty()
	return m, command
}

func (m *App) closeMemoryBrowser() {
	if m.memoryBrowser != nil {
		m.memoryBrowser.Close()
	}
	if value, ok := memoryBrowserControllers.Load(m); ok {
		value.(*memoryBrowserController).cancel()
	}
	if m.termDock != nil {
		m.releasePlacement(termdock.PanelMemory)
	}
	m.markMemoryBrowserDirty()
}

func (m *App) resetMemoryBrowser() {
	m.closeMemoryBrowser()
	memoryBrowserControllers.Delete(m)
	m.memoryBrowser = nil
}

func (m *App) syncMemoryBrowserLifecycle() {
	if m.memoryBrowser != nil && m.memoryBrowser.IsOpen() && !m.memoryBrowserVisible() {
		m.closeMemoryBrowser()
	}
	if value, ok := memoryBrowserControllers.Load(m); ok {
		controller := value.(*memoryBrowserController)
		if controller.owner != m.ws || controller.generation != m.workspaceSwitchGen.Load() {
			m.resetMemoryBrowser()
		}
	}
}

func (m *App) ownsMemoryBrowserResult(owner shellport.Workspace, generation uint64, target *memorybrowser.Screen) bool {
	return owner == m.ws && generation == m.workspaceSwitchGen.Load() && target == m.memoryBrowser && m.memoryBrowserVisible()
}

func memoryBrowserWorkspace(owner shellport.Workspace) shellport.MemoryStore {
	if owner == nil {
		return nil
	}
	return owner.BindMemory()
}

func (m *App) memoryBrowserOwnsFocus(id ui.ComponentID) bool {
	return m.memoryBrowserVisible() && (id == memorybrowser.ScreenID || strings.HasPrefix(string(id), "memorybrowser/"))
}

func (m *App) handleMemoryBrowserSearch(msg memorybrowser.SearchMsg) tea.Cmd {
	if !m.memoryBrowserVisible() || msg.Target != m.memoryBrowser || !msg.Target.OwnsSearch(msg) {
		return nil
	}
	controller := m.memoryBrowserController()
	if controller.searchRequest.Target == msg.Target && controller.searchRequest.Epoch == msg.Epoch && controller.searchRequest.Sequence == msg.Sequence {
		return nil
	}
	controller.searchRequest = msg
	if controller.searchCancel != nil {
		controller.searchCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), memoryBrowserTimeout)
	controller.searchCancel = cancel
	owner, generation := m.ws, m.workspaceSwitchGen.Load()
	service := memoryBrowserWorkspace(owner)
	return func() tea.Msg {
		defer cancel()
		result := memoryBrowserSearchResultMsg{Request: msg, Owner: owner, WorkspaceGeneration: generation}
		if msg.Mode == memorybrowser.ModeEpisodes {
			result.Episodes, result.Err = service.SearchMemoryEpisodes(ctx, msg.Query, 100)
		} else {
			result.Records, result.Err = service.SearchMemories(ctx, memory.ListQuery{Text: msg.Query, Status: msg.Status, Limit: 200})
		}
		return result
	}
}

func (m *App) applyMemoryBrowserSearch(msg memoryBrowserSearchResultMsg) {
	if !m.ownsMemoryBrowserResult(msg.Owner, msg.WorkspaceGeneration, msg.Request.Target) {
		return
	}
	if m.memoryBrowser.ApplySearch(msg.Request, msg.Records, msg.Episodes, msg.Err) {
		m.markMemoryBrowserDirty()
	}
}

func (m *App) handleMemoryBrowserDetail(msg memorybrowser.DetailMsg) tea.Cmd {
	if !m.memoryBrowserVisible() || msg.Target != m.memoryBrowser || !msg.Target.OwnsDetail(msg) {
		return nil
	}
	controller := m.memoryBrowserController()
	if controller.detailRequest.Target == msg.Target && controller.detailRequest.Epoch == msg.Epoch && controller.detailRequest.Sequence == msg.Sequence {
		return nil
	}
	controller.detailRequest = msg
	if controller.detailCancel != nil {
		controller.detailCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), memoryBrowserTimeout)
	controller.detailCancel = cancel
	owner, generation := m.ws, m.workspaceSwitchGen.Load()
	service := memoryBrowserWorkspace(owner)
	return func() tea.Msg {
		defer cancel()
		result := memoryBrowserDetailResultMsg{Request: msg, Owner: owner, WorkspaceGeneration: generation}
		if msg.Mode == memorybrowser.ModeEpisodes {
			result.Detail.Episode = msg.Episode
		} else {
			detail, err := service.InspectMemory(ctx, msg.ID)
			result.Detail = memorybrowser.DetailData{Record: detail.Record, Revisions: detail.Revisions, Feedback: detail.Feedback, Exposures: detail.Exposures}
			result.Err = err
		}
		return result
	}
}

func (m *App) applyMemoryBrowserDetail(msg memoryBrowserDetailResultMsg) {
	if !m.ownsMemoryBrowserResult(msg.Owner, msg.WorkspaceGeneration, msg.Request.Target) {
		return
	}
	if m.memoryBrowser.ApplyDetail(msg.Request, msg.Detail, msg.Err) {
		m.markMemoryBrowserDirty()
	}
}

func (m *App) handleMemoryBrowserMutation(msg memorybrowser.MutationMsg) tea.Cmd {
	if !m.memoryBrowserVisible() || msg.Target != m.memoryBrowser || !msg.Target.OwnsMutation(msg) {
		return nil
	}
	controller := m.memoryBrowserController()
	if controller.mutationRequest.Target == msg.Target && controller.mutationRequest.Epoch == msg.Epoch && controller.mutationRequest.Sequence == msg.Sequence {
		return nil
	}
	controller.mutationRequest = msg
	if controller.mutationCancel != nil {
		controller.mutationCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), memoryBrowserTimeout)
	controller.mutationCancel = cancel
	owner, generation := m.ws, m.workspaceSwitchGen.Load()
	service := memoryBrowserWorkspace(owner)
	return func() tea.Msg {
		defer cancel()
		result := memoryBrowserMutationResultMsg{Request: msg, Owner: owner, WorkspaceGeneration: generation}
		switch msg.Kind {
		case memorybrowser.MutationPin:
			result.Err = service.PinMemory(ctx, msg.ID, msg.ExpectedRevision, msg.Pinned)
			result.Record = msg.Replacement
			result.Record.Pinned = msg.Pinned
			result.Record.Revision++
		case memorybrowser.MutationRetire:
			result.Err = service.ForgetMemoryRecord(ctx, msg.ID, msg.ExpectedRevision)
			result.Record = msg.Replacement
			result.Record.Status = memory.StatusSuperseded
			result.Record.Revision++
		case memorybrowser.MutationCorrect:
			replacement := msg.Replacement
			replacement.ID, replacement.Revision = msg.ID, msg.ExpectedRevision
			result.Record, result.Err = service.CorrectMemory(ctx, replacement)
		default:
			result.Err = errors.New("unknown memory mutation")
		}
		return result
	}
}

func (m *App) applyMemoryBrowserMutation(msg memoryBrowserMutationResultMsg) tea.Cmd {
	if !m.ownsMemoryBrowserResult(msg.Owner, msg.WorkspaceGeneration, msg.Request.Target) {
		return nil
	}
	if !m.memoryBrowser.ApplyMutation(msg.Request, msg.Record, msg.Err) {
		return nil
	}
	m.markMemoryBrowserDirty()
	if msg.Err != nil {
		return nil
	}
	return tea.Batch(m.memoryBrowser.Refresh(), m.memoryBrowser.ReloadDetail())
}

func (m *App) handleMemoryBrowserCancel(msg memorybrowser.CancelMsg) {
	if msg.Target == m.memoryBrowser && m.memoryBrowserVisible() && msg.Epoch == m.memoryBrowser.Epoch() {
		m.closeMemoryBrowser()
	}
}

func (m *App) refreshMemoryBrowser() tea.Cmd {
	if !m.memoryBrowserVisible() {
		return nil
	}
	return m.memoryBrowser.Refresh()
}

func (m *App) markMemoryBrowserDirty() {
	if m.rootHost != nil {
		m.rootHost.MarkLayout()
	}
}
