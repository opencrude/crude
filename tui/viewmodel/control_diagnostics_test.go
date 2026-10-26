package viewmodel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/internal/wiretest"
	"crude/metrics"
	"crude/provider"
	"crude/shellport"
	"crude/tui/screens/perfdash"
	"crude/types/control"
	"crude/types/inspect"
)

func diagnosticModelResult(t *testing.T, request provider.ChatRequest, id string) control.Result {
	t.Helper()
	for _, message := range request.Messages {
		if message.Role == "tool" && message.ToolCallID == id {
			var result control.Result
			if err := json.Unmarshal([]byte(message.ContentString()), &result); err != nil {
				t.Fatalf("tool %s not readable JSON: %q (%v)", id, message.ContentString(), err)
			}
			return result
		}
	}
	t.Fatalf("actual model request missing result for %s", id)
	return control.Result{}
}

func TestDiagnosticShellLateCompletionRejectsSwitchAndShutdown(t *testing.T) {
	for _, boundary := range []string{"workspace_switch", "shutdown"} {
		t.Run(boundary, func(t *testing.T) {
			runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
			shell := newRemoteJourneyShell(t, runtime, t.TempDir())
			remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return len(shell.fifo) == 0 })
			app := shell.app
			app.Input.SetInput("preserve diagnostic lifecycle draft")
			revision := app.Input.DraftRevision()
			descriptor, ok := control.Lookup("performance.shell")
			if !ok {
				t.Fatal("diagnostic command not discoverable")
			}
			replies := make(chan control.Result, 1)
			request := shellport.ControlRequestMsg{Owner: app.ws.(shellport.ShellControl), Envelope: coreprotocol.ControlEnvelope{
				Descriptor: descriptor,
				Request: control.Request{ID: "late-diagnostic-" + boundary, CommandID: descriptor.ID,
					Caller: control.Caller{Kind: control.ExternalCaller, ClientID: "lifecycle-reader"},
					Target: app.ws.(shellport.ShellControl).ControlIdentity(), Input: json.RawMessage(`{}`)},
			}, Reply: func(_ context.Context, result control.Result) error { replies <- result; return nil }}
			command, handled := app.handleDiagnosticControl(request)
			if !handled || command == nil || app.shellControlPending != 1 {
				t.Fatal("shell diagnostic did not acquire owned work")
			}
			// Collect via the real process owner and withhold only Update delivery.
			message, ok := command().(diagnosticControlDoneMsg)
			if !ok {
				t.Fatal("real diagnostic command did not produce owned completion")
			}
			assertDiagnosticProcess(t, diagnosticPage(t, message.result, inspect.DefaultBytes))
			if boundary == "workspace_switch" {
				app.workspaceSwitchGen.Add(1)
				app.cancelClientIOOperations()
			} else {
				app.StopAsyncIO()
			}
			runAppCommand(t, app, app.finishDiagnosticControl(message))
			select {
			case result := <-replies:
				if result.State != control.Failed || result.Fault == nil || result.Fault.Code != control.Stale || len(result.Output) != 0 || result.RequestID != request.Envelope.Request.ID {
					t.Fatal("late diagnostic applied to replacement lifetime", result)
				}
			default:
				t.Fatal("late diagnostic did not settle original request")
			}
			if app.shellControlPending != 0 || app.perf.screen != nil || app.perf.inFlight || app.Input.Value() != "preserve diagnostic lifecycle draft" || app.Input.DraftRevision() != revision {
				t.Fatal("late result leaked pending accounting, dashboard or draft mutation")
			}
		})
	}
}

func diagnosticPage(t *testing.T, result control.Result, budget int) inspect.Page {
	t.Helper()
	if result.Fault != nil || (result.State != control.Committed && result.State != control.Applied) || !json.Valid(result.Output) || len(result.Output) > budget {
		t.Fatalf("diagnostic result is not successful readable bounded JSON: %+v", result)
	}
	var page inspect.Page
	if err := json.Unmarshal(result.Output, &page); err != nil || page.SnapshotID == "" || page.CollectedAt.IsZero() || !page.ExpiresAt.After(page.CollectedAt) {
		t.Fatalf("missing observation lifetime: %s (%v)", result.Output, err)
	}
	return page
}

func assertDiagnosticProcess(t *testing.T, page inspect.Page) {
	t.Helper()
	var source struct {
		PID          int       `json:"pid"`
		Process      string    `json:"process"`
		Incarnation  string    `json:"incarnation"`
		HostIdentity string    `json:"host_identity"`
		Taken        time.Time `json:"taken"`
		Completed    time.Time `json:"completed"`
	}
	if err := json.Unmarshal(page.Source, &source); err != nil || source.PID != os.Getpid() || source.Incarnation == "" || source.HostIdentity == "" || source.Taken.IsZero() || source.Completed.Before(source.Taken) {
		t.Fatalf("missing actual same-process fixture provenance: %s (%v)", page.Source, err)
	}
}

func TestDiagnosticNativeRootDiscoveryPagingRunFenceAndExternalReadScope(t *testing.T) {
	// These are real observations in the process registry, not injected snapshots.
	filter := "diagnostic.journey." + strings.ReplaceAll(t.Name(), "/", ".")
	for i := range 29 {
		metrics.NewCounter(metrics.Meta{Name: fmt.Sprintf("%s.%02d", filter, i), Group: "diagnostic journey", Title: "Observed fixture work"}).Inc()
	}
	var calls atomic.Int32
	requests := make(chan provider.ChatRequest, 20)
	var oldCore, oldShell inspect.Request // accessed by the serial provider handler
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		requests <- request
		step := int(calls.Add(1))
		name := "crude_control"
		var params map[string]any
		series := map[string]any{"page": inspect.Request{View: "series", Filter: filter, Limit: 3, MaxBytes: 4096}}
		switch step {
		case 1:
			name, params = "crude_commands", map[string]any{"query": "performance"}
		case 2:
			params = map[string]any{"command": "performance.core", "arguments": map[string]any{}}
		case 3:
			params = map[string]any{"command": "performance.shell", "arguments": map[string]any{}}
		case 4:
			params = map[string]any{"command": "performance.core", "arguments": series}
		case 5, 7:
			var prior inspect.Page
			for _, message := range request.Messages {
				if message.Role == "tool" && message.ToolCallID == fmt.Sprintf("diagnostic-%d", step-1) {
					var result control.Result
					if err := json.Unmarshal([]byte(message.ContentString()), &result); err != nil {
						t.Error(err)
					} else if err := json.Unmarshal(result.Output, &prior); err != nil {
						t.Error(err)
					}
				}
			}
			if prior.Next == nil {
				t.Error("dense real registry did not produce continuation")
			} else if step == 5 {
				oldCore = *prior.Next
			} else {
				oldShell = *prior.Next
			}
			command, next := "performance.core", oldCore
			if step == 7 {
				command, next = "performance.shell", oldShell
			}
			params = map[string]any{"command": command, "arguments": map[string]any{"page": next}}
		case 6:
			params = map[string]any{"command": "performance.shell", "arguments": series}
		case 8:
			params = map[string]any{"command": "connection.inspect", "arguments": map[string]any{}}
		case 10:
			params = map[string]any{"command": "performance.core", "arguments": map[string]any{"page": oldCore}}
		case 11:
			params = map[string]any{"command": "performance.shell", "arguments": map[string]any{"page": oldShell}}
		case 12:
			params = map[string]any{"command": "performance.core", "arguments": series}
		default:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Diagnostic observations complete.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		raw, _ := json.Marshal(params)
		wire, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprintf("diagnostic-%d", step), "type": "function", "function": map[string]any{"name": name, "arguments": string(raw)}}}}, "finish_reason": "tool_calls"}}})
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", wire)
	}))
	defer endpoint.Close()
	runtime := remoteJourneyRuntime(t, endpoint.URL)
	root := t.TempDir()
	one := newRemoteJourneyShell(t, runtime, root)
	id := remoteJourneyCreate(t, one)
	peer := newRemoteJourneyShell(t, runtime, root)
	shells := []remoteJourneyShell{one, peer}
	remoteJourneyUntil(t, shells, func() bool {
		return peer.app.AgentView.ActiveID() == id && peer.app.ActiveSession != nil &&
			peer.app.composerHydratedFor == peer.app.ActiveSession && !peer.app.draftOwner(peer.app.ActiveSession).loading &&
			one.app.composerHydratedFor == one.app.ActiveSession && !one.app.draftOwner(one.app.ActiveSession).loading
	})
	for _, shell := range shells {
		shell.app.ws.(shellport.ShellControl).StartShellControl()
		shell.app.View()
	}
	peer.app.Input.SetInput("peer private diagnostic draft")
	peerRevision, peerCursor := peer.app.Input.DraftRevision(), peer.app.Input.CursorFlat()
	peerFocus, peerGroup, peerAgent := peer.app.rootHost.Focused(), peer.app.Win.ActiveGroup(), peer.app.AgentView.ActiveID()
	if one.app.ws.YoloForAgent(id) || one.app.ActiveSession.SessionID == "" {
		t.Fatal("journey requires a real session root and ordinary Read admission, not YOLO")
	}
	submit := func(text string, want int32) {
		t.Helper()
		one.app.Input.SetInput(text)
		cmd := one.app.submitInput(id, text, nil)
		if cmd == nil {
			t.Fatal("real native root composer did not admit diagnostic request")
		}
		runAppCommand(t, one.app, cmd)
		remoteJourneyUntil(t, shells, func() bool { return calls.Load() >= want && !one.app.ws.AgentActivity(id).Active })
	}
	submit("Inspect core and submitting shell without opening Performance.", 9)
	firstTurn := make([]provider.ChatRequest, 9)
	for i := range firstTurn {
		firstTurn[i] = <-requests
	}
	advertised := map[string]bool{}
	for _, tool := range firstTurn[0].Tools {
		advertised[tool.Function.Name] = true
	}
	if !advertised["crude_commands"] || !advertised["crude_control"] {
		t.Fatal("native root did not advertise actual semantic control tools")
	}
	found := map[string]bool{}
	for _, message := range firstTurn[1].Messages {
		if message.Role == "tool" && message.ToolCallID == "diagnostic-1" {
			var commands []control.Descriptor
			if err := json.Unmarshal([]byte(message.ContentString()), &commands); err != nil {
				t.Fatal(err)
			}
			for _, d := range commands {
				if d.Effect == control.Read && d.Availability.Available {
					found[d.ID] = true
				}
			}
		}
	}
	if !found["performance.core"] || !found["performance.shell"] {
		t.Fatal("native discovery did not return usable distinct sources", found)
	}
	for _, test := range []struct {
		index int
		role  string
	}{{2, "core"}, {3, "shell"}} {
		page := diagnosticPage(t, diagnosticModelResult(t, firstTurn[test.index], fmt.Sprintf("diagnostic-%d", test.index)), inspect.DefaultBytes)
		assertDiagnosticProcess(t, page)
		roleFound := false
		for _, record := range page.Records {
			if record.Kind == "verdict" {
				var verdict struct{ Role, Verdict string }
				if err := json.Unmarshal(record.Value, &verdict); err != nil || verdict.Role != test.role || verdict.Verdict == "" {
					t.Fatalf("wrong process role findings: %s (%v)", record.Value, err)
				}
				roleFound = true
			}
		}
		if !roleFound {
			t.Fatal("agent evidence omitted shared dashboard verdict")
		}
	}
	for _, start := range []int{4, 6} {
		first := diagnosticPage(t, diagnosticModelResult(t, firstTurn[start], fmt.Sprintf("diagnostic-%d", start)), 4096)
		next := diagnosticPage(t, diagnosticModelResult(t, firstTurn[start+1], fmt.Sprintf("diagnostic-%d", start+1)), 4096)
		if first.Total != 29 || first.Returned != 3 || first.Next == nil || next.SnapshotID != first.SnapshotID || next.Offset != 3 || !reflect.DeepEqual(first.Source, next.Source) {
			t.Fatalf("native real-registry continuation lost bounds or immutable evidence: %+v %+v", first, next)
		}
		for _, record := range append(first.Records, next.Records...) {
			if record.Kind != "counter" || !strings.HasPrefix(record.ID, filter) || !strings.Contains(string(record.Value), `"observed":true`) {
				t.Fatalf("native model did not receive observed fixture work: %+v", record)
			}
		}
	}
	connection := diagnosticModelResult(t, firstTurn[8], "diagnostic-8")
	var link struct {
		Shell   control.ShellOwner `json:"shell"`
		State   string             `json:"state"`
		Carrier string             `json:"carrier"`
	}
	bound := one.app.ws.(shellport.ShellControl).ControlIdentity()
	if connection.State != control.Applied || json.Unmarshal(connection.Output, &link) != nil || link.Shell != *bound.Shell || link.State != "connected" || link.Carrier != "local" {
		t.Fatal("native observation lost original shell connection", connection)
	}
	submit("Try continuing the previous run's pages, then collect fresh evidence.", 13)
	secondTurn := make([]provider.ChatRequest, 4)
	for i := range secondTurn {
		secondTurn[i] = <-requests
	}
	for _, step := range []int{10, 11} {
		out := diagnosticModelResult(t, secondTurn[step-9], fmt.Sprintf("diagnostic-%d", step))
		if out.State != control.Failed || out.Fault == nil || !strings.Contains(out.Fault.Message, "owner") {
			t.Fatalf("new native run continued previous run snapshot: %+v", out)
		}
	}
	fresh := diagnosticPage(t, diagnosticModelResult(t, secondTurn[3], "diagnostic-12"), 4096)
	if fresh.SnapshotID == oldCore.SnapshotID {
		t.Fatal("fresh run reused prior observation")
	}

	// External admission uses the same gateway but cannot widen its launch shell.
	external, err := runtime.Connect("diagnostic-external-read")
	if err != nil {
		t.Fatal(err)
	}
	defer external.Close()
	call := func(id, method string, value any) coreprotocol.Response {
		raw, err := wiretest.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return external.Call(t.Context(), coreprotocol.Request{ID: id, Owner: external.Owner, WorkspaceID: bound.WorkspaceID, Method: method, Payload: raw})
	}
	if out := call("attach-diagnostics", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}); out.Error != nil {
		t.Fatal(out.Error)
	}
	if out := call("grant-diagnostics", coreprotocol.ControlClientRegister, coreprotocol.ControlClientGrant{Effects: []control.Effect{control.Read}, Shell: bound.Shell}); out.Error != nil {
		t.Fatal(out.Error)
	}
	execute := func(id, command string, target control.Target, input any) control.Result {
		t.Helper()
		raw, _ := json.Marshal(input)
		done := make(chan coreprotocol.Response, 1)
		go func() {
			done <- call(id, coreprotocol.ControlExecute, control.Request{ID: id, CommandID: command, Target: target, Input: raw})
		}()
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
		var out control.Result
		if response.Error != nil || coreprotocol.Decode(response.Payload, &out) != nil {
			t.Fatal("external diagnostic gateway", response)
		}
		return out
	}
	for _, command := range []string{"performance.core", "performance.shell"} {
		page := diagnosticPage(t, execute("external-"+command, command, bound, map[string]any{}), inspect.DefaultBytes)
		assertDiagnosticProcess(t, page)
	}
	result := execute("external-link", "connection.inspect", bound, map[string]any{})
	if result.State != control.Applied || json.Unmarshal(result.Output, &link) != nil || link.Shell != *bound.Shell {
		t.Fatal("external shell target lost", result)
	}
	foreign := peer.app.ws.(shellport.ShellControl).ControlIdentity()
	foreignResponse := call("foreign-link", coreprotocol.ControlExecute, control.Request{ID: "foreign-link", CommandID: "connection.inspect", Target: foreign, Input: json.RawMessage(`{}`)})
	if foreignResponse.Error == nil || !strings.Contains(foreignResponse.Error.Message, "outside external launch grant") {
		t.Fatal("external read grant retargeted peer shell", foreignResponse)
	}
	result = execute("deny-diagnostic-write", "panel.show", bound, map[string]any{"id": "performance"})
	if result.Fault == nil || result.Fault.Code != control.Denied {
		t.Fatal("read-only diagnostic grant admitted navigation", result)
	}
	for _, shell := range shells {
		shell.app.View()
		if shell.app.perf.screen != nil || shell.app.perf.inFlight || shell.app.perf.generation != 0 {
			t.Fatal("diagnostic observation opened or refreshed Performance dashboard")
		}
		if rect, mounted := shell.app.rootHost.ScreenRect(perfdash.BodyID); mounted && !rect.Empty() {
			t.Fatal("diagnostic observer mounted dashboard")
		}
	}
	if peer.app.Input.Value() != "peer private diagnostic draft" || peer.app.Input.DraftRevision() != peerRevision || peer.app.Input.CursorFlat() != peerCursor || peer.app.rootHost.Focused() != peerFocus || peer.app.Win.ActiveGroup() != peerGroup || peer.app.AgentView.ActiveID() != peerAgent {
		t.Fatal("native/external diagnostics changed peer focus, selection or private draft")
	}
}
