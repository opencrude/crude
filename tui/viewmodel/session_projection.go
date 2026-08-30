package viewmodel

import (
	"crude/tui/screens/backgroundjobs"
	"crude/tui/screens/composer"
	"crude/tui/screens/confirmation"
	"crude/tui/screens/question"
	"crude/tui/screens/sessionsettings"
	textoverlay "crude/tui/screens/textoverlay"
	"crude/tui/screens/yolo"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

// sessionProjection retains all TUI interaction objects that belong to one
// workspace session. The workspace owns only the corresponding domain data;
// projections are keyed by the stable Session object so switching tabs cannot
// leak dialog, form, or text-input state between conversations.
type sessionProjection struct {
	backgroundJobs      *backgroundjobs.Screen
	confirmation        *confirmation.Screen
	confirmationAgentID string
	confirmationBlockID string
	question            *question.Screen
	questionAgentID     string
	questionBlockID     string
	yolo                *yolo.Screen
	yoloAgentID         string
	textOverlay         *textoverlay.Screen
	composer            *composer.Screen
	settings            *sessionsettings.Screen
	deck                *ui.PanelDeck
	deckHost            *ui.Host
	deckFocused         bool
	deckFocus           ui.ComponentID
}

type sessionProjectionRegistry struct {
	theme *theme.Theme
	items map[*workspace.Session]*sessionProjection
}

func newSessionProjectionRegistry(th *theme.Theme) *sessionProjectionRegistry {
	return &sessionProjectionRegistry{
		theme: th,
		items: make(map[*workspace.Session]*sessionProjection),
	}
}

func (r *sessionProjectionRegistry) Theme() *theme.Theme {
	if r == nil {
		return nil
	}
	return r.theme
}

func (r *sessionProjectionRegistry) Get(session *workspace.Session) *sessionProjection {
	if session == nil {
		return nil
	}
	if projection := r.items[session]; projection != nil {
		return projection
	}

	var effective, base = sessionSettingsSnapshot(session)
	settings := sessionsettings.New(r.theme, effective, base, sessionSettingsOverrides(session))
	deck := ui.NewPanelDeck("session-deck", ui.NewTokenSet(r.theme))
	projection := &sessionProjection{
		backgroundJobs: backgroundjobs.New(r.theme),
		confirmation:   confirmation.New(r.theme),
		question:       question.New(r.theme),
		yolo:           yolo.New(r.theme),
		textOverlay:    textoverlay.New(r.theme),
		composer:       composer.New(r.theme),
		settings:       settings,
		deck:           deck,
		deckHost:       ui.NewHost(r.theme, deck),
	}
	r.items[session] = projection
	return projection
}

func (m *App) sessionProjectionForAgent(agentID string) (*workspace.Session, *sessionProjection) {
	if m == nil || m.AgentMgr == nil || agentID == "" {
		return nil, nil
	}
	orchestrator := m.AgentMgr.OrchestratorFor(agentID)
	if orchestrator == nil {
		return nil, nil
	}
	session := m.SessionsByAgent[orchestrator.ID]
	if session == nil {
		return nil, nil
	}
	if m.sessionProjections == nil {
		m.sessionProjections = newSessionProjectionRegistry(m.Theme)
	}
	return session, m.sessionProjections.Get(session)
}

func (m *App) activeSessionProjection() *sessionProjection {
	if m == nil || m.ActiveSession == nil {
		return nil
	}
	if m.sessionProjections == nil {
		m.sessionProjections = newSessionProjectionRegistry(m.Theme)
	}
	return m.sessionProjections.Get(m.ActiveSession)
}

func (r *sessionProjectionRegistry) projectionForConfirmation(screen *confirmation.Screen) (*workspace.Session, *sessionProjection) {
	for session, projection := range r.items {
		if projection != nil && projection.confirmation == screen {
			return session, projection
		}
	}
	return nil, nil
}

func (m *App) sessionTextOverlay() *textoverlay.Screen {
	if projection := m.activeSessionProjection(); projection != nil {
		return projection.textOverlay
	}
	return nil
}

func (m *App) sessionComposer() *composer.Screen {
	if projection := m.activeSessionProjection(); projection != nil {
		return projection.composer
	}
	return nil
}

func (m *App) sessionSettings() *sessionsettings.Screen {
	if projection := m.activeSessionProjection(); projection != nil {
		return projection.settings
	}
	return nil
}

func (r *sessionProjectionRegistry) Remove(session *workspace.Session) {
	if session != nil {
		delete(r.items, session)
	}
}

func (r *sessionProjectionRegistry) SetTheme(th *theme.Theme) {
	if r == nil || th == nil || r.theme == th {
		return
	}
	r.theme = th
	tokens := ui.NewTokenSet(th)
	for _, projection := range r.items {
		if projection == nil {
			continue
		}
		if projection.deckHost != nil {
			projection.deckHost.SetTheme(th)
		}
		if projection.composer != nil {
			projection.composer.SetTokens(tokens)
		}
		if projection.textOverlay != nil {
			projection.textOverlay.SetTokens(tokens)
		}
		if projection.settings != nil {
			projection.settings.SetTokens(tokens)
		}
		if projection.confirmation != nil {
			projection.confirmation.SetTokens(tokens)
		}
		if projection.yolo != nil {
			projection.yolo.SetTokens(tokens)
		}
		if projection.backgroundJobs != nil {
			projection.backgroundJobs.SetTokens(tokens)
		}
	}
}

func (r *sessionProjectionRegistry) InvalidateWidthProfile() {
	if r == nil {
		return
	}
	for _, projection := range r.items {
		if projection != nil && projection.deckHost != nil {
			projection.deckHost.MarkLayout()
		}
	}
}

func (r *sessionProjectionRegistry) projectionForSettings(screen *sessionsettings.Screen) (*workspace.Session, *sessionProjection) {
	if r == nil || screen == nil {
		return nil, nil
	}
	for session, projection := range r.items {
		if projection != nil && projection.settings == screen {
			return session, projection
		}
	}
	return nil, nil
}

func (r *sessionProjectionRegistry) projectionForQuestion(screen *question.Screen) (*workspace.Session, *sessionProjection) {
	if r == nil || screen == nil {
		return nil, nil
	}
	for session, projection := range r.items {
		if projection != nil && projection.question == screen {
			return session, projection
		}
	}
	return nil, nil
}

func (r *sessionProjectionRegistry) projectionForYolo(screen *yolo.Screen) (*workspace.Session, *sessionProjection) {
	if r == nil || screen == nil {
		return nil, nil
	}
	for session, projection := range r.items {
		if projection != nil && projection.yolo == screen {
			return session, projection
		}
	}
	return nil, nil
}

func (m *App) refreshSessionSettingsProjection(session *workspace.Session) {
	if session == nil {
		return
	}
	projection := m.activeSessionProjection()
	if projection == nil || m.ActiveSession != session {
		if m.sessionProjections == nil {
			m.sessionProjections = newSessionProjectionRegistry(m.Theme)
		}
		projection = m.sessionProjections.Get(session)
	}
	effective, base := sessionSettingsSnapshot(session)
	projection.settings.SetInheritance(effective, base, sessionSettingsOverrides(session))
	if projection.deck != nil && projection.deck.Activate(sessionSettingsPanelID) {
		projection.deck.Open(sessionSettingsPanel(projection.settings))
		projection.deckHost.MarkLayout()
	}
}

func (m *App) refreshAllSessionSettingsProjections() {
	for _, session := range m.Sessions {
		m.refreshSessionSettingsProjection(session)
	}
}

func sessionSettingsSnapshot(session *workspace.Session) (effective, base types.Settings) {
	if session == nil || session.Settings == nil {
		return
	}
	return session.Settings.Effective, session.Settings.Base
}

func sessionSettingsOverrides(session *workspace.Session) map[string]types.CategorySettings {
	if session == nil || session.Settings == nil {
		return nil
	}
	return session.Settings.Overrides
}
