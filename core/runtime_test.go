package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/provider"
	"crude/session"
	"crude/types"
	"crude/workspace"
)

type coreTestLogger struct{}

func (coreTestLogger) Info(string, string, ...any)  {}
func (coreTestLogger) Warn(string, string, ...any)  {}
func (coreTestLogger) Error(string, string, ...any) {}
func (coreTestLogger) Debug(string, string, ...any) {}

func callCore(t *testing.T, c *Connection, id, ws, method string, value any) coreprotocol.Response {
	t.Helper()
	raw, err := testPayload(value)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result := c.Call(ctx, coreprotocol.Request{ID: id, Owner: c.Owner, WorkspaceID: ws, Method: method, Payload: raw})
	if result.Error != nil {
		t.Fatalf("%s: %v", method, result.Error)
	}
	return result
}

func TestRetainedTenClientsSameAgentAndOneSubmission(t *testing.T) {
	setCoreTestHome(t, t.TempDir())
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"response\",\"choices\":[{\"delta\":{\"content\":\"retained answer\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	root, home := t.TempDir(), t.TempDir()
	var creations atomic.Int32
	runtime, err := New(func(root string) (*workspace.Workspace, error) {
		creations.Add(1)
		ws := workspace.NewWorkspace(root, "test-model", "tester", types.Settings{}, coreTestLogger{})
		ws.SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}})
		agents := session.DefaultAgentsConfig()
		off := false
		profile := agents.Profiles["default"]
		profile.MultiAgent = &off
		profile.Categories["default"] = session.CategoryConfig{AccountID: "test", Model: "test-model", ModelSupportsTools: true}
		agents.Profiles["default"] = profile
		ws.SetBackendConfig(&session.Config{Agents: agents, Accounts: map[string]provider.Account{"test": {ID: "test", ProviderID: "openai", APIKey: "test", Connection: provider.Template{Name: "test", ProviderURL: server.URL, APIStandard: "openai", AuthType: provider.AuthProtocol}}}}, home)
		return ws, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	clients := make([]*Connection, 10)
	for i := range clients {
		clients[i], err = runtime.Connect(fmt.Sprintf("client-%d", i))
		if err != nil {
			t.Fatal(err)
		}
		defer clients[i].Close()
	}
	ids := make([]string, 10)
	var wg sync.WaitGroup
	for i, c := range clients {
		wg.Add(1)
		go func(i int, c *Connection) {
			defer wg.Done()
			result := callCore(t, c, "attach", "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root})
			var attached coreprotocol.AttachResult
			if err := coreprotocol.Decode(result.Payload, &attached); err != nil {
				t.Error(err)
			}
			ids[i] = attached.WorkspaceID
		}(i, c)
	}
	wg.Wait()
	if creations.Load() != 1 {
		t.Fatalf("factory ran %d times", creations.Load())
	}
	for _, id := range ids {
		if id != ids[0] {
			t.Fatal("different workspace owners")
		}
	}
	result := callCore(t, clients[0], "create", ids[0], coreprotocol.NewSession, coreprotocol.InvocationSessionRequest{})
	var created workspace.NewSessionResult
	if err := coreprotocol.Decode(result.Payload, &created); err != nil {
		t.Fatal(err)
	}
	for _, c := range clients {
		result = callCore(t, c, "open", ids[0], coreprotocol.OpenSession, coreprotocol.InvocationSessionRequest{SessionID: created.SessionID})
		var opened workspace.NewSessionResult
		if err := coreprotocol.Decode(result.Payload, &opened); err != nil {
			t.Fatal(err)
		}
		if opened.OrchID != created.OrchID {
			t.Fatal("open cloned execution")
		}
	}
	submit := coreprotocol.InputRequest{AgentID: created.OrchID, Text: "Say retained answer", DraftRevision: 1}
	result = callCore(t, clients[0], "submit", ids[0], coreprotocol.SubmitInput, submit)
	var admitted coreprotocol.InputResult
	if err := coreprotocol.Decode(result.Payload, &admitted); err != nil {
		t.Fatal(err)
	}
	if admitted.Status != workspace.InputAdmissionAccepted {
		t.Fatalf("admission: %+v", admitted)
	}
	clients[0].Close()
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		result = callCore(t, clients[1], "changes", ids[1], coreprotocol.AgentChanges, coreprotocol.AgentChangesRequest{})
		var changes coreprotocol.AgentChangesResult
		if err := coreprotocol.Decode(result.Payload, &changes); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, delta := range changes.Agents {
			for _, message := range delta.Conversation.Upserts {
				if message.Content == "retained answer" {
					found = true
				}
			}
		}
		if found {
			break
		}
		select {
		case <-deadline:
			t.Fatal("detaching origin lost execution")
		case <-ticker.C:
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("provider execution count: %d", calls.Load())
	}
	replacement, err := runtime.Connect("client-0")
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	callCore(t, replacement, "attach-again", "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root})
	again := callCore(t, replacement, "submit", ids[0], coreprotocol.SubmitInput, submit)
	if string(again.Payload) != string(resultPayload(admitted)) {
		var recovered coreprotocol.InputResult
		_ = coreprotocol.Decode(again.Payload, &recovered)
		if recovered.Receipt != admitted.Receipt {
			t.Fatal("lost receipt response")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("reconnection replayed input")
	}
	stale := replacement.Call(context.Background(), coreprotocol.Request{ID: "stale", Owner: clients[0].Owner, WorkspaceID: ids[0], Method: coreprotocol.Snapshot, Payload: emptyPayload})
	if stale.Error == nil || stale.Error.Code != "invalid_owner" {
		t.Fatal("stale epoch accepted")
	}
	forged := replacement.Call(context.Background(), coreprotocol.Request{ID: "forged", Owner: replacement.Owner, WorkspaceID: ids[0], Method: coreprotocol.SubmitInput, Payload: withExtraField(mustTestPayload(coreprotocol.InputRequest{AgentID: "x", Text: "hello"}), "runtime")})
	if forged.Error == nil || forged.Error.Code != "invalid_request" {
		t.Fatal("forged origin accepted")
	}
}
func resultPayload(value any) []byte { raw, _ := json.Marshal(value); return raw }

func TestTopicJournalEvictionRequiresResetAndRetainsCoveredOrder(t *testing.T) {
	var hub topicHub
	for i := 1; i <= topicJournalEntries+10; i++ {
		hub.publish([]byte(fmt.Sprint(i)))
	}
	if _, _, covered := hub.next(1); covered {
		t.Fatal("evicted cursor presented as a plausible delta")
	}
	for seq := uint64(11); seq < hub.sequence(); seq++ {
		entry, _, covered := hub.next(seq)
		if !covered || entry.seq != seq+1 || string(entry.body) != fmt.Sprint(seq+1) {
			t.Fatalf("covered journal lost order: %+v %v", entry, covered)
		}
	}
	if len(hub.entries) != topicJournalEntries || hub.bytes > topicJournalBytes {
		t.Fatal("topic journal exceeded bounds")
	}
}

func TestDomainPublicationNeverEntersPrivateSessionInbox(t *testing.T) {
	r := failureRuntime(t)
	c, ws := resourceConnection(t, r, t.TempDir(), "topic-only")
	c.mu.Lock()
	a := c.attached[ws]
	c.mu.Unlock()
	if err := a.inspect(t.Context(), func() {
		before := a.revision
		for i := range 1000 {
			a.broadcast(coreprotocol.Change{Files: []coreprotocol.FileChange{{Path: fmt.Sprintf("file-%d", i)}}})
		}
		if a.revision != before+1000 {
			t.Error("publication pacing delayed accepted mutation")
		}
		if len(a.topic.pending.Files) != 1000 || !a.topic.dirty {
			t.Error("authoritative topic lost accepted facts")
		}
		if err := a.flushTopic(); err != nil {
			t.Error(err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if len(c.privateEffects) != 0 {
		t.Fatal("domain publication duplicated traffic on the session stream")
	}
}

func TestPrivateClipboardAdmissionRefusesWithoutEvictingAcceptedEffects(t *testing.T) {
	r := failureRuntime(t)
	c, err := r.Connect("private-admission")
	if err != nil {
		t.Fatal(err)
	}
	for i := range coreprotocol.SessionPrivateMaxOutstanding {
		value := []byte(fmt.Sprint(i))
		if !c.offerClipboard("ws", coreprotocol.TerminalClipboard{TerminalID: "term", Generation: 1, Clipboard: [][]byte{value}}) {
			t.Fatal("bounded offer refused before capacity")
		}
		value[0] = 'x'
	}
	if c.offerClipboard("ws", coreprotocol.TerminalClipboard{TerminalID: "term", Generation: 1, Clipboard: [][]byte{[]byte("overflow")}}) {
		t.Fatal("full private queue admitted an offer")
	}
	for i := range coreprotocol.SessionPrivateMaxOutstanding {
		event := <-c.privateEffects
		if string(event.TerminalClipboard.Clipboard[0]) != fmt.Sprint(i) {
			t.Fatal("accepted immutable FIFO offer displaced", event)
		}
		c.takePrivateEffect(event)
	}
	if c.privateBytes != 0 {
		t.Fatal("private byte accounting leaked")
	}
	if c.offerClipboard("ws", coreprotocol.TerminalClipboard{Clipboard: [][]byte{make([]byte, coreprotocol.SessionPrivateMaxBytes)}}) {
		t.Fatal("oversized offer admitted")
	}
}

func TestDrainingConnectionRejectsEffectsButPermitsObservation(t *testing.T) {
	r := failureRuntime(t)
	c, err := r.Connect("drain-admission")
	if err != nil {
		t.Fatal(err)
	}
	// Model the owner's validated handover transition, which permits its own
	// administrative connection while fencing every later effect.
	r.mu.Lock()
	r.draining = true
	r.mu.Unlock()
	for _, method := range []string{coreprotocol.Attach, coreprotocol.CoreShutdownMethod, coreprotocol.StartupInspect, coreprotocol.ControlShellReply} {
		result := c.Call(t.Context(), coreprotocol.Request{ID: "after-drain", Owner: c.Owner, Method: method, Payload: emptyPayload})
		if result.Error == nil || result.Error.Code != "core_draining" {
			t.Fatalf("draining admitted %s: %+v", method, result.Error)
		}
	}
	result := c.Call(t.Context(), coreprotocol.Request{ID: "observe", Owner: c.Owner, Method: coreprotocol.CoreStatusMethod, Payload: emptyPayload})
	if result.Error != nil {
		t.Fatal("drain blocked status observation", result.Error)
	}
	result = c.Call(t.Context(), coreprotocol.Request{ID: "receipt", Owner: c.Owner, Method: coreprotocol.GetReceipt, Payload: mustTestPayload(coreprotocol.ReceiptRequest{RequestID: "unknown"})})
	if result.Error != nil && result.Error.Code == "core_draining" {
		t.Fatal("drain blocked receipt observation")
	}
}

func TestFactoryIODoesNotHoldRegistryLock(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	runtime, err := New(func(string) (*workspace.Workspace, error) {
		close(entered)
		<-release
		return nil, fmt.Errorf("expected factory failure")
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	c, err := runtime.Connect("factory-client")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	result := make(chan coreprotocol.Response, 1)
	raw, _ := coreprotocol.Marshal(coreprotocol.Ptr(coreprotocol.AttachRequest{Root: t.TempDir()}))
	go func() {
		result <- c.Call(context.Background(), coreprotocol.Request{ID: "attach", Owner: c.Owner, Method: coreprotocol.Attach, Payload: raw})
	}()
	<-entered
	connected := make(chan *Connection, 1)
	go func() { other, _ := runtime.Connect("independent-client"); connected <- other }()
	select {
	case other := <-connected:
		if other == nil {
			t.Fatal("connection rejected")
		}
		other.Close()
	case <-time.After(time.Second):
		close(release)
		t.Fatal("factory held registry lock")
	}
	close(release)
	if response := <-result; response.Error == nil {
		t.Fatal("factory failure hidden")
	}
}

func TestActorRejectsQueuedDetachedOwnerAndStaleDecision(t *testing.T) {
	setCoreTestHome(t, t.TempDir())
	runtime, err := New(func(root string) (*workspace.Workspace, error) {
		return workspace.NewWorkspace(root, "test", "tester", types.Settings{}, coreTestLogger{}), nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	c, err := runtime.Connect("decision-client")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	result := callCore(t, c, "attach", "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: t.TempDir()})
	var attached coreprotocol.AttachResult
	if err := coreprotocol.Decode(result.Payload, &attached); err != nil {
		t.Fatal(err)
	}
	raw, _ := coreprotocol.Marshal(coreprotocol.Ptr(coreprotocol.DecisionRequest{AgentID: "missing", BlockID: "old", Choice: "allow"}))
	decision := c.Call(context.Background(), coreprotocol.Request{ID: "decision", Owner: c.Owner, WorkspaceID: attached.WorkspaceID, Method: coreprotocol.Decide, Payload: raw})
	if decision.Error == nil || decision.Error.Code != "stale_decision" {
		t.Fatal("stale decision accepted")
	}
	c.mu.Lock()
	a := c.attached[attached.WorkspaceID]
	c.mu.Unlock()
	callCore(t, c, "detach", attached.WorkspaceID, coreprotocol.Detach, coreprotocol.Empty{})
	raw = json.RawMessage(`{}`)
	stale := a.call(context.Background(), c, coreprotocol.Request{ID: "queued-before-detach", Owner: c.Owner, WorkspaceID: attached.WorkspaceID, Method: coreprotocol.Snapshot, Payload: raw})
	if stale.Error == nil || stale.Error.Code != "detached" {
		t.Fatal("detached owner executed")
	}
}
