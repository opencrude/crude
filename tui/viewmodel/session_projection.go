package viewmodel

import (
	"crude/tui/screens/sessionsettings"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/widgets"
	"crude/types"
	"crude/workspace"
)

// sessionProjection retains all TUI interaction objects that belong to one
// workspace session. The workspace owns only the corresponding domain data;
// projections are keyed by the stable Session object so switching tabs cannot
// leak dialog, form, or text-input state between conversations.
type sessionProjection struct {
	confirm      widgets.ConfirmDialog
	questionForm widgets.QuestionForm
	yoloDlg      widgets.YoloDialog
	textOverlay  widgets.TextOverlay
	composer     *widgets.Composer
	settings     *sessionsettings.Screen
	deck         *ui.PanelDeck
	deckHost     *ui.Host
	deckFocused  bool
	deckFocus    ui.ComponentID
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
		confirm:      widgets.NewConfirmDialog(r.theme),
		questionForm: widgets.NewQuestionForm(r.theme),
		yoloDlg:      widgets.NewYoloDialog(r.theme),
		textOverlay:  widgets.NewTextOverlay(r.theme),
		composer:     widgets.NewComposer(r.theme),
		settings:     settings,
		deck:         deck,
		deckHost:     ui.NewHost(r.theme, deck),
	}
	r.items[session] = projection
	return projection
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

func (m *App) sessionConfirm() *widgets.ConfirmDialog {
	if projection := m.activeSessionProjection(); projection != nil {
		return &projection.confirm
	}
	return nil
}

func (m *App) sessionQuestionForm() *widgets.QuestionForm {
	if projection := m.activeSessionProjection(); projection != nil {
		return &projection.questionForm
	}
	return nil
}

func (m *App) sessionYoloDialog() *widgets.YoloDialog {
	if projection := m.activeSessionProjection(); projection != nil {
		return &projection.yoloDlg
	}
	return nil
}

func (m *App) sessionTextOverlay() *widgets.TextOverlay {
	if projection := m.activeSessionProjection(); projection != nil {
		return &projection.textOverlay
	}
	return nil
}

func (m *App) sessionComposer() *widgets.Composer {
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
	for _, projection := range r.items {
		if projection != nil && projection.deckHost != nil {
			projection.deckHost.SetTheme(th)
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
