package viewmodel

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/coreprotocol"
	"crude/provider"
	"crude/shellport"
	"crude/types/control"
	"github.com/charmbracelet/x/ansi"
)

func TestProductComposerExactOwnDraftStaging(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	one := newRemoteJourneyShell(t, runtime, root)
	id := remoteJourneyCreate(t, one)
	two := newRemoteJourneyShell(t, runtime, root)
	shells := []remoteJourneyShell{one, two}
	remoteJourneyUntil(t, shells, func() bool { return two.app.ws.AgentMgr().Get(id) != nil })
	m := one.app
	remoteJourneyUntil(t, shells, func() bool { return m.composerHydratedFor == m.ActiveSession && !m.draftOwner(m.ActiveSession).loading })
	session := m.ActiveSession.SessionID
	two.app.Input.SetInput("neighbor's draft")
	result := productRequest(t, m, "composer.set", id, session, map[string]any{"text": "staged own draft"}, nil)
	if result.State != control.Applied || m.Input.Value() != "staged own draft" || two.app.Input.Value() != "neighbor's draft" {
		t.Fatal("wrong draft owner", result)
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "staged own draft") {
		t.Fatal("draft not painted")
	}
	revision := m.Input.DraftRevision()
	m.Input.SetCursorFlat(1)
	result = productRequest(t, m, "composer.set", id, session, map[string]any{"text": "overwrite"}, func(r *control.Request) { r.Preconditions.Revision = &revision })
	if result.State != control.Failed || result.Fault.Code != control.Stale || m.Input.Value() != "staged own draft" {
		t.Fatal("new cursor state overwritten", result)
	}
	result = productRequest(t, m, "composer.inspect", id, session, struct{}{}, func(r *control.Request) {
		r.Caller = control.Caller{Kind: control.AgentCaller, AgentID: "foreign-agent", RunID: "run"}
	})
	if result.State != control.Failed || len(result.Output) != 0 || result.Fault.Code != control.Denied {
		t.Fatal("foreign draft leaked", result)
	}
	result = productRequest(t, m, "composer.inspect", id, session, struct{}{}, nil)
	if result.State != control.Applied || !strings.Contains(string(result.Output), "staged own draft") {
		t.Fatal("explicit own inspection unavailable", result)
	}
	result = productRequest(t, m, "composer.reference", id, session, map[string]any{"id": "foreign-reference"}, nil)
	if result.State != control.Failed || m.Input.Value() != "staged own draft" {
		t.Fatal("foreign reference inserted", result)
	}
	result = productRequest(t, m, "composer.send", id, session, struct{}{}, nil)
	if result.State != control.Failed || result.Fault.Code != control.OperationFailed || m.Input.Value() != "staged own draft" {
		t.Fatal("unadmitted send bypassed core admission", result)
	}
	m.Input, _, _ = m.Input.Update(tea.PasteMsg{Content: strings.Repeat("x", 4096)}, false, false)
	result = productRequest(t, m, "composer.send", id, session, struct{}{}, nil)
	if result.State != control.Failed || result.Fault.Code != control.Unavailable || m.Input.Value() != "staged own draft" || !m.Input.HasPendingPastes() {
		t.Fatal("pending attachment barrier bypassed", result)
	}
}

func TestConnectedExternalComposerSendReachesProviderWithOriginalProvenance(t *testing.T) {
	var calls atomic.Int32
	requests := make(chan provider.ChatRequest, 4)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
			return
		}
		requests <- in
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Control input received.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer endpoint.Close()
	runtime := remoteJourneyRuntime(t, endpoint.URL)
	root := t.TempDir()
	one := newRemoteJourneyShell(t, runtime, root)
	id := remoteJourneyCreate(t, one)
	two := newRemoteJourneyShell(t, runtime, root)
	shells := []remoteJourneyShell{one, two}
	remoteJourneyUntil(t, shells, func() bool {
		return two.app.ws.AgentMgr().Get(id) != nil && one.app.composerHydratedFor == one.app.ActiveSession && !one.app.draftOwner(one.app.ActiveSession).loading
	})
	one.app.Input.SetInput("externally submitted captured draft")
	two.app.Input.SetInput("peer private draft")
	target := one.app.ws.(shellport.ShellControl).ControlIdentity()
	target.SessionID, target.ResourceID = one.app.ActiveSession.SessionID, id
	external, err := runtime.Connect("composer-external")
	if err != nil {
		t.Fatal(err)
	}
	defer external.Close()
	call := func(name, method string, value any) coreprotocol.Response {
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return external.Call(t.Context(), coreprotocol.Request{ID: name, Owner: external.Owner, WorkspaceID: target.WorkspaceID, Method: method, Payload: payload})
	}
	if out := call("attach", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}); out.Error != nil {
		t.Fatal(out.Error)
	}
	requestShellForTest(t, runtime, shells, one, "shell.inspect", struct{}{}, nil)
	if out := call("grant", coreprotocol.ControlClientRegister, coreprotocol.ControlClientGrant{Effects: []control.Effect{control.Communication}, Shell: target.Shell}); out.Error != nil {
		t.Fatal(out.Error)
	}
	revision, epoch := one.app.Input.DraftRevision(), one.app.Input.DraftEpoch()
	r := control.Request{ID: "original-composer-send", CommandID: "composer.send", Target: target, Caller: control.Caller{Kind: control.ExternalCaller, ClientID: external.Owner.ClientID}, Input: json.RawMessage(`{}`), Preconditions: control.Preconditions{Revision: &revision, OpenEpoch: &epoch}}
	done := make(chan coreprotocol.Response, 1)
	go func() { done <- call(r.ID, coreprotocol.ControlExecute, r) }()
	var response coreprotocol.Response
	finished := false
	remoteJourneyUntil(t, shells, func() bool {
		select {
		case response = <-done:
			finished = true
		default:
		}
		return finished
	})
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	var result control.Result
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.State != control.Committed || one.app.Input.Value() != "" || two.app.Input.Value() != "peer private draft" {
		t.Fatal("receipt or draft owner incorrect", result)
	}
	remoteJourneyUntil(t, shells, func() bool { return calls.Load() == 1 })
	request := <-requests
	text, source := false, false
	for _, message := range request.Messages {
		text = text || strings.Contains(message.ContentString(), "externally submitted captured draft")
		source = source || strings.Contains(message.ContentString(), "original-composer-send") && strings.Contains(message.ContentString(), "composer-external") && strings.Contains(message.ContentString(), "control_input")
	}
	if !text || !source {
		t.Fatal("real provider request lost control provenance", request.Messages)
	}
	remoteJourneyUntil(t, shells, func() bool {
		for _, message := range one.app.ws.AgentMgr().Get(id).State.Messages.Messages() {
			if message.Content == "externally submitted captured draft" {
				if message.Origin != provider.OriginRuntimeContext {
					t.Fatal("external input became human", message.Origin)
				}
				return true
			}
		}
		return false
	})
	// A repeated original gateway call observes its receipt, not a fresh turn.
	replay := call(r.ID, coreprotocol.ControlExecute, r)
	if replay.Error != nil || calls.Load() != 1 {
		t.Fatal("original control replayed", replay.Error, calls.Load())
	}
}

func TestControlComposerInputRetainsNonhumanProvenance(t *testing.T) {
	for _, caller := range []control.Caller{
		{Kind: control.ExternalCaller, ClientID: "external"},
		{Kind: control.AgentCaller, AgentID: "agent", RunID: "original-run"},
	} {
		refs := []string{"owned-reference"}
		value := provider.InputFromControl("captured draft", refs, provider.ControlInputProvenance{ControlRequestID: "original-control", Caller: caller})
		refs[0] = "changed"
		if value.EffectiveOrigin() != provider.OriginRuntimeContext || value.ReferenceIDs[0] != "owned-reference" || len(value.ContextParts) != 1 {
			t.Fatal("lost immutable nonhuman input", value)
		}
		var evidence struct {
			ControlInput provider.ControlInputProvenance `json:"control_input"`
		}
		if err := json.Unmarshal([]byte(value.ContextParts[0]), &evidence); err != nil {
			t.Fatal(err)
		}
		if evidence.ControlInput.ControlRequestID != "original-control" || evidence.ControlInput.Caller != caller {
			t.Fatal("control attributed to wrong source", evidence)
		}
	}
}
