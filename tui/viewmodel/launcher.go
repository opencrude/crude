package viewmodel

import (
	"strings"
	"time"

	"crude/sessiondb"
	"crude/tui/widgets"

	tea "charm.land/bubbletea/v2"
)

// LauncherState holds the state for the full-screen session launcher.
// Selected index 0 = "New Session", 1..n = actual sessions.
type LauncherState struct {
	sessions     []sessiondb.SessionMeta
	selected     int
	filterActive bool
	filterText   string
	filtered     []int // indices into sessions matching the filter
	scrollOffset int
	// lastClickIndex tracks the most recently clicked item, so a
	// second click on the same item activates it (the "double-click"
	// UX in terminals that don't emit a double-click message). -1
	// means "no recent click" (e.g. just opened, or just activated).
	lastClickIndex int
}

func NewLauncherState(sessions []sessiondb.SessionMeta) *LauncherState {
	return &LauncherState{
		sessions:       sessions,
		selected:       0,
		lastClickIndex: -1,
	}
}

// TotalItems returns the total selectable items (1 for "New Session" + len(filtered or all sessions))
func (l *LauncherState) TotalItems() int {
	return 1 + l.visibleCount()
}

func (l *LauncherState) visibleCount() int {
	if l.filterActive && l.filterText != "" {
		return len(l.filtered)
	}
	return len(l.sessions)
}

func (l *LauncherState) visibleSessions() []sessiondb.SessionMeta {
	if l.filterActive && l.filterText != "" {
		result := make([]sessiondb.SessionMeta, 0, len(l.filtered))
		for _, idx := range l.filtered {
			result = append(result, l.sessions[idx])
		}
		return result
	}
	return l.sessions
}

// SelectedSessionID returns the session ID if a session is selected, or "" for "New Session".
func (l *LauncherState) SelectedSessionID() string {
	if l.selected == 0 {
		return ""
	}
	visible := l.visibleSessions()
	idx := l.selected - 1
	if idx >= 0 && idx < len(visible) {
		return visible[idx].ID
	}
	return ""
}

// SelectedSession returns the session meta if a session is selected.
func (l *LauncherState) SelectedSession() *sessiondb.SessionMeta {
	if l.selected == 0 {
		return nil
	}
	visible := l.visibleSessions()
	idx := l.selected - 1
	if idx >= 0 && idx < len(visible) {
		s := visible[idx]
		return &s
	}
	return nil
}

func (l *LauncherState) moveUp() {
	if l.selected > 0 {
		l.selected--
	}
}

func (l *LauncherState) moveDown() {
	if l.selected < l.TotalItems()-1 {
		l.selected++
	}
}

func (l *LauncherState) activateFilter() {
	l.filterActive = true
	l.filterText = ""
	l.filtered = nil
}

func (l *LauncherState) deactivateFilter() {
	l.filterActive = false
	l.filterText = ""
	l.filtered = nil
	l.selected = 0
}

func (l *LauncherState) updateFilter(text string) {
	l.filterText = text
	if text == "" {
		l.filtered = nil
		return
	}
	lower := strings.ToLower(text)
	l.filtered = nil
	for i, s := range l.sessions {
		projectMatch := strings.Contains(strings.ToLower(s.ProjectName), lower)
		promptMatch := s.FirstPrompt.Valid && strings.Contains(strings.ToLower(s.FirstPrompt.String), lower)
		lastMatch := s.LastPrompt.Valid && strings.Contains(strings.ToLower(s.LastPrompt.String), lower)
		if projectMatch || promptMatch || lastMatch {
			l.filtered = append(l.filtered, i)
		}
	}
	// Reset selection to stay within bounds
	if l.selected >= l.TotalItems() {
		l.selected = l.TotalItems() - 1
		if l.selected < 0 {
			l.selected = 0
		}
	}
}

func (l *LauncherState) deleteSelected() string {
	if l.selected == 0 {
		return "" // can't delete "New Session"
	}
	sessionID := l.SelectedSessionID()
	if sessionID == "" {
		return ""
	}
	// Remove from sessions slice
	for i, s := range l.sessions {
		if s.ID == sessionID {
			l.sessions = append(l.sessions[:i], l.sessions[i+1:]...)
			break
		}
	}
	// Re-apply filter
	if l.filterActive && l.filterText != "" {
		l.updateFilter(l.filterText)
	}
	// Adjust selection
	if l.selected >= l.TotalItems() {
		l.selected = l.TotalItems() - 1
		if l.selected < 0 {
			l.selected = 0
		}
	}
	l.scrollOffset = 0
	return sessionID
}

// HitTest maps a rendered line index (0-based, within the launcher's
// rendered output) to a flat item index. Returns -1 if the line is
// not on a selectable item (e.g. a date separator, blank line, or
// out of range).
//
// The rendered layout is:
//   - New Session box: RenderNewSessionEntry (4 lines: top border,
//     2 content lines, bottom border). ALL 4 lines map to item 0 —
//     the whole bordered box is one clickable button.
//   - If sessions exist: 1 transition line + 1 blank line (not clickable).
//   - For each group: 1 date separator (not clickable) + N session lines
//     (each clickable, flat indices 1..N).
//
// Performance: the common case (mouse over the New Session box,
// lines 0-3) returns immediately without building date groups. Group
// building is deferred to the session-row path only. This keeps the
// hover handler cheap even when called on every mouse-motion event.
func (l *LauncherState) HitTest(renderedLine int) int {
	if l == nil {
		return -1
	}
	// New Session box = 4 lines (0..3). ALL 4 lines are clickable
	// — the whole bordered box is one button. This is the fast
	// path: no group building needed.
	if renderedLine >= 0 && renderedLine <= 3 {
		return 0
	}
	if renderedLine < 4 {
		return -1
	}
	visible := l.visibleSessions()
	if len(visible) == 0 {
		// After the box: 1 blank line + 1 "No past sessions" message.
		return -1
	}
	// Transition line (line 4) + blank line (line 5), both non-clickable.
	if renderedLine <= 5 {
		return -1
	}
	// Session rows start at line 6. Build groups to find which
	// session the click landed on. This is the slow path, but it
	// only runs when the mouse is actually over a session row.
	idx := 6
	groups := widgets.BuildLauncherGroups(visible)
	for _, g := range groups {
		if renderedLine == idx {
			return -1 // date separator
		}
		idx++
		for si := range g.Sessions {
			if renderedLine == idx {
				return 1 + si
			}
			idx++
		}
	}
	return -1
}

// totalRenderedLines returns the number of lines the launcher's
// render function will produce. Useful for sanity-checking the
// view-height and for clamping scroll offsets.
func (l *LauncherState) totalRenderedLines() int {
	if l == nil {
		return 0
	}
	// New Session box: 3 lines.
	n := 3
	visible := l.visibleSessions()
	if len(visible) > 0 {
		// Transition + blank.
		n += 2
		// Groups: 1 date sep + N session lines.
		groups := widgets.BuildLauncherGroups(visible)
		for _, g := range groups {
			n++ // date separator
			n += len(g.Sessions)
		}
	} else {
		n++ // "No past sessions found." line
	}
	return n
}
func (m *App) handleLauncherKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.LastInteractionTime = time.Now()
	launcher := m.launcher

	// Filter mode input handling
	if launcher.filterActive {
		switch msg.Code {
		case tea.KeyEsc:
			launcher.deactivateFilter()
			return m, nil
		case tea.KeyEnter:
			// Select current item
			return m, m.launcherSelect()
		case tea.KeyBackspace:
			if len(launcher.filterText) > 0 {
				launcher.filterText = launcher.filterText[:len(launcher.filterText)-1]
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

	// Normal launcher key handling
	switch msg.String() {
	case "up", "k":
		launcher.moveUp()
	case "down", "j":
		launcher.moveDown()
	case "enter":
		return m, m.launcherSelect()
	case "/":
		launcher.activateFilter()
	case "d":
		if launcher.selected > 0 { // can't delete "New Session"
			sessionID := launcher.deleteSelected()
			if sessionID != "" {
				if m.Logger != nil {
					m.Logger.Info("SESSION", "deleted_from_launcher", "id", sessionID)
				}
				// Delete session DB file
				if m.OnDeleteSession != nil {
					m.OnDeleteSession(sessionID)
				}
			}
		}
	case "q":
		m.Quit = true
		return m, tea.Quit
	}
	return m, nil
}

// handleLauncherClick translates a screen (X, Y) into a flat item
// index in the launcher's list, and acts on it. Single-click selects
// the item (matches the keyboard behavior — Up/Down moves the
// selection, Enter activates it). The user can then press Enter or
// click again to activate. We deliberately don't activate on
// single-click to avoid misclicks silently starting a new session.
//
// Screen coordinates are translated to rendered-line index using the
// same layout that widgets.RenderLauncher produces:
//   - chatAreaTop is the first row of the main content area
//   - chatAreaHeight is the height of that area
//   - launcher.scrollOffset is the offset of the first visible line
//     within the launcher's full rendered output
func (m *App) handleLauncherClick(screenX, screenY int) (tea.Model, tea.Cmd) {
	if m.launcher == nil {
		return m, nil
	}
	layout := m.computeLayout()
	// The launcher content area is the chat area when the launcher
	// is the main view.
	if screenY < layout.ChatAreaTop || screenY >= layout.ChatAreaTop+m.ChatAreaHeight {
		return m, nil
	}
	// Translate screen Y to the rendered-line index (0-based within
	// the launcher's full render output).
	visibleLine := screenY - layout.ChatAreaTop
	renderedLine := visibleLine + m.launcher.scrollOffset
	flatIdx := m.launcher.HitTest(renderedLine)
	if flatIdx < 0 {
		return m, nil
	}
	// Update the selection to the clicked item.
	m.launcher.selected = flatIdx
	// "Click again to activate" pattern: if the user clicks the same
	// item twice in a row, treat it as a double-click and activate.
	// This gives a double-click-equivalent UX in terminals that don't
	// emit a double-click event.
	if m.launcher.lastClickIndex == flatIdx {
		m.launcher.lastClickIndex = -1
		return m, m.launcherSelect()
	}
	m.launcher.lastClickIndex = flatIdx
	m.dirty = true
	m.contentDirty = true // launcher IS the main view
	return m, nil
}

// handleLauncherHover translates a screen (X, Y) into a flat item
// index and updates the launcher's selection for visual feedback.
// No-op for the launcher since the render already shows the selected
// item. (We could add hover highlighting in a follow-up; for now the
// click-to-select UX matches the keyboard model.)
func (m *App) handleLauncherHover(screenX, screenY int) {
	if m.launcher == nil {
		return
	}
	layout := m.computeLayout()
	if screenY < layout.ChatAreaTop || screenY >= layout.ChatAreaTop+m.ChatAreaHeight {
		return
	}
	visibleLine := screenY - layout.ChatAreaTop
	renderedLine := visibleLine + m.launcher.scrollOffset
	flatIdx := m.launcher.HitTest(renderedLine)
	if flatIdx < 0 {
		return
	}
	if m.launcher.selected != flatIdx {
		m.launcher.selected = flatIdx
		m.dirty = true
		m.contentDirty = true // launcher IS the main view
	}
}

// launcherSelect handles enter key in the launcher.
func (m *App) launcherSelect() tea.Cmd {
	launcher := m.launcher
	sessionID := launcher.SelectedSessionID()

	if sessionID == "" {
		m.startLSPForWorkspace()
		if m.Logger != nil {
			m.Logger.Info("SESSION", "new_session_from_launcher")
		}
		if m.OnNewSession != nil {
			result := m.OnNewSession()
			if result != nil {
				m.AddOrchestrator(result.OrchID, result.InputCh, result.BtwCh, result.CancelCh, result.BlockResolveCh)
			}
		}
		return nil
	}

	// Resume existing session
	if m.Logger != nil {
		m.Logger.Info("SESSION", "resume_selected", "id", sessionID)
	}
	if m.OnResumeSession != nil {
		m.OnResumeSession(sessionID)
	}
	m.Quit = true
	return tea.Quit
}
