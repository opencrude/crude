package viewmodel

import (
	"context"
	"crude/core"
	"crude/coreprotocol"
	"crude/internal/wiretest"
	"crude/shellport"
	"crude/tui/screens/question"
	"crude/tui/screens/sessionsettings"
	"crude/tui/ui"
	"crude/types/control"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func invokeDeckControl(t *testing.T, one remoteJourneyShell, suffix string, op ui.ControlOperation) (shellControlIntent, control.Descriptor, control.Node) {
	t.Helper()
	one.app.View()
	target := one.app.ws.(shellport.ShellControl).ControlIdentity()
	cursor := ""
	for {
		page, err := one.app.inspectShellControls(target, control.PageRequest{Limit: 128, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range page.Nodes {
			if strings.HasSuffix(n.ID, "::"+suffix) {
				in := shellControlIntent{ComponentID: n.ID, Operation: op}
				d, node, err := one.app.describeShellControl(in, target)
				if err != nil {
					t.Fatal(err)
				}
				in.ExpectedAction = d.ID
				return in, d, node
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	t.Fatal("missing deck control", suffix)
	return shellControlIntent{}, control.Descriptor{}, control.Node{}
}
func TestConnectedSessionDeckThinkingSaveReceipt(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	one := newRemoteJourneyShell(t, runtime, root)
	id := remoteJourneyCreate(t, one)
	two := newRemoteJourneyShell(t, runtime, root)
	shells := []remoteJourneyShell{one, two}
	remoteJourneyUntil(t, shells, func() bool { return two.app.ws.AgentMgr().Get(id) != nil })
	one.app.Input.SetInput("owner draft")
	two.app.Input.SetInput("peer draft")
	// A distinct session on the peer does not authorize addressing this deck
	// with its session ID, even though both sessions belong to one workspace.
	otherID := remoteJourneyCreate(t, two)
	remoteJourneyUntil(t, shells, func() bool { return one.app.ws.AgentMgr().Get(otherID) != nil })
	foreignSession := two.app.ActiveSession.SessionID
	two.app.Input.SetInput("peer draft")
	requestShellForTest(t, runtime, shells, one, "settings.open", map[string]any{"scope": "session"}, nil)
	in, d, node := invokeDeckControl(t, one, "session-settings/form/default", ui.ControlOperation{Verb: "activate"})
	negative := func(sessionID string, epoch uint64) control.Result {
		return requestShellForTest(t, runtime, shells, one, "shell.control", in, func(e *coreprotocol.ControlEnvelope) {
			e.Descriptor = d
			e.Descriptor.ID = "shell.control"
			e.Request.Target.ResourceID = in.ComponentID
			e.Request.Target.SessionID = sessionID
			e.Request.Preconditions = control.Preconditions{Revision: &node.Revision, OpenEpoch: &epoch}
		})
	}
	if r := negative(foreignSession, node.OpenEpoch); r.State != control.Failed || r.Fault == nil || r.Fault.Code != control.Denied {
		t.Fatalf("foreign session deck accepted: %+v", r)
	}
	if r := negative(node.Target.SessionID, node.OpenEpoch+1); r.State != control.Failed || r.Fault == nil || r.Fault.Code != control.Stale {
		t.Fatalf("stale deck accepted: %+v", r)
	}
	invoke := func(suffix string, op ui.ControlOperation) control.Result {
		in, d, node := invokeDeckControl(t, one, suffix, op)
		return requestShellForTest(t, runtime, shells, one, "shell.control", in, func(e *coreprotocol.ControlEnvelope) {
			e.Descriptor = d
			e.Descriptor.ID = "shell.control"
			e.Request.Target.ResourceID = in.ComponentID
			e.Request.Target.SessionID = node.Target.SessionID
			e.Request.Preconditions = control.Preconditions{Revision: &node.Revision, OpenEpoch: &node.OpenEpoch}
		})
	}
	if r := invoke("session-settings/form/default", ui.ControlOperation{Verb: "activate"}); r.State != control.Applied {
		t.Fatal(r)
	}
	if r := invoke("session-settings/form/thinking-effort", ui.ControlOperation{Verb: "select", ItemID: "high"}); r.State != control.Applied {
		t.Fatal(r)
	}
	if r := invoke("session-settings/actions/apply", ui.ControlOperation{Verb: "activate"}); r.State != control.Applied {
		t.Fatal(r)
	}
	if r := invoke("session-settings/actions/save", ui.ControlOperation{Verb: "activate"}); r.State != control.Applied || r.Revision == nil || *r.Revision == 0 {
		t.Fatalf("save is not a revision receipt: %+v", r)
	}
	if one.app.activeSessionDeck() != nil && one.app.activeSessionDeck().deck.IsOpen() {
		t.Fatal("acknowledged save did not close")
	}
	one.app.openSessionSettings()
	s := one.app.sessionSettings().ToSettings()
	if s.ThinkingEffort != "high" {
		t.Fatalf("receipt not persisted: %s", s.ThinkingEffort)
	}
	if one.app.Input.Value() != "owner draft" || two.app.Input.Value() != "peer draft" {
		t.Fatal("save consumed a composer")
	}
	// The contextual form's delegation switch cannot become an ordinary write.
	in, d, _ = invokeDeckControl(t, one, string(ui.NewID(sessionsettings.FormID, "multi-agent")), ui.ControlOperation{Verb: "set_value", Value: []byte(`"on"`)})
	if in.ComponentID == "" || !d.HumanOnly || d.Effect != control.Authority {
		t.Fatal("delegation authority not preserved")
	}
	// Reset can remove delegation/permission choices, so its generic agent
	// control remains human-owned. Drive the native human action for this check.
	reset := one.app.sessionSettings().HandleAction(ui.EventContext{}, ui.Action{ID: "session-settings.reset"})
	_, cmd := one.app.Update(reset.Cmd())
	runAppCommand(t, one.app, cmd)
	one.app.openSessionSettings()
	if got := one.app.sessionSettings().ToSettings(); len(got.SessionOverrides) != 0 || len(got.SessionEdits) != 0 || got.SessionMultiAgent != nil {
		t.Fatalf("Reset repinned the effective view or reopened a dirty draft: %+v", got.SessionOverrides)
	}
	if one.app.Input.Value() != "owner draft" || two.app.Input.Value() != "peer draft" {
		t.Fatal("reset consumed a composer")
	}
}

func admitDeckController(t *testing.T, runtime *core.Runtime, root string) {
	t.Helper()
	c, err := runtime.Connect("test-controller")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	call := func(id, ws, method string, input any) coreprotocol.Response {
		raw, err := wiretest.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		r := <-c.Dispatch(t.Context(), coreprotocol.Request{ID: id, WorkspaceID: ws, Method: method, Owner: c.Owner, Payload: raw})
		if r.Error != nil {
			t.Fatal(r.Error)
		}
		return r
	}
	r := call("attach-controller", "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root})
	var attached coreprotocol.AttachResult
	if err := coreprotocol.Decode(r.Payload, &attached); err != nil {
		t.Fatal(err)
	}
	call("grant-controller", attached.WorkspaceID, coreprotocol.ControlClientRegister, coreprotocol.ControlClientGrant{Effects: []control.Effect{control.Write, control.Read}})
}

func TestConnectedQuestionControlReceiptAndFailedAckRetainsDraft(t *testing.T) {
	evidence := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Messages []struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		answered := false
		for _, m := range input.Messages {
			answered = answered || m.Role == "tool"
			if m.Role == "tool" {
				raw, _ := json.Marshal(m.Content)
				evidence <- string(raw)
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if !answered {
			args := `{"questions":[{"header":"Choice","question":"Choose fixture","options":["Yes","No"]}]}`
			payload, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "fixture-question", "type": "function", "function": map[string]any{"name": "question", "arguments": args}}}}, "finish_reason": "tool_calls"}}})
			_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", payload)
		} else {
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"QUESTION RECEIPT ACCEPTED\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}
	}))
	defer server.Close()
	runtime := remoteJourneyRuntime(t, server.URL)
	root := t.TempDir()
	one := newRemoteJourneyShell(t, runtime, root)
	id := remoteJourneyCreate(t, one)
	two := newRemoteJourneyShell(t, runtime, root)
	shells := []remoteJourneyShell{one, two}
	admitDeckController(t, runtime, root)
	remoteJourneyUntil(t, shells, func() bool { return two.app.ws.AgentMgr().Get(id) != nil })
	runAppCommand(t, one.app, one.app.submitInput(id, "Ask fixture", nil))
	remoteJourneyUntil(t, shells, func() bool { return len(one.app.agentPrompts) == 1 && len(two.app.agentPrompts) == 1 })
	one.app.Input.SetInput("preserved composer")
	two.app.Input.SetInput("peer composer")
	invoke := func(suffix string, op ui.ControlOperation) control.Result {
		in, d, node := invokeDeckControl(t, one, suffix, op)
		return requestShellForTest(t, runtime, shells, one, "shell.control", in, func(e *coreprotocol.ControlEnvelope) {
			e.Descriptor = d
			e.Descriptor.ID = "shell.control"
			e.Request.Target.ResourceID = in.ComponentID
			e.Request.Target.SessionID = node.Target.SessionID
			e.Request.Preconditions = control.Preconditions{Revision: &node.Revision, OpenEpoch: &node.OpenEpoch}
		})
	}
	// Typed RPC without its admitted shell receipt must not consume the prompt.
	state := one.app.agentPrompts[0]
	one.app.applyQuestionReply(questionReplyMsg{target: state.question, workspace: one.app.ws, generation: one.app.workspaceSwitchGen.Load(), agentID: id, blockID: state.request.blockID, receipt: coreprotocol.QuestionControlResult{Accepted: false, BlockID: state.request.blockID}})
	if one.app.promptForScreen(state.question) == nil || !one.app.sessionDeckBlocking() {
		t.Fatal("failed acknowledgment consumed question prompt")
	}
	port := one.app.ws.(shellport.QuestionControl)
	_, err := port.ResolveQuestionControl(context.Background(), coreprotocol.QuestionControlRequest{ControlRequestID: "forged", SessionID: one.app.ActiveSession.SessionID, AgentID: id, BlockID: state.request.blockID, Answers: map[string]string{"1": "No"}})
	if err == nil || len(one.app.agentPrompts) != 1 {
		t.Fatal("unbound question input accepted")
	}
	if r := invoke(string(question.InputID), ui.ControlOperation{Verb: "set_value", Value: json.RawMessage(`"Agent-authored fixture answer"`)}); r.State != control.Applied {
		t.Fatalf("write-in: %+v", r)
	}
	if r := invoke(string(ui.NewID(question.ActionsID, "next")), ui.ControlOperation{Verb: "activate"}); r.State != control.Applied {
		t.Fatalf("question falsely settled: %+v", r)
	}
	if len(one.app.agentPrompts) != 1 || !state.question.Reviewing() || len(two.app.agentPrompts) != 1 {
		t.Fatal("final answer disappeared without explicit review submission")
	}
	if r := invoke(string(ui.NewID(question.ActionsID, "submit")), ui.ControlOperation{Verb: "activate"}); r.State != control.Applied {
		t.Fatalf("review submission: %+v", r)
	}
	remoteJourneyUntil(t, shells, func() bool {
		return len(one.app.agentPrompts) == 0 && len(two.app.agentPrompts) == 0 && strings.Contains(one.app.View().RenderContent(), "QUESTION RECEIPT ACCEPTED")
	})
	if one.app.Input.Value() != "preserved composer" || two.app.Input.Value() != "peer composer" {
		t.Fatal("question consumed composer")
	}
	select {
	case text := <-evidence:
		if !strings.Contains(text, "external") || !strings.Contains(text, "test-controller") {
			t.Fatalf("control answer fabricated human attribution: %s", text)
		}
	default:
		t.Fatal("provider did not receive tool evidence")
	}
}
