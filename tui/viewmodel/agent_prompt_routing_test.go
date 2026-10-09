package viewmodel

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/agent"
	"crude/provider"
	"crude/tui/screens/confirmation"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

type promptFixtureSender struct{ done chan string }

func (s promptFixtureSender) SendAgentEvent(kind string, data any) {
	if kind == "done" || kind == "error" {
		select {
		case s.done <- fmt.Sprintf("%s: %v", kind, data):
		default:
		}
	}
}

func (s promptFixtureSender) SendAgentEventWithID(kind string, data any, _ string) {
	s.SendAgentEvent(kind, data)
}

func newBackendPromptFamilyApp(t *testing.T) (*App, string, string, map[string]chan types.BlockResponse) {
	t.Helper()
	app, ws := newRealShellApp(t)
	t.Cleanup(ws.Close)
	wsOf(app).SessionsByID = make(map[string]*workspace.Session)
	wsOf(app).SessionsByAgent = make(map[string]*workspace.Session)
	t.Cleanup(wsOf(app).CloseAllSessions)
	channels := make(map[string]chan types.BlockResponse)
	var roots []string
	for _, label := range []string{"Alpha", "Beta"} {
		cfg := testAccountConfig()
		cfg.Agents.Enabled = true
		profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
		enabled := true
		profile.MultiAgent = &enabled
		cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var request struct {
				Stream bool `json:"stream"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, `{"data":[]}`)
				return
			}
			first := requests.Add(1) == 1
			if request.Stream {
				w.Header().Set("Content-Type", "text/event-stream")
				if first {
					_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"fixture\",\"type\":\"function\",\"function\":{\"name\":\"agent_start\",\"arguments\":\"{\\\"description\\\":\\\"fixture\\\",\\\"category\\\":\\\"invalid-fixture\\\"}\"}}]}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
				} else {
					_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Ready\"}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				}
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if first {
				// Invalid launch arguments initialize the real delegation owner
				// through tool dispatch, but start no child or external request.
				_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"fixture","type":"function","function":{"name":"agent_start","arguments":"{\"description\":\"fixture\",\"category\":\"invalid-fixture\"}"}}]},"finish_reason":"tool_calls"}]}`)
				return
			}
			_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"Ready"},"finish_reason":"stop"}]}`)
		}))
		t.Cleanup(server.Close)
		account := cfg.Accounts["test"]
		template := account.Connection
		template.ProviderURL = server.URL
		account.Connection = template
		cfg.Accounts["test"] = account
		sess, err := wsOf(app).CreateSession(cfg, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		backend := app.ws.BackendSession(sess.SessionID)
		sender := promptFixtureSender{done: make(chan string, 1)}
		orch, err := wsOf(app).StartOrchestrator(sess.SessionID, sender)
		if err != nil {
			t.Fatal(err)
		}
		app.AddOrchestrator(orch.ID)
		app.ws.AgentMgr().Get(orch.ID).Label = label
		roots = append(roots, orch.ID)
		channels[orch.ID] = orch.BlockResolveCh
		if label != "Alpha" {
			continue
		}
		backend.RuntimeProfile().GetOrCreateOverride(orch.ID).MultiAgent = &enabled
		if !orch.SubmitInput(provider.UserInput{Text: "Initialize the isolated permission test."}) {
			t.Fatal("could not initialize test runner")
		}
		terminal := ""
		select {
		case terminal = <-sender.done:
		case <-time.After(10 * time.Second):
			t.Fatal("local test runner initialization did not settle")
		}
		runner := backend.SubAgentRunner()
		if runner == nil {
			t.Fatalf("delegation dispatch did not create the test runner: %s (%d requests)", terminal, requests.Load())
		}
		for _, child := range []struct{ id, parent string }{{"child-a", orch.ID}, {"child-b", orch.ID}, {"nested", "child-a"}} {
			blockCh := make(chan types.BlockResponse, 16)
			handle := &agent.SubAgentHandle{Spec: agent.SubAgentSpec{
				ID: agent.AgentID(child.id), ParentID: agent.AgentID(child.parent), Name: child.id, Category: agent.CatCrafter,
			}, BlockCh: blockCh, Cancel: func() {}}
			if err := runner.Manager().Register(handle); err != nil {
				t.Fatal(err)
			}
			channels[child.id] = blockCh
			pane := app.ws.PaneMgr().GetOrCreate(child.id, workspace.PaneAgent, child.id)
			app.ws.AgentMgr().AddSubAgent(child.id, child.parent, child.id, "Test", "crafter", workspace.NewAgentState(child.id, pane.Messages, app.ws.Logger()))
		}
	}
	app.syncAgentWindowTabs()
	app.switchToAgentView(roots[0])
	app.width, app.height = 120, 40
	app.paintShellFrame()
	return app, roots[0], roots[1], channels
}

func TestFamilyTabCloseDeliversDenyToExactChildBackend(t *testing.T) {
	app, alpha, beta, channels := newBackendPromptFamilyApp(t)
	emitTestPermission(app, "child-a", "a-block")
	emitTestPermission(app, "child-b", "b-block")
	first, second := app.agentPrompts[0], app.agentPrompts[1]
	first.projection.deck.Activate(second.panelID())
	app.paintShellFrame()
	app.closeAgentPromptTab(first.panelID())
	select {
	case response := <-channels["child-a"]:
		if response.RequestID != "a-block" || response.Value != "deny" || response.Outcome != types.BlockAnswered {
			t.Fatalf("tab X response=%+v", response)
		}
	default:
		t.Fatal("tab X did not deliver denial to the child backend")
	}
	for _, owner := range []string{alpha, beta, "child-b", "nested"} {
		if len(channels[owner]) != 0 {
			t.Fatalf("child denial was delivered to unrelated owner %s", owner)
		}
	}
	app.paintShellFrame()
	_, command := app.dispatchKeyPress(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if command == nil {
		t.Fatal("remaining request lost keyboard focus when the tab strip disappeared")
	}
	app.Update(command())
	select {
	case response := <-channels["child-b"]:
		if response.RequestID != "b-block" || response.Value != "allow" {
			t.Fatalf("approve response=%+v", response)
		}
	default:
		t.Fatal("approval did not reach the remaining child backend")
	}
	if len(app.agentPrompts) != 0 || first.projection.deck.IsOpen() {
		t.Fatal("resolved backend requests retained tabs")
	}
}

func newPromptFamilyApp(t testing.TB) *App {
	t.Helper()
	app := newFullTestApp(t)
	app.width, app.height = 120, 40
	app.AddOrchestrator("alpha")
	app.AddOrchestrator("beta")
	alpha := &workspace.Session{SessionID: "alpha-family", AgentID: "alpha"}
	beta := &workspace.Session{SessionID: "beta-family", AgentID: "beta"}
	wsOf(app).SessionsByAgent = map[string]*workspace.Session{"alpha": alpha, "beta": beta}
	for _, child := range []struct{ id, parent string }{{"child-a", "alpha"}, {"child-b", "alpha"}, {"nested", "child-a"}} {
		pane := app.ws.PaneMgr().GetOrCreate(child.id, workspace.PaneAgent, child.id)
		app.ws.AgentMgr().AddSubAgent(child.id, child.parent, child.id, "model", "researcher", workspace.NewAgentState(child.id, pane.Messages, app.ws.Logger()))
	}
	app.syncAgentWindowTabs()
	app.switchToAgentView("alpha")
	app.paintShellFrame()
	return app
}

func emitTestPermission(app *App, owner, block string) {
	if app.ws.AgentMgr().Get(owner) == nil {
		for _, node := range app.ws.AgentMgr().AllNodes() {
			if node.Label == owner {
				owner = node.ID
				break
			}
		}
	}
	app.handleAgentEvent(types.AgentEventMsg{AgentID: owner, Type: "blocking", Data: map[string]string{
		"block_id": block, "operation": "Update", "tool": "apply_patch", "target": owner + ".go",
		"question": "Allow " + owner + " edit?", "reason": "This request belongs to " + owner,
	}})
}

func TestFamilyPermissionTabsRetainExactOwnersAndResolveIndependently(t *testing.T) {
	app := newPromptFamilyApp(t)
	app.Input.SetInput("unfinished prompt with exact whitespace\n  keep this")
	emitTestPermission(app, "child-a", "a")
	first := app.agentPrompts[0]
	projection := first.projection
	app.paintShellFrame()
	if first.confirmation.Bounds().Y != 1 {
		t.Fatalf("single request reserves an unwanted strip: bounds=%+v", first.confirmation.Bounds())
	}
	emitTestPermission(app, "child-b", "b")
	second := app.agentPrompts[1]
	if first.confirmation == second.confirmation || projection != second.projection || projection.deck.Count() != 2 {
		t.Fatal("family requests do not own separate screens in one deck")
	}
	if projection.deck.Active().ID != first.panelID() {
		t.Fatal("a later family request displaced the user's active request")
	}
	app.paintShellFrame()
	if first.confirmation.Bounds().Y != 1+ui.StripHeight {
		t.Fatal("multiple requests did not reserve the shared tab strip")
	}
	result := projection.deck.HandleAction(ui.EventContext{}, ui.Action{ID: "tabs.activate", Payload: second.panelID()})
	if !result.Handled || projection.deck.Active().OwnerID != "child-b" {
		t.Fatal("blocking family request tabs cannot be browsed")
	}
	if second.confirmation.Details().Target != "child-b.go" || first.confirmation.Details().Target != "child-a.go" {
		t.Fatal("tab selection rewrote request evidence")
	}
	// Close the nonselected tab through the same action routed by a mouse X.
	result = projection.deck.HandleAction(ui.EventContext{}, ui.Action{ID: "tabs.close", Payload: first.panelID()})
	if result.Action == nil || result.Action.ID != "agent-prompt.close" || result.Action.Payload != first.panelID() {
		t.Fatal("tab close silently discarded the request instead of routing denial")
	}
	app.handleHostAction(result.Action)
	if len(app.agentPrompts) != 1 || app.agentPrompts[0] != second || projection.deck.Count() != 1 {
		t.Fatal("closing one request removed or retained the wrong tab")
	}
	app.paintShellFrame()
	if second.confirmation.Bounds().Y != 1 {
		t.Fatal("tab bar left an empty row after returning to one request")
	}
	app.resolvePromptConfirmation(second.confirmation, "allow")
	if projection.deck.IsOpen() || len(app.agentPrompts) != 0 || app.OverlayState.IsBlocking() {
		t.Fatal("last resolved request left stale deck/blocking state")
	}
	if got := app.Input.Value(); got != "unfinished prompt with exact whitespace\n  keep this" {
		t.Fatalf("family resolution discarded the composer: %q", got)
	}
}

func TestFamilyDockSurvivesParentSiblingNestedAndFamilySwitches(t *testing.T) {
	app := newPromptFamilyApp(t)
	emitTestPermission(app, "child-a", "a")
	emitTestPermission(app, "nested", "nested")
	projection := app.activeSessionProjection()
	selected := projection.deck.Active().ID
	for _, agent := range []string{"child-b", "nested", "alpha"} {
		app.switchToAgentView(agent)
		app.paintShellFrame()
		if app.activeSessionDeck() != projection || projection.deck.Count() != 2 || projection.deck.Active().ID != selected || app.sessionDeckRect.Empty() {
			t.Fatalf("family dock changed when viewing %s", agent)
		}
	}
	app.switchToAgentView("beta")
	app.paintShellFrame()
	if app.sessionDeckBlocking() || !app.sessionDeckRect.Empty() || app.overlayHost.IsOpen(ui.OverlayAgentPrompt) || projection.deck.Count() != 2 {
		t.Fatal("leaving a family exposed, discarded, or modalized its retained dock")
	}
	emitTestPermission(app, "beta", "beta")
	if app.activeSessionDeck() == projection || app.activeSessionDeck().deck.Count() != 1 {
		t.Fatal("independent family request merged into the hidden family deck")
	}
	app.switchToAgentView("child-b")
	app.paintShellFrame()
	if app.activeSessionDeck() != projection || projection.deck.Active().ID != selected || projection.deck.Count() != 2 {
		t.Fatal("returning to a family did not restore its exact unresolved dock")
	}
}

func TestBackgroundModalQueueMigratesLosslesslyToViewedFamily(t *testing.T) {
	app := newPromptFamilyApp(t)
	app.switchToAgentView("beta")
	emitTestPermission(app, "child-a", "a")
	first := app.agentPrompt
	emitTestPermission(app, "child-b", "b")
	emitTestPermission(app, "nested", "nested")
	if app.agentPrompt != first || len(app.agentPrompts) != 3 || first.request.agentID != "child-a" {
		t.Fatal("concurrent background requests overwrote the active FIFO modal")
	}
	app.resolveAgentConfirmationChoice(2)
	if len(app.agentPrompts) != 2 || app.agentPrompt.request.agentID != "child-b" {
		t.Fatal("resolving a modal did not advance to the next exact owner")
	}
	app.switchToAgentView("alpha")
	app.paintShellFrame()
	if app.agentPrompt != nil || app.overlayHost.IsOpen(ui.OverlayAgentPrompt) || app.activeSessionDeck().deck.Count() != 2 {
		t.Fatal("returning to the owner family lost requests or left a stale modal")
	}
	for _, state := range app.agentPrompts {
		if !state.inline {
			t.Fatal("queued family request did not migrate into its retained tab")
		}
	}
}

func TestTerminatedOwnerRemovesOwnSubtreeRequestsAndAdvancesOtherFamilies(t *testing.T) {
	app := newPromptFamilyApp(t)
	app.switchToAgentView("beta")
	emitTestPermission(app, "child-a", "a")
	emitTestPermission(app, "nested", "nested")
	emitTestPermission(app, "child-b", "b")
	app.cleanupAgent("child-a")
	app.openNextAgentPrompt()
	if len(app.agentPrompts) != 1 || app.agentPrompt == nil || app.agentPrompt.request.agentID != "child-b" {
		t.Fatal("terminating an owner retained its own/nested modal or dropped a sibling")
	}
	app.cleanupAgent("alpha")
	app.openNextAgentPrompt()
	if len(app.agentPrompts) != 0 || app.agentPrompt != nil || app.overlayHost.IsOpen(ui.OverlayAgentPrompt) {
		t.Fatal("terminating a family retained descendant prompts")
	}
	if app.ws.AgentMgr().Get("beta") == nil {
		t.Fatal("terminating one family removed another root")
	}
}

func TestOwnerCompletionAndLateResponsesCannotRemoveNewRequests(t *testing.T) {
	app := newPromptFamilyApp(t)
	emitTestPermission(app, "child-a", "old")
	old := app.agentPrompts[0].confirmation
	app.resolvePromptConfirmation(old, "deny")
	emitTestPermission(app, "child-a", "new")
	current := app.agentPrompts[0]
	app.handleAppMessages(confirmation.ResultMsg{Target: old, Choice: "allow"})
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "child-a", Type: "blocking_complete", Data: map[string]string{"block_id": "old"}})
	if len(app.agentPrompts) != 1 || app.agentPrompts[0] != current || app.ws.AgentMgr().Get("child-a").State.PendingBlockID != "new" {
		t.Fatal("late result/completion discarded the newer pending request")
	}
	app.handleSubAgentCompleted(workspace.AgentEvent{Type: "sub_agent_completed", Data: map[string]string{"agent_id": "child-a", "status": "cancelled"}})
	if len(app.agentPrompts) != 0 || current.projection.deck.IsOpen() {
		t.Fatal("owner-side cancellation retained the request dock")
	}
}

func TestSubAgentConfirmationDocksInParentAndKeepsChildOwner(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 30
	app.AddOrchestrator("root")
	session := &workspace.Session{SessionID: "root-session"}
	app.ActiveSession = session
	wsOf(app).SessionsByAgent = map[string]*workspace.Session{"root": session}
	pane := app.ws.PaneMgr().GetOrCreate("child", workspace.PaneAgent, "child")
	childState := workspace.NewAgentState("child", pane.Messages, app.ws.Logger())
	app.ws.AgentMgr().AddSubAgent("child", "root", "Child", "model", "researcher", childState)
	app.switchToAgentView("root")
	app.paintShellFrame()

	app.enqueueAgentConfirmation(agentPromptRequest{kind: agentPromptConfirmation, agentID: "child", blockID: "child-block", question: "Allow child write?"})
	projection := app.activeSessionProjection()
	if projection == nil || !projection.deck.IsOpen() || projection.deck.Active().OwnerID != "child" {
		t.Fatal("child request did not dock in its parent's family with exact child ownership")
	}
	if app.overlayHost.IsOpen(ui.OverlayAgentPrompt) {
		t.Fatal("same-family child request opened a modal")
	}
}

func TestActiveAgentConfirmationUsesModalOverEditorAndPreservesDraft(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 30
	app.AddOrchestrator("root")
	session := &workspace.Session{SessionID: "root-session"}
	app.ActiveSession = session
	wsOf(app).SessionsByAgent = map[string]*workspace.Session{"root": session}
	app.switchToAgentView("root")
	app.Input.SetInput("unfinished user prompt")
	openTestEditor(t, app)
	app.paintShellFrame()

	app.enqueueAgentConfirmation(agentPromptRequest{kind: agentPromptConfirmation, agentID: "root", blockID: "editor-block", question: "Allow write?"})
	if app.agentPrompt == nil || app.agentPrompt.inline || !app.overlayHost.IsOpen(ui.OverlayAgentPrompt) {
		t.Fatalf("editor-owned confirmation did not use modal routing: %+v", app.agentPrompt)
	}
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "root", Type: "blocking_complete"})
	if got := app.Input.Value(); got != "unfinished user prompt" {
		t.Fatalf("modal confirmation completion discarded composer draft: %q", got)
	}
}

func TestOtherSessionConfirmationUsesModalWithoutChangingActiveSession(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 30
	app.AddOrchestrator("first")
	app.AddOrchestrator("second")
	first := &workspace.Session{SessionID: "first-session"}
	second := &workspace.Session{SessionID: "second-session"}
	wsOf(app).SessionsByAgent = map[string]*workspace.Session{"first": first, "second": second}
	app.ActiveSession = second
	app.switchToAgentView("second")
	app.paintShellFrame()

	app.enqueueAgentConfirmation(agentPromptRequest{kind: agentPromptConfirmation, agentID: "first", blockID: "other-session", question: "Allow write?"})
	if app.agentPrompt == nil || app.agentPrompt.inline || !app.overlayHost.IsOpen(ui.OverlayAgentPrompt) {
		t.Fatalf("other-session confirmation did not use modal routing: %+v", app.agentPrompt)
	}
	if app.ActiveSession != second || app.AgentView.ActiveID() != "second" {
		t.Fatal("background confirmation stole the active session")
	}
}
