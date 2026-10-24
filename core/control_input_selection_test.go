package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/provider"
	"crude/session"
	"crude/types/control"
	"crude/workspace"
	"crude/worktree"
)

func TestControlInputCapturesSelectedExecutionForFreshSend(t *testing.T) {
	fixture := controlExecutionFixture(t)
	ws := fixture.ws
	var calls atomic.Int32
	wire := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []provider.ChatMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		raw, _ := json.Marshal(body)
		wire <- string(raw)
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"selected-read\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"disk.txt\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
		} else {
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Finished selected read.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}
	}))
	t.Cleanup(server.Close)
	agents := session.DefaultAgentsConfig()
	profile := agents.Profiles["default"]
	profile.Categories["default"] = session.CategoryConfig{AccountID: "test", Model: "selection-control-model", ModelSupportsTools: true}
	agents.Profiles["default"] = profile
	cfg := &session.Config{Agents: agents, Accounts: map[string]provider.Account{"test": {ID: "test", ProviderID: "openai", APIKey: "test", Connection: provider.Template{Name: "test", ProviderURL: server.URL, APIStandard: "openai", AuthType: provider.AuthProtocol}}}}
	ws.SetBackendConfig(cfg, t.TempDir())
	runtime, err := New(func(string) (*workspace.Workspace, error) { return ws, nil }, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	c := controlWireClient(t, runtime, "selection-control-shell")
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	var attached coreprotocol.AttachResult
	if err := c.Call(ctx, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: ws.RootPath}, &attached); err != nil {
		t.Fatal(err)
	}
	if err := c.RegisterControlShell(ctx, attached.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, attached.WorkspaceID, coreprotocol.ControlClientRegister, &coreprotocol.ControlClientGrant{Effects: []control.Effect{control.Communication}}, nil); err != nil {
		t.Fatal(err)
	}
	var created workspace.NewSessionResult
	if err := c.Call(ctx, attached.WorkspaceID, coreprotocol.NewSession, &coreprotocol.InvocationSessionRequest{}, &created); err != nil || created.Error != "" {
		t.Fatal(created, err)
	}
	backend := ws.BackendSession(created.SessionID)
	backend.SetYolo(true)
	execution, err := ws.Executions.Create(ctx, worktree.Request{Owner: worktree.Owner{AgentID: "client:" + c.Owner().ClientID, RunID: "selection-allocation"}, Mode: worktree.Isolated})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(execution.Root, "disk.txt"), []byte("selected-only evidence\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.SelectExecution(ctx, execution.ID, 0); err != nil {
		t.Fatal(err)
	}
	// The trusted shell admission records original attribution. The input RPC
	// below is the real product handoff; no synthetic input or turn is installed.
	envelope := shellEnvelope("selected-composer-send", attached.WorkspaceID, c.Owner())
	envelope.Request.CommandID, envelope.Descriptor.ID = "composer.send", "composer.send"
	envelope.Descriptor.Effect = control.Communication
	envelope.Request.Caller = control.Caller{Kind: control.ExternalCaller, ClientID: c.Owner().ClientID}
	envelope.Request.Target.SessionID, envelope.Request.Target.ResourceID = created.SessionID, created.OrchID
	revision, epoch := uint64(1), uint64(1)
	envelope.Request.Preconditions.Revision, envelope.Request.Preconditions.OpenEpoch = &revision, &epoch
	result := make(chan error, 1)
	go func() {
		_, err := runtime.RequestShellControl(ctx, attached.WorkspaceID, *envelope.Request.Target.Shell, envelope)
		result <- err
	}()
	select {
	case <-c.ControlRequests():
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var admitted coreprotocol.InputResult
	if err := c.Call(ctx, attached.WorkspaceID, coreprotocol.SubmitControlInput, &coreprotocol.ControlInputRequest{ControlRequestID: envelope.Request.ID, SessionID: created.SessionID, AgentID: created.OrchID, DraftEpoch: epoch, DraftRevision: revision, Text: "Read disk.txt in the selected execution."}, &admitted); err != nil || admitted.Error != "" || admitted.Status != session.InputAdmissionAccepted {
		t.Fatal(admitted, err)
	}
	if err := c.ReplyControl(ctx, attached.WorkspaceID, control.Result{RequestID: envelope.Request.ID, OperationID: envelope.Request.ID, State: control.Applied}); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		select {
		case got := <-wire:
			if !strings.Contains(got, wireText(execution.Root)) || i == 1 && !strings.Contains(got, "selected-only evidence") {
				t.Fatalf("control input ignored selected checkout, request %d: %s", i, got)
			}
		case <-ctx.Done():
			t.Fatal("selected tool loop did not complete", ctx.Err())
		}
	}
	if data, err := os.ReadFile(filepath.Join(ws.RootPath, "disk.txt")); err != nil || string(data) != "human dirty\n" {
		t.Fatal("logical checkout changed", string(data), err)
	}
}
