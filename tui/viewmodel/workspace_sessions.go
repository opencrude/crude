package viewmodel

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/sessiondb"
	"crude/shellport"
	"crude/tui/screens/sessionpicker"
	"crude/tui/screens/statusoverview"
	"crude/tui/ui"
)

// Cache the lightweight workspace registry, not an enrichment scan that opens
// every session database. Only the visible Workspace panel refreshes it.
type workspaceSessionCache struct {
	db       shellport.WorkspaceStore
	root     string
	readAt   time.Time
	sessions []sessiondb.SessionMeta
	err      error
}

func (m *App) refreshWorkspaceSessions(force bool) {
	cache := &m.workspaceSessions
	if cache.db != m.ws.WorkspaceDB() || cache.root != m.ws.RootPath() {
		*cache = workspaceSessionCache{db: m.ws.WorkspaceDB(), root: m.ws.RootPath()}
	}
	if !force && time.Since(cache.readAt) < time.Second {
		return
	}
	cache.readAt = time.Now()
	cache.sessions, cache.err = nil, nil
	if cache.db != nil {
		cache.sessions, cache.err = cache.db.ListSessions()
	}
}

func (m *App) workspaceSessionItems() []statusoverview.Item {
	items := []statusoverview.Item{{ID: "previous-sessions", Label: "Previous Sessions", Header: true}}
	cache := &m.workspaceSessions
	if cache.db != m.ws.WorkspaceDB() || cache.root != m.ws.RootPath() {
		return items
	}
	if cache.err != nil {
		return append(items, statusoverview.Item{ID: "sessions-error", Label: "Unable to load previous sessions", Summary: "Reopen Workspace to retry", Header: true})
	}
	sessions := make([]sessiondb.SessionMeta, 0, len(cache.sessions))
	for _, meta := range cache.sessions {
		if m.ws.SessionsByID()[meta.ID] == nil {
			sessions = append(sessions, meta)
		}
	}
	if len(sessions) == 0 {
		return append(items, statusoverview.Item{ID: "sessions-empty", Label: "No previous sessions yet", Header: true})
	}
	items[0].Label = fmt.Sprintf("Previous Sessions (%d)", len(sessions))
	live := m.sessionLiveInfos(sessions)
	for _, group := range sessionpicker.BuildSessionGroups(sessions) {
		items = append(items, statusoverview.Item{ID: "sessions-date:" + group.Label, Label: group.Label, Header: true})
		for _, meta := range group.Sessions {
			label := firstNonempty(meta.FirstPrompt.String, meta.LastPrompt.String, meta.ProjectName, "Untitled session")
			label = strings.Join(strings.Fields(label), " ")
			info := live[meta.ID]
			state := "saved"
			if info.Status != sessionpicker.StatusTerminated {
				state = info.Status.String()
			}
			parts := []string{fmt.Sprintf("%d prompts", meta.TurnCount)}
			if info.SubAgents > 0 {
				parts = append(parts, fmt.Sprintf("%d sub-agents", info.SubAgents))
			}
			if meta.CreatedAt > 0 {
				parts = append(parts, time.Unix(0, meta.CreatedAt).Format("15:04"))
			}
			items = append(items, statusoverview.Item{
				ID: "session:" + meta.ID, Label: firstNonempty(meta.AgentName.String, "Session"), Summary: state,
				Context: label, Compact: strings.Join(parts, " · "),
				Lines: []statusoverview.DetailLine{
					{Label: "First prompt", Value: meta.FirstPrompt.String},
					{Label: "Last prompt", Value: meta.LastPrompt.String},
					{Label: "Model", Value: meta.Model, Role: ui.ColorMuted},
					{Label: "Created", Value: time.Unix(0, meta.CreatedAt).Format("2006-01-02 15:04"), Role: ui.ColorMuted},
					{Label: "Session ID", Value: meta.ID, Role: ui.ColorMuted},
				},
				PrimaryAction: "session:resume:" + meta.ID, PrimaryLabel: "Resume session", PrimaryEnabled: true,
			})
		}
	}
	return items
}

func (m *App) resumeWorkspaceSession(id string) tea.Cmd {
	cache := &m.workspaceSessions
	if cache.db != m.ws.WorkspaceDB() || cache.root != m.ws.RootPath() {
		return nil // A queued click from a different workspace must not navigate.
	}
	for _, meta := range cache.sessions {
		if meta.ID != id {
			continue
		}
		cmd := m.launcherResumeSession(id, &meta)
		if m.ActiveSession != nil && m.ActiveSession.SessionID == id {
			m.termDock.ClosePanel()
			m.setTermDockFocused(false)
		}
		return cmd
	}
	return nil
}
