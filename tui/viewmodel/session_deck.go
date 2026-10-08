package viewmodel

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/fileio"
	"crude/tui/screens/backgroundjobs"
	"crude/tui/screens/sessionsettings"
	"crude/tui/screens/yolo"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

const (
	sessionBackgroundJobsPanelID = "session-background-jobs"
	sessionSettingsPanelID       = "session-settings"
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
		state := types.OperationRunning
		switch command.Status {
		case fileio.AsyncCompleted:
			state = types.OperationSucceeded
		case fileio.AsyncFailed:
			state = types.OperationFailed
		case fileio.AsyncKilled:
			state = types.OperationCancelled
		}
		owner := types.ResourceRef{Kind: types.ResourceAgent, ID: agentID}
		if session := m.SessionsByAgent[agentID]; session != nil {
			owner.SessionID = session.SessionID
		}
		if m.Status != nil {
			m.Status.SetOperation(types.OperationSnapshot{
				ID: "command:" + command.Handle, Owner: owner, Kind: "background-command",
				Label: command.Command, State: state, StartedAt: command.StartedAt,
				EndedAt: command.EndedAt, Error: command.Error, OutputRef: command.Handle,
				Cancellable: state == types.OperationRunning,
			})
			if state == types.OperationFailed {
				m.Status.Notify(types.NotificationRecord{
					DedupKey: "command-failed:" + command.Handle, Owner: owner,
					Severity: types.SeverityError, Source: "background-command",
					Summary: "Background command failed", Detail: command.Command + ": " + command.Error,
				})
			}
		}
		jobs = append(jobs, backgroundjobs.Job{
			OwnerID: agentID, Handle: command.Handle, Command: command.Command, PID: command.PID, Status: string(command.Status),
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
	accountID  string
	generation uint64
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
		// Wide-readable so the "inherited from workspace" trailing column on
		// each agent row stays visible without stretching to the deck edge.
		Width: ui.PanelWideReadable,
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
	if m.Win != nil && m.activeAgentTabID() == "" {
		return nil
	}
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

func (m *App) openSessionAccountPicker() tea.Cmd {
	m.openSessionSettings()
	projection := m.activeSessionDeck()
	if projection == nil || projection.settings == nil {
		return nil
	}
	cmd := projection.settings.OpenAccountPicker("default")
	projection.deckFocus = sessionsettings.ScreenID
	projection.deckHost.MarkLayout()
	return cmd
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
	if panel := projection.deck.Active(); panel != nil && panel.BlockingGroup == "agent-prompts" {
		projection.deckFocused, projection.deckFocus = true, panel.FocusID
	}
	m.normalizeSessionDeckFocus(projection)
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
	if m.sessionDeckAllowsNavigation() && (m.focusTarget() == FocusTree || m.focusTarget() == FocusTabBar) {
		return false
	}
	return m.sessionDeckFocused() || m.sessionDeckBlocking()
}

func (m *App) sessionDeckAllowsNavigation() bool {
	if projection := m.activeSessionDeck(); projection != nil {
		if panel := projection.deck.Active(); panel != nil {
			return panel.BlockingGroup == "agent-prompts"
		}
	}
	return false
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
	return tea.Batch(result.Cmd, m.handleHostAction(result.Action))
}

func (m *App) normalizeSessionDeckFocus(projection *sessionProjection) {
	if projection == nil {
		return
	}
	panel := projection.deck.Active()
	if panel != nil && panel.Policy != ui.PanelPassive {
		return
	}
	if panel != nil && panel.ID == sessionBackgroundJobsPanelID && projection.deckFocused &&
		projection.backgroundJobs.ActionFocused(projection.deckHost.Focused()) {
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

func (m *App) resolveSessionYolo(target *yolo.Screen, enabled bool) {
	_, projection := m.sessionProjections.projectionForYolo(target)
	if projection == nil || projection.yoloAgentID == "" {
		return
	}
	agentID := projection.yoloAgentID
	if enabled {
		m.setAgentYolo(agentID, true)
	}
	projection.yoloAgentID = ""
	m.closeProjectionPanel(projection, sessionYoloPanelID)
}

func (m *App) setAgentYolo(agentID string, enabled bool) {
	previous := m.YoloForAgent(agentID)
	if !m.SetYoloForAgent(agentID, enabled) {
		m.notifyStatus(types.SeverityError, "safety", "YOLO mode was not changed", "The owning session is no longer available.", types.ResourceRef{Kind: types.ResourceAgent, ID: agentID})
		return
	}
	node := m.AgentMgr.OrchestratorFor(agentID)
	if node != nil && m.SessionsByAgent[node.ID] == m.ActiveSession {
		m.OverlayState.SetYolo(m.YoloForAgent(agentID))
	}
	if previous == enabled {
		return
	}
	phase, content := "yolo_disabled", "Tool approval prompts are restored for this session."
	if enabled {
		phase, content = "yolo_enabled", "Commands and file changes can run without confirmation. Hard blocks and restore safeguards remain enforced. Use /yolo to restore approval prompts."
	}
	if node != nil && node.State != nil && node.State.Messages != nil {
		now := time.Now()
		id := phase + ":" + now.Format(time.RFC3339Nano)
		node.State.Messages.AppendMessage(workspace.Message{InternalID: id, Role: types.RoleStatus, LifecyclePhase: phase, LifecycleOperationID: id, Content: content, Time: now})
		m.syncAgentMessages(node.ID, false)
	}
}
