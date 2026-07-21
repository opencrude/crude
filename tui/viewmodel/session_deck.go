package viewmodel

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/sessionsettings"
	"crude/tui/ui"
	"crude/types"
)

const sessionSettingsPanelID = "session-settings"

type sessionSettingsModelsResultMsg struct {
	target     *sessionsettings.Screen
	providerID string
	models     []types.ModelEntry
	err        error
}

type sessionSettingsSaveResultMsg struct {
	target   *sessionsettings.Screen
	settings types.Settings
	err      error
}

func sessionSettingsPanel(screen *sessionsettings.Screen) ui.PanelEntry {
	return ui.PanelEntry{
		ID: sessionSettingsPanelID, Title: "Session Settings", Content: screen,
		Closable: true, Policy: ui.PanelInteractive, PreferredHeight: 12,
	}
}

func (m *App) activeSessionDeck() *sessionProjection {
	projection := m.activeSessionProjection()
	if projection == nil || projection.deck == nil || projection.deckHost == nil {
		return nil
	}
	return projection
}

func (m *App) openSessionSettingsPanel() {
	projection := m.activeSessionDeck()
	if projection == nil || projection.settings == nil {
		return
	}
	projection.deck.Open(sessionSettingsPanel(projection.settings))
	projection.deckFocused = true
	projection.deckFocus = sessionsettings.FormID
	projection.deckHost.MarkLayout()
	m.mainContentCached = ""
}

func (m *App) closeSessionPanel(id string) {
	projection := m.activeSessionDeck()
	m.closeProjectionPanel(projection, id)
}

func (m *App) closeProjectionPanel(projection *sessionProjection, id string) {
	if projection == nil || !projection.deck.Close(id) {
		return
	}
	projection.deckHost.MarkLayout()
	m.mainContentCached = ""
	if !projection.deck.IsOpen() {
		projection.deckFocused = false
		projection.deckFocus = ui.RootID
	}
}

func (m *App) sessionDeckHeight(maxHeight int) int {
	projection := m.activeSessionDeck()
	if projection == nil || !projection.deck.IsOpen() {
		return 0
	}
	return projection.deck.PreferredHeight(maxHeight)
}

func (m *App) sessionDeckFocused() bool {
	projection := m.activeSessionDeck()
	return projection != nil && projection.deckFocused && projection.deck.IsOpen()
}

func (m *App) setSessionDeckFocused(focused bool) {
	if projection := m.activeSessionDeck(); projection != nil {
		projection.deckFocused = focused && projection.deck.IsOpen()
	}
}

func (m *App) handleSessionDeckEvent(msg tea.Msg) tea.Cmd {
	projection := m.activeSessionDeck()
	if projection == nil || !projection.deck.IsOpen() {
		return nil
	}
	result := projection.deckHost.Handle(msg)
	if result.Invalidate&ui.InvalidateLayout != 0 {
		m.mainContentCached = ""
	}
	if result.Action != nil && result.Action.ID == "paneldeck.empty" {
		projection.deckFocused = false
		projection.deckFocus = ui.RootID
	}
	return result.Cmd
}

func (m *App) translateSessionDeckMouse(msg tea.Msg) tea.Msg {
	r := m.sessionDeckRect
	switch msg := msg.(type) {
	case tea.MouseClickMsg:
		return tea.MouseClickMsg{X: msg.X - r.X, Y: msg.Y - r.Y, Button: msg.Button}
	case tea.MouseMotionMsg:
		return tea.MouseMotionMsg{X: msg.X - r.X, Y: msg.Y - r.Y, Button: msg.Button}
	case tea.MouseReleaseMsg:
		return tea.MouseReleaseMsg{X: msg.X - r.X, Y: msg.Y - r.Y, Button: msg.Button}
	case tea.MouseWheelMsg:
		return tea.MouseWheelMsg{X: msg.X - r.X, Y: msg.Y - r.Y, Button: msg.Button}
	default:
		return msg
	}
}

func (m *App) focusSessionDeckAfterLayout() {
	projection := m.activeSessionDeck()
	if projection == nil || !projection.deckFocused || projection.deckFocus == ui.RootID {
		return
	}
	if projection.deckHost.Focus(projection.deckFocus) {
		projection.deckFocus = ui.RootID
	}
}
