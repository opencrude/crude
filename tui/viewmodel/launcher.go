package viewmodel

import (
	"fmt"
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
}

func NewLauncherState(sessions []sessiondb.SessionMeta) *LauncherState {
	return &LauncherState{
		sessions: sessions,
		selected: 0,
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

// handleLauncherKey processes key events in ModeLauncher.
func (m *App) handleLauncherKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.lastInteractionTime = time.Now()
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
				m.logger.Info("SESSION", "deleted_from_launcher", "id", sessionID)
				// Delete session DB file
				if m.onDeleteSession != nil {
					m.onDeleteSession(sessionID)
				}
			}
		}
	case "q":
		m.Quit = true
		return m, tea.Quit
	}
	return m, nil
}

// launcherSelect handles enter key in the launcher.
func (m *App) launcherSelect() tea.Cmd {
	launcher := m.launcher
	sessionID := launcher.SelectedSessionID()

	if sessionID == "" {
		m.setMode(ModeChat)
		m.launcher = nil
		m.startLSPForWorkspace()
		m.logger.Info("SESSION", "new_session_from_launcher")
		return nil
	}

	// Resume existing session
	m.logger.Info("SESSION", "resume_selected", "id", sessionID)
	if m.OnResumeSession != nil {
		m.OnResumeSession(sessionID)
	}
	m.Quit = true
	return tea.Quit
}

// launcherPreviewLines returns preview text for the selected session.
func (m *App) launcherPreviewLines() []string {
	if m.launcher == nil {
		return nil
	}
	s := m.launcher.SelectedSession()
	if s == nil {
		return []string{"Start a new conversation"}
	}

	var meta strings.Builder
	meta.WriteString(widgets.FormatTurnCount(s.TurnCount))
	meta.WriteString(" · ")
	meta.WriteString(s.Model)
	if s.CreatedAt > 0 {
		t := time.Unix(0, s.CreatedAt)
		elapsed := time.Since(t)
		meta.WriteString(" · ")
		meta.WriteString(formatElapsed(elapsed))
	}

	return []string{meta.String()}
}

func formatElapsed(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		mins := int(d.Minutes())
		if mins == 1 {
			return "1 min ago"
		}
		return fmt.Sprintf("%dm ago", mins)
	}
	if d < 24*time.Hour {
		hours := int(d.Hours())
		if hours == 1 {
			return "1h ago"
		}
		return fmt.Sprintf("%dh ago", hours)
	}
	days := int(d.Hours() / 24)
	if days == 1 {
		return "1 day ago"
	}
	return fmt.Sprintf("%d days ago", days)
}
