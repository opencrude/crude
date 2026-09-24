package viewmodel

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/sessiondb"
	"crude/tui/screens/sessionpicker"
	"crude/tui/screens/statusoverview"
	"crude/workspace"
)

// Cache the lightweight workspace registry, not an enrichment scan that opens
// every session database. Only the visible Workspace panel refreshes it.
type workspaceSessionCache struct {
	db       *workspace.WorkspaceDB
	root     string
	readAt   time.Time
	sessions []sessiondb.SessionMeta
	err      error
}

func (m *App) refreshWorkspaceSessions(force bool) {
	cache := &m.workspaceSessions
	if cache.db != m.WorkspaceDB || cache.root != m.RootPath {
		*cache = workspaceSessionCache{db: m.WorkspaceDB, root: m.RootPath}
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
	if cache.db != m.WorkspaceDB || cache.root != m.RootPath {
		return items
	}
	if cache.err != nil {
		return append(items, statusoverview.Item{ID: "sessions-error", Label: "Unable to load previous sessions", Summary: "Reopen Workspace to retry", Header: true})
	}
	if len(cache.sessions) == 0 {
		return append(items, statusoverview.Item{ID: "sessions-empty", Label: "No previous sessions yet", Header: true})
	}
	live := m.sessionLiveInfos(cache.sessions)
	for _, group := range sessionpicker.BuildSessionGroups(cache.sessions) {
		items = append(items, statusoverview.Item{ID: "sessions-date:" + group.Label, Label: group.Label, Header: true})
		for _, meta := range group.Sessions {
			label := firstNonempty(meta.FirstPrompt.String, meta.LastPrompt.String, meta.ProjectName, "Untitled session")
			label = strings.Join(strings.Fields(label), " ")
			if meta.AgentName.String != "" {
				label = meta.AgentName.String + " — " + label
			}
			info := live[meta.ID]
			state := "saved"
			if info.Status != sessionpicker.StatusTerminated {
				state = info.Status.String()
			}
			parts := []string{state, fmt.Sprintf("%d prompts", meta.TurnCount)}
			if info.SubAgents > 0 {
				parts = append(parts, fmt.Sprintf("%d sub-agents", info.SubAgents))
			}
			if meta.Model != "" {
				parts = append(parts, meta.Model)
			}
			if meta.CreatedAt > 0 {
				parts = append(parts, time.Unix(0, meta.CreatedAt).Format("15:04"))
			}
			items = append(items, statusoverview.Item{
				ID: "session:" + meta.ID, Label: label, Summary: strings.Join(parts, " · "),
				Subtitle:       strings.Join([]string{meta.ID, meta.Provider, meta.LastPrompt.String}, " "),
				ActivateAction: "session:resume:" + meta.ID,
			})
		}
	}
	return items
}

func (m *App) resumeWorkspaceSession(id string) tea.Cmd {
	cache := &m.workspaceSessions
	if cache.db != m.WorkspaceDB || cache.root != m.RootPath {
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
