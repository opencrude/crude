package viewmodel

import (
	"strings"
	"testing"
	"time"

	"crude/prompt"
	"crude/tui/screens/decision"
	"crude/types"
	"crude/workspace"
)

func TestProposedPlanTakesTheLastStandaloneBlock(t *testing.T) {
	reply := "Draft:\n<proposed_plan>\n# Old\n</proposed_plan>\nRevised:\n  <proposed_plan>\n# Fix add\n\n- Change calc.py\n</proposed_plan>\nDone."
	plan, ok := proposedPlan(reply)
	if !ok || plan != "# Fix add\n\n- Change calc.py" {
		t.Fatalf("plan=%q ok=%v", plan, ok)
	}
	for _, reply := range []string{"no plan", "inline <proposed_plan>x</proposed_plan>", "<proposed_plan>\n\n</proposed_plan>", "<proposed_plan>\nunclosed"} {
		if _, ok := proposedPlan(reply); ok {
			t.Fatalf("accepted %q", reply)
		}
	}
}

// A completed Plan mode turn with a proposed plan offers the three Codex
// choices; implementing in a new session sends only the plan, under the
// fresh-context preface, as that session's first message.
func TestPlanDecisionOffersImplementationInANewSession(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	id := addBackendTestOrchestrator(t, app)
	t.Cleanup(wsOf(app).CloseAllSessions)
	app.ActiveSession = app.ws.SessionsByAgent()[id]
	app.AgentView.SetActive(id)
	if err := app.ws.SetPlanModeForAgent(id, true); err != nil {
		t.Fatal(err)
	}
	reply := "Explored.\n<proposed_plan>\n# Fix add\n\n- Return a + b\n</proposed_plan>"
	app.ws.AppendAgentMessages(id, []workspace.Message{{Role: types.RoleAssistant, Content: reply, Time: time.Now()}}, false)
	app.offerPlanImplementation(id)
	projection := app.activeSessionProjection()
	if projection == nil || projection.decisionAgentID != id || projection.decision.Purpose() != "implement_plan" || projection.decisionData != "# Fix add\n\n- Return a + b" {
		t.Fatalf("plan decision not offered with its plan: %+v", projection)
	}
	cmd := app.resolveSessionDecision(decision.ResultMsg{Target: projection.decision, Purpose: "implement_plan", Confirmed: true, Alternate: true})
	if cmd == nil {
		t.Fatal("implementing in a new session did nothing")
	}
	msg, ok := cmd().(remoteSessionReadyMsg)
	if !ok || msg.initial.Text != prompt.PlanImplementationFreshContext("# Fix add\n\n- Return a + b") {
		t.Fatalf("new session first message = %#v", msg.initial)
	}
	if !strings.Contains(msg.initial.Text, "fresh context") || strings.Contains(msg.initial.Text, "Explored.") {
		t.Fatalf("fresh-context message carries planning conversation: %q", msg.initial.Text)
	}
	if !app.ws.PlanModeForAgent(id) {
		t.Fatal("implementing elsewhere turned off the planning session's Plan mode")
	}
}
