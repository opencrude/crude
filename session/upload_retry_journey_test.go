package session

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crude/agent"
	"crude/collaboration"
	"crude/network"
	"crude/provider"
	"crude/sessiondb"
	recoverytypes "crude/types/recovery"
)

// Exercise real provider creation, HTTP/2 flow control, TCP loss and supervisor
// cooldown expiry. Only event observation is instrumented; no provider is mocked.
func TestUploadBlackholeRecoveryDurableOwners(t *testing.T) {
	for _, owner := range []string{"root", "child"} {
		for _, stop := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stop=%t", owner, stop), func(t *testing.T) {
				checkUploadBlackholeRecovery(t, owner, stop)
			})
		}
	}
}

func checkUploadBlackholeRecovery(t *testing.T, owner string, stop bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	home, sessions, project := t.TempDir(), t.TempDir(), t.TempDir()
	model := fmt.Sprintf("upload-blackhole-%s-%t", owner, stop)
	parentModel := model + "-parent"
	original := "retain exact upload assignment " + strings.Repeat("full original payload ", 64*1024)
	path := filepath.Join(project, "effect.txt")
	const content = "completed effect before upload blackhole\n"
	const callID = "completed-effect"
	var parentID string
	var requests atomic.Int32
	var mu sync.Mutex
	var replacementAt time.Time
	var failedRemote, replacementRemote string
	fixture := sessionSupervisedProtocolFixtures()["openai"]
	var armBlackhole func(string)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Errorf("expected real HTTP/2, got %s", r.Proto)
		}
		// The parent seed uses a separate model/client. For the actual owner, the
		// second request follows one accepted tool effect on the same connection.
		if r.Header.Get("X-Upload-Journey-Owner") == "target" && requests.Load() == 1 {
			if r.ContentLength <= 512<<10 {
				t.Errorf("upload fixture must exceed advertised flow window: content length=%d", r.ContentLength)
			}
			requests.Add(1)
			mu.Lock()
			failedRemote = r.RemoteAddr
			mu.Unlock()
			armBlackhole(r.RemoteAddr)
			// Do not consume body DATA or return an error: the TCP relay silently drops
			// traffic while HTTP/2 flow control prevents completing this large upload.
			<-r.Context().Done()
			return
		}
		var raw json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Errorf("decode local HTTP request: %v", err)
			return
		}
		var body struct{ Model string }
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if body.Model == parentModel {
			_, _ = fmt.Fprint(w, fixture.reply("parent seed complete", "", ""))
			return
		}
		if body.Model != model || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("wrong actual gateway adapter: model=%q path=%q", body.Model, r.URL.Path)
		}
		switch index := requests.Add(1); index {
		case 1:
			args, err := json.Marshal(map[string]string{"path": path, "content": content})
			if err != nil {
				t.Error(err)
				return
			}
			_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":%q,\"type\":\"function\",\"function\":{\"name\":\"write_file\",\"arguments\":%q}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", callID, string(args))
		default:
			if stop {
				t.Error("cancelled cooldown restarted HTTP execution")
			}
			mu.Lock()
			if replacementAt.IsZero() {
				replacementAt = time.Now()
				replacementRemote = r.RemoteAddr
			}
			mu.Unlock()
			wire := string(raw)
			if strings.Count(wire, original) != 1 || !strings.Contains(wire, callID) || !strings.Contains(wire, "runtime_recovery") {
				t.Errorf("replacement lost accepted input/tool evidence or recovery control")
			}
			if owner == "child" && index == 3 {
				args, err := json.Marshal(map[string]any{"recipient_ids": []string{parentID}, "kind": "final", "summary": "Gateway recovered", "body": "The completed effect was retained exactly once."})
				if err != nil {
					t.Error(err)
					return
				}
				_, _ = fmt.Fprint(w, fixture.reply("", "gateway-final-report", string(args)))
			} else {
				_, _ = fmt.Fprint(w, fixture.reply("gateway task completed", "", ""))
			}
		}
	}))
	server.EnableHTTP2 = true
	// Keep the failure in the actual upload phase independently of scheduler
	// timing: default HTTP/2 flow windows can buffer the entire fixture before
	// the handler arms the blackhole, especially under the race detector.
	server.Config.HTTP2 = &http.HTTP2Config{MaxReceiveBufferPerConnection: 1 << 20, MaxReceiveBufferPerStream: 256 << 10}
	server.StartTLS()
	t.Cleanup(server.Close)
	proxy, dials, arm := uploadJourneyTCPBlackhole(t, server.Listener.Addr().String())
	armBlackhole = arm
	previousPolicy := network.Current()
	policy := network.DefaultSettings()
	policy.ProxyMode = network.ProxyModeDirect
	policy.TrustedCertificateHost = "127.0.0.1"
	policy.TrustedCertificateSHA256 = fmt.Sprintf("%x", sha256.Sum256(server.Certificate().Raw))
	if err := network.ConfigureDefault(policy); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := network.ConfigureDefault(previousPolicy); err != nil {
			t.Error(err)
		}
	})
	rootModel := model
	if owner == "child" {
		rootModel = parentModel
	}
	cfg := modelAttributionConfig("https://"+proxy, rootModel)
	account := cfg.Accounts["account"]
	account.Connection.APIStandard = "openai"
	// Exhaust the request budget immediately; the TASK supervisor, not a
	// transport retry or manual Retry now, must own the next request.
	account.Connection.Dialer = &provider.DialSettings{MaxAttempts: 1,
		ProgressStartupTimeoutSeconds: 1, ProgressTimeoutFloorSeconds: 1, ProgressTimeoutCeilingSeconds: 1}
	account.Connection.Headers = map[string]string{"X-Upload-Journey-Owner": "target"}
	cfg.Accounts["account"] = account
	profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	on, zero := true, 0
	profile.MultiAgent = &on
	category := profile.Categories[CategoryDefault]
	category.ModelContextWindow = 2_000_000
	category.ContextCompactionPercent, category.ModelSupportsTools = &zero, true
	category.AllowedTools = []string{"write_file"}
	profile.Categories[CategoryDefault] = category
	category.Model, category.AllowedTools = model, []string{"write_file", "publish_report"}
	profile.Categories[CategoryCrafter] = category
	cfg.Agents.Enabled, cfg.Agents.MaxConcurrent = true, 1
	cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
	s, err := NewWithSessionDir("", cfg, home, sessions)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.SetWorkDir(project)
	s.SetYolo(true)
	journal := sessiondb.OpenAndMigrateAt(t.TempDir())
	t.Cleanup(func() { _ = journal.Close() })
	store, err := collaboration.Attach(journal, project, filepath.Join(home, "locks"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(); _ = store.Close() })
	s.SetCollaboration(store, "http-recovery-fixture")
	// Large uploads emit real progress events before the observed recovery row;
	// retain enough bounded capacity that observation cannot backpressure HTTP I/O.
	sender := &sessionSupervisedJourneySender{mockEventSender: &mockEventSender{ch: make(chan agentEvent, 4096)}}
	type evidence struct {
		snapshot sessionSupervisedJourneySnapshot
		err      error
	}
	observed := make(chan evidence, 1)
	var once sync.Once
	sender.observe = func(typ string, data any, id string) {
		status, ok := data.(recoverytypes.Status)
		if typ != "recovery" || !ok || status.State != recoverytypes.NeedsAttention && (status.State != recoverytypes.Recovering || status.RetryAt.IsZero()) {
			return
		}
		once.Do(func() {
			ownerPath, sessionID := s.dbPath, s.sessionID
			if owner == "child" {
				ownerPath, sessionID = filepath.Join(s.SessionDir(), "subagents", id+".db"), id
			}
			snapshot, loadErr := loadSessionSupervisedJourneySnapshot(ctx, ownerPath, sessionID, sessionID, status.ID)
			observed <- evidence{snapshot, loadErr}
		})
	}
	orch, err := s.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	parentID = orch.ID
	ownerID, ownerSessionID, ownerPath := orch.ID, s.sessionID, s.dbPath
	var runner *SubAgentRunner
	if owner == "child" {
		if !orch.SubmitInput(provider.PlainInput("seed parent")) {
			t.Fatal("parent admission rejected")
		}
		waitTransitionTurn(t, sender.mockEventSender, orch)
		if !s.initSubAgentRunner() {
			t.Fatal("session child runner unavailable")
		}
		runner = s.SubAgentRunner()
		// Prevent the report notification starting an unrelated parent turn.
		s.CloseOrchestrator(orch.ID)
		waitSessionSupervisedJourney(t, "parent input loop close", s.orchWg.Wait)
		result, startErr := runner.StartAgent(ctx, map[string]any{"category": CategoryCrafter, "description": original, "isolation": "shared", "allowed_tools": []string{"write_file", "publish_report"}})
		if startErr != nil {
			t.Fatal(startErr)
		}
		var identity struct {
			AgentID string `json:"agent_id"`
		}
		if err := json.Unmarshal([]byte(result.Content), &identity); err != nil || identity.AgentID == "" {
			t.Fatalf("child launch identity: %s err=%v", result.Content, err)
		}
		ownerID, ownerSessionID = identity.AgentID, identity.AgentID
		ownerPath = filepath.Join(s.SessionDir(), "subagents", ownerID+".db")
	} else if !orch.SubmitInput(provider.PlainInput(original)) {
		t.Fatal("root admission rejected")
	}
	var first evidence
	select {
	case first = <-observed:
	case <-ctx.Done():
		t.Fatalf("no durable countdown after exhausted HTTP request: requests=%d", requests.Load())
	}
	if first.err != nil {
		t.Fatal(first.err)
	}
	cp := first.snapshot.checkpoint
	failedDials := dials.Load()
	if !strings.Contains(cp.Status.Detail, "upload") {
		t.Fatalf("not an upload stall: %+v", cp.Status)
	}
	assertHTTPRecoveryEffect(t, cp.ActiveContext, path, content, callID)
	if cp.Status.State != recoverytypes.Recovering || cp.Status.RetryAt.IsZero() || !cp.Status.RetryAt.After(cp.Status.UpdatedAt) || cp.Status.RetryAt.Sub(cp.Status.UpdatedAt) > 5*time.Minute {
		t.Fatalf("upload stall parked instead of automatic countdown: %+v", cp.Status)
	}
	if requests.Load() != 2 || cp.Status.AgentID != ownerID || cp.Status.Generation != 1 || cp.Status.ID == "" || !cp.InputAdmitted || cp.ContextOwnerID != ownerSessionID || cp.PendingInput.InternalID == "" || !strings.Contains(cp.PendingInput.Text, original) || cp.ContextRevision != first.snapshot.context.Revision || !reflect.DeepEqual(cp.ActiveContext, first.snapshot.context.Messages) {
		t.Fatalf("countdown lost exact durable ownership/source: requests=%d checkpoint=%+v", requests.Load(), cp)
	}
	if owner == "child" {
		mirror, found, err := sessiondb.LoadRecoveryCheckpoint(s.db, s.sessionID, ownerID)
		if err != nil || !found || mirror.Status.ID != cp.Status.ID || mirror.Status.Generation != cp.Status.Generation || !reflect.DeepEqual(mirror.ActiveContext, cp.ActiveContext) {
			t.Fatalf("child countdown lacks exact parent mirror: found=%t err=%v", found, err)
		}
	}
	if stop && !s.CancelRecoveryFor(ownerID, cp.Status.ID, cp.Status.Generation) {
		t.Fatal("exact cooldown cancellation rejected")
	}
	// No RetryRecoveryFor, wake signal or configuration edit anywhere in this
	// journey: the real one-second supervisor cooldown must expire itself.
	wantReason := "completed"
	if stop {
		wantReason = "cancelled"
	}
	settled := false
	for !settled {
		select {
		case event := <-sender.ch:
			if event.agentID != ownerID {
				continue
			}
			if event.typ == "recovery" && event.data.(recoverytypes.Status).State == recoverytypes.NeedsAttention {
				t.Fatalf("HTTP recovery unexpectedly parked: %+v", event.data)
			}
			if event.typ == "done" {
				data, _ := event.data.(map[string]any)
				if data["reason"] != wantReason || !stop && requests.Load() < 3 {
					t.Fatalf("false terminal event before recovered success: %+v requests=%d", data, requests.Load())
				}
				settled = true
			}
		case <-ctx.Done():
			t.Fatalf("automatic HTTP recovery never settled: requests=%d", requests.Load())
		}
	}
	if runner != nil {
		waitSessionSupervisedJourney(t, "child settlement", runner.Wait)
	}
	if stop {
		// Observe beyond the abandoned cooldown, proving no delayed restart.
		select {
		case <-time.After(time.Until(cp.Status.RetryAt) + 200*time.Millisecond):
		case <-ctx.Done():
			t.Fatal("cancelled cooldown observation timed out")
		}
	} else {
		mu.Lock()
		resumedAt := replacementAt
		mu.Unlock()
		if resumedAt.Before(cp.Status.RetryAt) {
			t.Fatalf("HTTP replacement bypassed automatic countdown: %s < %s", resumedAt, cp.Status.RetryAt)
		}
		mu.Lock()
		if replacementRemote == "" || replacementRemote == failedRemote {
			t.Errorf("replacement reused failed physical connection: failed=%q replacement=%q", failedRemote, replacementRemote)
		}
		mu.Unlock()
	}
	wantRequests, wantTools := int32(3), int32(1)
	if owner == "child" && !stop {
		wantRequests, wantTools = 4, 2 // completed file effect plus required report
	}
	if stop {
		wantRequests = 2
	}
	wantDials := failedDials + 1
	if stop {
		wantDials = failedDials
	}
	// Child report publication can create another ordinary provider client after
	// recovery. The first replacement's socket identity above is the decisive
	// evidence; Stop must admit no later dial at all.
	if dials.Load() < wantDials || stop && dials.Load() != wantDials {
		t.Fatalf("automatic recovery failed to redial exact blackholed connection: dials=%d want=%d", dials.Load(), wantDials)
	}
	if requests.Load() != wantRequests || sender.toolStarts.Load() != wantTools || sender.toolResults.Load() != wantTools {
		t.Fatalf("replayed/lost effects: requests=%d/%d tools=%d/%d results=%d", requests.Load(), wantRequests, sender.toolStarts.Load(), wantTools, sender.toolResults.Load())
	}
	terminal, err := loadSessionSupervisedJourneySnapshot(ctx, ownerPath, ownerSessionID, ownerSessionID, cp.Status.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantState, wantTurn := recoverytypes.Recovered, sessiondb.TurnCompleted
	if stop {
		wantState, wantTurn = recoverytypes.Cancelled, sessiondb.TurnInterrupted
	}
	if terminal.checkpoint.Status.State != wantState || !terminal.checkpoint.Status.RetryAt.IsZero() || terminal.checkpoint.Status.AgentID != ownerID || terminal.checkpoint.Status.Generation != cp.Status.Generation || terminal.turn == nil || terminal.turn.AgentID != ownerID || terminal.turn.State != wantTurn {
		t.Fatalf("terminal event preceded durable exact-owner settlement: recovery=%+v turn=%+v", terminal.checkpoint.Status, terminal.turn)
	}
	assertSessionSupervisedJourneyIdentities(t, first.snapshot, terminal)
	assertHTTPRecoveryEffect(t, terminal.context.Messages, path, content, callID)
	if owner == "child" && !stop {
		result, found := runner.Manager().GetCompleted(agent.AgentID(ownerID))
		if !found || result.Status != agent.SubAgentCompleted || result.ReportID == "" || result.ReportRevision != 1 {
			t.Fatalf("recovered child completed without mandatory report: %+v", result)
		}
		artifact, err := store.Artifact(ctx, result.ReportID, result.ReportRevision)
		if err != nil || artifact.AuthorID != ownerID || artifact.Kind != collaboration.ReportFinal || artifact.RunID == "" {
			t.Fatalf("recovered child report provenance: %+v err=%v", artifact, err)
		}
		run, err := store.Run(ctx, artifact.RunID)
		if err != nil || run.AgentID != ownerID || run.State != collaboration.RunSucceeded {
			t.Fatalf("report publication did not settle actual child run: %+v err=%v", run, err)
		}
		page, err := store.Page(ctx, collaboration.PageQuery{RecipientID: parentID, Kind: collaboration.EventReportPublished})
		if err != nil || len(page.Events) != 1 || page.Events[0].SenderID != ownerID || page.Events[0].RunID != artifact.RunID {
			t.Fatalf("final report not addressed exactly once to parent: %+v err=%v", page, err)
		}
	}
}

// Each relay retains its sockets through the cooldown, with no reset or timeout.
// Arming selects the exact connection observed by the HTTP/2 handler, so the
// child's unrelated parent-seed connection is never mistaken for its failed one.
func uploadJourneyTCPBlackhole(t *testing.T, upstream string) (string, *atomic.Int32, func(string)) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dials := new(atomic.Int32)
	var mu sync.Mutex
	var connections []net.Conn
	holes := make(map[string]*atomic.Bool)
	var workers sync.WaitGroup
	acceptDone := make(chan struct{})
	workers.Add(1)
	go func() {
		defer workers.Done()
		defer close(acceptDone)
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			dials.Add(1)
			server, err := net.Dial("tcp", upstream)
			if err != nil {
				_ = client.Close()
				return
			}
			hole := new(atomic.Bool)
			mu.Lock()
			connections = append(connections, client, server)
			holes[server.LocalAddr().String()] = hole
			mu.Unlock()
			for _, pair := range [][2]net.Conn{{client, server}, {server, client}} {
				workers.Add(1)
				go func(source, destination net.Conn) {
					defer workers.Done()
					buffer := make([]byte, 32*1024)
					for {
						n, err := source.Read(buffer)
						if n > 0 && !hole.Load() {
							if _, writeErr := destination.Write(buffer[:n]); writeErr != nil {
								return
							}
						}
						if err != nil {
							return
						}
					}
				}(pair[0], pair[1])
			}
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-acceptDone
		mu.Lock()
		for _, conn := range connections {
			_ = conn.Close()
		}
		mu.Unlock()
		workers.Wait()
	})
	arm := func(remote string) {
		mu.Lock()
		hole := holes[remote]
		mu.Unlock()
		if hole == nil {
			t.Errorf("no actual TCP connection for HTTP/2 owner %q", remote)
			return
		}
		hole.Store(true)
	}
	return listener.Addr().String(), dials, arm
}
