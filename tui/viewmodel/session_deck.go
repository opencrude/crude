package viewmodel

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/fileio"
	"crude/tui/screens/backgroundjobs"
	"crude/tui/screens/confirmation"
	"crude/tui/screens/question"
	"crude/tui/screens/sessionsettings"
	"crude/tui/screens/yolo"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

const (
	sessionBackgroundJobsPanelID = "session-background-jobs"
	sessionSettingsPanelID       = "session-settings"
	sessionQuestionPanelID       = "session-question"
	sessionConfirmationPanelID   = "session-confirmation"
	sessionYoloPanelID           = "session-yolo"
)

func sessionBackgroundJobsPanel(screen *backgroundjobs.Screen, ownerID string) ui.PanelEntry {
	return ui.PanelEntry{
		ID: sessionBackgroundJobsPanelID, Title: "Background jobs", OwnerID: ownerID,
		Content: screen, Closable: false, Attention: screen.NeedsAttention(), Policy: ui.PanelPassive,
	}
}

func (m *App) updateSessionBackgroundJobs(agentID string, commands []fileio.AsyncCmdInfo, now time.Time) {
	_, projection := m.sessionProjectionForAgent(agentID)
	if projection == nil || projection.backgroundJobs == nil {
		return
	}
	jobs := make([]backgroundjobs.Job, 0, len(commands))
	for _, command := range commands {
		jobs = append(jobs, backgroundjobs.Job{
			Handle: command.Handle, Command: command.Command, PID: command.PID, Status: string(command.Status),
			StartedAt: command.StartedAt, EndedAt: command.EndedAt, Duration: command.Duration,
			ExitCode: command.ExitCode, Error: command.Error,
		})
	}
	projection.backgroundJobs.SetJobs(jobs, now)
	if projection.backgroundJobs.VisibleCount() == 0 {
		m.closeProjectionPanel(projection, sessionBackgroundJobsPanelID)
		return
	}
	projection.deck.Open(sessionBackgroundJobsPanel(projection.backgroundJobs, agentID))
	projection.deckHost.MarkLayout()
}

func (m *App) refreshSessionBackgroundJobs(now time.Time) {
	if m.sessionProjections == nil {
		return
	}
	for _, projection := range m.sessionProjections.items {
		if projection == nil || projection.backgroundJobs == nil || !projection.backgroundJobs.Refresh(now) {
			continue
		}
		if projection.backgroundJobs.VisibleCount() == 0 {
			m.closeProjectionPanel(projection, sessionBackgroundJobsPanelID)
			continue
		}
		projection.deckHost.MarkLayout()
	}
}

type sessionSettingsModelsResultMsg struct {
	target     *sessionsettings.Screen
	providerID string
	models     []types.ModelEntry
	err        error
}

func sessionConfirmationPanel(screen *confirmation.Screen) ui.PanelEntry {
	return ui.PanelEntry{ID: sessionConfirmationPanelID, Title: "Confirmation", Content: screen, Closable: false, Policy: ui.PanelBlocking, PreferredHeight: 12}
}
func (m *App) openSessionConfirmationPanel(projection *sessionProjection) {
	if projection == nil || projection.confirmation == nil {
		return
	}
	projection.deck.Open(sessionConfirmationPanel(projection.confirmation))
	if panel := projection.deck.Active(); panel == nil || panel.ID != sessionConfirmationPanelID {
		projection.deckHost.MarkLayout()
		return
	}
	projection.deckFocused = true
	projection.deckFocus = confirmation.ActionsID
	projection.deckHost.MarkLayout()
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
		// Wide-readable so the "inherited from workspace" trailing column on
		// each agent row stays visible without stretching to the deck edge.
		Width: ui.PanelWideReadable,
	}
}

func sessionQuestionPanel(screen *question.Screen) ui.PanelEntry {
	return ui.PanelEntry{
		ID: sessionQuestionPanelID, Title: "Questions", Content: screen,
		Closable: false, Policy: ui.PanelBlocking, PreferredHeight: 12,
	}
}

func sessionYoloPanel(screen *yolo.Screen) ui.PanelEntry {
	return ui.PanelEntry{ID: sessionYoloPanelID, Title: "YOLO", Content: screen, Closable: false, Policy: ui.PanelBlocking, PreferredHeight: 12}
}

func (m *App) openSessionYoloPanel() {
	projection := m.activeSessionProjection()
	if projection == nil || projection.yolo == nil {
		return
	}
	projection.yolo.Reset()
	projection.yoloAgentID = m.AgentMgr.ActiveID()
	projection.deck.Open(sessionYoloPanel(projection.yolo))
	if panel := projection.deck.Active(); panel == nil || panel.ID != sessionYoloPanelID {
		projection.deckHost.MarkLayout()
		return
	}
	projection.deckFocused = true
	projection.deckFocus = yolo.ActionsID
	projection.deckHost.MarkLayout()
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
	if panel := projection.deck.Active(); panel == nil || panel.ID != sessionSettingsPanelID {
		return
	}
	projection.deckFocused = true
	projection.deckFocus = sessionsettings.FormID
	projection.deckHost.MarkLayout()
}

func (m *App) openSessionModelPicker() tea.Cmd {
	m.openSessionSettings()
	projection := m.activeSessionDeck()
	if projection == nil || projection.settings == nil {
		return nil
	}
	cmd := projection.settings.OpenModelPicker("default")
	projection.deckFocus = sessionsettings.ScreenID
	projection.deckHost.MarkLayout()
	return cmd
}

func (m *App) openSessionQuestionPanel(projection *sessionProjection) {
	if projection == nil || projection.deck == nil || projection.deckHost == nil || projection.question == nil || projection.question.Count() == 0 {
		return
	}
	projection.deck.Open(sessionQuestionPanel(projection.question))
	if panel := projection.deck.Active(); panel == nil || panel.ID != sessionQuestionPanelID {
		projection.deckHost.MarkLayout()
		return
	}
	projection.deckFocused = true
	projection.deckFocus = question.OptionsID
	projection.deckHost.MarkLayout()
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
	m.normalizeSessionDeckFocus(projection)
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

func (m *App) sessionDeckBlocking() bool {
	projection := m.activeSessionDeck()
	if projection == nil {
		return false
	}
	panel := projection.deck.Active()
	return panel != nil && panel.Policy == ui.PanelBlocking
}

func (m *App) sessionDeckCapturesInput() bool {
	return m.sessionDeckFocused() || m.sessionDeckBlocking()
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
	before := m.deckPanelIDs()
	result := projection.deckHost.Handle(msg)
	if result.Action != nil {
		if m.handleTerminalAction(*result.Action) {
			m.normalizeSessionDeckFocus(projection)
			return result.Cmd
		}
		if result.Action.ID == "paneldeck.empty" {
			projection.deckFocused = false
			projection.deckFocus = ui.RootID
		}
	}
	m.reconcileClosedTerminalPanels(before)
	m.normalizeSessionDeckFocus(projection)
	return result.Cmd
}

func (m *App) normalizeSessionDeckFocus(projection *sessionProjection) {
	if projection == nil {
		return
	}
	panel := projection.deck.Active()
	if panel != nil && panel.Policy != ui.PanelPassive {
		return
	}
	projection.deckFocused = false
	projection.deckFocus = ui.RootID
	projection.deckHost.Focus(ui.RootID)
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

func (m *App) resolveSessionQuestion(target *question.Screen, result question.Result, cancelled bool) {
	_, projection := m.sessionProjections.projectionForQuestion(target)
	if projection == nil {
		m.StatusBar.SetError("Question owner is no longer available")
		return
	}
	agentID := projection.questionAgentID
	m.StatusBar.SetConfirming(false)
	m.OverlayState.SetBlocking(false)
	if node := m.AgentMgr.Get(agentID); node != nil && node.State != nil {
		node.State.Thinking = true
	}
	m.StatusBar.SetThinkingForAgent(agentID, true, false, false)
	m.ResolveQuestion(agentID, result.Answers, result.Questions, cancelled)
	target.Dismiss()
	m.closeProjectionPanel(projection, sessionQuestionPanelID)
}
func (m *App) resolveSessionConfirmation(target *confirmation.Screen, choice string) {
	_, projection := m.sessionProjections.projectionForConfirmation(target)
	if projection == nil {
		return
	}
	agentID := projection.confirmationAgentID
	details := target.Details()
	m.StatusBar.SetConfirming(false)
	m.OverlayState.SetBlocking(false)
	if node := m.AgentMgr.Get(agentID); node != nil && node.State != nil {
		node.State.Thinking = true
		switch choice {
		case "allow":
			node.State.PendingPermTag = "allowed"
		case "always":
			node.State.PendingPermTag = "always"
		case "deny":
			node.State.PendingPermTag = "denied"
			if projected := m.subPaneProjections.GetExisting(agentID); projected != nil && projected.renderer != nil {
				projected.renderer.SetToolPermTag(details.Tool, "", "denied")
			}
		}
	}
	m.StatusBar.SetThinkingForAgent(agentID, true, false, false)
	if m.ResolveConfirmation(agentID, choice) && m.Logger != nil {
		m.Logger.Info("BLOCK", "resolved", "response", choice, "operation", details.Operation, "tool", details.Tool)
	}
	target.Dismiss()
	m.closeProjectionPanel(projection, sessionConfirmationPanelID)
}

func (m *App) resolveSessionYolo(target *yolo.Screen, enabled bool) {
	_, projection := m.sessionProjections.projectionForYolo(target)
	if projection == nil {
		return
	}
	m.OverlayState.SetYolo(enabled)
	content := "YOLO mode cancelled. Safety checks remain active."
	if enabled {
		content = "YOLO mode ACTIVE: all safety checks disabled. Use /yolo again to restore."
		if m.Logger != nil {
			m.Logger.Warn("SAFETY", "yolo_enabled")
		}
	} else {
		if m.Logger != nil {
			m.Logger.Info("SAFETY", "yolo_disabled")
		}
	}
	if node := m.AgentMgr.Get(projection.yoloAgentID); node != nil && node.State != nil && node.State.Messages != nil {
		node.State.Messages.AppendMessage(workspace.Message{Role: types.RoleAssistant, Content: content, Time: time.Now()})
		m.syncAgentMessages(node.ID, false)
	}
	m.SetYolo(enabled)
	m.closeProjectionPanel(projection, sessionYoloPanelID)
}
