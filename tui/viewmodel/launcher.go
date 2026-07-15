package viewmodel

import (
	"path/filepath"
	"strings"
	"time"

	"crude/sessiondb"
	"crude/tui/widgets"
	"crude/workspace"

	tea "charm.land/bubbletea/v2"
)

// LauncherState holds the two-column welcome launcher.
// Selectable: New Session, New Workspace, workspaces...
// Hits use rectangular bounds so whole rows are clickable.
// workspaceScroll only scrolls the right-hand workspace viewport.
type LauncherState struct {
	cwdPath      string
	sessions     []sessiondb.SessionMeta
	workspaces   []workspace.WorkspaceInfo
	items        []widgets.LauncherItem
	selected     int
	filterActive bool
	filterText   string
	filtered     []int
	// workspaceScroll is the first visible workspace row.
	workspaceScroll int
	// scrollOffset kept for compatibility; welcome fills the viewport.
	scrollOffset int
	lineToItem   []int
	hits         []widgets.LauncherHit
	selectedLine int
	totalLines   int
	// Workspace pane geometry from last paint (content coords).
	workspacePaneY0, workspacePaneY1       int
	workspacePaneX0, workspacePaneX1       int
	workspaceSearchY                       int
	workspaceListY0, workspaceListY1       int
	workspaceViewportH, workspaceItemCount int
	workspaceScrollMax                     int
}

// NewLauncherState creates a unified welcome launcher.
// cwd is the absolute project path; sessions are from the current workspace.
func NewLauncherState(cwd string, sessions []sessiondb.SessionMeta) *LauncherState {
	cwdAbs := cwd
	if abs, err := filepath.Abs(cwd); err == nil {
		cwdAbs = abs
	}
	infos, _ := workspace.ListWorkspaceInfos()
	filtered := make([]workspace.WorkspaceInfo, 0, len(infos))
	for _, info := range infos {
		if info.ProjectDir != "" {
			abs, _ := filepath.Abs(info.ProjectDir)
			if abs == cwdAbs {
				continue // current folder is represented by New Session
			}
		}
		filtered = append(filtered, info)
	}
	l := &LauncherState{
		cwdPath:    cwdAbs,
		sessions:   sessions,
		workspaces: filtered,
		selected:   0,
	}
	l.rebuildItems()
	return l
}

// Refresh reloads workspaces and sessions after a project switch.
func (l *LauncherState) Refresh(cwd string, sessions []sessiondb.SessionMeta) {
	*l = *NewLauncherState(cwd, sessions)
}

func (l *LauncherState) rebuildItems() {
	wsEntries := make([]widgets.WorkspaceEntry, 0, len(l.workspaces))
	for _, w := range l.workspaces {
		wsEntries = append(wsEntries, widgets.WorkspaceEntry{
			Name:         w.ProjectName,
			Path:         w.ProjectDir,
			SessionCount: w.SessionCount,
		})
	}
	l.items = widgets.BuildLauncherItems(wsEntries, l.sessions)
	l.filtered = nil
	for i, it := range l.items {
		if it.Kind == widgets.LauncherItemWorkspace && it.Workspace != nil && widgets.WorkspaceEntryMatches(*it.Workspace, l.filterText) {
			l.filtered = append(l.filtered, i)
		}
	}
	if l.selected >= len(l.items) {
		l.selected = len(l.items) - 1
	}
	if l.selected < 0 && len(l.items) > 0 {
		l.selected = 0
	}
	if l.filterActive && !l.workspaceVisible(l.selected) {
		if len(l.filtered) > 0 {
			l.selected = l.filtered[0]
		}
	}
}

func (l *LauncherState) workspaceVisible(itemIdx int) bool {
	for _, i := range l.filtered {
		if i == itemIdx {
			return true
		}
	}
	return false
}

// TotalItems returns selectable item count.
func (l *LauncherState) TotalItems() int {
	if l == nil {
		return 0
	}
	return len(l.items)
}

// SelectedItem returns the currently selected item, or nil.
func (l *LauncherState) SelectedItem() *widgets.LauncherItem {
	if l == nil || l.selected < 0 || l.selected >= len(l.items) {
		return nil
	}
	it := l.items[l.selected]
	return &it
}

// SelectedSessionID returns the session ID if a session is selected.
func (l *LauncherState) SelectedSessionID() string {
	it := l.SelectedItem()
	if it == nil || it.Kind != widgets.LauncherItemSession || it.Session == nil {
		return ""
	}
	return it.Session.ID
}

// SelectedSession returns the session meta if a session is selected.
func (l *LauncherState) SelectedSession() *sessiondb.SessionMeta {
	it := l.SelectedItem()
	if it == nil || it.Kind != widgets.LauncherItemSession {
		return nil
	}
	return it.Session
}

// SelectedWorkspace returns workspace info if a workspace row is selected.
func (l *LauncherState) SelectedWorkspace() *workspace.WorkspaceInfo {
	it := l.SelectedItem()
	if it == nil || it.Kind != widgets.LauncherItemWorkspace || it.Workspace == nil {
		return nil
	}
	path := it.Workspace.Path
	for i := range l.workspaces {
		if l.workspaces[i].ProjectDir == path {
			w := l.workspaces[i]
			return &w
		}
	}
	// Fallback synthetic info from the display entry.
	return &workspace.WorkspaceInfo{
		ProjectDir:   path,
		ProjectName:  it.Workspace.Name,
		SessionCount: it.Workspace.SessionCount,
	}
}

func (l *LauncherState) moveUp() {
	l.moveSelection(-1)
}

func (l *LauncherState) moveDown() {
	l.moveSelection(1)
}

func (l *LauncherState) moveSelection(delta int) {
	indices := l.selectableIndices()
	if len(indices) == 0 {
		return
	}
	pos := 0
	for i, idx := range indices {
		if idx == l.selected {
			pos = i
			break
		}
	}
	pos = clampInt(pos+delta, 0, len(indices)-1)
	l.selected = indices[pos]
	l.ensureSelectedVisible(l.workspaceViewportH)
}

func (l *LauncherState) selectableIndices() []int {
	if l.filterActive {
		return append([]int(nil), l.filtered...)
	}
	indices := make([]int, len(l.items))
	for i := range l.items {
		indices[i] = i
	}
	return indices
}

func (l *LauncherState) activateFilter() {
	l.filterActive = true
	l.filterText = ""
	l.rebuildItems()
	if len(l.filtered) > 0 {
		l.selected = l.filtered[0]
	}
	l.workspaceScroll = 0
}

func (l *LauncherState) deactivateFilter() {
	l.filterActive = false
	l.filterText = ""
	l.filtered = nil
	l.rebuildItems()
	l.workspaceScroll = 0
}

func (l *LauncherState) updateFilter(text string) {
	l.filterText = text
	l.rebuildItems()
	l.workspaceScroll = 0
}

// scrollWorkspaces scrolls only the right-hand workspace list.
func (l *LauncherState) scrollWorkspaces(delta int) {
	if l == nil {
		return
	}
	l.workspaceScroll += delta
	if l.workspaceScroll < 0 {
		l.workspaceScroll = 0
	}
	if l.workspaceScroll > l.workspaceScrollMax {
		l.workspaceScroll = l.workspaceScrollMax
	}
}

// scrollBy is an alias used by the mouse wheel path.
func (l *LauncherState) scrollBy(delta, viewportH int) {
	_ = viewportH
	l.scrollWorkspaces(delta)
}

func (l *LauncherState) ensureSelectedVisible(viewportH int) {
	if l == nil {
		return
	}
	if viewportH <= 0 {
		viewportH = l.workspaceViewportH
	}
	if viewportH <= 0 || !l.workspaceVisible(l.selected) {
		return
	}
	row := 0
	for i, itemIdx := range l.filtered {
		if itemIdx == l.selected {
			row = i
			break
		}
	}
	if row < l.workspaceScroll {
		l.workspaceScroll = row
	} else if row >= l.workspaceScroll+viewportH {
		l.workspaceScroll = row - viewportH + 1
	}
	l.workspaceScroll = clampInt(l.workspaceScroll, 0, l.workspaceScrollMax)
}

// SetPaint stores hit-map metadata from the latest render.
func (l *LauncherState) SetPaint(paint widgets.LauncherPaint) {
	if l == nil {
		return
	}
	l.lineToItem = paint.LineToItem
	l.hits = paint.Hits
	l.selectedLine = paint.SelectedLine
	l.totalLines = len(paint.Lines)
	l.workspacePaneY0 = paint.WorkspacePaneY0
	l.workspacePaneY1 = paint.WorkspacePaneY1
	l.workspacePaneX0 = paint.WorkspacePaneX0
	l.workspacePaneX1 = paint.WorkspacePaneX1
	l.workspaceSearchY = paint.WorkspaceSearchY
	l.workspaceListY0 = paint.WorkspaceListY0
	l.workspaceListY1 = paint.WorkspaceListY1
	l.workspaceViewportH = paint.WorkspaceViewportH
	l.workspaceItemCount = paint.WorkspaceVisibleCount
	l.workspaceScrollMax = paint.WorkspaceScrollMax
	if l.workspaceScroll > l.workspaceScrollMax {
		l.workspaceScroll = l.workspaceScrollMax
	}
}

// HelpView is rendered in the bottom-right status region while welcome is open.
func (l *LauncherState) HelpView() string {
	if l != nil && l.filterActive {
		return "type search  ·  ↑↓ select  ·  enter open  ·  esc clear"
	}
	return "↑↓ navigate  ·  enter open  ·  / search  ·  d remove  ·  ctrl+shift+p palette"
}

func (l *LauncherState) workspacePaneContains(x, y int) bool {
	return l != nil && x >= l.workspacePaneX0 && x <= l.workspacePaneX1 && y >= l.workspacePaneY0 && y <= l.workspacePaneY1
}

func (l *LauncherState) workspaceSearchContains(x, y int) bool {
	return l != nil && x >= l.workspacePaneX0 && x <= l.workspacePaneX1 && y == l.workspaceSearchY
}

func (l *LauncherState) workspaceScrollbarContains(x, y int) bool {
	return l != nil && l.workspaceItemCount > l.workspaceViewportH && x == l.workspacePaneX1 && y >= l.workspaceListY0 && y <= l.workspaceListY1
}

func (l *LauncherState) scrollWorkspaceTo(y int) {
	if l == nil || l.workspaceViewportH <= 0 || l.workspaceScrollMax <= 0 {
		return
	}
	row := clampInt(y-l.workspaceListY0, 0, l.workspaceViewportH-1)
	if l.workspaceViewportH == 1 {
		l.workspaceScroll = 0
		return
	}
	l.workspaceScroll = row * l.workspaceScrollMax / (l.workspaceViewportH - 1)
}

func clampInt(v, low, high int) int {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}

// HitTest maps a rendered line index to a flat item index (Y-only fallback).
func (l *LauncherState) HitTest(renderedLine int) int {
	if l == nil || renderedLine < 0 || renderedLine >= len(l.lineToItem) {
		return -1
	}
	return l.lineToItem[renderedLine]
}

// HitTestXY maps content (x,y) to an item using rectangular hit regions.
func (l *LauncherState) HitTestXY(x, y int) int {
	if l == nil {
		return -1
	}
	for _, h := range l.hits {
		if y >= h.Y0 && y <= h.Y1 && x >= h.X0 && x <= h.X1 {
			return h.ItemIdx
		}
	}
	// Fallback: Y-only map for left column lines without full rects.
	return l.HitTest(y)
}

// deleteSelected removes a workspace or session from the list.
// Returns "ws:<id>", session id, or "" if nothing deleted.
func (l *LauncherState) deleteSelected() string {
	it := l.SelectedItem()
	if it == nil {
		return ""
	}
	switch it.Kind {
	case widgets.LauncherItemWorkspace:
		ws := l.SelectedWorkspace()
		if ws == nil {
			return ""
		}
		id := ws.ID
		for i, w := range l.workspaces {
			if w.ID == id || (id == "" && w.ProjectDir == ws.ProjectDir) {
				l.workspaces = append(l.workspaces[:i], l.workspaces[i+1:]...)
				break
			}
		}
		l.rebuildItems()
		l.scrollOffset = 0
		if id != "" {
			return "ws:" + id
		}
		return "ws:"
	case widgets.LauncherItemSession:
		if it.Session == nil {
			return ""
		}
		sessionID := it.Session.ID
		for i, s := range l.sessions {
			if s.ID == sessionID {
				l.sessions = append(l.sessions[:i], l.sessions[i+1:]...)
				break
			}
		}
		l.rebuildItems()
		l.scrollOffset = 0
		return sessionID
	}
	return ""
}

func (m *App) handleLauncherKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.LastInteractionTime = time.Now()
	launcher := m.launcher
	if launcher == nil {
		return m, nil
	}

	if launcher.filterActive {
		switch msg.Code {
		case tea.KeyEsc:
			launcher.deactivateFilter()
			return m, nil
		case tea.KeyEnter:
			if launcher.workspaceVisible(launcher.selected) {
				return m, m.launcherSelect()
			}
			return m, nil
		case tea.KeyBackspace:
			if runes := []rune(launcher.filterText); len(runes) > 0 {
				launcher.filterText = string(runes[:len(runes)-1])
				launcher.updateFilter(launcher.filterText)
			} else {
				launcher.deactivateFilter()
			}
			return m, nil
		case tea.KeyUp:
			launcher.moveUp()
			return m, nil
		case tea.KeyDown:
			launcher.moveDown()
			return m, nil
		default:
			if len(msg.Text) > 0 {
				launcher.filterText += msg.Text
				launcher.updateFilter(launcher.filterText)
				return m, nil
			}
		}
		return m, nil
	}

	switch msg.String() {
	case "up", "k":
		launcher.moveUp()
	case "down", "j":
		launcher.moveDown()
	case "enter":
		return m, m.launcherSelect()
	case "/":
		launcher.activateFilter()
	case "esc":
		// Esc only cancels filter (handled above). Never quit / never close tabs.
		return m, nil
	case "d":
		deleted := launcher.deleteSelected()
		if deleted == "" {
			return m, nil
		}
		if strings.HasPrefix(deleted, "ws:") {
			wsID := strings.TrimPrefix(deleted, "ws:")
			if wsID != "" {
				if m.Logger != nil {
					m.Logger.Info("WORKSPACE", "deleted_from_launcher", "id", wsID)
				}
				_ = workspace.DeleteWorkspace(wsID)
			}
		} else {
			if m.Logger != nil {
				m.Logger.Info("SESSION", "deleted_from_launcher", "id", deleted)
			}
			m.DeleteSession(deleted)
		}
	case "pgup", "pgdown", "home", "end":
		// Optional navigation aids
		switch msg.String() {
		case "pgup":
			for i := 0; i < 5; i++ {
				launcher.moveUp()
			}
		case "pgdown":
			for i := 0; i < 5; i++ {
				launcher.moveDown()
			}
		case "home":
			if indices := launcher.selectableIndices(); len(indices) > 0 {
				launcher.selected = indices[0]
			}
		case "end":
			if indices := launcher.selectableIndices(); len(indices) > 0 {
				launcher.selected = indices[len(indices)-1]
			}
		}
		launcher.ensureSelectedVisible(launcher.workspaceViewportH)
	}
	return m, nil
}

// handleLauncherClick: single click on a card's full bounds activates it.
func (m *App) handleLauncherClick(screenX, screenY int) (tea.Model, tea.Cmd) {
	if m.launcher == nil {
		return m, nil
	}
	layout := m.computeLayout()
	if screenY < layout.ChatAreaTop || screenY >= layout.ChatAreaTop+m.ChatAreaHeight {
		return m, nil
	}
	x := screenX - layout.SidebarW - layout.PadLR
	y := screenY - layout.ChatAreaTop
	if m.launcher.workspaceSearchContains(x, y) {
		m.launcher.activateFilter()
		return m, nil
	}
	if m.launcher.workspaceScrollbarContains(x, y) {
		m.launcher.scrollWorkspaceTo(y)
		return m, nil
	}
	flatIdx := m.launcher.HitTestXY(x, y)
	if flatIdx < 0 {
		return m, nil
	}
	m.launcher.selected = flatIdx
	return m, m.launcherSelect()
}

// handleLauncherHover updates selection when the pointer is over a card.
func (m *App) handleLauncherHover(screenX, screenY int) {
	if m.launcher == nil {
		return
	}
	layout := m.computeLayout()
	if screenY < layout.ChatAreaTop || screenY >= layout.ChatAreaTop+m.ChatAreaHeight {
		return
	}
	x := screenX - layout.SidebarW - layout.PadLR
	y := screenY - layout.ChatAreaTop
	flatIdx := m.launcher.HitTestXY(x, y)
	if flatIdx < 0 {
		return
	}
	if m.launcher.selected != flatIdx {
		m.launcher.selected = flatIdx
	}
}

// launcherSelect activates the selected welcome item.
func (m *App) launcherSelect() tea.Cmd {
	if m.launcher == nil {
		return nil
	}
	it := m.launcher.SelectedItem()
	if it == nil {
		return nil
	}

	switch it.Kind {
	case widgets.LauncherItemOpenFolder:
		// Reuse the file-picker floating window in folder mode.
		m.openFolderPicker()
		return nil

	case widgets.LauncherItemNewSession:
		return m.launcherNewSession()

	case widgets.LauncherItemWorkspace:
		ws := m.launcher.SelectedWorkspace()
		if ws == nil || ws.ProjectDir == "" {
			return nil
		}
		return m.launcherOpenFolder(ws.ProjectDir)

	case widgets.LauncherItemSession:
		return m.launcherResumeSession()
	}
	return nil
}

func (m *App) launcherNewSession() tea.Cmd {
	m.startLSPForWorkspace()
	if m.Logger != nil {
		m.Logger.Info("SESSION", "new_session_from_launcher")
	}
	result := m.NewSessionFromLauncher()
	if result != nil {
		m.AddOrchestrator(result.OrchID, result.InputCh, result.BtwCh, result.CancelCh, result.BlockResolveCh)
	}
	return nil
}

func (m *App) launcherOpenFolder(projectDir string) tea.Cmd {
	if m.Logger != nil {
		m.Logger.Info("WORKSPACE", "open_from_launcher", "path", projectDir)
	}
	if err := m.SwitchProject(projectDir); err != nil {
		if m.StatusBar != nil {
			m.StatusBar.SetError("Failed to open workspace: " + err.Error())
		}
		return nil
	}
	// Stay on the single-phase launcher; refresh lists for the new project.
	m.launcher.Refresh(projectDir, m.ListSessions())
	if m.StatusBar != nil {
		m.StatusBar.SetInfo("Opened " + filepath.Base(projectDir))
	}
	return nil
}

func (m *App) launcherResumeSession() tea.Cmd {
	sessionID := m.launcher.SelectedSessionID()
	if sessionID == "" {
		return nil
	}
	// Switch project if the session belongs to another folder.
	if s := m.launcher.SelectedSession(); s != nil && s.ProjectPath != "" {
		abs, _ := filepath.Abs(s.ProjectPath)
		cur, _ := filepath.Abs(m.RootPath)
		if abs != "" && abs != cur {
			if err := m.SwitchProject(abs); err != nil {
				if m.StatusBar != nil {
					m.StatusBar.SetError("Failed to open session project: " + err.Error())
				}
				return nil
			}
		}
	}
	if m.Logger != nil {
		m.Logger.Info("SESSION", "resume_selected", "id", sessionID)
	}
	result := m.ResumeSessionFromLauncher(sessionID)
	if result == nil {
		if m.StatusBar != nil {
			m.StatusBar.SetError("Failed to resume session")
		}
		return nil
	}
	m.startLSPForWorkspace()
	m.AddOrchestrator(result.OrchID, result.InputCh, result.BtwCh, result.CancelCh, result.BlockResolveCh)
	if m.ActiveSession != nil {
		if msgs := m.ActiveSession.HydratedMessages(); len(msgs) > 0 {
			if ml := m.activeMsgList(); ml != nil {
				for _, hm := range msgs {
					ml.AppendMessage(hm)
				}
				ml.ScrollToBottom()
			}
		}
		if refs := m.ActiveSession.HydratedRefs(); len(refs) > 0 {
			m.SetReferences(refs)
		}
	}
	return nil
}

// openLauncher shows the unified welcome launcher (sessions + workspaces).
func (m *App) openLauncher() {
	cwd := m.RootPath
	if cwd == "" {
		cwd = "."
	}
	m.launcher = NewLauncherState(cwd, m.ListSessions())
	m.LastInteractionTime = time.Now()
}

// openFolderPicker opens the existing file-picker UI in folder-select mode.
func (m *App) openFolderPicker() {
	start := m.RootPath
	if start == "" {
		start = "."
	}
	m.FilePicker.OpenFolder(start)
	m.setMode(ModeFilePicker)
	// agent_tabs / setMode opens the filepicker floating window.
}
