package cranemcp

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"crude/core"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/internal/testhome"
	"crude/types"
	"crude/types/control"
	"crude/workspace"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMain(m *testing.M) { testhome.Run(m) }

type logger struct{}

func (logger) Info(string, string, ...any)  {}
func (logger) Warn(string, string, ...any)  {}
func (logger) Error(string, string, ...any) {}
func (logger) Debug(string, string, ...any) {}

func wire(t *testing.T, r *core.Runtime, id, root string) (*coreclient.Client, string) {
	t.Helper()
	a, b := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- core.ServeConnection(t.Context(), r, b) }()
	c, err := coreclient.Dial(t.Context(), a, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = c.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("core wire did not close")
		}
	})
	go func() {
		for range c.Events() {
		}
	}()
	var attached coreprotocol.AttachResult
	if err := c.Call(t.Context(), "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &attached); err != nil {
		t.Fatal(err)
	}
	return c, attached.WorkspaceID
}

// Evidence timing is deliberately controlled: cancellation happens only after
// the mutation was admitted. Receipt observation must retain the same identity
// and use a bounded independent context, never resubmit the effect.
type interruptedMutation struct {
	admitted     chan string
	executions   int
	observations int
	requestID    string
	receiptState string
	disconnected bool
}

func (c *interruptedMutation) Owner() coreprotocol.Owner {
	return coreprotocol.Owner{ClientID: "interrupted", Epoch: 1}
}

func (c *interruptedMutation) CallRequest(ctx context.Context, request coreprotocol.Request) (coreprotocol.Response, error) {
	if request.Method == coreprotocol.ControlResolve {
		return coreprotocol.Response{Payload: json.RawMessage(`{}`)}, nil
	}
	c.executions++
	c.requestID = request.ID
	c.admitted <- request.ID
	<-ctx.Done()
	return coreprotocol.Response{}, &coreclient.UncertainError{RequestID: request.ID, Cause: ctx.Err()}
}

func (c *interruptedMutation) Call(ctx context.Context, _ string, method string, input, output any) error {
	if method != coreprotocol.GetReceipt {
		return errors.New("unexpected operation")
	}
	c.observations++
	if ctx.Err() != nil {
		return errors.New("receipt inherited cancelled context")
	}
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 3*time.Second {
		return errors.New("receipt observation is not bounded")
	}
	if input.(coreprotocol.ReceiptRequest).RequestID != c.requestID {
		return errors.New("receipt identity changed")
	}
	if c.disconnected {
		return coreclient.ErrClosed
	}
	payload, _ := json.Marshal(control.Result{RequestID: c.requestID, OperationID: c.requestID, State: control.Committed})
	*output.(*coreprotocol.Receipt) = coreprotocol.Receipt{State: c.receiptState, Response: coreprotocol.Response{Payload: payload}}
	return nil
}

func TestMutationCancellationEvidenceTiming(t *testing.T) {
	for _, test := range []struct {
		name, state  string
		disconnected bool
		want         control.ResultState
	}{
		{"completion_after_cancel", "completed", false, control.Committed},
		{"still_pending", "pending", false, control.Uncertain},
		{"core_disconnected", "", true, control.Uncertain},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &interruptedMutation{admitted: make(chan string, 1), receiptState: test.state, disconnected: test.disconnected}
			a := &adapter{client: client, workspace: "workspace"}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			type answer struct {
				value any
				err   error
			}
			done := make(chan answer, 1)
			go func() {
				value, _, err := a.invoke(ctx, &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "crude_control", Arguments: json.RawMessage(`{"command":"settings.set","arguments":{"scope":"application","path":"editor.word_wrap","value":true,"expected_revision":"original"}}`)}})
				done <- answer{value, err}
			}()
			var id string
			select {
			case id = <-client.admitted:
			case <-time.After(time.Second):
				t.Fatal("mutation not admitted")
			}
			cancel()
			select {
			case answer := <-done:
				if answer.err != nil {
					t.Fatal(answer.err)
				}
				result := answer.value.(control.Result)
				if result.RequestID != id || result.OperationID != id || result.State != test.want {
					t.Fatalf("lost mutation evidence: %#v", result)
				}
				if client.executions != 1 || client.observations != 1 {
					t.Fatalf("effect replayed: executions=%d observations=%d", client.executions, client.observations)
				}
			case <-time.After(time.Second):
				t.Fatal("cancelled/disconnected request did not settle promptly")
			}
		})
	}
}
func sdk(t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	a, b := net.Pipe()
	s, err := server.Connect(t.Context(), &mcp.IOTransport{Reader: a, Writer: a}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	c, err := mcp.NewClient(&mcp.Implementation{Name: "acceptance", Version: "1"}, nil).Connect(t.Context(), &mcp.IOTransport{Reader: b, Writer: b}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}
func call(t *testing.T, c *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	out, err := c.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func outcome(t *testing.T, out *mcp.CallToolResult) control.Result {
	t.Helper()
	raw, err := json.Marshal(out.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var value control.Result
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

// Lose only the response after real core completion, preserving the original
// request's durable receipt. The adapter must observe it, not execute again.
type lostReply struct {
	*coreclient.Client
	executions int
}

func (c *lostReply) CallRequest(ctx context.Context, request coreprotocol.Request) (coreprotocol.Response, error) {
	response, err := c.Client.CallRequest(ctx, request)
	if request.Method == coreprotocol.ControlExecute && err == nil && response.Error == nil {
		c.executions++
		return coreprotocol.Response{}, &coreclient.UncertainError{RequestID: request.ID, Cause: context.DeadlineExceeded}
	}
	return response, err
}

func TestSDKCoreDomainAndExactShell(t *testing.T) {
	r, err := core.New(func(root string) (*workspace.Workspace, error) {
		ws := workspace.NewWorkspace(root, "", "tester", types.Settings{}, logger{})
		ws.SetAppDefaults(types.AppSettings{Editor: types.EditorSettings{WordWrap: true, TabSize: 4}})
		ws.OnAppPreferencesCommit = func(_, next types.AppSettings) (types.AppSettings, error) { return next, nil }
		return ws, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	root := t.TempDir()
	a, ws := wire(t, r, "shell-a", root)
	b, _ := wire(t, r, "shell-b", root)
	for _, c := range []*coreclient.Client{a, b} {
		if err := c.RegisterControlShell(t.Context(), ws); err != nil {
			t.Fatal(err)
		}
	}
	reads, _ := wire(t, r, "read-adapter", root)
	s, err := New(t.Context(), reads, ws, coreprotocol.ControlClientGrant{Effects: []control.Effect{control.Read}})
	if err != nil {
		t.Fatal(err)
	}
	c := sdk(t, s)
	listed, err := c.ListTools(t.Context(), nil)
	if err != nil || len(listed.Tools) != 4 {
		t.Fatalf("tools: %#v %v", listed, err)
	}
	if out := call(t, c, "crude_commands", map[string]any{"query": "settings"}); out.IsError || out.StructuredContent == nil {
		t.Fatal(out)
	}
	if out := call(t, c, "crude_shells", map[string]any{}); out.IsError {
		t.Fatal(out)
	}
	get := outcome(t, call(t, c, "crude_control", map[string]any{"command": "settings.get", "arguments": map[string]any{"scope": "application"}}))
	var settings struct {
		Revision string            `json:"application_revision"`
		Settings types.AppSettings `json:"application"`
	}
	if err := json.Unmarshal(get.Output, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Revision == "" {
		t.Fatalf("missing preimage: %s", get.Output)
	}
	args := map[string]any{"command": "settings.set", "arguments": map[string]any{"scope": "application", "path": "editor.word_wrap", "value": false, "expected_revision": settings.Revision}}
	if out := call(t, c, "crude_control", args); !out.IsError {
		t.Fatal("read launch admitted write")
	}
	writer, _ := wire(t, r, "write-adapter", root)
	owner := control.ShellOwner{ClientID: a.Owner().ClientID, Epoch: a.Owner().Epoch}
	s, err = New(t.Context(), writer, ws, coreprotocol.ControlClientGrant{Effects: []control.Effect{control.Read, control.Write}, Shell: &owner})
	if err != nil {
		t.Fatal(err)
	}
	w := sdk(t, s)
	changed := outcome(t, call(t, w, "crude_control", args))
	if changed.State != control.Committed {
		t.Fatalf("mutation: %#v", changed)
	}
	after := outcome(t, call(t, c, "crude_control", map[string]any{"command": "settings.get", "arguments": map[string]any{"scope": "application"}}))
	settings.Settings = types.AppSettings{}
	if err := json.Unmarshal(after.Output, &settings); err != nil || settings.Settings.Editor.WordWrap || settings.Settings.Editor.TabSize != 4 {
		t.Fatalf("wrong domain state: %s %v", after.Output, err)
	}
	go func() {
		for e := range a.ControlRequests() {
			_ = a.ReplyControl(context.Background(), ws, control.Result{RequestID: e.Request.ID, OperationID: e.Request.ID, State: control.Applied, Output: json.RawMessage(`{"shell":"a"}`)})
		}
	}()
	inspected := outcome(t, call(t, w, "crude_control", map[string]any{"command": "shell.inspect", "arguments": map[string]any{}}))
	if inspected.State != control.Applied || string(inspected.Output) != `{"shell":"a"}` {
		t.Fatalf("target: %#v", inspected)
	}
	select {
	case <-b.ControlRequests():
		t.Fatal("wrong shell received control")
	default:
	}
	if out := call(t, w, "crude_control", map[string]any{"command": "shell.inspect", "arguments": map[string]any{}, "target": map[string]any{"shell": map[string]any{"client_id": "shell-b", "epoch": 1}}}); !out.IsError {
		t.Fatal("target widened")
	}
	if out := call(t, w, "crude_receipt", map[string]any{"request_id": changed.RequestID}); out.IsError {
		t.Fatal(out)
	}
	lostClient, _ := wire(t, r, "lost-adapter", root)
	lost := &lostReply{Client: lostClient}
	lostServer, err := New(t.Context(), lost, ws, coreprotocol.ControlClientGrant{Effects: []control.Effect{control.Read, control.Write}})
	if err != nil {
		t.Fatal(err)
	}
	recovered := outcome(t, call(t, sdk(t, lostServer), "crude_control", map[string]any{"command": "settings.set", "arguments": map[string]any{"scope": "application", "path": "editor.word_wrap", "value": true, "expected_revision": settings.Revision}}))
	if recovered.State != control.Committed || lost.executions != 1 {
		t.Fatalf("lost outcome replayed/not recovered: %#v executions=%d", recovered, lost.executions)
	}
}
