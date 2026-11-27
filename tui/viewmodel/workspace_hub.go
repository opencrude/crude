package viewmodel

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/core/shellport"
	"crude/tui/rendering"
	"crude/tui/screens/gitdiff"
	historyscreen "crude/tui/screens/localhistory"
	"crude/tui/screens/statusoverview"
	"crude/tui/screens/termdock"
	"crude/tui/screens/workspacehub"
	"crude/tui/ui"
	"crude/types/statuscenter"
	"crude/workspace"
	"crude/workspace/localhistory"
)

// hubReads tracks the Workspace panel's asynchronous reads. Each read keeps
// the sequence it started with; a later read for the same page supersedes it.
type hubReads struct {
	usageSeq, storageSeq, eventsSeq, previewSeq, receiptsSeq uint64
	usageBusy, storageBusy, eventsBusy                       bool
	usageAt, storageAt, eventsAt                             time.Time
	cleanup                                                  string
	usage                                                    workspace.UsageReport
	usageRead                                                bool
}

type hubUsageMsg struct {
	owner      shellport.Workspace
	generation uint64
	sequence   uint64
	report     workspace.UsageReport
	err        error
}

type hubStorageMsg struct {
	owner      shellport.Workspace
	generation uint64
	sequence   uint64
	report     workspace.StorageReport
	err        error
}

type hubCleanupMsg struct {
	owner      shellport.Workspace
	generation uint64
	kind       string
	result     workspace.CleanupResult
	err        error
}

type historyEventsMsg struct {
	owner      shellport.Workspace
	generation uint64
	sequence   uint64
	path       string
	events     []localhistory.Event
	stats      localhistory.Stats
	err        error
}

type historyPreviewMsg struct {
	owner      shellport.Workspace
	generation uint64
	sequence   uint64
	key        string
	more       bool
	files      []gitdiff.File
	after      string
	empty      string
	err        error
}

type historyReceiptsMsg struct {
	owner      shellport.Workspace
	generation uint64
	sequence   uint64
	query      localhistory.AgentReceiptQuery
	page       localhistory.RunChangePage
	err        error
}

// newWorkspaceHub builds the Workspace panel and its pages.
func (m *App) newWorkspaceHub() *workspacehub.Hub {
	tokens := ui.NewTokenSet(m.Theme)
	focus := func(id ui.ComponentID) {
		if host := m.hostOf(id); host != nil {
			host.Focus(id)
		}
	}
	m.hubTabs = statusoverview.NewDocked(m.Theme, statusoverview.Projection{Active: statusoverview.ViewTabs})
	m.hubTabs.SetSearchPlaceholder("Search agents, files and terminals")
	m.hubTabs.SetFocusHandler(focus)
	m.hubTabs.SetFocusRing(true)
	m.hubSessions = statusoverview.NewDocked(m.Theme, statusoverview.Projection{Active: statusoverview.ViewTabs})
	m.hubSessions.SetSearchPlaceholder("Search previous sessions")
	m.hubSessions.SetFocusHandler(focus)
	m.hubSessions.SetFocusRing(true)
	m.historyPanel = historyscreen.NewPanel(tokens)
	_, safety := m.ws.(shellport.HistorySafety)
	m.historyPanel.SetSafetyAvailable(safety)
	m.hubOverview = workspacehub.NewOverview(tokens)
	m.hubStorage = workspacehub.NewStorage(tokens)
	hub := workspacehub.New(tokens, map[workspacehub.Tab]ui.Component{
		workspacehub.TabOverview: m.hubOverview,
		workspacehub.TabTabs:     m.hubTabs,
		workspacehub.TabSessions: m.hubSessions,
		workspacehub.TabHistory:  m.historyPanel,
		workspacehub.TabStorage:  m.hubStorage,
	})
	request := func(id ui.ComponentID) bool {
		host := m.hostOf(id)
		return host != nil && host.Focus(id)
	}
	focused := func() ui.ComponentID {
		if host := m.hostOf(workspacehub.HubID); host != nil {
			return host.Focused()
		}
		return ""
	}
	hub.SetFocusHandler(request, focused)
	m.historyPanel.SetFocusHandler(request, focused)
	return hub
}

// hostOf is the host that mounts id: the shell, the main panes or a pane
// window.
func (m *App) hostOf(id ui.ComponentID) *ui.Host {
	if m.rootHost != nil && m.rootHost.Component(id) != nil {
		return m.rootHost
	}
	if host := m.mainHost(); host != nil && host.Component(id) != nil {
		return host
	}
	if m.mainPanes != nil {
		for _, pw := range m.mainPanes.windows {
			if host := pw.frame.Host(); host.Component(id) != nil {
				return host
			}
		}
	}
	return nil
}

func (m *App) workspaceHubShown() bool {
	return m.hub != nil && m.panelShown(termdock.PanelWorkspace)
}

// openWorkspaceHub shows tab. A hub already on screen switches tabs where it
// is; otherwise it opens in its own window, as a detail view does.
func (m *App) openWorkspaceHub(tab workspacehub.Tab) tea.Cmd {
	if m.hub == nil || m.termDock == nil {
		return nil
	}
	m.hub.SetActive(tab)
	switch {
	case m.revealPlacedPanel(termdock.PanelWorkspace):
	case m.workspaceHubShown():
		m.setTermDockFocused(true)
		focus := m.hub.PreferredFocus()
		if host := m.hostOf(focus); host != nil {
			host.Focus(focus)
		}
	default:
		m.openPanelAsWindow(termdock.PanelWorkspace)
	}
	m.workspacePanelLastSecond = 0
	m.syncWorkspacePanels()
	m.forceFrame()
	return m.refreshWorkspaceHub(true)
}

// openWorkspaceTabs is the header's tab count: the Tabs page with the
// pointed-to agent or file selected and expanded.
func (m *App) openWorkspaceTabs(targetID string) tea.Cmd {
	cmd := m.openWorkspaceHub(workspacehub.TabTabs)
	if targetID != "" && m.hubTabs != nil {
		m.hubTabs.Reveal(targetID)
	}
	return cmd
}

// syncHubPages refreshes the Tabs and Sessions projections from the shell's
// own inventory.
func (m *App) syncHubPages() {
	if m.hubTabs != nil {
		m.hubTabs.SetProjection(statusoverview.Projection{Active: statusoverview.ViewTabs, Views: map[statusoverview.ViewID][]statusoverview.Item{statusoverview.ViewTabs: m.statusTabItems()}})
	}
	if m.hubSessions != nil {
		m.hubSessions.SetProjection(statusoverview.Projection{Active: statusoverview.ViewTabs, Views: map[statusoverview.ViewID][]statusoverview.Item{statusoverview.ViewTabs: m.workspaceSessionItems()}})
	}
	if m.historyPanel != nil {
		m.historyPanel.SetAgentNames(m.historyAgentNames())
	}
	if m.hubOverview != nil {
		data := m.hubOverview.Data()
		data.Session = m.sessionSpend()
		m.hubOverview.SetData(data)
	}
}

// sessionSpend is the shown session's cost from the live usage rollup.
func (m *App) sessionSpend() workspacehub.SessionSpend {
	if m.ws == nil || m.ActiveSession == nil {
		return workspacehub.SessionSpend{}
	}
	root := m.ActiveSession.AgentID
	if root == "" {
		return workspacehub.SessionSpend{}
	}
	usage, ok := m.ws.UsageSummary(root, statuscenter.UsageSession)
	if !ok {
		return workspacehub.SessionSpend{}
	}
	return workspacehub.SessionSpend{CostUSD: usage.Cost.USD, Known: usage.Cost.Known, Requests: usage.Requests}
}

// refreshWorkspaceHub reads what the shown page needs. force reads even when
// a recent result is still fresh.
func (m *App) refreshWorkspaceHub(force bool) tea.Cmd {
	if !m.workspaceHubShown() {
		return nil
	}
	now := time.Now()
	reads := &m.hubReads
	switch m.hub.Active() {
	case workspacehub.TabOverview, workspacehub.TabSessions:
		if force || now.Sub(reads.usageAt) >= 20*time.Second {
			return m.readWorkspaceUsage()
		}
	case workspacehub.TabStorage:
		if force && reads.storageAt.IsZero() || now.Sub(reads.storageAt) >= 5*time.Minute {
			return m.readWorkspaceStorage()
		}
	case workspacehub.TabHistory:
		if force || now.Sub(reads.eventsAt) >= 5*time.Second {
			cmds := []tea.Cmd{m.readHistoryEvents()}
			if m.historyPanel.Mode() == historyscreen.ModeAgent && force {
				agent := m.historyPanel.Agent()
				cmds = append(cmds, m.readHistoryReceipts(localhistory.AgentReceiptQuery{SessionID: agent.SessionID, AgentID: agent.AgentID}))
			}
			return tea.Batch(cmds...)
		}
	}
	return nil
}

func (m *App) workspaceData() (shellport.WorkspaceData, bool) {
	data, ok := m.ws.(shellport.WorkspaceData)
	return data, ok
}

func (m *App) readWorkspaceUsage() tea.Cmd {
	reads := &m.hubReads
	data, ok := m.workspaceData()
	if !ok {
		m.hubOverview.SetData(workspacehub.OverviewData{Problem: "Cost totals are available when Crude runs with its background service.", Session: m.sessionSpend()})
		return nil
	}
	if reads.usageBusy {
		return nil
	}
	reads.usageSeq++
	reads.usageBusy, reads.usageAt = true, time.Now()
	overview := m.hubOverview.Data()
	overview.Loading, overview.Session = true, m.sessionSpend()
	m.hubOverview.SetData(overview)
	owner, generation, sequence := m.ws, m.workspaceSwitchGen.Load(), reads.usageSeq
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	return m.clientIOCommand(ctx, func(ctx context.Context) tea.Msg {
		defer cancel()
		report, err := data.WorkspaceUsage(ctx)
		return hubUsageMsg{owner: owner, generation: generation, sequence: sequence, report: report, err: err}
	})
}

func (m *App) applyWorkspaceUsage(msg hubUsageMsg) {
	reads := &m.hubReads
	if msg.owner != m.ws || msg.generation != m.workspaceSwitchGen.Load() || msg.sequence != reads.usageSeq {
		return
	}
	reads.usageBusy = false
	data := m.hubOverview.Data()
	data.Loading, data.Session = false, m.sessionSpend()
	if msg.err != nil {
		data.Problem = "Crude cannot read the cost of this workspace: " + msg.err.Error()
	} else {
		data.Problem, data.Report, data.Read = "", msg.report, time.Now()
		reads.usage, reads.usageRead = msg.report, true
	}
	m.hubOverview.SetData(data)
	m.syncHubPages()
	m.forceFrame()
}

func (m *App) readWorkspaceStorage() tea.Cmd {
	reads := &m.hubReads
	data, ok := m.workspaceData()
	if !ok {
		m.hubStorage.SetData(workspacehub.StorageData{Problem: "Storage is measured when Crude runs with its background service."})
		return nil
	}
	if reads.storageBusy {
		return nil
	}
	reads.storageSeq++
	reads.storageBusy, reads.storageAt = true, time.Now()
	storage := m.hubStorage.Data()
	storage.Loading = true
	m.hubStorage.SetData(storage)
	owner, generation, sequence := m.ws, m.workspaceSwitchGen.Load(), reads.storageSeq
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	return m.clientIOCommand(ctx, func(ctx context.Context) tea.Msg {
		defer cancel()
		report, err := data.WorkspaceStorage(ctx)
		return hubStorageMsg{owner: owner, generation: generation, sequence: sequence, report: report, err: err}
	})
}

func (m *App) applyWorkspaceStorage(msg hubStorageMsg) {
	reads := &m.hubReads
	if msg.owner != m.ws || msg.generation != m.workspaceSwitchGen.Load() || msg.sequence != reads.storageSeq {
		return
	}
	reads.storageBusy = false
	data := m.hubStorage.Data()
	data.Loading = false
	if msg.err != nil {
		data.Problem = "Crude cannot measure the saved data: " + msg.err.Error()
	} else {
		data.Problem, data.Report, data.Read = "", msg.report, time.Now()
	}
	m.hubStorage.SetData(data)
	m.forceFrame()
}

// hubCleanupPrompt is the confirmation before one Storage cleanup.
type hubCleanupPrompt struct {
	kind     string
	activate func(int) tea.Cmd
}

const hubCleanupOverlay ui.OverlayID = "workspace-cleanup-confirm"

// confirmWorkspaceCleanup asks before removing anything and names what the
// cleanup keeps. Cancel is focused.
func (m *App) confirmWorkspaceCleanup(kind string) {
	if m.hubReads.cleanup != "" || m.hubCleanup != nil {
		return
	}
	var category workspace.StorageCategory
	for _, c := range m.hubStorage.Data().Report.Categories {
		if c.ID == kind {
			category = c
		}
	}
	var question, detail, label string
	switch kind {
	case workspace.StorageTemporary:
		question, label = "Remove temporary files of closed sessions?", "Remove files"
		detail = fmt.Sprintf("This frees about %s. Agents keep downloads, builds and evidence there while they work. Open sessions keep their files.", rendering.FormatBytes(category.Reclaimable))
	case workspace.StorageHistory:
		settings := m.ws.LoadAppSettings().LocalHistory
		question, label = "Clean up Local History now?", "Clean up"
		detail = "Crude keeps the copies that your Local History settings keep"
		if settings.RetentionKeepHours > 0 || settings.RetentionKeepDaily > 0 {
			detail += fmt.Sprintf(": every copy from the last %d hours and one copy a day for %d days", settings.RetentionKeepHours, settings.RetentionKeepDaily)
		}
		detail += ". Copies that protect a reviewed edit or a restore stay. Older copies are removed."
	case workspace.StorageWorktrees:
		question, label = "Remove finished worktrees?", "Remove worktrees"
		detail = "Crude removes agent checkouts that are finished and have no unsaved work. Kept checkouts, running agents and checkouts with changes stay."
	default:
		return
	}
	prompt := &hubCleanupPrompt{kind: kind}
	prompt.activate = func(index int) tea.Cmd {
		if m.hubCleanup != prompt {
			return nil
		}
		m.hubCleanup = nil
		m.overlayHost.Close(hubCleanupOverlay)
		if index != 0 {
			return nil
		}
		return m.runWorkspaceCleanup(kind)
	}
	m.hubCleanup = prompt
	frame := ui.BuildConfirmFrame(m.Theme, ui.ConfirmFrameSpec{
		Title: "Storage", Question: question, Detail: detail, Severity: ui.ConfirmPromptWarning,
		Width: min(72, max(36, m.width-4)), FocusedIndex: 1,
		Buttons: []ui.ConfirmButtonSpec{{Label: label, ActionID: ui.ConfirmActionYes}, {Label: "Cancel", ActionID: ui.ConfirmActionCancel}},
	})
	m.openConfirmModal(hubCleanupOverlay, frame, prompt.activate, func() {
		if m.hubCleanup == prompt {
			m.hubCleanup = nil
		}
	})
}

func (m *App) runWorkspaceCleanup(kind string) tea.Cmd {
	data, ok := m.workspaceData()
	if !ok || m.hubReads.cleanup != "" {
		return nil
	}
	m.hubReads.cleanup = kind
	storage := m.hubStorage.Data()
	storage.Busy, storage.Notice = kind, ""
	m.hubStorage.SetData(storage)
	m.forceFrame()
	owner, generation := m.ws, m.workspaceSwitchGen.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	return m.clientIOCommand(ctx, func(ctx context.Context) tea.Msg {
		defer cancel()
		result, err := data.CleanWorkspace(ctx, kind)
		return hubCleanupMsg{owner: owner, generation: generation, kind: kind, result: result, err: err}
	})
}

func (m *App) applyWorkspaceCleanup(msg hubCleanupMsg) tea.Cmd {
	if msg.owner != m.ws || msg.generation != m.workspaceSwitchGen.Load() || msg.kind != m.hubReads.cleanup {
		return nil
	}
	m.hubReads.cleanup = ""
	data := m.hubStorage.Data()
	data.Busy = ""
	switch {
	case msg.err != nil:
		data.Notice, data.NoticeRole = "Nothing was removed: "+msg.err.Error(), ui.ColorError
	default:
		data.Notice, data.NoticeRole = cleanupNotice(msg.result), ui.ColorSuccess
		if msg.result.Kept > 0 {
			data.NoticeRole = ui.ColorWarning
		}
	}
	m.hubStorage.SetData(data)
	m.hubReads.storageAt = time.Time{}
	cmds := []tea.Cmd{m.readWorkspaceStorage()}
	if msg.kind == workspace.StorageHistory && m.historyPanel != nil {
		cmds = append(cmds, m.readHistoryEvents())
	}
	return tea.Batch(cmds...)
}

// cleanupNotice reports a cleanup in one sentence, with what it kept.
func cleanupNotice(result workspace.CleanupResult) string {
	var removed string
	switch result.Kind {
	case workspace.StorageTemporary:
		removed = fmt.Sprintf("Removed temporary files of %s", countNoun(result.Removed, "session"))
	case workspace.StorageHistory:
		removed = fmt.Sprintf("Removed %d old copies from Local History", result.Removed)
		if result.Removed == 1 {
			removed = "Removed 1 old copy from Local History"
		}
	case workspace.StorageWorktrees:
		removed = fmt.Sprintf("Removed %s", countNoun(result.Removed, "worktree"))
	}
	text := removed + " and freed " + rendering.FormatBytes(result.FreedBytes) + "."
	if result.Kept > 0 {
		text += fmt.Sprintf(" Kept %d", result.Kept)
		if len(result.Reasons) > 0 {
			text += ": " + result.Reasons[0]
		}
		text += "."
	}
	return text
}

// pollWorkspaceHub keeps the shown page current while it is on screen.
func (m *App) pollWorkspaceHub() tea.Cmd {
	if !m.workspaceHubShown() {
		return nil
	}
	return m.refreshWorkspaceHub(false)
}

// handleWorkspaceHubAction routes the hub's and its pages' intents.
func (m *App) handleWorkspaceHubAction(action *ui.Action) (tea.Cmd, bool) {
	switch action.ID {
	case workspacehub.ActionErrand:
		return m.openWorkspaceErrand(), true
	case workspacehub.ActionTab:
		m.syncHubPages()
		return m.refreshWorkspaceHub(true), true
	case workspacehub.ActionRefreshUsage:
		m.hubReads.usageBusy = false
		return m.readWorkspaceUsage(), true
	case workspacehub.ActionMeasure:
		m.hubReads.storageBusy = false
		return m.readWorkspaceStorage(), true
	case workspacehub.ActionCleanup:
		if kind, ok := action.Payload.(string); ok {
			m.confirmWorkspaceCleanup(kind)
		}
		return nil, true
	}
	return m.handleHistoryPanelAction(action)
}

// historyAgentNames resolves agent IDs to the names their tabs show.
func (m *App) historyAgentNames() map[string]string {
	names := map[string]string{}
	if m.ws == nil || m.ws.AgentMgr() == nil {
		return names
	}
	for _, node := range m.ws.AgentMgr().AllNodes() {
		names[node.ID] = m.agentLabel(node)
	}
	return names
}

func (m *App) readHistoryEvents() tea.Cmd {
	panel := m.historyPanel
	if panel == nil {
		return nil
	}
	store := m.ws.LocalHistory()
	if store == nil {
		panel.SetTimeline(panel.Path(), nil, "", "Local History is off for this workspace. Turn it on in Settings, Local History.")
		return nil
	}
	reads := &m.hubReads
	reads.eventsSeq++
	reads.eventsBusy, reads.eventsAt = true, time.Now()
	owner, generation, sequence, path := m.ws, m.workspaceSwitchGen.Load(), reads.eventsSeq, panel.Path()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	return m.clientIOCommand(ctx, func(ctx context.Context) tea.Msg {
		defer cancel()
		msg := historyEventsMsg{owner: owner, generation: generation, sequence: sequence, path: path}
		msg.events, msg.err = store.Events(ctx, path)
		if msg.err == nil {
			msg.stats, _ = store.Stats()
		}
		return msg
	})
}

func (m *App) applyHistoryEvents(msg historyEventsMsg) tea.Cmd {
	panel := m.historyPanel
	if panel == nil || msg.owner != m.ws || msg.generation != m.workspaceSwitchGen.Load() || msg.sequence != m.hubReads.eventsSeq {
		return nil
	}
	m.hubReads.eventsBusy = false
	panel.SetAgentNames(m.historyAgentNames())
	problem, summary := "", ""
	if msg.err != nil {
		problem = "Crude cannot read Local History: " + msg.err.Error()
	} else {
		summary = historySummary(msg.events, msg.stats)
		panel.SetActivity(historyActivity(msg.events, time.Now()))
	}
	panel.SetTimeline(msg.path, msg.events, summary, problem)
	m.forceFrame()
	return m.historySelectionCmd()
}

// historySummary states how much history there is, in the user's words.
func historySummary(events []localhistory.Event, stats localhistory.Stats) string {
	text := countNoun(len(events), "change") + " recorded"
	if len(events) > 0 {
		oldest := time.Unix(0, events[len(events)-1].CreatedAt)
		text += " since " + workspacehub.Day(oldest, time.Now())
	}
	if stats.PhysicalBytes > 0 {
		text += " · " + rendering.FormatBytes(stats.PhysicalBytes) + " on disk"
	}
	return text
}

// historyActivity counts recorded changes per local day over the last 30
// days, oldest first.
func historyActivity(events []localhistory.Event, now time.Time) []float64 {
	const days = 30
	counts := make([]float64, days)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -(days - 1))
	for _, event := range events {
		at := time.Unix(0, event.CreatedAt)
		if at.Before(start) || len(event.Paths) == 0 {
			continue
		}
		index := int(at.Sub(start).Hours() / 24)
		if index >= 0 && index < days {
			counts[index]++
		}
	}
	return counts
}

// historySelectionCmd reads the preview of the highlighted row when it is
// not shown yet.
func (m *App) historySelectionCmd() tea.Cmd {
	if m.historyPanel == nil {
		return nil
	}
	action := m.historyPanel.SelectionAction()
	if action == nil {
		return nil
	}
	cmd, _ := m.handleHistoryPanelAction(action)
	return cmd
}

func (m *App) readHistoryPreview(q localhistory.EventQuery) tea.Cmd {
	panel, store := m.historyPanel, m.ws.LocalHistory()
	if panel == nil || store == nil {
		return nil
	}
	more := q.After != ""
	if !more {
		event, ok := panel.SelectedEvent()
		if !ok || event.ID != q.SnapshotID {
			return nil
		}
		title := historyscreen.EventTitle(event, func(id string) string { return firstNonempty(m.historyAgentNames()[id], "Agent") })
		panel.BeginPreview(q.SnapshotID, title, historyscreen.PointTime(time.Unix(0, event.CreatedAt), time.Now()), false)
	} else {
		panel.BeginPreview(q.SnapshotID, "", "", true)
	}
	m.hubReads.previewSeq++
	owner, generation, sequence, root := m.ws, m.workspaceSwitchGen.Load(), m.hubReads.previewSeq, m.ws.RootPath()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	return m.clientIOCommand(ctx, func(ctx context.Context) tea.Msg {
		defer cancel()
		msg := historyPreviewMsg{owner: owner, generation: generation, sequence: sequence, key: q.SnapshotID, more: more}
		page, err := store.EventChanges(ctx, q)
		if err != nil {
			msg.err = err
			return msg
		}
		msg.files, msg.err = prepareHistoryFiles(ctx, root, page.Files)
		msg.after = page.After
		msg.empty = "This point changed no files."
		if page.Event.BaseID == "" && len(page.Event.Paths) == 0 {
			msg.empty = "Local History started here. There is nothing earlier to compare."
		}
		return msg
	})
}

// prepareHistoryFiles diffs and highlights recorded versions off the UI
// goroutine, as the Git History preview does.
func prepareHistoryFiles(ctx context.Context, root string, files []localhistory.EventFile) ([]gitdiff.File, error) {
	comparisons := make([]workspace.GitComparison, 0, len(files))
	for _, file := range files {
		comparisons = append(comparisons, workspace.GitComparison{
			Path: file.Path, AbsolutePath: filepath.Join(root, filepath.FromSlash(file.Path)),
			BaseLabel: "Before", TargetLabel: "After", Before: file.Before, After: file.After,
			BeforePresent: file.BeforePresent, AfterPresent: file.AfterPresent, Binary: file.Binary,
			Deleted: file.BeforePresent && !file.AfterPresent, Historical: true,
		})
	}
	prepared, err := gitdiff.PrepareComparisonsContext(ctx, comparisons)
	if err != nil {
		return nil, err
	}
	for i := range prepared {
		if i < len(files) && files[i].Note != "" {
			prepared[i].Note = files[i].Note
		}
	}
	return prepared, nil
}

func (m *App) applyHistoryPreview(msg historyPreviewMsg) {
	panel := m.historyPanel
	if panel == nil || msg.owner != m.ws || msg.generation != m.workspaceSwitchGen.Load() || msg.sequence != m.hubReads.previewSeq {
		return
	}
	if msg.err != nil {
		panel.SetPreviewError(msg.key, "Crude cannot read these files: "+msg.err.Error()+".")
		m.forceFrame()
		return
	}
	files := msg.files
	if msg.more {
		files = append(append([]gitdiff.File(nil), panel.PreviewFiles()...), files...)
	}
	panel.SetPreview(msg.key, files, msg.after, msg.empty)
	m.forceFrame()
}

func (m *App) readHistoryReceipts(q localhistory.AgentReceiptQuery) tea.Cmd {
	panel, store := m.historyPanel, m.ws.LocalHistory()
	if panel == nil || store == nil || q.AgentID == "" || q.SessionID == "" {
		return nil
	}
	m.hubReads.receiptsSeq++
	owner, generation, sequence := m.ws, m.workspaceSwitchGen.Load(), m.hubReads.receiptsSeq
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	return m.clientIOCommand(ctx, func(ctx context.Context) tea.Msg {
		defer cancel()
		page, err := store.AgentReceipts(ctx, q)
		return historyReceiptsMsg{owner: owner, generation: generation, sequence: sequence, query: q, page: page, err: err}
	})
}

func (m *App) applyHistoryReceipts(msg historyReceiptsMsg) tea.Cmd {
	panel := m.historyPanel
	if panel == nil || msg.owner != m.ws || msg.generation != m.workspaceSwitchGen.Load() || msg.sequence != m.hubReads.receiptsSeq {
		return nil
	}
	problem := ""
	if msg.err != nil {
		problem = "Crude cannot read the agent's file edits: " + msg.err.Error()
	}
	panel.SetReceipts(msg.query, msg.page, problem)
	m.forceFrame()
	return m.historySelectionCmd()
}

func (m *App) readReceiptPreview(q localhistory.AgentReceiptQuery) tea.Cmd {
	panel, store := m.historyPanel, m.ws.LocalHistory()
	if panel == nil || store == nil {
		return nil
	}
	key := historyscreen.ReceiptKey(q)
	panel.BeginPreview(key, q.Path, "Recorded edit", false)
	m.hubReads.previewSeq++
	owner, generation, sequence, root := m.ws, m.workspaceSwitchGen.Load(), m.hubReads.previewSeq, m.ws.RootPath()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	return m.clientIOCommand(ctx, func(ctx context.Context) tea.Msg {
		defer cancel()
		msg := historyPreviewMsg{owner: owner, generation: generation, sequence: sequence, key: key, empty: "This edit changed only the file's type or permissions."}
		page, err := store.AgentReceipts(ctx, q)
		switch {
		case err != nil:
			msg.err = err
		case len(page.Changes) != 1 || page.Changes[0].File == nil:
			msg.empty = firstNonempty(firstReceiptNote(page), "The recorded versions of this edit are no longer kept.")
		default:
			msg.files, msg.err = prepareHistoryFiles(ctx, root, []localhistory.EventFile{*page.Changes[0].File})
		}
		return msg
	})
}

func firstReceiptNote(page localhistory.RunChangePage) string {
	if len(page.Changes) == 1 {
		return page.Changes[0].DiffNote
	}
	return ""
}

// handleHistoryPanelAction routes the Local History tab's intents.
func (m *App) handleHistoryPanelAction(action *ui.Action) (tea.Cmd, bool) {
	panel := m.historyPanel
	if panel == nil {
		return nil, false
	}
	switch action.ID {
	case historyscreen.ActionSelectEvent:
		if q, ok := action.Payload.(localhistory.EventQuery); ok {
			return m.readHistoryPreview(q), true
		}
		return nil, true
	case historyscreen.ActionSelectReceipt:
		if q, ok := action.Payload.(localhistory.AgentReceiptQuery); ok {
			return m.readReceiptPreview(q), true
		}
		return nil, true
	case historyscreen.ActionMoreReceipts:
		if q, ok := action.Payload.(localhistory.AgentReceiptQuery); ok {
			return m.readHistoryReceipts(q), true
		}
		return nil, true
	case historyscreen.ActionRefresh:
		return m.refreshWorkspaceHub(true), true
	case historyscreen.ActionShowAll:
		panel.BeginTimeline("")
		return m.readHistoryEvents(), true
	case historyscreen.ActionKept:
		return m.openHistorySafetyWindow(), true
	case historyscreen.ActionFilter:
		return m.openHistoryFilterMenu(), true
	case historyscreen.ActionUndoEvent:
		if event, ok := action.Payload.(localhistory.Event); ok {
			return m.openRestorePreview(m.undoEventRequest(event)), true
		}
		return nil, true
	case historyscreen.ActionRestoreEvent:
		if restore, ok := action.Payload.(historyscreen.EventRestore); ok {
			when := historyscreen.PointTime(time.Unix(0, restore.Event.CreatedAt), time.Now())
			heading := "Restore every file to " + when + "?"
			if len(restore.Paths) == 1 {
				heading = "Restore " + restore.Paths[0] + " to " + when + "?"
			}
			return m.openRestorePreview(historyscreen.PreviewRequest{SnapshotID: restore.Event.ID, Paths: restore.Paths, Heading: heading}), true
		}
		return nil, true
	case historyscreen.ActionUndoAgent:
		if agent, ok := action.Payload.(historyscreen.AgentRef); ok {
			return m.openLatestHistoryCmd(agent.SessionID, agent.AgentID), true
		}
		return nil, true
	case gitdiff.ActionFile:
		if intent, ok := action.Payload.(gitdiff.FileIntent); ok && panel.OwnsFocus(m.focusedHistoryControl()) {
			intent.Historical = true
			return m.handleGitFileIntent(intent), true
		}
	}
	return nil, false
}

// focusedHistoryControl is the focused control of the host that mounts the
// Local History tab.
func (m *App) focusedHistoryControl() ui.ComponentID {
	if host := m.hostOf(historyscreen.PanelID); host != nil {
		return host.Focused()
	}
	return ""
}

// undoEventRequest previews reversing one point. An agent turn undoes
// through its message scope, which keeps later edits by other agents;
// anything else restores the point's files to the point before it.
func (m *App) undoEventRequest(event localhistory.Event) historyscreen.PreviewRequest {
	names := m.historyAgentNames()
	title := historyscreen.EventTitle(event, func(id string) string { return firstNonempty(names[id], "Agent") })
	request := historyscreen.PreviewRequest{Heading: "Undo “" + title + "”?"}
	if event.Kind == "after_message" && event.SessionID != "" && event.MessageID != "" {
		request.SessionID, request.MessageID = event.SessionID, event.MessageID
		return request
	}
	request.SnapshotID, request.Paths = event.BaseID, append([]string(nil), event.Paths...)
	return request
}

// openHistoryFilterMenu offers every change or one agent's file edits. The
// agents are the open session's agents and those the timeline names.
func (m *App) openHistoryFilterMenu() tea.Cmd {
	panel := m.historyPanel
	names := m.historyAgentNames()
	items := []ui.ContextMenuItem{{Label: "All changes", Detail: "every recorded point", Action: func() {
		panel.SetMode(historyscreen.ModeTimeline)
		m.runDetachedAsyncCmd(m.readHistoryEvents())
	}}}
	seen := map[historyscreen.AgentRef]bool{}
	add := func(session, agent string) {
		ref := historyscreen.AgentRef{SessionID: session, AgentID: agent, Name: firstNonempty(names[agent], "Agent")}
		if session == "" || agent == "" || seen[ref] {
			return
		}
		seen[ref] = true
		items = append(items, ui.ContextMenuItem{Label: ref.Name + "'s file edits", Action: func() {
			panel.SetMode(historyscreen.ModeAgent)
			panel.SetAgent(ref)
			m.runDetachedAsyncCmd(m.readHistoryReceipts(localhistory.AgentReceiptQuery{SessionID: ref.SessionID, AgentID: ref.AgentID}))
		}})
	}
	if m.ActiveSession != nil {
		for _, node := range m.ws.AgentMgr().AllNodes() {
			if session := m.ws.SessionForAgent(node.ID); session != nil && session.SessionID == m.ActiveSession.SessionID {
				add(session.SessionID, node.ID)
			}
		}
	}
	for _, event := range panel.Events() {
		for _, agent := range event.AgentIDs {
			add(event.SessionID, agent)
		}
	}
	menu := &ui.ContextMenuState{Title: "Show", Sections: []ui.ContextMenuSection{{Items: items}}}
	if anchor, ok := m.screenRectOf(historyscreen.FilterID); ok && !anchor.Empty() {
		return m.openMenuFor(anchor, menu)
	}
	m.openContextMenuState(tea.Mouse{X: max(0, m.width/2-20), Y: max(m.shellTopRow(), m.height/3)}, menu)
	return nil
}
