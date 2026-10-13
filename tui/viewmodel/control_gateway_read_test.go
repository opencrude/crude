package viewmodel

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"crude/coreprotocol"
	"crude/provider"
	"crude/shellport"
	"crude/tui/screens/memorybrowser"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	"crude/types/control"
)

// Read effects intentionally have no approval preimage. The second generic
// resolution must not mistake that absence for a changed authorization.
func TestControlGatewayReadNativeRootAndExternalReadOnlyScope(t *testing.T) {
	var calls atomic.Int32
	requests := make(chan provider.ChatRequest, 8)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		requests <- request
		step := calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		params := map[string]any{"command": "shell.describe_control", "arguments": shellControlIntent{ComponentID: "memorybrowser/actions/retry", Operation: ui.ControlOperation{Verb: "activate"}}}
		if step == 2 {
			var described struct {
				Descriptor control.Descriptor `json:"descriptor"`
				Node       control.Node       `json:"node"`
			}
			found := false
			for _, message := range request.Messages {
				if message.Role != "tool" {
					continue
				}
				var result control.Result
				if json.Unmarshal([]byte(message.ContentString()), &result) == nil && json.Unmarshal(result.Output, &described) == nil && described.Descriptor.ID == "memorybrowser.retry" {
					found = true
					break
				}
			}
			if !found || described.Descriptor.Effect != control.Read {
				t.Error("native description did not return the exact Memory read action")
			} else {
				params = map[string]any{"command": "shell.control", "arguments": shellControlIntent{ComponentID: described.Node.ID, Operation: ui.ControlOperation{Verb: "activate"}, ExpectedAction: described.Descriptor.ID}, "target": map[string]any{"resource_id": described.Node.ID}, "preconditions": control.Preconditions{Revision: &described.Node.Revision, OpenEpoch: &described.Node.OpenEpoch}}
			}
		}
		if step <= 2 {
			raw, _ := json.Marshal(params)
			wire, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprintf("memory-read-%d", step), "type": "function", "function": map[string]any{"name": "crude_control", "arguments": string(raw)}}}}, "finish_reason": "tool_calls"}}})
			_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", wire)
		} else {
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Memory refreshed in the submitting shell.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}
	}))
	defer endpoint.Close()
	runtime := remoteJourneyRuntime(t, endpoint.URL)
	root := t.TempDir()
	one := newRemoteJourneyShell(t, runtime, root)
	id := remoteJourneyCreate(t, one)
	peer := newRemoteJourneyShell(t, runtime, root)
	shells := []remoteJourneyShell{one, peer}
	remoteJourneyUntil(t, shells, func() bool {
		return peer.app.ws.AgentMgr().Get(id) != nil && one.app.composerHydratedFor == one.app.ActiveSession && !one.app.draftOwner(one.app.ActiveSession).loading
	})
	one.app.ws.(shellport.ShellControl).StartShellControl()
	peer.app.ws.(shellport.ShellControl).StartShellControl()
	_, cmd := one.app.toggleMemoryBrowser()
	runAppCommand(t, one.app, cmd)
	peer.app.Input.SetInput("peer private work")
	one.app.View()
	if one.app.ws.YoloForAgent(id) {
		t.Fatal("read regression must not use YOLO approval")
	}
	before := one.app.memoryBrowser.ControlEditGeneration()
	one.app.Input.SetInput("refresh the open Memory browser")
	cmd = one.app.submitInput(id, one.app.Input.Value(), nil)
	if cmd == nil {
		t.Fatal("real composer did not admit input")
	}
	runAppCommand(t, one.app, cmd)
	remoteJourneyUntil(t, shells, func() bool { return calls.Load() >= 3 })
	first, second, third := <-requests, <-requests, <-requests
	advertised := false
	for _, definition := range first.Tools {
		advertised = advertised || definition.Function.Name == "crude_control"
	}
	if !advertised {
		t.Fatal("native control tool missing from real root request")
	}
	if len(second.Messages) == 0 {
		t.Fatal("description result did not reach model")
	}
	applied := false
	for _, message := range third.Messages {
		var result control.Result
		if message.Role == "tool" && json.Unmarshal([]byte(message.ContentString()), &result) == nil && result.State == control.Applied && strings.Contains(message.ToolCallID, "memory-read-2") {
			applied = true
		}
	}
	if !applied || one.app.memoryBrowser.ControlEditGeneration() <= before || one.app.memoryBrowser.SearchError() != nil {
		t.Fatal("unapproved generic Read did not complete through the actual core gateway", applied)
	}
	if !one.app.memoryBrowserVisible() || peer.app.memoryBrowserVisible() || peer.app.Input.Value() != "peer private work" {
		t.Fatal("read retargeted the shell or consumed peer work")
	}
	if one.app.termDock.ActivePanel() != termdock.PanelMemory {
		t.Fatal("read changed original panel")
	}

	external, err := runtime.Connect("read-only-controller")
	if err != nil {
		t.Fatal(err)
	}
	defer external.Close()
	target := one.app.ws.(shellport.ShellControl).ControlIdentity()
	call := func(name, method string, value any) coreprotocol.Response {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return external.Call(t.Context(), coreprotocol.Request{ID: name, Owner: external.Owner, WorkspaceID: target.WorkspaceID, Method: method, Payload: raw})
	}
	if out := call("attach-read", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}); out.Error != nil {
		t.Fatal(out.Error)
	}
	if out := call("grant-read", coreprotocol.ControlClientRegister, coreprotocol.ControlClientGrant{Effects: []control.Effect{control.Read}, Shell: target.Shell}); out.Error != nil {
		t.Fatal(out.Error)
	}
	execute := func(r control.Request) coreprotocol.Response {
		done := make(chan coreprotocol.Response, 1)
		go func() { done <- call(r.ID, coreprotocol.ControlExecute, r) }()
		var out coreprotocol.Response
		finished := false
		remoteJourneyUntil(t, shells, func() bool {
			select {
			case out = <-done:
				finished = true
			default:
			}
			return finished
		})
		return out
	}
	caller := control.Caller{Kind: control.ExternalCaller, ClientID: external.Owner.ClientID}
	readIntent := shellControlIntent{ComponentID: string(memorybrowser.ActionsID) + "/retry", Operation: ui.ControlOperation{Verb: "activate"}}
	readInput, _ := json.Marshal(readIntent)
	readResponse := execute(control.Request{ID: "external-describe-read", CommandID: "shell.describe_control", Target: target, Caller: caller, Input: readInput})
	var readResult control.Result
	var readDescription struct {
		Descriptor control.Descriptor `json:"descriptor"`
		Node       control.Node       `json:"node"`
	}
	if readResponse.Error != nil || json.Unmarshal(readResponse.Payload, &readResult) != nil || readResult.State != control.Applied || json.Unmarshal(readResult.Output, &readDescription) != nil || readDescription.Descriptor.Effect != control.Read {
		t.Fatal("external read-only scope cannot describe exact read", readResponse)
	}
	readIntent.ExpectedAction = readDescription.Descriptor.ID
	readInput, _ = json.Marshal(readIntent)
	readTarget := target.Clone()
	readTarget.ResourceID = readDescription.Node.ID
	readResponse = execute(control.Request{ID: "external-generic-read", CommandID: "shell.control", Target: readTarget, Caller: caller, Input: readInput, Preconditions: control.Preconditions{Revision: &readDescription.Node.Revision, OpenEpoch: &readDescription.Node.OpenEpoch}})
	if readResponse.Error != nil || json.Unmarshal(readResponse.Payload, &readResult) != nil || readResult.State != control.Applied {
		t.Fatal("external read-only scope cannot invoke exact read", readResponse)
	}
	intent := shellControlIntent{ComponentID: string(memorybrowser.ActionsID) + "/hide", Operation: ui.ControlOperation{Verb: "activate"}}
	d, node, err := one.app.describeShellControl(intent, target)
	if err != nil || d.Effect != control.Write {
		t.Fatalf("write fixture: %+v %v", d, err)
	}
	intent.ExpectedAction = d.ID
	input, _ := json.Marshal(intent)
	writeTarget := target.Clone()
	writeTarget.ResourceID = node.ID
	out := execute(control.Request{ID: "deny-generic-write", CommandID: "shell.control", Target: writeTarget, Caller: caller, Input: input, Preconditions: control.Preconditions{Revision: &node.Revision, OpenEpoch: &node.OpenEpoch}})
	var denied control.Result
	if out.Error != nil || json.Unmarshal(out.Payload, &denied) != nil || denied.Fault == nil || denied.Fault.Code != control.Denied {
		t.Fatal("external read scope invoked resolved Write", out)
	}
	path := filepath.Join(root, "preserved.txt")
	if err := os.WriteFile(path, []byte("keep exact bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	deleteTarget := target.Clone()
	deleteTarget.Shell = nil
	out = execute(control.Request{ID: "deny-delete", CommandID: "files.delete", Target: deleteTarget, Caller: caller, Input: json.RawMessage(`{"path":"preserved.txt"}`)})
	if out.Error != nil || json.Unmarshal(out.Payload, &denied) != nil || denied.Fault == nil || denied.Fault.Code != control.Denied {
		t.Fatal("external read scope invoked Delete", out)
	}
	bytes, err := os.ReadFile(path)
	if err != nil || string(bytes) != "keep exact bytes" || !one.app.memoryBrowserVisible() || peer.app.Input.Value() != "peer private work" {
		t.Fatal("denied effects changed actual work", err)
	}
}
