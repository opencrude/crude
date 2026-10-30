package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crude/buildinfo"
	"crude/coreprotocol"
	"crude/provider"
	"crude/retry"
	"crude/session"
	"crude/types"
	"crude/types/control"
	"crude/types/handover"
	"crude/workspace"
)

func TestCoreRestartNaturalCheckpointColdSuccessorAndOneContinuation(t *testing.T) {
	setCoreTestHome(t, t.TempDir())
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	first, second := make(chan struct{}), make(chan struct{})
	releaseFirst, releaseSecond := make(chan struct{}), make(chan struct{})
	var finishFirst, finishSecond sync.Once
	t.Cleanup(func() {
		finishFirst.Do(func() { close(releaseFirst) })
		finishSecond.Do(func() { close(releaseSecond) })
	})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Stream bool            `json:"stream"`
			Tools  json.RawMessage `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if !body.Stream || len(body.Tools) == 0 || string(body.Tools) == "[]" {
			// Retained memory maintenance uses the same configured fixture
			// endpoint but is not another root turn. Let its owned nonstreaming
			// extraction finish instead of deadlocking it on the successor gate.
			content := `{"memories":[],"summary":{"title":"Checkpoint","summary":"No durable facts to extract from the fixture.","outcome":"in-progress","key_files":[]}}`
			if body.Stream {
				w.Header().Set("Content-Type", "text/event-stream")
				message, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": content}, "finish_reason": "stop"}}})
				_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", message)
			} else {
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
			}
			return
		}
		switch calls.Add(1) {
		case 1:
			close(first)
			select {
			case <-releaseFirst:
			case <-r.Context().Done():
				return
			}
		case 2:
			close(second)
			select {
			case <-releaseSecond:
			case <-r.Context().Done():
				return
			}
		default:
			t.Error("restart produced more than the single authorized continuation")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Durable checkpoint answer.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	agents := session.DefaultAgentsConfig()
	profile := agents.Profiles["default"]
	off := false
	profile.MultiAgent = &off
	profile.Categories["default"] = session.CategoryConfig{AccountID: "checkpoint", Model: "checkpoint-model", ModelSupportsTools: true}
	agents.Profiles["default"] = profile
	cfg := &session.Config{Agents: agents, Accounts: map[string]provider.Account{
		"checkpoint": {ID: "checkpoint", ProviderID: "openai", Connection: provider.Template{
			Name: "Checkpoint fixture", ProviderURL: server.URL, APIStandard: "openai", AuthType: provider.AuthNone,
		}},
	}}
	root, home := t.TempDir(), t.TempDir()
	factory := func(root string) (*workspace.Workspace, error) {
		ws := workspace.NewWorkspace(root, "", "checkpoint", types.Settings{}, coreTestLogger{})
		ws.SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}})
		ws.SetBackendConfig(cfg, home)
		return ws, nil
	}
	newRuntime := func(generation string) *Runtime {
		r, err := New(factory, filepath.Join(home, "restart-receipts.db"))
		if err != nil {
			t.Fatal(err)
		}
		r.SetOwnerGeneration(generation)
		t.Cleanup(func() { _ = r.Close() })
		return r
	}
	connect := func(r *Runtime) (*Connection, string) {
		c, err := r.Connect("original-checkpoint-profile")
		if err != nil {
			t.Fatal(err)
		}
		// In-process admission models the private local transport's authenticated
		// host fact. No payload-supplied boolean can establish this in production.
		c.localHost = true
		t.Cleanup(c.Close)
		result := callCore(t, c, NewClientID(), "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: root})
		var attached coreprotocol.AttachResult
		if err := coreprotocol.Decode(result.Payload, &attached); err != nil {
			t.Fatal(err)
		}
		callCore(t, c, NewClientID(), attached.WorkspaceID, coreprotocol.ControlShellRegister, &coreprotocol.Empty{})
		return c, attached.WorkspaceID
	}
	r := newRuntime("original-owner")
	c, wsID := connect(r)
	result := callCore(t, c, "create-checkpoint-session", wsID, coreprotocol.NewSession, &coreprotocol.InvocationSessionRequest{})
	var created workspace.NewSessionResult
	if err := coreprotocol.Decode(result.Payload, &created); err != nil || created.Error != "" {
		t.Fatal(created, err)
	}
	var ws *workspace.Workspace
	if err := r.Each(ctx, func(value *workspace.Workspace) { ws = value }); err != nil {
		t.Fatal(err)
	}
	backend := ws.BackendSession(created.SessionID)
	callCore(t, c, "source-checkpoint-input", wsID, coreprotocol.SubmitInput, &coreprotocol.InputRequest{
		AgentID: created.OrchID, Text: "Reach a natural durable checkpoint.", DraftRevision: 1,
	})
	select {
	case <-first:
	case <-ctx.Done():
		t.Fatal("source turn never reached the fixture provider")
	}
	goal, err := backend.SetGoal("Continue the preserved checkpoint objective.", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	member, err := ws.Collaboration.Agent(ctx, created.OrchID)
	if err != nil || member.CurrentRunID == "" {
		t.Fatal(member, err)
	}
	descriptor, ok := control.Lookup("core.restart")
	if !ok {
		t.Fatal("restart has no discoverable semantic entry point")
	}
	input, _ := json.Marshal(map[string]any{
		"executable": "/verified/by/the/application", "sha256": strings.Repeat("a", 64), "goal_id": goal.ID, "delay_seconds": 0,
	})
	epoch, revision := uint64(1), uint64(1)
	shell := control.ShellOwner{ClientID: c.Owner.ClientID, Epoch: c.Owner.Epoch}
	envelope := coreprotocol.ControlEnvelope{Descriptor: descriptor, Request: control.Request{
		ID: "checkpoint-operation", CommandID: "core.restart", Input: input,
		Caller:        control.Caller{Kind: control.AgentCaller, AgentID: created.OrchID, RunID: member.CurrentRunID},
		Target:        control.Target{WorkspaceID: wsID, SessionID: created.SessionID, Shell: &shell},
		Preconditions: control.Preconditions{OpenEpoch: &epoch, Revision: &revision},
	}}
	waiter := make(chan error, 1)
	go func() {
		_, err := r.RequestShellControl(ctx, wsID, shell, envelope)
		waiter <- err
	}()
	select {
	case <-c.Controls():
	case <-ctx.Done():
		t.Fatal("restart was not delivered to the original shell")
	}
	const token = "one-private-application-operation-nonce"
	sum := sha256.Sum256([]byte(token))
	reservation := handover.Reservation{
		OperationID: envelope.Request.ID, GoalID: goal.ID, TokenSHA256: fmt.Sprintf("%x", sum),
		SuccessorCohort: buildinfo.ProtocolBuild, SuccessorSchema: coreprotocol.Schema, ExpiresAt: time.Now().Add(5 * time.Minute),
		SourceAgentID: created.OrchID, SourceRunID: member.CurrentRunID, SourceCoreOwner: "original-owner", SourceShell: shell,
	}
	refused := func(method string, value coreprotocol.Value) {
		t.Helper()
		raw, err := coreprotocol.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		result := c.Call(ctx, coreprotocol.Request{ID: NewClientID(), Owner: c.Owner, WorkspaceID: wsID, Method: method, Payload: raw})
		if result.Error == nil || result.Error.Code != "handover_refused" {
			t.Fatalf("unadmitted restart route was not refused: %s %+v", method, result)
		}
	}
	for _, variant := range []string{"missing-control", "different-goal", "different-owner", "different-run", "different-shell", "unbounded-deadline", "peer-client", "invalid-permit"} {
		in := coreprotocol.CoreRestartReservationRequest{ControlRequestID: envelope.Request.ID, SessionID: created.SessionID, Reservation: reservation}
		var peer *Connection
		switch variant {
		case "missing-control":
			in.ControlRequestID = "no-such-original-control"
		case "different-goal":
			in.Reservation.GoalID = "a-newly-selected-goal"
		case "different-owner":
			in.Reservation.SourceCoreOwner = "replacement-owner"
		case "different-run":
			in.Reservation.SourceRunID = "replacement-run"
		case "different-shell":
			in.Reservation.SourceShell.ClientID = "focused-peer"
		case "unbounded-deadline":
			in.Reservation.ExpiresAt = time.Now().Add(time.Hour)
		case "invalid-permit":
			in.Reservation.TokenSHA256 = ""
		case "peer-client":
			peer, err = r.Connect("neighbor-not-owned-by-restart")
			if err != nil {
				t.Fatal(err)
			}
		}
		refused(coreprotocol.CoreRestartReserve, &in)
		if peer != nil {
			peer.Close()
		}
		if _, err := backend.GoalHandover(reservation.OperationID); err == nil {
			t.Fatalf("refused %s published a reservation anyway", variant)
		}
	}
	result = callCore(t, c, "reserve-checkpoint", wsID, coreprotocol.CoreRestartReserve, &coreprotocol.CoreRestartReservationRequest{
		ControlRequestID: envelope.Request.ID, SessionID: created.SessionID, Reservation: reservation,
	})
	var reserved coreprotocol.CoreRestartResult
	if err := coreprotocol.Decode(result.Payload, &reserved); err != nil || reserved.State.Phase != handover.Reserved {
		t.Fatal(reserved, err)
	}
	if calls.Load() != 1 || !backend.IsTurnActive() {
		t.Fatal("scheduling cancelled or duplicated the source turn")
	}
	refused(coreprotocol.CoreRestartContinue, &coreprotocol.CoreRestartPermitRequest{
		SessionID: created.SessionID, OperationID: envelope.Request.ID, Token: token,
	})
	callCore(t, c, "restart-scheduled-reply", wsID, coreprotocol.ControlShellReply, &control.Result{
		RequestID: envelope.Request.ID, OperationID: envelope.Request.ID, State: control.Committed,
	})
	if err := <-waiter; err != nil {
		t.Fatal(err)
	}
	finishFirst.Do(func() { close(releaseFirst) })
	for {
		state, err := backend.GoalHandover(envelope.Request.ID)
		if err != nil {
			t.Fatal(err)
		}
		if state.Phase == handover.Ready {
			break
		}
		if state.Phase != handover.Reserved {
			t.Fatalf("natural source did not produce a ready checkpoint: %+v", state)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("natural checkpoint never became ready: activity=%+v root requests=%d", backend.Activity(), calls.Load())
		case <-time.After(5 * time.Millisecond):
		}
	}
	c.Close()
	r.SetLifetimePolicy(false, time.Minute, func() {})
	for attempt := 1; ; attempt++ {
		// Use graceful shutdown, including known no-effect refusal while the
		// detached shell's resource releases join. A zero-delay BeginDrain
		// sample is not the product's bounded scheduled-handover path.
		stopped := r.shutdown(ctx, nil, coreprotocol.ShutdownRequest{ExpectedOwner: "original-owner"})
		if stopped.Accepted {
			break
		}
		if attempt >= 8 || ctx.Err() != nil {
			t.Fatalf("the exact checkpoint could not reserve graceful shutdown: %+v", stopped)
		}
		if err := retry.Wait(ctx, retry.Exponential(attempt, 25*time.Millisecond, 250*time.Millisecond)); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	successor := newRuntime("successor-owner")
	next, nextWS := connect(successor)
	callCore(t, next, "open-exact-checkpoint", nextWS, coreprotocol.OpenSession, &coreprotocol.InvocationSessionRequest{SessionID: created.SessionID})
	var reopened *session.Session
	if err := successor.Each(ctx, func(ws *workspace.Workspace) { reopened = ws.BackendSession(created.SessionID) }); err != nil {
		t.Fatal(err)
	}
	if shown, _ := reopened.Goal(); !shown.Waiting || calls.Load() != 1 {
		t.Fatal("ordinary cold reopen activated a goal without its permit")
	}
	consume := &coreprotocol.CoreRestartPermitRequest{SessionID: created.SessionID, OperationID: envelope.Request.ID, Token: token}
	callCore(t, next, "consume-exact-checkpoint", nextWS, coreprotocol.CoreRestartContinue, consume)
	select {
	case <-second:
	case <-ctx.Done():
		t.Fatal("successor did not activate the single permitted continuation")
	}
	if _, err := reopened.PauseGoal("Explicit user pause after the permitted turn started."); err != nil {
		t.Fatal(err)
	}
	callCore(t, next, "observe-repeat-permit", nextWS, coreprotocol.CoreRestartContinue, consume)
	if shown, _ := reopened.Goal(); shown.Status != types.GoalPaused {
		t.Fatal("repeat permit overwrote the newer user pause")
	}
	finishSecond.Do(func() { close(releaseSecond) })
}

func TestCoreRestartOwnerMethodsAreJournalledExceptInspection(t *testing.T) {
	for _, method := range []string{coreprotocol.CoreRestartReserve, coreprotocol.CoreRestartInspect, coreprotocol.CoreRestartCancel, coreprotocol.CoreRestartContinue} {
		if !ownerOperation(method) || ownerMutation(method) != (method != coreprotocol.CoreRestartInspect) {
			t.Fatalf("restart escaped the existing operation/receipt lane: %s", method)
		}
	}
}
