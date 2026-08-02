package viewmodel

import (
	"testing"
	"time"

	"crude/fileio"
	"crude/tui/screens/confirmation"
	"crude/tui/screens/question"
	"crude/types"
	"crude/workspace"
)

func TestSessionDeckIsRetainedPerSession(t *testing.T) {
	app := newFullTestApp(t)
	first := &workspace.Session{SessionID: "first", Settings: workspace.NewSessionSettingsState(types.Settings{}, types.Settings{}, nil)}
	second := &workspace.Session{SessionID: "second", Settings: workspace.NewSessionSettingsState(types.Settings{}, types.Settings{}, nil)}

	app.ActiveSession = first
	app.openSessionSettings()
	firstProjection := app.activeSessionDeck()
	if firstProjection == nil || !firstProjection.deck.IsOpen() || !firstProjection.deckFocused {
		t.Fatal("first session deck did not open and focus")
	}

	app.ActiveSession = second
	app.openSessionSettings()
	secondProjection := app.activeSessionDeck()
	if secondProjection == nil || secondProjection == firstProjection || !secondProjection.deck.IsOpen() {
		t.Fatal("second session did not receive an independent deck")
	}
	app.closeSessionPanel(sessionSettingsPanelID)
	if secondProjection.deck.IsOpen() {
		t.Fatal("closing second session panel left its deck open")
	}

	app.ActiveSession = first
	if !firstProjection.deck.IsOpen() || firstProjection.settings != app.sessionSettings() {
		t.Fatal("switching back did not restore the first session deck")
	}
}

func TestConfirmationAndYoloResultsTargetOwningProjection(t *testing.T) {
	app := newFullTestApp(t)
	first := &workspace.Session{SessionID: "first", Settings: workspace.NewSessionSettingsState(types.Settings{}, types.Settings{}, nil)}
	second := &workspace.Session{SessionID: "second", Settings: workspace.NewSessionSettingsState(types.Settings{}, types.Settings{}, nil)}
	blockCh := make(chan string, 1)
	app.AgentMgr.AddOrchestrator("first-agent", nil, nil, nil, blockCh, &workspace.AgentState{})

	app.ActiveSession = first
	firstProjection := app.activeSessionProjection()
	firstProjection.confirmation.Reset(confirmation.Details{Question: "Proceed?", Tool: "bash"})
	firstProjection.confirmationAgentID = "first-agent"
	app.openSessionConfirmationPanel(firstProjection)

	app.ActiveSession = second
	secondProjection := app.activeSessionProjection()
	app.resolveSessionConfirmation(firstProjection.confirmation, "always")
	select {
	case got := <-blockCh:
		if got != "always" {
			t.Fatalf("confirmation response=%q, want always", got)
		}
	default:
		t.Fatal("inactive confirmation result did not reach its owning agent")
	}
	if firstProjection.deck.IsOpen() || secondProjection.deck.IsOpen() || app.ActiveSession != second {
		t.Fatal("inactive confirmation resolution changed the active session")
	}

	app.ActiveSession = first
	firstProjection.yolo.Reset()
	firstProjection.yoloAgentID = "first-agent"
	firstProjection.deck.Open(sessionYoloPanel(firstProjection.yolo))
	app.ActiveSession = second
	app.resolveSessionYolo(firstProjection.yolo, false)
	if firstProjection.deck.IsOpen() || secondProjection.deck.IsOpen() || app.ActiveSession != second {
		t.Fatal("inactive YOLO resolution changed the active session")
	}
}

func TestBlockingQuestionPanelCapturesInputAndKeepsSessionOwnership(t *testing.T) {
	app := newFullTestApp(t)
	first := &workspace.Session{SessionID: "first", Settings: workspace.NewSessionSettingsState(types.Settings{}, types.Settings{}, nil)}
	second := &workspace.Session{SessionID: "second", Settings: workspace.NewSessionSettingsState(types.Settings{}, types.Settings{}, nil)}
	app.ActiveSession = first
	firstProjection := app.activeSessionProjection()
	firstProjection.question.Reset(`[{"question":"Continue?","options":["yes","no"]}]`)
	firstProjection.questionAgentID = "first-agent"
	app.openSessionQuestionPanel(firstProjection)

	if !app.sessionDeckBlocking() || !app.sessionDeckCapturesInput() {
		t.Fatal("blocking question panel did not capture deck input")
	}
	if region := app.classifyShellRegion(0, 0, false); region != regionSessionDeck {
		t.Fatalf("blocking question click classified as %v", region)
	}

	app.ActiveSession = second
	secondProjection := app.activeSessionProjection()
	app.resolveSessionQuestion(firstProjection.question, question.Result{}, true)
	if firstProjection.deck.IsOpen() {
		t.Fatal("inactive question owner retained a resolved panel")
	}
	if secondProjection.deck.IsOpen() || app.ActiveSession != second {
		t.Fatal("resolving an inactive question changed the active session projection")
	}
}

func TestSessionDeckReservesNativeRegionBelowMessages(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 100, 30
	app.launcherScreen = nil
	app.ActiveSession = &workspace.Session{SessionID: "session", Settings: workspace.NewSessionSettingsState(types.Settings{}, types.Settings{}, nil)}
	app.openSessionSettings()
	_ = app.computeContentLayout()

	if app.sessionDeckRect.Empty() {
		t.Fatal("layout did not reserve a native session deck region")
	}
	if got, want := app.sessionDeckRect.Y, app.ChatAreaHeight+3; got != want {
		t.Fatalf("deck Y=%d, want directly below messages at %d", got, want)
	}
	if region := app.classifyShellRegion(app.sessionDeckRect.X, app.sessionDeckRect.Y, false); region != regionSessionDeck {
		t.Fatalf("deck hit classified as %v", region)
	}
}

func TestSettingsProjectionLookupTargetsInactiveSession(t *testing.T) {
	app := newFullTestApp(t)
	first := &workspace.Session{SessionID: "first", Settings: workspace.NewSessionSettingsState(types.Settings{}, types.Settings{}, nil)}
	second := &workspace.Session{SessionID: "second", Settings: workspace.NewSessionSettingsState(types.Settings{}, types.Settings{}, nil)}
	firstProjection := app.sessionProjections.Get(first)
	app.sessionProjections.Get(second)
	app.ActiveSession = second

	owner, projection := app.sessionProjections.projectionForSettings(firstProjection.settings)
	if owner != first || projection != firstProjection {
		t.Fatal("async settings result was not routed to its owning inactive session")
	}
}

func TestBackgroundJobsUpdateOwningInactiveSessionWithoutStealingFocus(t *testing.T) {
	app := newFullTestApp(t)
	first := &workspace.Session{SessionID: "first", Settings: workspace.NewSessionSettingsState(types.Settings{}, types.Settings{}, nil)}
	second := &workspace.Session{SessionID: "second", Settings: workspace.NewSessionSettingsState(types.Settings{}, types.Settings{}, nil)}
	app.AgentMgr.AddOrchestrator("first-agent", nil, nil, nil, nil, &workspace.AgentState{})
	app.SessionsByAgent = make(map[string]*workspace.Session)
	app.SessionsByAgent["first-agent"] = first

	app.ActiveSession = first
	app.openSessionSettings()
	firstProjection := app.activeSessionProjection()
	app.ActiveSession = second
	secondProjection := app.activeSessionProjection()
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	app.updateSessionBackgroundJobs("first-agent", []fileio.AsyncCmdInfo{{
		Handle: "tests", Command: "go test ./...", Status: fileio.AsyncFailed, EndedAt: now, ExitCode: 1,
	}}, now)

	if app.ActiveSession != second || secondProjection.deck.IsOpen() {
		t.Fatal("inactive background update changed the active session projection")
	}
	if firstProjection.deck.Count() != 2 || firstProjection.deck.Active().ID != sessionSettingsPanelID {
		t.Fatalf("passive update displaced active panel: count=%d active=%#v", firstProjection.deck.Count(), firstProjection.deck.Active())
	}
	if !firstProjection.deckFocused || firstProjection.backgroundJobs.VisibleCount() != 1 {
		t.Fatal("passive update stole focus or failed to update its owning screen")
	}
}

func TestBackgroundJobsPanelClosesAfterCompletedRowsExpire(t *testing.T) {
	app := newFullTestApp(t)
	session := &workspace.Session{SessionID: "session", Settings: workspace.NewSessionSettingsState(types.Settings{}, types.Settings{}, nil)}
	app.AgentMgr.AddOrchestrator("agent", nil, nil, nil, nil, &workspace.AgentState{})
	app.SessionsByAgent = make(map[string]*workspace.Session)
	app.SessionsByAgent["agent"] = session
	app.ActiveSession = session
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	app.updateSessionBackgroundJobs("agent", []fileio.AsyncCmdInfo{{
		Handle: "done", Command: "true", Status: fileio.AsyncCompleted, EndedAt: now,
	}}, now)
	projection := app.activeSessionProjection()
	if !projection.deck.IsOpen() || projection.deckFocused {
		t.Fatal("passive background panel did not open without focus")
	}

	app.refreshSessionBackgroundJobs(now.Add(16 * time.Second))
	if projection.deck.IsOpen() {
		t.Fatal("expired background panel remained open")
	}
}

func TestBackgroundJobsDoesNotRetainFocusAfterBlockingPanelCloses(t *testing.T) {
	app := newFullTestApp(t)
	session := &workspace.Session{SessionID: "session", Settings: workspace.NewSessionSettingsState(types.Settings{}, types.Settings{}, nil)}
	app.AgentMgr.AddOrchestrator("agent", nil, nil, nil, nil, &workspace.AgentState{})
	app.SessionsByAgent = map[string]*workspace.Session{"agent": session}
	app.ActiveSession = session
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	app.updateSessionBackgroundJobs("agent", []fileio.AsyncCmdInfo{{
		Handle: "running", Command: "go test ./...", Status: fileio.AsyncRunning, StartedAt: now,
	}}, now)
	projection := app.activeSessionProjection()
	projection.confirmation.Reset(confirmation.Details{Question: "Proceed?", Tool: "bash"})
	app.openSessionConfirmationPanel(projection)
	if !projection.deckFocused || projection.deck.Active().ID != sessionConfirmationPanelID {
		t.Fatal("blocking panel did not take deck focus")
	}

	app.closeProjectionPanel(projection, sessionConfirmationPanelID)
	if projection.deck.Active().ID != sessionBackgroundJobsPanelID || projection.deckFocused {
		t.Fatal("passive panel retained focus after the blocker closed")
	}
}
