package viewmodel

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/fileio"
	"crude/shellport"
	"crude/tui/screens/backgroundjobs"
	"crude/tui/screens/decision"
	"crude/tui/screens/sessionsettings"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

const (
	sessionBackgroundJobsPanelID = "session-background-jobs"
	sessionSettingsPanelID       = "session-settings"
	sessionDecisionPanelID       = "session-decision"
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
	target     *sessionsettings.Screen
	workspace  shellport.Workspace
	generation uint64
	result     workspace.SessionSettingsResult
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

// sessionDecisionPanel is an interactive dock, never a shell modal: the session
// change applies only on its explicit answer, so an unanswered offer (goal
// resume, plan implementation, YOLO) stays retained while every other IDE
// surface, the composer included, remains usable.
func sessionDecisionPanel(screen *decision.Screen, title string) ui.PanelEntry {
	return ui.PanelEntry{ID: sessionDecisionPanelID, Title: title, Content: screen, Closable: false, Policy: ui.PanelInteractive, PreferredHeight: 12}
}

// openSessionDecision asks the active session one docked yes/no question
// about agentID's session; a newer question replaces an unanswered one.
func (m *App) openSessionDecision(agentID, title string, spec decision.Spec) {
	projection := m.activeSessionProjection()
	if projection == nil || projection.decision == nil {
		return
	}
	projection.decision.Show(spec)
	projection.decisionAgentID, projection.decisionData = agentID, ""
	projection.deck.Close(sessionDecisionPanelID)
	projection.deck.Open(sessionDecisionPanel(projection.decision, title))
	if panel := projection.deck.Active(); panel == nil || panel.ID != sessionDecisionPanelID {
		projection.deckHost.MarkLayout()
		return
	}
	projection.deckFocused = true
	projection.deckFocus = decision.ActionsID
	projection.deckHost.MarkLayout()
}

func (m *App) openSessionYoloPanel() {
	m.openSessionDecision(m.AgentView.ActiveID(), "YOLO", decision.Spec{
		Purpose: "yolo", Title: "Enable YOLO for this session?", Hint: "Use /yolo to restore approvals.",
		Facts: []decision.Fact{
			{Label: "Approval prompts", Value: "Skipped"},
			{Label: "Commands / edits", Value: "No confirmation"},
			{Label: "Hard blocks", Value: "Enforced"},
			{Label: "Restore safeguards", Value: "Enforced"},
		},
		Confirm:       decision.Answer{Label: "Enable YOLO", Shortcut: "y"},
		Cancel:        decision.Answer{Label: "Cancel", Shortcut: "n"},
		ConfirmDanger: true, SemanticsLabel: "YOLO mode confirmation",
	})
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
		// A remaining request changes the deck's local focus, not the user's
		// deliberate shell focus (for example an editor or workspace panel).
		projection.deckFocus = panel.FocusID
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

// sessionDecisionPending reports an unanswered consequential decision (agent
// prompt or session decision) in the active session. It is not modality: the
// human keeps every surface. It gates only automation that could otherwise
// act around the decision, such as self-control effects, a core restart, or a
// remote shell receipt that must report the decision as pending.
func (m *App) sessionDecisionPending() bool {
	if m.sessionDeckBlocking() {
		return true
	}
	projection := m.activeSessionDeck()
	return projection != nil && projection.decisionAgentID != "" && projection.deck.IsOpen()
}

// sessionDeckCapturesInput reports explicit deck focus. A docked panel is
// never a shell modal: blocking agent prompts hold their owner's continuation
// and composer submission, but keys and pointers may always move to other IDE
// content. Only floating OverlayHost windows can be modal.
func (m *App) sessionDeckCapturesInput() bool {
	return m.sessionDeckFocused()
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

func (m *App) resolveSessionDecision(msg decision.ResultMsg) tea.Cmd {
	_, projection := m.sessionProjections.projectionForDecision(msg.Target)
	if projection == nil || projection.decisionAgentID == "" {
		return nil
	}
	agentID, data := projection.decisionAgentID, projection.decisionData
	projection.decisionAgentID, projection.decisionData = "", ""
	m.closeProjectionPanel(projection, sessionDecisionPanelID)
	if !msg.Confirmed {
		return nil
	}
	switch msg.Purpose {
	case "yolo":
		m.setAgentYolo(agentID, true)
	case "implement_plan":
		if msg.Alternate {
			return m.implementPlanInNewSession(data)
		}
		return m.implementProposedPlan(agentID)
	case "goal_replace":
		return m.goalAction(agentID, workspace.GoalReplace, data)
	case "goal_resume":
		return m.goalAction(agentID, workspace.GoalResume, "")
	}
	return nil
}

// setAgentYolo changes the owning session's policy; the session records the
// change's transcript row for every shell.
func (m *App) setAgentYolo(agentID string, enabled bool) {
	if !m.ws.SetYoloForAgent(agentID, enabled) {
		m.notifyStatus(types.SeverityError, "safety", "YOLO mode was not changed", "The owning session is no longer available.", types.ResourceRef{Kind: types.ResourceAgent, ID: agentID})
		return
	}
	node := m.ws.AgentMgr().OrchestratorFor(agentID)
	if node != nil && m.ws.SessionsByAgent()[node.ID] == m.ActiveSession {
		m.OverlayState.SetYolo(m.ws.YoloForAgent(agentID))
	}
}
