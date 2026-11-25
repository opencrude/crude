package viewmodel

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/internal/providersim"
	"crude/tui/screens/decision"
	"crude/tui/ui"
	"crude/types/conversation"
)

// planReviewRequest reads the user messages of each request from the journal.
// Runtime context travels as user messages before and after the one the user wrote.
func planReviewRequest(sim *providersim.Server, i int) (string, bool) {
	requests := sim.Requests()
	if i >= len(requests) {
		return "", false
	}
	return simulatedUserText(requests[i], "\n"), true
}

// The whole review: a Plan mode turn proposes a plan, Review opens it as a
// file beside the chat, the user edits it and comments on its text, sends
// the comments, the agent revises, and the user implements the revision.
func TestPlanReviewConnectedJourney(t *testing.T) {
	server := simulatedChat(t,
		simulatedAnswer("Explored.\n<proposed_plan>\n# Add addition\n\n## Steps\n- Implement and test addition\n- Update the README\n</proposed_plan>"),
		simulatedAnswer("Revised.\n<proposed_plan>\n# Add addition\n\n## Steps\n- Implement addition with overflow checks\n- Add table tests\n</proposed_plan>"),
		simulatedAnswer("Implemented."),
	)
	server.Respond(func(providersim.Request) providersim.Turn { return simulatedAnswer("Done.") })
	runtime := remoteJourneyRuntime(t, server.BaseURL(providersim.Chat))
	shell := newRemoteJourneyShell(t, runtime, t.TempDir())
	app := shell.app
	id := remoteJourneyCreate(t, shell)
	until := func(condition func() bool) { t.Helper(); remoteJourneyUntil(t, []remoteJourneyShell{shell}, condition) }

	app.Input.SetInput("/plan Propose a plan to add addition.")
	app.setFocusInput()
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, app, cmd)
	until(func() bool {
		projection := app.activeSessionProjection()
		return projection != nil && projection.decision != nil && projection.decision.Purpose() == "implement_plan"
	})
	first, ok := app.latestPlanRevision(id)
	if !ok || first.Number != 1 || first.Title != "Add addition" {
		t.Fatalf("first revision = %+v", first)
	}
	if data, err := os.ReadFile(first.Path); err != nil || !strings.Contains(string(data), "Implement and test addition") {
		t.Fatalf("plan file %s: %q %v", first.Path, data, err)
	}
	app.paintShellFrame()
	if frame := frameText(app); !strings.Contains(frame, "Review") || !strings.Contains(frame, "Implement this plan?") {
		t.Fatalf("plan decision does not offer Review:\n%s", frame)
	}

	// Review keeps the question open and opens the plan beside the chat.
	projection := app.activeSessionProjection()
	runAppCommand(t, app, app.resolveSessionDecision(decision.ResultMsg{Target: projection.decision, Purpose: "implement_plan", Confirmed: true, Choice: planChoiceReview, Stay: true}))
	until(func() bool { return app.EditorState.Editor != nil && app.EditorState.Editor.FilePath() == first.Path })
	if projection.decision == nil || projection.decisionAgentID != id {
		t.Fatal("Review closed the plan decision")
	}
	ed := app.EditorState.Editor
	app.setFocusEditor()
	app.paintShellFrame()
	if frame := frameText(app); !strings.Contains(frame, "✎ Revision 1  [ Implement ]") || !strings.Contains(frame, "Implement and test addition") {
		t.Fatalf("plan editor lacks its review bar:\n%s", frame)
	}

	// Edit: add a line at the end of the steps.
	lines := strings.Split(ed.Value(), "\n")
	readme := -1
	for i, line := range lines {
		if strings.Contains(line, "Update the README") {
			readme = i
		}
	}
	ed.SetCursorPosition(readme, len(lines[readme]))
	pressKeys(t, app, tea.KeyPressMsg{Code: tea.KeyEnter})
	for _, r := range "- Add a changelog entry" {
		pressKeys(t, app, tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	// Comment on a selected passage with Ctrl+Alt+C.
	target := -1
	for i, line := range strings.Split(ed.Value(), "\n") {
		if strings.Contains(line, "Implement and test addition") {
			target = i
		}
	}
	ed.SetSelection(target, 2, target, len("- Implement and test addition"))
	pressKeys(t, app, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl | tea.ModAlt})
	if app.TextPrompt == nil {
		t.Fatal("Ctrl+Alt+C did not ask for a comment")
	}
	for _, r := range "Handle integer overflow." {
		pressKeys(t, app, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	pressKeys(t, app, tea.KeyPressMsg{Code: tea.KeyEnter})
	until(func() bool {
		revision, _ := app.latestPlanRevision(id)
		return len(revision.Comments) == 1
	})
	revision, _ := app.latestPlanRevision(id)
	if comment := revision.Comments[0]; comment.Anchor.Exact != "Implement and test addition" || comment.Body != "Handle integer overflow." || comment.Sent {
		t.Fatalf("comment = %+v", comment)
	}
	until(func() bool {
		app.paintShellFrame()
		frame := frameText(app)
		return strings.Contains(frame, "Handle integer overflow.") && strings.Contains(frame, "Revision 1 · 1 comment  [ Send comments ]")
	})
	app.paintShellFrame()
	if frame := frameText(app); !strings.Contains(frame, "Send your comments?") {
		t.Fatalf("plan decision did not follow the pending comment:\n%s", frame)
	}

	// Send the comments from the review bar.
	send, handled := app.handlePlanReviewAction(ui.Action{ID: actionPlanSend, Payload: ed})
	if !handled {
		t.Fatal("the review bar did not handle Send comments")
	}
	runAppCommand(t, app, send)
	until(func() bool { _, ok := planReviewRequest(server, 1); return ok })
	feedback, _ := planReviewRequest(server, 1)
	for _, want := range []string{"<plan_review revision=\"1\">", "<quoted_text>Implement and test addition</quoted_text>",
		"<note>Handle integer overflow.</note>", "<my_edits>", "+- Add a changelog entry", "<comment lines=\"4\"", "<plan>\n# Add addition\n\n## Steps\n"} {
		if !strings.Contains(feedback, want) {
			t.Fatalf("feedback lacks %q:\n%s", want, feedback)
		}
	}
	if data, err := os.ReadFile(first.Path); err != nil || !strings.Contains(string(data), "Add a changelog entry") {
		t.Fatalf("sending did not save the edited plan: %q %v", data, err)
	}
	until(func() bool {
		latest, ok := app.latestPlanRevision(id)
		return ok && latest.Number == 2
	})
	review, _ := app.ws.PlanReviewForAgent(id)
	if old, _ := review.Revision(1); old.Outcome != conversation.PlanOutcomeReplaced || !old.Comments[0].Sent {
		t.Fatalf("revision 1 after the revision = %+v", old)
	}
	if !app.ws.PlanModeForAgent(id) {
		t.Fatal("sending comments turned off Plan mode")
	}
	app.paintShellFrame()
	if frame := frameText(app); !strings.Contains(frame, "Revision 2 replaced it.") {
		t.Fatalf("old plan editor does not say it was replaced:\n%s", frame)
	}

	// Implement revision 2 from the decision in the chat.
	until(func() bool {
		projection := app.activeSessionProjection()
		return projection != nil && projection.decision != nil && strings.HasPrefix(projection.decisionData, "2:implement")
	})
	projection = app.activeSessionProjection()
	runAppCommand(t, app, app.resolveSessionDecision(decision.ResultMsg{Target: projection.decision, Purpose: "implement_plan", Confirmed: true}))
	until(func() bool { _, ok := planReviewRequest(server, 2); return ok && !app.ws.PlanModeForAgent(id) })
	if implement, _ := planReviewRequest(server, 2); !strings.Contains(implement, "Implement the plan") {
		t.Fatalf("implementation request = %q", implement)
	}
	until(func() bool {
		latest, _ := app.latestPlanRevision(id)
		return latest.Outcome == conversation.PlanOutcomeImplemented
	})
}

func TestPlanFilePathAcceptsOnlySessionPlanFiles(t *testing.T) {
	for path, want := range map[string]bool{
		"/home/u/.crude/sessions/s1/plans/add-v1.md":    true,
		`C:\Users\u\.crude\sessions\s1\plans\add-v1.md`: true,
		"/repo/docs/plans/editor-parity.md":             true, // checked against the record next
		"/repo/plans.md":                                false,
		"add-v1.md":                                     false,
	} {
		if got := planFilePath(path); got != want {
			t.Errorf("planFilePath(%q) = %v", path, got)
		}
	}
	if allocs := testing.AllocsPerRun(100, func() { planFilePath("/home/u/.crude/sessions/s1/plans/add-v1.md") }); allocs != 0 {
		t.Fatalf("planFilePath allocates %.0f times", allocs)
	}
}

func TestPlanDecisionAsksToSendPendingComments(t *testing.T) {
	revision := conversation.PlanRevision{Number: 3, Title: "Cache", Comments: []conversation.PlanComment{{ID: "a"}, {ID: "b", Sent: true}}}
	spec, data := planDecisionSpec(revision)
	if data != "3:send:1" || spec.Confirm.Label != "Send comments" || len(spec.Alternates) != 1 || spec.Alternates[0].ID != planChoiceReview || !spec.Alternates[0].Stay {
		t.Fatalf("pending comments decision = %+v %q", spec, data)
	}
	revision.Comments = revision.Comments[1:]
	spec, data = planDecisionSpec(revision)
	if data != "3:implement:0" || spec.Confirm.Label != "Implement" || len(spec.Alternates) != 2 {
		t.Fatalf("approval decision = %+v %q", spec, data)
	}
}

// New session from the chat decision: the shell creates the destination, the
// core sends the reviewed plan as its first input, and only that accepted
// input closes the source review. The new session opens in its own tab and
// the source stays in Plan mode.
func TestPlanNewSessionConnectedJourney(t *testing.T) {
	server := simulatedChat(t,
		simulatedAnswer("Explored.\n<proposed_plan>\n# Add addition\n\n## Steps\n- Implement and test addition\n</proposed_plan>"),
		simulatedAnswer("Implemented in a fresh context."),
	)
	server.Respond(func(providersim.Request) providersim.Turn { return simulatedAnswer("Done.") })
	runtime := remoteJourneyRuntime(t, server.BaseURL(providersim.Chat))
	shell := newRemoteJourneyShell(t, runtime, t.TempDir())
	app := shell.app
	id := remoteJourneyCreate(t, shell)
	until := func(condition func() bool) { t.Helper(); remoteJourneyUntil(t, []remoteJourneyShell{shell}, condition) }

	app.Input.SetInput("/plan Propose a plan to add addition.")
	app.setFocusInput()
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, app, cmd)
	until(func() bool {
		projection := app.activeSessionProjection()
		return projection != nil && projection.decision != nil && projection.decision.Purpose() == "implement_plan"
	})
	app.paintShellFrame()
	if frame := frameText(app); !strings.Contains(frame, "New session") {
		t.Fatalf("plan decision does not offer New session:\n%s", frame)
	}
	projection := app.activeSessionProjection()
	runAppCommand(t, app, app.resolveSessionDecision(decision.ResultMsg{Target: projection.decision, Purpose: "implement_plan", Confirmed: true, Choice: planChoiceNewSession}))
	until(func() bool { active := app.AgentView.ActiveID(); return active != "" && active != id })
	destination := app.AgentView.ActiveID()
	until(func() bool { _, ok := planReviewRequest(server, 1); return ok })
	if fresh, _ := planReviewRequest(server, 1); !strings.Contains(fresh, "Implement the plan in a fresh context") ||
		!strings.Contains(fresh, "- Implement and test addition") || strings.Contains(fresh, "Propose a plan to add addition") {
		t.Fatalf("destination's first request = %q", fresh)
	}
	until(func() bool {
		latest, _ := app.latestPlanRevision(id)
		return latest.Outcome == conversation.PlanOutcomeNewSession
	})
	latest, _ := app.latestPlanRevision(id)
	if session := app.ws.SessionsByAgent()[destination]; session == nil || latest.NewSessionID != session.SessionID {
		t.Fatalf("source review names destination %q, the new tab is %q", latest.NewSessionID, destination)
	}
	if !app.ws.PlanModeForAgent(id) || app.ws.PlanModeForAgent(destination) {
		t.Fatal("New session changed the wrong session's Plan mode")
	}
	until(func() bool {
		app.paintShellFrame()
		return strings.Contains(frameText(app), "Implemented in a fresh context.")
	})
	if _, again := planReviewRequest(server, 2); again {
		t.Fatal("the plan was sent more than once")
	}
	if _, source := app.sessionProjectionForAgent(id); source != nil && source.decision != nil && source.decisionAgentID == id {
		t.Fatal("the settled plan decision is still open in the source")
	}
}
