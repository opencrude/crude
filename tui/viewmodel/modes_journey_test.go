package viewmodel

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

func TestModeCommandsShowPlanAndInspectableGoal(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	id := addBackendTestOrchestrator(t, app)
	t.Cleanup(wsOf(app).CloseAllSessions)
	app.switchToAgentView(id)
	app.width, app.height = 120, 32
	app.setFocusInput()
	send := func(text string) {
		t.Helper()
		app.Input.SetInput(text)
		_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		runAppCommand(t, app, cmd)
	}
	send("/plan")
	app.paintShellFrame()
	if header, footer := rowText(app, 0), rowText(app, app.height-1); !strings.Contains(header, "Plan mode") || !strings.Contains(footer, "Plan on") {
		t.Fatalf("plan enable lacks feedback: %q / %q", header, footer)
	}
	for _, notice := range app.ws.Status().Notifications(false) {
		if notice.Transient {
			app.ws.Status().Dismiss(notice.ID)
		}
	}
	app.paintShellFrame()
	if footer := rowText(app, app.height-1); !strings.Contains(footer, "Plan · next turn") {
		t.Fatalf("plan indicator vanished with feedback: %q", footer)
	}
	objective := "Prove the entire objective, including " + strings.Repeat("the complete acceptance evidence ", 25) + "FINAL REQUIREMENT."
	send("/goal " + objective)
	app.Input.SetInput("a private unsent draft")
	app.goalCommand(id, "")
	screen := app.sessionTextOverlay()
	if screen == nil || !screen.ReadOnly() || !app.overlayHost.IsOpen(ui.OverlayTextOverlay) {
		t.Fatal("/goal did not open the read-only status reader")
	}
	if text := screen.Value(); !strings.Contains(text, objective) || !strings.Contains(text, "automatic continuation waits for Plan mode") ||
		!strings.Contains(text, "/goal pause") {
		t.Fatalf("goal reader omitted the objective, wait or controls: %q", text)
	}
	if app.Input.Value() != "a private unsent draft" {
		t.Fatal("inspecting a goal consumed the composer draft")
	}
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	runAppCommand(t, app, cmd)
	if app.overlayHost.IsOpen(ui.OverlayTextOverlay) || app.Input.Value() != "a private unsent draft" {
		t.Fatal("Esc did not close the goal reader without consuming the draft")
	}
	send("/goal pause")
	send("/plan")
	app.paintShellFrame()
	if header, footer := rowText(app, 0), rowText(app, app.height-1); strings.Contains(header, "Plan mode") || !strings.Contains(footer, "Plan off") {
		t.Fatalf("plan disable is misleading: %q / %q", header, footer)
	}
}

// Two real shell ports see the same core-owned schedule; after both detach,
// making the retained session eligible starts the scheduled turn without a UI.
func TestLoopConnectedShellsAndDetachedCore(t *testing.T) {
	requests := make(chan struct{}, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests <- struct{}{}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Loop checked\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	runtime := remoteJourneyRuntime(t, server.URL)
	root := t.TempDir()
	one := newRemoteJourneyShell(t, runtime, root)
	id := remoteJourneyCreate(t, one)
	two := newRemoteJourneyShell(t, runtime, root)
	shells := []remoteJourneyShell{one, two}
	remoteJourneyUntil(t, shells, func() bool { return two.app.AgentView.ActiveID() == id })
	send := func(shell remoteJourneyShell, text string) {
		t.Helper()
		shell.app.Input.SetInput(text)
		shell.app.setFocusInput()
		_, cmd := shell.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		runAppCommand(t, shell.app, cmd)
	}
	send(one, "/plan")
	remoteJourneyUntil(t, shells, func() bool {
		return one.app.ws.PlanModeForAgent(id) && two.app.ws.PlanModeForAgent(id)
	})
	send(one, "/loop 1m Check status and report.")
	remoteJourneyUntil(t, shells, func() bool {
		return len(one.app.ws.LoopsForAgent(id)) == 1 && len(two.app.ws.LoopsForAgent(id)) == 1
	})
	loop := two.app.ws.LoopsForAgent(id)[0]
	if loop.Prompt != "Check status and report." || loop.Interval != time.Minute || loop.Paused {
		t.Fatalf("replica lost schedule: %+v", loop)
	}
	two.app.paintShellFrame()
	if header := rowText(two.app, 0); !strings.Contains(header, "Loop · 1 waiting") {
		t.Fatalf("connected header omitted the loop's Plan-mode wait: %q", header)
	}
	two.app.Input.SetInput("peer draft")
	two.app.loopCommand(id, "")
	if text := two.app.sessionTextOverlay().Value(); !strings.Contains(text, loop.ID) ||
		!strings.Contains(text, loop.Prompt) || !strings.Contains(text, "waiting for Plan mode") {
		t.Fatalf("loop reader omitted authoritative schedule: %q", text)
	}
	if two.app.Input.Value() != "peer draft" {
		t.Fatal("loop inspection consumed the peer draft")
	}
	_, closeCmd := two.app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	runAppCommand(t, two.app, closeCmd)
	for _, shell := range shells {
		if err := shell.app.CloseWorkspaceAttachment(); err != nil {
			t.Fatal(err)
		}
	}
	status, err := runtime.Status(context.Background())
	if err != nil || len(status.Workspaces) != 1 || status.Workspaces[0].Active.Loops != 1 {
		t.Fatalf("detached core did not report its retained loop: %+v, %v", status, err)
	}
	if idle, err := runtime.Idle(context.Background(), 0); err != nil || idle {
		t.Fatalf("core treated a scheduled loop as idle: idle=%v err=%v", idle, err)
	}
	select {
	case <-requests:
		t.Fatal("loop ran while Plan mode was on")
	default:
	}
	var enable func() error
	if err := runtime.Each(context.Background(), func(ws *workspace.Workspace) {
		enable, _ = ws.PrepareSetPlanMode(id, false)
	}); err != nil || enable == nil {
		t.Fatalf("resolve retained session: %v", err)
	}
	if err := enable(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-requests:
	case <-time.After(8 * time.Second):
		t.Fatal("detached core did not run its due loop")
	}
	three := newRemoteJourneyShell(t, runtime, root)
	remoteJourneyUntil(t, []remoteJourneyShell{three}, func() bool {
		return three.app.AgentView.ActiveID() == id && len(three.app.ws.LoopsForAgent(id)) == 1
	})
	send(three, "/loop pause "+loop.ID)
	remoteJourneyUntil(t, []remoteJourneyShell{three}, func() bool {
		loops := three.app.ws.LoopsForAgent(id)
		return len(loops) == 1 && loops[0].Paused
	})
	send(three, "/loop clear "+loop.ID)
	remoteJourneyUntil(t, []remoteJourneyShell{three}, func() bool { return len(three.app.ws.LoopsForAgent(id)) == 0 })
}

func TestLoopIntervalValidation(t *testing.T) {
	for _, test := range []struct {
		text string
		want time.Duration
	}{
		{"1m", time.Minute}, {"90m", 90 * time.Minute}, {"2h", 2 * time.Hour}, {"7d", 7 * 24 * time.Hour},
		{"1s", 0}, {"8d", 0}, {"-5m", 0}, {"bad", 0},
	} {
		got, err := parseLoopInterval(test.text)
		if got != test.want || (err != nil) != (test.want == 0) {
			t.Errorf("%q: interval=%s err=%v", test.text, got, err)
		}
	}
}

func TestPlanProposalOfferConnectedJourney(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"<proposed_plan>\\n# Add\\n- Implement and test addition\\n</proposed_plan>\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	runtime := remoteJourneyRuntime(t, server.URL)
	shell := newRemoteJourneyShell(t, runtime, t.TempDir())
	id := remoteJourneyCreate(t, shell)
	shell.app.Input.SetInput("/plan Propose a plan to add addition.")
	shell.app.setFocusInput()
	_, cmd := shell.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, shell.app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		projection := shell.app.activeSessionProjection()
		return projection != nil && projection.decision.Purpose() == "implement_plan"
	})
	projection := shell.app.activeSessionProjection()
	if !shell.app.ws.PlanModeForAgent(id) || !strings.Contains(projection.decisionData, "Implement and test addition") {
		t.Fatalf("connected proposal was not offered in Plan mode: %q", projection.decisionData)
	}
}

func TestGoalDetailsDoNotHideBlockedReasonOrBudget(t *testing.T) {
	goal := types.Goal{Objective: "Ship", Status: types.GoalBudgetLimited, TokensUsed: 1200, TokenBudget: 1000, Reason: "Budget reached."}
	text := goalDetails(goal, false)
	for _, want := range []string{"limited by budget", "1.2k / 1.0k", "Budget reached.", "Ship"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %q", want, text)
		}
	}
}

func TestSessionInfoClosesWhenSwitchingOwners(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	first := addBackendTestOrchestrator(t, app)
	second := addBackendTestOrchestrator(t, app)
	t.Cleanup(wsOf(app).CloseAllSessions)
	app.switchToAgentView(first)
	app.Input.SetInput("first session draft")
	app.goalCommand(first, "")
	if !app.overlayHost.IsOpen(ui.OverlayTextOverlay) {
		t.Fatal("goal reader did not open")
	}
	app.switchToAgentView(second)
	if app.overlayHost.IsOpen(ui.OverlayTextOverlay) {
		t.Fatal("session information stayed open over another owner")
	}
	app.switchToAgentView(first)
	if app.Input.Value() != "first session draft" {
		t.Fatalf("session switch lost the draft: %q", app.Input.Value())
	}
}
